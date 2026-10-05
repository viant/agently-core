package agui

import (
	"context"
	"fmt"
	recovery "github.com/viant/agently-core/internal/datly/agui/recovery/read"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"time"
)

// ApprovalRecoveryCandidate is private watchdog evidence, never a transport DTO.
type ApprovalRecoveryCandidate struct {
	RunKey          string
	Principal       string
	ThreadID        string
	RunID           string
	SecurityContext string
}

func (s *ComponentStore) ReadApprovalRecovery(ctx context.Context, after string, before time.Time, limit int) ([]ApprovalRecoveryCandidate, error) {
	if s == nil || s.Invoker == nil || before.IsZero() || limit < 1 || limit > 50 {
		return nil, fmt.Errorf("bounded approval recovery scope is required")
	}
	input := &recovery.Input{}
	input.SetAfterKey(after)
	input.SetBefore(before)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: "github.com/viant/agently-core/internal/datly/agui/recovery/read", Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/ag-ui/recovery"}}, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*recovery.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("approval recovery returned %T", value)
	}
	text := func(value any) string {
		switch v := value.(type) {
		case string:
			return v
		case *string:
			if v != nil {
				return *v
			}
		}
		return ""
	}
	if len(output.Data) > limit {
		output.Data = output.Data[:limit]
	}
	result := make([]ApprovalRecoveryCandidate, 0, len(output.Data))
	previous := after
	for _, row := range output.Data {
		if row == nil || text(row.RunKey) <= previous || text(row.ThreadId) == "" || text(row.RunId) == "" || text(row.Principal) == "" {
			return nil, fmt.Errorf("approval recovery identity is inconsistent")
		}
		previous = text(row.RunKey)
		result = append(result, ApprovalRecoveryCandidate{RunKey: previous, Principal: text(row.Principal), ThreadID: text(row.ThreadId), RunID: text(row.RunId), SecurityContext: text(row.SecurityContext)})
	}
	return result, nil
}
