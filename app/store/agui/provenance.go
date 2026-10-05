package agui

import (
	"context"
	"fmt"
	"strings"

	provenance "github.com/viant/agently-core/internal/datly/agui/provenance/read"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

// ExecutionProvenance is internal server-owned evidence, not a public run DTO.
// The authorized observer reads across principals: a shared viewer must not
// mistake another principal's AG-UI execution for native-origin execution.
type ExecutionProvenance struct {
	ThreadID        string
	TurnID          string
	NativeTurnFound bool
	AGUIOwned       bool
	ParentThreadID  string
	ParentTurnID    string
}

type ExecutionProvenanceReader interface {
	ReadExecutionProvenance(context.Context, string, string) (*ExecutionProvenance, error)
}

// ReadExecutionProvenance requires an already-authorized native conversation
// scope. The component has no public route and never exposes accepted input.
func (s *ComponentStore) ReadExecutionProvenance(ctx context.Context, threadID, turnID string) (*ExecutionProvenance, error) {
	if s == nil || s.Invoker == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return nil, fmt.Errorf("execution provenance scope is required")
	}
	input := &provenance.Input{}
	input.SetThreadID(threadID)
	input.SetTurnID(turnID)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{
		Component: spec.Key{Kind: spec.KindComponent, Scope: "github.com/viant/agently-core/internal/datly/agui/provenance/read", Name: "reader"},
		Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/ag-ui/provenance"},
	}, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*provenance.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("execution provenance returned %T", value)
	}
	if len(output.Data) == 0 {
		return nil, ErrNotFound
	}
	if len(output.Data) != 1 || output.Data[0] == nil {
		return nil, fmt.Errorf("execution provenance identity is ambiguous")
	}
	row := output.Data[0]
	text := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	if text(row.ThreadId) != threadID || (text(row.NativeTurnId) != "" && text(row.NativeTurnId) != turnID) || (row.AguiOwned != 0 && row.AguiOwned != 1) {
		return nil, fmt.Errorf("execution provenance identity is inconsistent")
	}
	return &ExecutionProvenance{ThreadID: threadID, TurnID: turnID, NativeTurnFound: text(row.NativeTurnId) == turnID, AGUIOwned: row.AguiOwned == 1, ParentThreadID: text(row.ParentThreadId), ParentTurnID: text(row.ParentTurnId)}, nil
}
