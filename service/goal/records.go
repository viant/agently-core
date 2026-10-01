package goal

import (
	"context"
	"time"

	goalwrite "github.com/viant/agently-core/internal/datly/goal/write"
)

// Record carries the persisted goal fields used by public goal callers.
// It keeps optional raw strings intact while controller policy uses Goal.
type Record struct {
	ID, ConversationID, Objective, Status      string
	StatusReason, PauseReason, ControllerSpec  *string
	TokenBudget                                *int64
	TokensUsed, TimeUsedSeconds                int64
	AutonomousTurnsUsed, ConsecutiveNoProgress int64
	LastContinuationFingerprint                *string
	CreatedAt                                  time.Time
	UpdatedAt                                  *time.Time
}

// Field distinguishes an omitted value from an explicit SQL NULL.
type Field[T any] struct {
	Present bool
	Value   T
}

// Mutation owns sparse goal changes without exposing generated persistence types.
type Mutation struct {
	ID                          string
	Delete                      bool
	ConversationID              Field[*string]
	Objective                   Field[*string]
	Status                      Field[*string]
	StatusReason                Field[*string]
	PauseReason                 Field[*string]
	ControllerSpec              Field[*string]
	TokenBudget                 Field[*int64]
	TokensUsed                  Field[*int64]
	TimeUsedSeconds             Field[*int64]
	AutonomousTurnsUsed         Field[*int64]
	ConsecutiveNoProgress       Field[*int64]
	LastContinuationFingerprint Field[*string]
}

// Repository extends controller storage with create, update, and delete used by
// the goal tool and embedded SDK. Datly owns the mutation lifecycle and DML.
type Repository interface {
	Store
	Get(context.Context, string) (*Record, error)
	Apply(context.Context, Mutation) error
}

func (s *dataStore) Get(ctx context.Context, conversationID string) (*Record, error) {
	view, err := s.read(ctx, conversationID)
	if err != nil || view == nil {
		return nil, err
	}
	return &Record{
		ID: view.Id, ConversationID: view.ConversationId, Objective: view.Objective, Status: view.Status,
		StatusReason: view.StatusReason, PauseReason: view.PauseReason, ControllerSpec: view.ControllerSpec,
		TokenBudget: view.TokenBudget, TokensUsed: view.TokensUsed, TimeUsedSeconds: view.TimeUsedSeconds,
		AutonomousTurnsUsed: view.AutonomousTurnsUsed, ConsecutiveNoProgress: view.ConsecutiveNoProgress,
		LastContinuationFingerprint: view.LastContinuationFingerprint, CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt,
	}, nil
}

func (s *dataStore) Apply(ctx context.Context, mutation Mutation) error {
	row := &goalwrite.Goal{}
	row.SetId(mutation.ID)
	if mutation.Delete {
		row.SetShouldDelete(true)
	}
	if f := mutation.ConversationID; f.Present {
		row.SetConversationId(f.Value)
	}
	if f := mutation.Objective; f.Present {
		row.SetObjective(f.Value)
	}
	if f := mutation.Status; f.Present {
		row.SetStatus(f.Value)
	}
	if f := mutation.StatusReason; f.Present {
		row.SetStatusReason(f.Value)
	}
	if f := mutation.PauseReason; f.Present {
		row.SetPauseReason(f.Value)
	}
	if f := mutation.ControllerSpec; f.Present {
		row.SetControllerSpec(f.Value)
	}
	if f := mutation.TokenBudget; f.Present {
		row.SetTokenBudget(f.Value)
	}
	if f := mutation.TokensUsed; f.Present {
		row.SetTokensUsed(f.Value)
	}
	if f := mutation.TimeUsedSeconds; f.Present {
		row.SetTimeUsedSeconds(f.Value)
	}
	if f := mutation.AutonomousTurnsUsed; f.Present {
		row.SetAutonomousTurnsUsed(f.Value)
	}
	if f := mutation.ConsecutiveNoProgress; f.Present {
		row.SetConsecutiveNoProgress(f.Value)
	}
	if f := mutation.LastContinuationFingerprint; f.Present {
		row.SetLastContinuationFingerprint(f.Value)
	}
	return s.patch(ctx, row)
}
