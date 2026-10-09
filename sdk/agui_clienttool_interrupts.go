package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/service/browsermcp"
)

func aguiClientToolInterrupt(call clienttool.PendingCall) agui.WireInterrupt {
	id := aguiPendingCallID(call)
	metadata := agui.WireMetadata{"agently": rawAGUI(map[string]any{"version": "1", "kind": "client-tool"})}
	schema := map[string]json.RawMessage{"type": rawAGUI("object"), "properties": json.RawMessage(`{"content":{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"object"}}]},"error":{"type":"string"}}`), "required": json.RawMessage(`["content"]`), "additionalProperties": rawAGUI(false)}
	message := "Execute frontend tool " + call.Name
	return agui.WireInterrupt{ID: id, Reason: "agently.client_tool", ToolCallID: &id, Message: &message, Metadata: &metadata, ResponseSchema: &schema}
}
func aguiClientToolAnswer(call clienttool.PendingCall, answer agui.WireResumeEntry) (json.RawMessage, string, error) {
	if answer.InterruptId != aguiPendingCallID(call) {
		return nil, "", fmt.Errorf("client tool answer identity mismatch")
	}
	if answer.Status == "cancelled" {
		return rawAGUI(""), "Frontend tool cancelled", nil
	}
	if err := browsermcp.VerifyResultMetadata(call.Name, call.Metadata, rawAGUI(answer.Metadata)); err != nil {
		return nil, "", err
	}
	if answer.Payload == nil {
		return nil, "", fmt.Errorf("client tool answer requires payload")
	}
	var payload struct {
		Content json.RawMessage `json:"content"`
		Error   string          `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(*answer.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, "", err
	}
	if len(payload.Content) == 0 {
		return nil, "", fmt.Errorf("client tool answer requires content")
	}
	message := rawAGUI(map[string]any{"id": call.ToolMessageID, "role": "tool", "toolCallId": aguiPendingCallID(call), "content": payload.Content})
	if err := agui.ValidateMessage(message); err != nil {
		return nil, "", err
	}
	return payload.Content, payload.Error, nil
}
