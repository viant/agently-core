package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Mode constants for tool execution behaviour.
const (
	ModeAsk      = "ask"       // ask user for every tool call
	ModeAuto     = "auto"      // execute automatically
	ModeDeny     = "deny"      // refuse tool execution
	ModeBestPath = "best_path" // auto-approve safe tools, block risky/destructive tools
)

// AskFunc is invoked when policy.Mode==ModeAsk. It should return true to
// approve the call, false to reject. It can optionally mutate the policy (for
// example to switch to auto mode after user confirmation).
type AskFunc func(ctx context.Context, name string, args map[string]interface{}, p *Policy) bool

// Policy controls runtime behaviour of tool execution.
type Policy struct {
	Mode      string   // ask, auto or deny
	AllowList []string // optional set of allowed tools (empty => all)
	BlockList []string // optional set of blocked tools
	Ask       AskFunc  // optional callback when Mode==ask
}

// PolicyError represents a tool execution rejection caused by approval policy.
type PolicyError struct {
	message string
}

func (e *PolicyError) Error() string { return e.message }

func newPolicyError(format string, args ...interface{}) error {
	return &PolicyError{message: fmt.Sprintf(format, args...)}
}

// IsPolicyError reports whether err is a policy denial/rejection.
func IsPolicyError(err error) bool {
	var perr *PolicyError
	return errors.As(err, &perr)
}

// NormalizeMode converts aliases to canonical policy modes.
func NormalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "bestpath", "best-path", "best":
		return ModeBestPath
	case "":
		return ModeAuto
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

// BestPathAllowed returns true when the tool appears safe for automatic
// execution under ModeBestPath. Internal tools remain always allowed.
func BestPathAllowed(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	// Preserve runtime/internal maintenance tools to avoid deadlocking
	// context recovery and orchestrator mechanics.
	if strings.HasPrefix(n, "internal/") {
		return true
	}
	switch n {
	case "system/exec:run", "system/exec/run",
		"system/exec:execute", "system/exec/execute",
		"system/exec:start", "system/exec/start",
		"system/exec:cancel", "system/exec/cancel",
		"system/patch:apply", "system/patch/apply",
		"message:remove", "message-remove", "internal/message:remove":
		return false
	}
	return true
}

// ValidateExecution checks whether a tool execution is permitted by policy.
// Returns nil when allowed; otherwise an explanatory error.
func ValidateExecution(ctx context.Context, p *Policy, name string, args map[string]interface{}) error {
	if p == nil {
		return nil
	}
	if !p.IsAllowed(name) {
		return newPolicyError("tool %q blocked by approval policy", name)
	}
	switch NormalizeMode(p.Mode) {
	case ModeAuto:
		return nil
	case ModeDeny:
		return newPolicyError("tool %q denied by approval policy", name)
	case ModeAsk:
		if p.Ask == nil {
			return newPolicyError("tool %q requires approval", name)
		}
		if !p.Ask(ctx, name, args, p) {
			return newPolicyError("tool %q approval rejected", name)
		}
		return nil
	case ModeBestPath:
		if !BestPathAllowed(name) {
			return newPolicyError("tool %q blocked by best_path approval policy", name)
		}
		return nil
	default:
		return fmt.Errorf("unknown approval policy mode %q", p.Mode)
	}
}

// IsAllowed checks whether a tool name is permitted by Allow/Block lists.
func (p *Policy) IsAllowed(name string) bool {
	if p == nil {
		return true
	}
	for _, b := range p.BlockList {
		if b == name {
			return false
		}
	}
	if len(p.AllowList) == 0 {
		return true
	}
	for _, a := range p.AllowList {
		if a == name {
			return true
		}
	}
	return false
}

// context key to carry policy
type policyKeyT struct{}

var policyKey = policyKeyT{}

// WithPolicy attaches policy to context.
func WithPolicy(ctx context.Context, p *Policy) context.Context {
	return context.WithValue(ctx, policyKey, p)
}

// FromContext retrieves policy from context; may be nil.
func FromContext(ctx context.Context) *Policy {
	val := ctx.Value(policyKey)
	if val == nil {
		return nil
	}
	if p, ok := val.(*Policy); ok {
		return p
	}
	return nil
}
