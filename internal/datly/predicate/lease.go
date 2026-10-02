package predicate

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/viant/xdatly/predicate"
)

// LeasePredicate uses the writer's typed owner and clock inputs.
type LeasePredicate struct {
	Owner string    `bind:"kind=input,in=LeaseOwner"`
	Now   time.Time `bind:"kind=input,in=LeaseNow"`
}

var LinkedLeaseTypes = []reflect.Type{reflect.TypeFor[LeasePredicate]()}
var LinkedLeaseHandler predicate.Handler = &LeasePredicate{}

func (h *LeasePredicate) Compute(_ context.Context, value any) (*predicate.Criteria, error) {
	mode, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("lease operation requires string input")
	}
	owner := strings.TrimSpace(h.Owner)
	if owner == "" {
		return nil, fmt.Errorf("lease owner is required")
	}
	switch mode {
	case "claim":
		if h.Now.IsZero() {
			return nil, fmt.Errorf("lease claim clock is required")
		}
		return &predicate.Criteria{Expression: "schedule.enabled = 1 AND (schedule.lease_until IS NULL OR schedule.lease_until < ? OR schedule.lease_owner = ?)", Placeholders: []any{h.Now.UTC(), owner}}, nil
	case "release":
		return &predicate.Criteria{Expression: "schedule.lease_owner = ?", Placeholders: []any{owner}}, nil
	default:
		return nil, fmt.Errorf("unsupported lease operation %q", mode)
	}
}
