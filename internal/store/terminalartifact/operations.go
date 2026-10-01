package terminalartifact

import (
	"context"
	"fmt"
	"reflect"
	"time"

	msgread "github.com/viant/agently-core/internal/datly/message/read"
	msgwrite "github.com/viant/agently-core/internal/datly/message/write"
	modelread "github.com/viant/agently-core/internal/datly/modelcall/read"
	modelwrite "github.com/viant/agently-core/internal/datly/modelcall/write"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	toolread "github.com/viant/agently-core/internal/datly/toolcall/read"
	toolwrite "github.com/viant/agently-core/internal/datly/toolcall/write"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

func target(typ reflect.Type, method, path string) dexec.ComponentTarget {
	return dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: typ.PkgPath(), Name: map[string]string{"GET": "reader", "PATCH": "writer"}[method]}, Route: spec.RouteRef{Method: method, Path: path}}
}

var modelReader = target(reflect.TypeFor[modelread.ReaderComponent](), "GET", "/v1/internal/agently/model-call")
var toolReader = target(reflect.TypeFor[toolread.ReaderComponent](), "GET", "/v1/internal/agently/tool-call")
var messageReader = target(reflect.TypeFor[msgread.ReaderComponent](), "GET", "/v1/internal/agently/message")
var turnReader = target(reflect.TypeFor[turnread.ReaderComponent](), "GET", "/v1/api/agently/turn/list/list")
var modelWriter = target(reflect.TypeFor[modelwrite.WriterComponent](), "PATCH", "/v1/api/agently/modelcall")
var toolWriter = target(reflect.TypeFor[toolwrite.WriterComponent](), "PATCH", "/v1/api/agently/toolcall")
var messageWriter = target(reflect.TypeFor[msgwrite.WriterComponent](), "PATCH", "/v1/api/agently/message")

func readProviders(kind string, fields []string) []locator.Provider {
	owner := ""
	return []locator.Provider{provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "internal":
			return true, true, nil
		case "mode":
			return "rows", true, nil
		}
		return nil, false, nil
	}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }), queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: fields}}})}
}

type artifactState struct{ status, cleanupStatus, turnID, runID string }
type turnState struct{ status, cleanupStatus, conversationID, runID string }

func readArtifact(ctx context.Context, invoker dexec.ComponentInvoker, candidate Candidate) (artifactState, bool, error) {
	switch candidate.Kind {
	case Message:
		return readMessage(ctx, invoker, candidate.ID)
	case ModelCall:
		input := &modelread.ModelCallsInput{}
		input.SetMessageId(candidate.ID)
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, true), Target: modelReader, Input: input, Providers: readProviders("modelcallaccess", []string{"message_id", "status", "cleanup_status", "turn_id", "run_id"})})
		if err != nil {
			return artifactState{}, false, err
		}
		output, ok := value.(*modelread.ModelCallsOutput)
		if !ok || output == nil {
			return artifactState{}, false, fmt.Errorf("model call reader returned %T", value)
		}
		if len(output.Data) == 0 {
			return artifactState{}, false, nil
		}
		if len(output.Data) != 1 || output.Data[0] == nil {
			return artifactState{}, false, fmt.Errorf("model call identity returned %d rows", len(output.Data))
		}
		r := output.Data[0]
		return artifactState{status: r.Status, cleanupStatus: r.CleanupStatus, turnID: stringValue(r.TurnId), runID: stringValue(r.RunId)}, true, nil
	case ToolCall:
		input := &toolread.ToolCallsInput{}
		input.SetMessageId(candidate.ID)
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, true), Target: toolReader, Input: input, Providers: readProviders("toolcallaccess", []string{"message_id", "status", "cleanup_status", "turn_id", "run_id"})})
		if err != nil {
			return artifactState{}, false, err
		}
		output, ok := value.(*toolread.ToolCallsOutput)
		if !ok || output == nil {
			return artifactState{}, false, fmt.Errorf("tool call reader returned %T", value)
		}
		if len(output.Data) == 0 {
			return artifactState{}, false, nil
		}
		if len(output.Data) != 1 || output.Data[0] == nil {
			return artifactState{}, false, fmt.Errorf("tool call identity returned %d rows", len(output.Data))
		}
		r := output.Data[0]
		return artifactState{status: r.Status, cleanupStatus: r.CleanupStatus, turnID: stringValue(r.TurnId), runID: stringValue(r.RunId)}, true, nil
	}
	return artifactState{}, false, fmt.Errorf("terminal artifact candidate kind is invalid")
}
func readMessage(ctx context.Context, invoker dexec.ComponentInvoker, id string) (artifactState, bool, error) {
	input := &msgread.MessagesInput{}
	input.SetId(id)
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, true), Target: messageReader, Input: input, Providers: readProviders("messageaccess", []string{"id", "status", "cleanup_status", "turn_id"})})
	if err != nil {
		return artifactState{}, false, err
	}
	output, ok := value.(*msgread.MessagesOutput)
	if !ok || output == nil {
		return artifactState{}, false, fmt.Errorf("message reader returned %T", value)
	}
	if len(output.Data) == 0 {
		return artifactState{}, false, nil
	}
	if len(output.Data) != 1 || output.Data[0] == nil {
		return artifactState{}, false, fmt.Errorf("message identity returned %d rows", len(output.Data))
	}
	r := output.Data[0]
	return artifactState{status: stringValue(r.Status), cleanupStatus: r.CleanupStatus, turnID: stringValue(r.TurnId)}, true, nil
}
func readTurn(ctx context.Context, invoker dexec.ComponentInvoker, id string) (turnState, bool, error) {
	input := &turnread.TurnRowsInput{}
	input.SetTurnId(id)
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, true), Target: turnReader, Input: input, Providers: readProviders("turnaccess", []string{"id", "status", "cleanup_status", "conversation_id", "run_id"})})
	if err != nil {
		return turnState{}, false, err
	}
	output, ok := value.(*turnread.TurnRowsOutput)
	if !ok || output == nil {
		return turnState{}, false, fmt.Errorf("turn reader returned %T", value)
	}
	if len(output.Data) == 0 {
		return turnState{}, false, nil
	}
	if len(output.Data) != 1 || output.Data[0] == nil {
		return turnState{}, false, fmt.Errorf("turn identity returned %d rows", len(output.Data))
	}
	r := output.Data[0]
	return turnState{status: r.Status, cleanupStatus: r.CleanupStatus, conversationID: r.ConversationId, runID: stringValue(r.RunId)}, true, nil
}
func repair(ctx context.Context, invoker dexec.ComponentInvoker, candidate Candidate, completedAt time.Time) error {
	reason := candidate.Reason
	switch candidate.Kind {
	case ModelCall:
		row := &modelwrite.ModelCall{}
		row.SetMessageId(candidate.ID)
		row.SetStatus("failed")
		row.SetErrorMessage(&reason)
		row.SetCompletedAt(&completedAt)
		input := &modelwrite.Input{}
		input.SetModelCalls([]*modelwrite.ModelCall{row})
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: modelWriter, Input: input})
		if err != nil {
			return err
		}
		if output, ok := value.(*modelwrite.Output); !ok || output == nil {
			return fmt.Errorf("model writer returned %T", value)
		}
	case ToolCall:
		if candidate.Linkage == MessageTurn {
			reason = "tool message terminalized after turn ended"
		}
		row := &toolwrite.ToolCall{}
		row.SetMessageId(candidate.ID)
		row.SetStatus("failed")
		row.SetErrorMessage(&reason)
		row.SetCompletedAt(&completedAt)
		input := &toolwrite.Input{}
		input.SetToolCalls([]*toolwrite.ToolCall{row})
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: toolWriter, Input: input})
		if err != nil {
			return err
		}
		if output, ok := value.(*toolwrite.Output); !ok || output == nil {
			return fmt.Errorf("tool writer returned %T", value)
		}
	case Message:
		status := "failed"
		row := &msgwrite.Message{}
		row.SetId(candidate.ID)
		row.SetStatus(&status)
		row.SetUpdatedAt(&completedAt)
		input := &msgwrite.Input{}
		input.SetMessages([]*msgwrite.Message{row})
		providers := []locator.Provider{provider.Named("messageaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "terminalCleanup" {
				return true, true, nil
			}
			return nil, false, nil
		})}
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: messageWriter, Input: input, Providers: providers})
		if err != nil {
			return err
		}
		if output, ok := value.(*msgwrite.Output); !ok || output == nil {
			return fmt.Errorf("message writer returned %T", value)
		}
	}
	return nil
}
