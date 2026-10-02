package predicate

import (
	"context"
	"fmt"
	"github.com/viant/xdatly/predicate"
	"reflect"
	"strings"
	"time"
)

// RunPatchCondition retains the legacy run business expectations.
// Datly's existing predicate system owns activation and mutation criteria.
type RunPatchCondition struct {
	Status     string  `json:",omitempty"`
	LeaseOwner *string `json:",omitempty"`
	Attempt    *int    `json:",omitempty"`
}

type RunExpected struct {
	Id         string
	Condition  *RunPatchCondition
	LeaseMode  string
	LeaseOwner string
	LeaseNow   time.Time
}

type RunPredicate struct{}

var LinkedRunTypes = []reflect.Type{reflect.TypeFor[RunPredicate](), reflect.TypeFor[RunPatchCondition](), reflect.TypeFor[RunExpected]()}
var LinkedRunHandler predicate.Handler = &RunPredicate{}

func (*RunPredicate) Compute(_ context.Context, value any) (*predicate.Criteria, error) {
	rows, ok := value.([]*RunExpected)
	if !ok {
		return nil, fmt.Errorf("run predicate requires typed expectations")
	}
	parts := []string{}
	args := []any{}
	seen := map[string]bool{}
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.Id) == "" {
			return nil, fmt.Errorf("run expectation requires identity")
		}
		if seen[row.Id] {
			return nil, fmt.Errorf("duplicate run expectation identity")
		}
		seen[row.Id] = true
		clauses := []string{"run.id = ?"}
		args = append(args, row.Id)
		if row.LeaseMode != "" {
			owner := strings.TrimSpace(row.LeaseOwner)
			if owner == "" {
				return nil, fmt.Errorf("run lease owner is required")
			}
			switch row.LeaseMode {
			case "claim":
				if row.LeaseNow.IsZero() {
					return nil, fmt.Errorf("run lease clock is required")
				}
				clauses = append(clauses, "run.completed_at IS NULL", "(run.lease_until IS NULL OR run.lease_until < ? OR run.lease_owner = ?)")
				args = append(args, row.LeaseNow.UTC(), owner)
			case "release":
				clauses = append(clauses, "run.lease_owner = ?")
				args = append(args, owner)
			default:
				return nil, fmt.Errorf("unsupported run lease operation")
			}
		} else if condition := row.Condition; condition != nil {
			if condition.LeaseOwner == nil || strings.TrimSpace(*condition.LeaseOwner) == "" {
				return nil, fmt.Errorf("conditional run patch requires observed owner")
			}
			status := strings.TrimSpace(condition.Status)
			if (status == "") != (condition.Attempt == nil) {
				return nil, fmt.Errorf("conditional run claim requires status and attempt together")
			}
			if status != "" {
				clauses = append(clauses, "run.status = ?", "run.attempt = ?")
				args = append(args, status, *condition.Attempt)
			}
			clauses = append(clauses, "run.lease_owner = ?")
			args = append(args, strings.TrimSpace(*condition.LeaseOwner))
		}
		parts = append(parts, "("+strings.Join(clauses, " AND ")+")")
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("run expectations are empty")
	}
	return &predicate.Criteria{Expression: "(" + strings.Join(parts, " OR ") + ")", Placeholders: args}, nil
}
