package goal

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/agently-core/app/store/data"
	aggoal "github.com/viant/agently-core/pkg/agently/goal"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
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
	store Store
}

func New(store Store) *Service {
	return &Service{store: store}
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
	ID              string  `json:"id"`
	Objective       string  `json:"objective"`
	Status          string  `json:"status"`
	StatusReason    *string `json:"statusReason,omitempty"`
	PauseReason     *string `json:"pauseReason,omitempty"`
	ControllerSpec  *string `json:"controllerSpec,omitempty"`
	TokenBudget     *int64  `json:"tokenBudget,omitempty"`
	TokensUsed      int64   `json:"tokensUsed"`
	TimeUsedSeconds int64   `json:"timeUsedSeconds"`
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
	output.Goal = projectGoal(current)
	return nil
}

func (s *Service) create(ctx context.Context, in, out interface{}) error {
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
	output.Goal = projectGoal(created)
	return nil
}

func (s *Service) update(ctx context.Context, in, out interface{}) error {
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
	output.Goal = projectGoal(updated)
	return nil
}

func (s *Service) pause(ctx context.Context, in, out interface{}) error {
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
	output.Goal = projectGoal(updated)
	return nil
}

func (s *Service) resume(ctx context.Context, in, out interface{}) error {
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
	output.Goal = projectGoal(updated)
	return nil
}

func (s *Service) clear(ctx context.Context, in, out interface{}) error {
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
	output.Cleared = true
	return nil
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

func defaultGoalID(conversationID string) string {
	return "goal-" + strings.TrimSpace(conversationID)
}

func normalizeStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
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
