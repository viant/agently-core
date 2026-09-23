package sdk

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"testing"

	workspaceproto "github.com/viant/agently-core/protocol/ui/workspace"
)

func TestWorkspaceAttachmentsRequireAcknowledgedResultAndFinalOwner(t *testing.T) {
	object := &workspaceproto.Object{Version: 1, ObjectID: "workspace:resource-1", Origin: workspaceproto.Origin{TurnID: "turn-1"}, Lifecycle: workspaceproto.Lifecycle{State: "ready"}, Navigation: map[string]string{"label": "Resource"}}
	payload, _ := json.Marshal(map[string]interface{}{"ok": true, "workspaceObject": object})
	turn := &TurnState{TurnID: "turn-1", Messages: []*TurnMessageState{{MessageID: "final-1", Role: "assistant", Content: "The resource is open."}}, Execution: &ExecutionState{Pages: []*ExecutionPageState{{ToolSteps: []*ToolStepState{{ToolName: "ui/view/open", Status: "completed", ResponsePayload: payload}}}}}}
	state := &ConversationState{Turns: []*TurnState{turn}}
	projectWorkspaceAttachments(state)
	if len(turn.Messages[0].Attachments) != 1 {
		t.Fatalf("missing structured attachment: %+v", turn.Messages[0])
	}
	attachment := turn.Messages[0].Attachments[0]
	if attachment.ObjectID != object.ObjectID || attachment.WorkspaceObject.Origin.MessageID != "final-1" {
		t.Fatalf("wrong owner: %+v", attachment)
	}
	turn.Messages[0].Attachments = nil
	object.Lifecycle.State = "opening"
	payload, _ = json.Marshal(map[string]interface{}{"ok": true, "workspaceObject": object})
	turn.Execution.Pages[0].ToolSteps[0].ResponsePayload = payload
	projectWorkspaceAttachments(state)
	if len(turn.Messages[0].Attachments) != 1 || turn.Messages[0].Attachments[0].WorkspaceObject.Lifecycle.State != "opening" {
		t.Fatal("accepted navigation must retain a reference while content loads")
	}
	turn.Messages[0].Attachments = nil
	payload, _ = json.Marshal(map[string]interface{}{"ok": false, "workspaceObject": object})
	turn.Execution.Pages[0].ToolSteps[0].ResponsePayload = payload
	projectWorkspaceAttachments(state)
	if len(turn.Messages[0].Attachments) != 0 {
		t.Fatal("rejected open claimed success")
	}
}

func TestWorkspaceToolResponsePreservesCompressedJSON(t *testing.T) {
	body := `{"ok":true,"workspaceObject":{"version":1,"objectId":"workspace:compressed","lifecycle":{"state":"ready"}}}`
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	inline := compressed.String()
	encoded := workspaceToolResponsePayload(&agconv.ModelCallStreamPayloadView{InlineBody: &inline, Compression: "gzip"})
	if string(workspaceResponseBody(encoded)) != body {
		t.Fatalf("compressed attachment was lost: %s", encoded)
	}
}
