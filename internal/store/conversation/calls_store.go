package conversation

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	modelwrite "github.com/viant/agently-core/internal/datly/modelcall/write"
	toolread "github.com/viant/agently-core/internal/datly/toolcall/read"
	toolwrite "github.com/viant/agently-core/internal/datly/toolcall/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

type CallsStore struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
}

var modelWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[modelwrite.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/modelcall"},
}
var toolWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[toolwrite.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/toolcall"},
}
var toolReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[toolread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-call"},
}

func (s *CallsStore) PatchModelTrusted(ctx context.Context, row *modelwrite.ModelCall) error {
	_, err := s.PatchModelTrustedResult(ctx, row)
	return err
}

// PatchModelTrustedResult returns successful generated mutation rows, including lifecycle
// defaults and presence, for callers that expose mutable input objects.
func (s *CallsStore) PatchModelTrustedResult(ctx context.Context, row *modelwrite.ModelCall) (*modelwrite.Output, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("call component store is not configured")
	}
	if row == nil {
		return nil, fmt.Errorf("model call mutation is required")
	}
	input := &modelwrite.Input{}
	input.SetModelCalls([]*modelwrite.ModelCall{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: modelWriterTarget, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*modelwrite.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("model call writer returned %T", value)
	}
	return output, nil
}

func (s *CallsStore) PatchToolTrusted(ctx context.Context, row *toolwrite.ToolCall) error {
	_, err := s.PatchToolTrustedResult(ctx, row)
	return err
}

// PatchToolTrustedResult returns successful generated mutation rows, including lifecycle
// defaults and presence, for callers that expose mutable input objects.
func (s *CallsStore) PatchToolTrustedResult(ctx context.Context, row *toolwrite.ToolCall) (*toolwrite.Output, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("call component store is not configured")
	}
	if row == nil {
		return nil, fmt.Errorf("tool call mutation is required")
	}
	input := &toolwrite.Input{}
	input.SetToolCalls([]*toolwrite.ToolCall{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: toolWriterTarget, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*toolwrite.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("tool call writer returned %T", value)
	}
	return output, nil
}

// TraceByOp retains the conversation constraint on this trusted lookup.
func (s *CallsStore) TraceByOp(ctx context.Context, conversationID, opID string) (string, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return "", fmt.Errorf("call component store is not configured")
	}
	conversationID, opID = strings.TrimSpace(conversationID), strings.TrimSpace(opID)
	if conversationID == "" || opID == "" {
		return "", nil
	}
	owner := strings.TrimSpace(s.OwnerID(ctx))
	input := &toolread.ToolCallsInput{}
	input.SetConversationId(conversationID)
	input.SetOpId(opID)
	providers := []locator.Provider{
		provider.Named("toolcallaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "mode":
				return "scopedByOp", true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: toolReaderTarget, Input: input, Providers: providers})
	if err != nil {
		return "", err
	}
	out, ok := value.(*toolread.ToolCallsOutput)
	if !ok || out == nil {
		return "", fmt.Errorf("tool call reader returned %T", value)
	}
	for _, row := range out.Data {
		if row != nil && row.TraceId != nil {
			return strings.TrimSpace(*row.TraceId), nil
		}
	}
	return "", nil
}
