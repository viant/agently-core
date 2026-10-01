package conversation

import (
	"context"
	toolread "github.com/viant/agently-core/internal/datly/toolcall/read"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"reflect"
	"testing"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	convwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
)

// TestToolCallTraceByOp_SQLite verifies that the Datly view for reading
// tool_call by op_id returns the persisted trace_id (LLM response.id anchor).
func TestToolCallTraceByOp_SQLite(t *testing.T) {
	ctx := context.Background()

	// Use an isolated workspace with a temp SQLite DB.
	tmp := t.TempDir()
	// Each native host uses the test SQLite workspace.
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	// Ensure no external DB overrides.
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	// Create Datly service and conversation API.
	dao := testNativeInvoker(t, "")
	svc, err := New(ctx, dao)
	if err != nil {
		t.Fatalf("conversation.New: %v", err)
	}

	// Prepare minimal conversation/message/tool_call rows via write components.
	convID := "conv_trace_test"
	conv := &convcli.MutableConversation{}
	// initialize Has marker for setters
	conv.Has = &convwrite.ConversationHas{}
	conv.SetId(convID)
	conv.SetVisibility("private")
	if err := svc.PatchConversations(ctx, conv); err != nil {
		t.Fatalf("PatchConversations: %v", err)
	}

	msgID := "msg_1"
	now := time.Now()
	msg := convcli.NewMessage()
	msg.SetId(msgID)
	msg.SetConversationID(convID)
	msg.SetRole("tool")
	msg.SetType("tool_op")
	msg.SetCreatedAt(now)
	if err := svc.PatchMessage(ctx, (*convcli.MutableMessage)(msg)); err != nil {
		t.Fatalf("PatchMessage: %v", err)
	}

	opID := "call_abc123"
	trace := "resp_test_anchor_001"
	tc := &convcli.MutableToolCall{}
	tc.SetMessageID(msgID)
	tc.SetOpID(opID)
	tc.SetAttempt(1)
	tc.SetToolName("test/tool")
	tc.SetToolKind("general")
	tc.SetStatus("completed")
	// Persist the trace (anchor)
	tc.TraceID = &trace
	tc.Has.TraceID = true
	if err := svc.PatchToolCall(ctx, tc); err != nil {
		t.Fatalf("PatchToolCall: %v", err)
	}

	// Exercise the read path
	got, err := svc.ToolCallTraceByOp(ctx, convID, opID)
	if err != nil {
		t.Fatalf("ToolCallTraceByOp error: %v", err)
	}
	if got != trace {
		t.Fatalf("expected trace %q, got %q", trace, got)
	}

	// A sparse update must identify the existing row and refresh the submitted trace
	// snapshot without treating omitted required creation fields as replacements.
	updatedTrace := "resp_test_anchor_002"
	patch := convcli.NewToolCall()
	patch.SetMessageID(msgID)
	patch.TraceID = &updatedTrace
	patch.Has.TraceID = true
	if err := svc.PatchToolCall(ctx, patch); err != nil {
		t.Fatalf("sparse trace update: %v", err)
	}
	if patch.MessageID != msgID || patch.TraceID == nil || *patch.TraceID != updatedTrace {
		t.Fatalf("sparse result lost identity/trace: %#v", patch)
	}
	input := &toolread.ToolCallsInput{}
	input.SetConversationId(convID)
	input.SetOpId(opID)
	value, readErr := dao.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[toolread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-call"}},
		Input:  input, Providers: []locator.Provider{provider.Named("toolcallaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "mode":
				return "rows", true, nil
			}
			return nil, false, nil
		})},
	})
	if readErr != nil {
		t.Fatalf("canonical tool resnapshot: %v", readErr)
	}
	snapshot := value.(*toolread.ToolCallsOutput)
	if len(snapshot.Data) != 1 {
		t.Fatalf("canonical row count = %d", len(snapshot.Data))
	}
	if snapshot.Data[0].MessageId != msgID || snapshot.Data[0].ToolName != "test/tool" || snapshot.Data[0].Status != "completed" || snapshot.Data[0].Attempt != 1 {
		t.Fatalf("sparse update changed existing row: %+v", *snapshot.Data[0])
	}
	got, err = svc.ToolCallTraceByOp(ctx, convID, opID)
	if err != nil || got != updatedTrace {
		t.Fatalf("updated trace = %q, error=%v", got, err)
	}
}
