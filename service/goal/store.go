package goal

import (
	"context"
	"fmt"
	"reflect"

	goalread "github.com/viant/agently-core/internal/datly/goal/read"
	goalwrite "github.com/viant/agently-core/internal/datly/goal/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

var (
	goalReaderKey = spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[goalread.ReaderComponent]().PkgPath(), Name: "reader"}
	goalWriterKey = spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[goalwrite.WriterComponent]().PkgPath(), Name: "writer"}
)

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
	// UpdateControllerState persists autonomous controller counters and
	// the latest continuation fingerprint.
	UpdateControllerState(ctx context.Context, goalID string, autonomousTurnsUsed, consecutiveNoProgress int64, fingerprint string) error
}

type dataStore struct {
	invoker dexec.ComponentInvoker
}

// NewStore returns a Store backed by the linked Datly 1.0 goal components.
func NewStore(invoker dexec.ComponentInvoker) Repository {
	return &dataStore{invoker: invoker}
}

func (s *dataStore) Current(ctx context.Context, conversationID string) (*Goal, error) {
	view, err := s.read(ctx, conversationID)
	if err != nil || view == nil {
		return nil, err
	}
	return toDomain(view)
}

func (s *dataStore) read(ctx context.Context, conversationID string) (*goalread.GoalView, error) {
	if s == nil || s.invoker == nil {
		return nil, fmt.Errorf("goal component invoker is required")
	}
	input := &goalread.GoalInput{}
	input.SetConversationID(conversationID)
	value, err := s.invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{
		Component: goalReaderKey,
		Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/goal/{conversationId}"},
	}, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*goalread.GoalOutput)
	if !ok || output == nil {
		return nil, fmt.Errorf("goal reader returned %T", value)
	}
	if len(output.Data) == 0 {
		return nil, nil
	}
	return output.Data[0], nil
}

func (s *dataStore) RecordUsage(ctx context.Context, goalID string, tokensUsed, timeUsedSeconds int64) error {
	row := &goalwrite.Goal{}
	row.SetId(goalID)
	row.SetTokensUsed(&tokensUsed)
	row.SetTimeUsedSeconds(&timeUsedSeconds)
	return s.patch(ctx, row)
}

func (s *dataStore) Transition(ctx context.Context, goalID string, status Status, reason string) error {
	row := &goalwrite.Goal{}
	row.SetId(goalID)
	value := string(status)
	row.SetStatus(&value)
	if reason != "" {
		row.SetStatusReason(&reason)
	}
	return s.patch(ctx, row)
}

func (s *dataStore) Pause(ctx context.Context, goalID string, reason PauseReason) error {
	row := &goalwrite.Goal{}
	row.SetId(goalID)
	status := string(StatusPaused)
	row.SetStatus(&status)
	if reason != "" {
		value := string(reason)
		row.SetPauseReason(&value)
	}
	return s.patch(ctx, row)
}

func (s *dataStore) UpdateControllerState(ctx context.Context, goalID string, autonomousTurnsUsed, consecutiveNoProgress int64, fingerprint string) error {
	row := &goalwrite.Goal{}
	row.SetId(goalID)
	row.SetAutonomousTurnsUsed(&autonomousTurnsUsed)
	row.SetConsecutiveNoProgress(&consecutiveNoProgress)
	row.SetLastContinuationFingerprint(&fingerprint)
	return s.patch(ctx, row)
}

func (s *dataStore) patch(ctx context.Context, row *goalwrite.Goal) error {
	if s == nil || s.invoker == nil {
		return fmt.Errorf("goal component invoker is required")
	}
	input := &goalwrite.Input{}
	input.SetGoals([]*goalwrite.Goal{row})
	_, err := s.invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{
		Component: goalWriterKey,
		Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/goal"},
	}, Input: input})
	return err
}

// toDomain maps a persisted goal view onto the runtime domain model. Invalid
// persisted state (unknown status, malformed controller spec) is surfaced as an
// error rather than silently coerced.
func toDomain(view *goalread.GoalView) (*Goal, error) {
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
		ID:                    view.Id,
		ConversationID:        view.ConversationId,
		Objective:             view.Objective,
		Status:                status,
		Controller:            spec,
		TokenBudget:           view.TokenBudget,
		TokensUsed:            view.TokensUsed,
		TimeUsedSeconds:       view.TimeUsedSeconds,
		AutonomousTurnsUsed:   view.AutonomousTurnsUsed,
		ConsecutiveNoProgress: view.ConsecutiveNoProgress,
		CreatedAt:             view.CreatedAt,
		UpdatedAt:             view.UpdatedAt,
	}
	if view.StatusReason != nil {
		g.StatusReason = *view.StatusReason
	}
	if view.PauseReason != nil {
		g.PauseReason = PauseReason(*view.PauseReason)
	}
	if view.LastContinuationFingerprint != nil {
		g.LastContinuationFingerprint = *view.LastContinuationFingerprint
	}
	return g, nil
}

// DatlyInvoker identifies the canonical native participant for transactional
// application composition; it exposes no DB or transaction handle.
func (s *dataStore) DatlyInvoker() dexec.ComponentInvoker {
	if s == nil {
		return nil
	}
	return s.invoker
}
