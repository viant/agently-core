package conversation

import (
	"context"
	"testing"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	convwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	toolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
)

func TestGetMessage_IncludeToolCall_PreservesMessageToolCall(t *testing.T) {
	ctx := context.Background()

	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	dao, err := NewDatly(ctx)
	if err != nil {
		t.Fatalf("NewDatly: %v", err)
	}
	svc, err := New(ctx, dao)
	if err != nil {
		t.Fatalf("conversation.New: %v", err)
	}

	convID := "conv_msg_relation"
	conv := &convcli.MutableConversation{}
	conv.Has = &convwrite.ConversationHas{}
	conv.SetId(convID)
	conv.SetVisibility("private")
	if err := svc.PatchConversations(ctx, conv); err != nil {
		t.Fatalf("PatchConversations: %v", err)
	}

	msgID := "msg_tool_result"
	msg := convcli.NewMessage()
	msg.SetId(msgID)
	msg.SetConversationID(convID)
	msg.SetRole("tool")
	msg.SetType("tool_op")
	msg.SetCreatedAt(time.Now())
	msg.SetContent(`{"plan":[{"step":"Inspect","status":"in_progress"}]}`)
	if err := svc.PatchMessage(ctx, (*convcli.MutableMessage)(msg)); err != nil {
		t.Fatalf("PatchMessage: %v", err)
	}

	opID := "call_plan_1"
	toolName := "orchestration/updatePlan"
	tc := &convcli.MutableToolCall{}
	tc.SetMessageID(msgID)
	tc.SetOpID(opID)
	tc.SetAttempt(1)
	tc.SetToolName(toolName)
	tc.SetToolKind("general")
	tc.SetStatus("completed")
	tc.Has = &toolcallwrite.ToolCallHas{
		MessageID: true,
		OpID:      true,
		Attempt:   true,
		ToolName:  true,
		ToolKind:  true,
		Status:    true,
	}
	if err := svc.PatchToolCall(ctx, tc); err != nil {
		t.Fatalf("PatchToolCall: %v", err)
	}

	got, err := svc.GetMessage(ctx, msgID, convcli.WithIncludeToolCall(true))
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got == nil {
		t.Fatalf("expected message, got nil")
	}
	if got.MessageToolCall == nil {
		t.Fatalf("expected MessageToolCall to be populated")
	}
	if got.MessageToolCall.OpId != opID {
		t.Fatalf("MessageToolCall.OpId = %q, want %q", got.MessageToolCall.OpId, opID)
	}
	if got.MessageToolCall.ToolName != toolName {
		t.Fatalf("MessageToolCall.ToolName = %q, want %q", got.MessageToolCall.ToolName, toolName)
	}
}
