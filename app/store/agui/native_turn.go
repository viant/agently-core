package agui

import (
	"context"
	"fmt"
	read "github.com/viant/agently-core/internal/datly/agui/native_turn/read"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

// ListRunsByNativeTurn is private cross-protocol-thread discovery for an
// already-authorized native turn. It never searches another principal.
func (s *ComponentStore) ListRunsByNativeTurn(ctx context.Context, principal, turnID string) ([]*Run, error) {
	if s == nil || s.Invoker == nil || principal == "" || turnID == "" {
		return nil, fmt.Errorf("owned native turn is required")
	}
	input := &read.Input{}
	input.SetPrincipal(principal)
	input.SetTurnID(turnID)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: "github.com/viant/agently-core/internal/datly/agui/native_turn/read", Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/ag-ui/native_turn"}}, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*read.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("native turn reader returned %T", value)
	}
	if len(output.Data) >= 100 {
		return nil, fmt.Errorf("native turn protocol history exceeds bounded lookup")
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
	runs := make([]*Run, 0, len(output.Data))
	for _, row := range output.Data {
		if row == nil || text(row.Principal) != principal || text(row.TurnId) != turnID {
			return nil, fmt.Errorf("native turn protocol scope mismatch")
		}
		thread, err := s.GetThreadByConversationID(ctx, principal, text(row.ConversationId))
		if err != nil {
			return nil, err
		}
		record, err := s.GetRun(ctx, principal, thread.ThreadID, text(row.RunId))
		if err != nil {
			return nil, err
		}
		runs = append(runs, record)
	}
	return runs, nil
}
