package goal

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/viant/agently-core/app/store/data"
	aggoal "github.com/viant/agently-core/pkg/agently/goal"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
	"github.com/viant/agently-core/service/scheduler"
	"github.com/viant/agently-core/workspace"
	wscfg "github.com/viant/agently-core/workspace/config"
)

const (
	Name           = "system/goal"
	StatusActive   = "active"
	StatusBlocked  = "blocked"
	StatusComplete = "complete"
)

type Store interface {
	GetGoal(ctx context.Context, conversationID string, in *aggoal.GoalInput, opts ...data.Option) (*aggoal.GoalView, error)
	PatchGoals(ctx context.Context, rows []*aggoalwrite.MutableGoalView) ([]*aggoalwrite.MutableGoalView, error)
	DeleteGoals(ctx context.Context, ids ...string) error
}

type Service struct {
	store     Store
	wakeups   wakeupCanceler
	schedules controllerScheduleReader
}

type wakeupCanceler interface {
	CancelGoalWakeups(ctx context.Context, conversationID, goalID string) error
}

type controllerScheduleReader interface {
	CurrentGoalWakeup(ctx context.Context, conversationID, goalID string) *scheduler.GoalControllerSchedule
}

func New(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) SetWakeupCanceler(canceler wakeupCanceler) {
	if s == nil {
		return
	}
	s.wakeups = canceler
}

func (s *Service) SetControllerScheduleReader(reader controllerScheduleReader) {
	if s == nil {
		return
	}
	s.schedules = reader
}

func (s *Service) Name() string { return Name }

func (s *Service) CacheableMethods() map[string]bool {
	return map[string]bool{}
}

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{
			Name:        "get",
			Description: "Return the current durable goal for this conversation. Returns null goal when none exists.",
			Input:       reflect.TypeOf(&GetInput{}),
			Output:      reflect.TypeOf(&GetOutput{}),
		},
		{
			Name:        "create",
			Description: "Create a durable active goal for this conversation when the user's task is clearly multi-step. Fails if a goal already exists.",
			Input:       reflect.TypeOf(&CreateInput{}),
			Output:      reflect.TypeOf(&CreateOutput{}),
		},
		{
			Name:        "update",
			Description: "Update the current durable goal status. Allowed statuses: complete, blocked.",
			Input:       reflect.TypeOf(&UpdateInput{}),
			Output:      reflect.TypeOf(&UpdateOutput{}),
		},
		{
			Name:        "pause",
			Description: "Pause the current durable goal for this conversation.",
			Input:       reflect.TypeOf(&PauseInput{}),
			Output:      reflect.TypeOf(&PauseOutput{}),
		},
		{
			Name:        "resume",
			Description: "Resume the current durable goal for this conversation by setting it back to active.",
			Input:       reflect.TypeOf(&ResumeInput{}),
			Output:      reflect.TypeOf(&ResumeOutput{}),
		},
		{
			Name:        "clear",
			Description: "Clear the current durable goal for this conversation.",
			Input:       reflect.TypeOf(&ClearInput{}),
			Output:      reflect.TypeOf(&ClearOutput{}),
		},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "get":
		return s.get, nil
	case "create":
		return s.create, nil
	case "update":
		return s.update, nil
	case "pause":
		return s.pause, nil
	case "resume":
		return s.resume, nil
	case "clear":
		return s.clear, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

type Goal struct {
	ID                 string              `json:"id"`
	Objective          string              `json:"objective"`
	Status             string              `json:"status"`
	StatusReason       *string             `json:"statusReason,omitempty"`
	PauseReason        *string             `json:"pauseReason,omitempty"`
	ControllerSpec     *string             `json:"controllerSpec,omitempty"`
	ControllerSchedule *ControllerSchedule `json:"controllerSchedule,omitempty"`
	TokenBudget        *int64              `json:"tokenBudget,omitempty"`
	TokensUsed         int64               `json:"tokensUsed"`
	TimeUsedSeconds    int64               `json:"timeUsedSeconds"`
}

type ControllerSchedule struct {
	Mode    string  `json:"mode,omitempty"`
	Preview *string `json:"preview,omitempty"`
	WakeAt  *string `json:"wakeAt,omitempty"`
}

type GetInput struct{}

type GetOutput struct {
	Goal *Goal `json:"goal,omitempty"`
}

type CreateInput struct {
	Objective      string  `json:"objective"`
	TokenBudget    *int64  `json:"tokenBudget,omitempty"`
	ControllerSpec *string `json:"controllerSpec,omitempty"`
}

type CreateOutput struct {
	Goal *Goal `json:"goal,omitempty"`
}

type UpdateInput struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type UpdateOutput struct {
	Goal *Goal `json:"goal,omitempty"`
}

type PauseInput struct {
	Reason string `json:"reason,omitempty"`
}

type PauseOutput struct {
	Goal *Goal `json:"goal,omitempty"`
}

type ResumeInput struct{}

type ResumeOutput struct {
	Goal *Goal `json:"goal,omitempty"`
}

type ClearInput struct{}

type ClearOutput struct {
	Cleared bool `json:"cleared"`
}

func (s *Service) get(ctx context.Context, in, out interface{}) error {
	if err := ensureGoalsEnabled(); err != nil {
		return err
	}
	if _, ok := in.(*GetInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*GetOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	convID, err := conversationIDFromContext(ctx)
	if err != nil {
		return err
	}
	current, err := s.store.GetGoal(ctx, convID, nil)
	if err != nil {
		return err
	}
	output.Goal = s.projectGoal(convID, current)
	return nil
}

func (s *Service) create(ctx context.Context, in, out interface{}) error {
	if err := ensureGoalsEnabled(); err != nil {
		return err
	}
	input, ok := in.(*CreateInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*CreateOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	convID, err := conversationIDFromContext(ctx)
	if err != nil {
		return err
	}
	objective := strings.TrimSpace(input.Objective)
	if objective == "" {
		return fmt.Errorf("objective is required")
	}
	current, err := s.store.GetGoal(ctx, convID, nil)
	if err != nil {
		return err
	}
	if current != nil {
		return fmt.Errorf("goal already exists for current conversation")
	}
	row := aggoalwrite.NewMutableGoalView(
		aggoalwrite.WithGoalID(defaultGoalID(convID)),
		aggoalwrite.WithGoalConversationID(convID),
		aggoalwrite.WithGoalObjective(objective),
		aggoalwrite.WithGoalStatus(StatusActive),
	)
	if input.TokenBudget != nil {
		row.SetTokenBudget(*input.TokenBudget)
	}
	if input.ControllerSpec != nil && strings.TrimSpace(*input.ControllerSpec) != "" {
		row.SetControllerSpec(strings.TrimSpace(*input.ControllerSpec))
	}
	if _, err := s.store.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{row}); err != nil {
		return err
	}
	created, err := s.store.GetGoal(ctx, convID, nil)
	if err != nil {
		return err
	}
	output.Goal = s.projectGoal(convID, created)
	s.publishGoalEvent(ctx, streaming.EventTypeGoalUpdated, output.Goal)
	return nil
}

func (s *Service) update(ctx context.Context, in, out interface{}) error {
	if err := ensureGoalsEnabled(); err != nil {
		return err
	}
	input, ok := in.(*UpdateInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*UpdateOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	convID, err := conversationIDFromContext(ctx)
	if err != nil {
		return err
	}
	status := normalizeStatus(input.Status)
	switch status {
	case StatusComplete, StatusBlocked:
	default:
		return fmt.Errorf("status must be one of: %s, %s", StatusComplete, StatusBlocked)
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return fmt.Errorf("reason is required")
	}
	current, err := s.store.GetGoal(ctx, convID, nil)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("goal does not exist for current conversation")
	}
	s.cancelGoalWakeups(ctx, convID, current.Id)
	row := aggoalwrite.NewMutableGoalView(
		aggoalwrite.WithGoalID(current.Id),
		aggoalwrite.WithGoalStatus(status),
		aggoalwrite.WithGoalStatusReason(reason),
	)
	if _, err := s.store.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{row}); err != nil {
		return err
	}
	updated, err := s.store.GetGoal(ctx, convID, nil)
	if err != nil {
		return err
	}
	output.Goal = s.projectGoal(convID, updated)
	s.publishGoalEvent(ctx, streaming.EventTypeGoalUpdated, output.Goal)
	return nil
}

func (s *Service) pause(ctx context.Context, in, out interface{}) error {
	if err := ensureGoalsEnabled(); err != nil {
		return err
	}
	input, ok := in.(*PauseInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*PauseOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	current, convID, err := s.currentGoal(ctx)
	if err != nil {
		return err
	}
	s.cancelGoalWakeups(ctx, convID, current.Id)
	row := aggoalwrite.NewMutableGoalView(
		aggoalwrite.WithGoalID(current.Id),
		aggoalwrite.WithGoalStatus("paused"),
	)
	row.StatusReason = nil
	row.Has.StatusReason = true
	if reason := strings.TrimSpace(input.Reason); reason != "" {
		row.SetPauseReason(reason)
	}
	if _, err := s.store.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{row}); err != nil {
		return err
	}
	updated, err := s.store.GetGoal(ctx, convID, nil)
	if err != nil {
		return err
	}
	output.Goal = s.projectGoal(convID, updated)
	s.publishGoalEvent(ctx, streaming.EventTypeGoalUpdated, output.Goal)
	return nil
}

func (s *Service) resume(ctx context.Context, in, out interface{}) error {
	if err := ensureGoalsEnabled(); err != nil {
		return err
	}
	if _, ok := in.(*ResumeInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ResumeOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	current, convID, err := s.currentGoal(ctx)
	if err != nil {
		return err
	}
	s.cancelGoalWakeups(ctx, convID, current.Id)
	row := aggoalwrite.NewMutableGoalView(
		aggoalwrite.WithGoalID(current.Id),
		aggoalwrite.WithGoalStatus(StatusActive),
	)
	row.StatusReason = nil
	row.Has.StatusReason = true
	row.PauseReason = nil
	row.Has.PauseReason = true
	if _, err := s.store.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{row}); err != nil {
		return err
	}
	updated, err := s.store.GetGoal(ctx, convID, nil)
	if err != nil {
		return err
	}
	output.Goal = s.projectGoal(convID, updated)
	s.publishGoalEvent(ctx, streaming.EventTypeGoalUpdated, output.Goal)
	return nil
}

func (s *Service) clear(ctx context.Context, in, out interface{}) error {
	if err := ensureGoalsEnabled(); err != nil {
		return err
	}
	if _, ok := in.(*ClearInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ClearOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	current, _, err := s.currentGoal(ctx)
	if err != nil {
		return err
	}
	if err := s.store.DeleteGoals(ctx, current.Id); err != nil {
		return err
	}
	s.cancelGoalWakeups(ctx, current.ConversationID, current.Id)
	output.Cleared = true
	s.publishGoalClearedEvent(ctx, current)
	return nil
}

func (s *Service) cancelGoalWakeups(ctx context.Context, conversationID, goalID string) {
	if s == nil || s.wakeups == nil {
		return
	}
	_ = s.wakeups.CancelGoalWakeups(context.WithoutCancel(ctx), strings.TrimSpace(conversationID), strings.TrimSpace(goalID))
}

func conversationIDFromContext(ctx context.Context) (string, error) {
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		if convID := strings.TrimSpace(turn.ConversationID); convID != "" {
			return convID, nil
		}
	}
	if convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)); convID != "" {
		return convID, nil
	}
	return "", fmt.Errorf("conversation context is required")
}

func (s *Service) currentGoal(ctx context.Context) (*aggoal.GoalView, string, error) {
	convID, err := conversationIDFromContext(ctx)
	if err != nil {
		return nil, "", err
	}
	current, err := s.store.GetGoal(ctx, convID, nil)
	if err != nil {
		return nil, "", err
	}
	if current == nil {
		return nil, "", fmt.Errorf("goal does not exist for current conversation")
	}
	return current, convID, nil
}

func ensureGoalsEnabled() error {
	cfg, err := wscfg.Load(workspace.Root())
	if err != nil {
		return err
	}
	if cfg != nil && !cfg.GoalsEnabled() {
		return fmt.Errorf("goals are not enabled in this workspace")
	}
	return nil
}

func defaultGoalID(conversationID string) string {
	return "goal-" + strings.TrimSpace(conversationID)
}

func (s *Service) projectGoal(conversationID string, in *aggoal.GoalView) *Goal {
	goal := projectGoal(in)
	if s == nil || s.schedules == nil || goal == nil {
		return goal
	}
	wakeup := s.schedules.CurrentGoalWakeup(context.Background(), strings.TrimSpace(conversationID), strings.TrimSpace(goal.ID))
	if wakeup == nil {
		return goal
	}
	controllerSchedule := &ControllerSchedule{
		Mode: wakeup.Mode,
	}
	if wakeup.Preview != nil {
		preview := strings.TrimSpace(*wakeup.Preview)
		controllerSchedule.Preview = &preview
	}
	if wakeup.WakeAt != nil && !wakeup.WakeAt.IsZero() {
		wakeAt := wakeup.WakeAt.UTC().Format(time.RFC3339Nano)
		controllerSchedule.WakeAt = &wakeAt
	}
	goal.ControllerSchedule = controllerSchedule
	return goal
}

func normalizeStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (s *Service) publishGoalEvent(ctx context.Context, eventType streaming.EventType, goal *Goal) {
	pub, ok := modelcallctx.StreamPublisherFromContext(ctx)
	if !ok || pub == nil || goal == nil {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	convID, err := conversationIDFromContext(ctx)
	if err != nil {
		return
	}
	statusReason := ""
	if goal.StatusReason != nil {
		statusReason = strings.TrimSpace(*goal.StatusReason)
	} else if goal.PauseReason != nil {
		statusReason = strings.TrimSpace(*goal.PauseReason)
	}
	ev := &streaming.Event{
		Type:           eventType,
		ConversationID: convID,
		StreamID:       convID,
		TurnID:         strings.TrimSpace(turn.TurnID),
		MessageID:      strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)),
		GoalID:         strings.TrimSpace(goal.ID),
		Status:         strings.TrimSpace(goal.Status),
		StatusReason:   statusReason,
		Content:        strings.TrimSpace(goal.Objective),
		Patch: map[string]interface{}{
			"goal": goal,
		},
		CreatedAt: time.Now(),
	}
	ev.NormalizeIdentity(convID, strings.TrimSpace(turn.TurnID))
	_ = pub.Publish(ctx, &modelcallctx.StreamEvent{
		ConversationID: convID,
		Event:          ev,
	})
}

func (s *Service) publishGoalClearedEvent(ctx context.Context, current *aggoal.GoalView) {
	pub, ok := modelcallctx.StreamPublisherFromContext(ctx)
	if !ok || pub == nil {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	convID, err := conversationIDFromContext(ctx)
	if err != nil {
		return
	}
	goalID := ""
	if current != nil {
		goalID = strings.TrimSpace(current.Id)
	}
	ev := &streaming.Event{
		Type:           streaming.EventTypeGoalCleared,
		ConversationID: convID,
		StreamID:       convID,
		TurnID:         strings.TrimSpace(turn.TurnID),
		MessageID:      strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)),
		GoalID:         goalID,
		Status:         "cleared",
		CreatedAt:      time.Now(),
	}
	ev.NormalizeIdentity(convID, strings.TrimSpace(turn.TurnID))
	_ = pub.Publish(ctx, &modelcallctx.StreamEvent{
		ConversationID: convID,
		Event:          ev,
	})
}

func projectGoal(in *aggoal.GoalView) *Goal {
	if in == nil {
		return nil
	}
	return &Goal{
		ID:              in.Id,
		Objective:       in.Objective,
		Status:          in.Status,
		StatusReason:    in.StatusReason,
		PauseReason:     in.PauseReason,
		ControllerSpec:  in.ControllerSpec,
		TokenBudget:     in.TokenBudget,
		TokensUsed:      in.TokensUsed,
		TimeUsedSeconds: in.TimeUsedSeconds,
	}
}
