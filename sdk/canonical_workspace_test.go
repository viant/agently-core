package sdk

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	conversationmodel "github.com/viant/agently-core/model/conversation"
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
	encoded := workspaceToolResponsePayload(&conversationmodel.ModelCallStreamPayloadView{InlineBody: &inline, Compression: "gzip"})
	if string(workspaceResponseBody(encoded)) != body {
		t.Fatalf("compressed attachment was lost: %s", encoded)
	}
}

func TestWorkspaceShowAttachesToLatestFinalResponse(t *testing.T) {
	object := &workspaceproto.Object{Version: 1, ObjectID: "workspace:advertisers", Origin: workspaceproto.Origin{TurnID: "first"}, Lifecycle: workspaceproto.Lifecycle{State: "ready"}}
	firstPayload, _ := json.Marshal(map[string]interface{}{"ok": true, "workspaceObject": object})
	first := &TurnState{TurnID: "first", Messages: []*TurnMessageState{{MessageID: "answer-1", Role: "assistant", Content: "Opened."}}, Execution: &ExecutionState{Pages: []*ExecutionPageState{{ToolSteps: []*ToolStepState{{ToolName: "ui/view/open", Status: "completed", ResponsePayload: firstPayload}}}}}}
	object.LastActivatedBy = workspaceproto.Origin{TurnID: "second", ToolName: "ui/window/show"}
	showPayload, _ := json.Marshal(map[string]interface{}{"ok": true, "workspaceObject": object})
	second := &TurnState{TurnID: "second", Messages: []*TurnMessageState{{MessageID: "answer-2", Role: "assistant", Content: "Filtered to Whoop."}}, Execution: &ExecutionState{Pages: []*ExecutionPageState{{ToolSteps: []*ToolStepState{{ToolName: "ui/window/show", Status: "completed", ResponsePayload: showPayload}}}}}}
	projectWorkspaceAttachments(&ConversationState{Turns: []*TurnState{first, second}})
	if len(first.Messages[0].Attachments) != 1 || len(second.Messages[0].Attachments) != 1 {
		t.Fatal("each acknowledged reference needs its own final-message attachment")
	}
	got := second.Messages[0].Attachments[0].WorkspaceObject
	if got.Origin.TurnID != "first" || got.LastActivatedBy.TurnID != "second" || got.LastActivatedBy.MessageID != "answer-2" {
		t.Fatalf("incorrect activation owner: %+v", got)
	}
}
