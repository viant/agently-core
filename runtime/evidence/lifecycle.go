package evidence

import (
	"context"
	"encoding/json"
	"time"
)

// Input is captured before routing/intake can modify visible request context.
// Context is a private copy; policy factories select only their reserved field.
type Input struct {
	Context    json.RawMessage
	ReceivedAt time.Time
	Nested     bool
}

// Turn is supplied after the real starter and execution run are durable.
// LeaseOwner resolves the current execution token, not an admission-time copy.
type Turn struct {
	ConversationID   string
	TurnID           string
	StarterMessageID string
	LeaseOwner       func() string
}

type Pending interface {
	Begin(context.Context, Turn) (context.Context, error)
}

// Factory is an optional composition-root dependency. Restore must load the
// original immutable admission; it must never recapture current input or clock.
type Factory interface {
	Capture(context.Context, Input) (Pending, error)
	Restore(context.Context, Turn) (context.Context, error)
}
