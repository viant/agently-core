package protection

import "context"

type State string

const (
	StateCompleted State = "completed"
	StateFailed    State = "failed"
	StateUnknown   State = "unknown"
)

// Claim describes one pre-dispatch protection decision.
type Claim struct {
	Protected bool
	Acquired  bool
	RuleID    string
	ClaimKey  string
}

// Guard is the narrow concrete-registry durable protection contract.
type Guard interface {
	IsProtected(name string) bool
	Claim(ctx context.Context, name string, args map[string]interface{}) (Claim, error)
	Finish(ctx context.Context, claim Claim, state State)
}
