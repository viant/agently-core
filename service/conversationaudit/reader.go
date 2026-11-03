package conversationaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	countview "github.com/viant/agently-core/internal/datly/conversationaudit/count"
	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

type Reader interface {
	Read(ctx context.Context, input *projection.Input) ([]*projection.Row, error)
}

// Counter is optional for readers that only support the original list API.
// A total is never approximated by loading or counting a bounded list page.
type Counter interface {
	Count(ctx context.Context, input *projection.Input) (int64, error)
}

type NativeReader struct{ Invoker dexec.ComponentInvoker }

func (r NativeReader) Read(ctx context.Context, input *projection.Input) ([]*projection.Row, error) {
	if r.Invoker == nil {
		return nil, fmt.Errorf("audit reader unavailable")
	}
	value, err := r.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{
		Target:    dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[projection.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/conversation-audit"}},
		Input:     input,
		Providers: auditProviders(input),
	})
	if err != nil {
		return nil, err
	}
	// Datly invokers can return either the typed output or its dynamic wrapper.
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if input.Detail && len(data) > MaxExportBytes {
		return nil, ErrTooLarge
	}
	var output projection.Output
	if err = json.Unmarshal(data, &output); err != nil {
		return nil, err
	}
	for _, row := range output.Data {
		if row.KnownInputTokens != row.ModelCalls {
			row.InputTokens = nil
		}
		if row.KnownOutputTokens != row.ModelCalls {
			row.OutputTokens = nil
		}
		if row.KnownCosts != row.ModelCalls {
			row.Cost = nil
		}
	}
	return output.Data, nil
}

func auditProviders(input *projection.Input) []locator.Provider {
	return []locator.Provider{provider.Named("auditaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "allowed":
			return input.Allowed, true, nil
		case "owner":
			return input.OwnerID, true, nil
		case "ownerContains":
			return input.OwnerIDContains, true, nil
		}
		return nil, false, nil
	})}
}

func (r NativeReader) Count(ctx context.Context, input *projection.Input) (int64, error) {
	if r.Invoker == nil {
		return 0, fmt.Errorf("audit reader unavailable")
	}
	value, err := r.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[countview.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/conversation-audit/count"}},
		Input: &countview.Input{Allowed: input.Allowed, OwnerID: input.OwnerID, OwnerIDContains: input.OwnerIDContains,
			ConversationID: input.ConversationID, From: input.From, To: input.To},
		Providers: auditProviders(input),
	})
	if err != nil {
		return 0, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	var output countview.Output
	if err = json.Unmarshal(data, &output); err != nil {
		return 0, err
	}
	if len(output.Data) != 1 || output.Data[0] == nil || output.Data[0].TotalCount < 0 {
		return 0, fmt.Errorf("invalid audit count result")
	}
	return output.Data[0].TotalCount, nil
}
