package goal

import (
	"context"
	"fmt"

	"github.com/viant/agently-core/app/store/data"
	aggoal "github.com/viant/agently-core/pkg/agently/goal"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
)

// dataAccess is the narrow subset of the application data service that the goal
// runtime depends on. Keeping it local decouples the package from the full
// data.Service and makes the store trivially fakeable in tests.
type dataAccess interface {
	GetGoal(ctx context.Context, conversationID string, in *aggoal.GoalInput, opts ...data.Option) (*aggoal.GoalView, error)
	PatchGoals(ctx context.Context, rows []*aggoalwrite.MutableGoalView) ([]*aggoalwrite.MutableGoalView, error)
}

// Store is the persistence boundary for the goal runtime, expressed entirely in
// domain terms. No Datly read/write type crosses it.
type Store interface {
	// Current returns the active goal for a conversation, or nil when none
	// exists.
	Current(ctx context.Context, conversationID string) (*Goal, error)
	// RecordUsage persists absolute usage counters for a goal. The caller is
	// responsible for computing the new totals.
	RecordUsage(ctx context.Context, goalID string, tokensUsed, timeUsedSeconds int64) error
	// Transition persists a status change with an accompanying status reason.
	Transition(ctx context.Context, goalID string, status Status, reason string) error
	// Pause persists a paused status with a dedicated pause reason.
	Pause(ctx context.Context, goalID string, reason PauseReason) error
}

type dataStore struct {
	access dataAccess
}

// NewStore returns a Store backed by the application data service.
func NewStore(access dataAccess) Store {
	return &dataStore{access: access}
}

func (s *dataStore) Current(ctx context.Context, conversationID string) (*Goal, error) {
	view, err := s.access.GetGoal(ctx, conversationID, nil)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return nil, nil
	}
	return toDomain(view)
}

func (s *dataStore) RecordUsage(ctx context.Context, goalID string, tokensUsed, timeUsedSeconds int64) error {
	row := aggoalwrite.NewMutableGoalView(
		aggoalwrite.WithGoalID(goalID),
		aggoalwrite.WithGoalTokensUsed(tokensUsed),
		aggoalwrite.WithGoalTimeUsedSeconds(timeUsedSeconds),
	)
	return s.patch(ctx, row)
}

func (s *dataStore) Transition(ctx context.Context, goalID string, status Status, reason string) error {
	row := aggoalwrite.NewMutableGoalView(
		aggoalwrite.WithGoalID(goalID),
		aggoalwrite.WithGoalStatus(string(status)),
	)
	if reason != "" {
		row.SetStatusReason(reason)
	}
	return s.patch(ctx, row)
}

func (s *dataStore) Pause(ctx context.Context, goalID string, reason PauseReason) error {
	row := aggoalwrite.NewMutableGoalView(
		aggoalwrite.WithGoalID(goalID),
		aggoalwrite.WithGoalStatus(string(StatusPaused)),
	)
	if reason != "" {
		row.SetPauseReason(string(reason))
	}
	return s.patch(ctx, row)
}

func (s *dataStore) patch(ctx context.Context, row *aggoalwrite.MutableGoalView) error {
	_, err := s.access.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{row})
	return err
}

// toDomain maps a persisted goal view onto the runtime domain model. Invalid
// persisted state (unknown status, malformed controller spec) is surfaced as an
// error rather than silently coerced.
func toDomain(view *aggoal.GoalView) (*Goal, error) {
	status, err := ParseStatus(view.Status)
	if err != nil {
		return nil, fmt.Errorf("goal %s: %w", view.Id, err)
	}
	var spec *ControllerSpec
	if view.ControllerSpec != nil {
		if spec, err = DecodeControllerSpec(*view.ControllerSpec); err != nil {
			return nil, fmt.Errorf("goal %s: %w", view.Id, err)
		}
	}
	g := &Goal{
		ID:              view.Id,
		ConversationID:  view.ConversationID,
		Objective:       view.Objective,
		Status:          status,
		Controller:      spec,
		TokenBudget:     view.TokenBudget,
		TokensUsed:      view.TokensUsed,
		TimeUsedSeconds: view.TimeUsedSeconds,
		CreatedAt:       view.CreatedAt,
		UpdatedAt:       view.UpdatedAt,
	}
	if view.StatusReason != nil {
		g.StatusReason = *view.StatusReason
	}
	if view.PauseReason != nil {
		g.PauseReason = PauseReason(*view.PauseReason)
	}
	return g, nil
}
