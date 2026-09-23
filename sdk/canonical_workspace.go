package sdk

import (
	"encoding/json"
	convstore "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"strings"

	workspaceproto "github.com/viant/agently-core/protocol/ui/workspace"
	"github.com/viant/agently-core/sdk/api"
)

// projectWorkspaceAttachments derives durable message attachments from structured,
// acknowledged tool results. It neither parses prose nor executes restored actions.
func projectWorkspaceAttachments(state *ConversationState) {
	seen := map[string]bool{}
	for _, turn := range state.Turns {
		if turn == nil || turn.Execution == nil {
			continue
		}
		var ownerID string
		var owner *api.TurnMessageState
		for _, message := range turn.Messages {
			if message != nil && message.Role == "assistant" && message.Interim == 0 && strings.TrimSpace(message.Content) != "" {
				owner = message
			}
		}
		if owner != nil {
			ownerID = owner.MessageID
		} else if turn.Assistant != nil && turn.Assistant.Final != nil {
			ownerID = turn.Assistant.Final.MessageID
		}
		if ownerID == "" {
			continue
		}
		var attachments []api.WorkspaceAttachment
		for _, page := range turn.Execution.Pages {
			if page == nil {
				continue
			}
			for _, step := range page.ToolSteps {
				if step == nil || string(step.Status) != "completed" {
					continue
				}
				name := strings.ReplaceAll(strings.ToLower(step.ToolName), ":", "/")
				if name != "ui/view/open" && name != "ui/window/open" {
					continue
				}
				payload := workspaceResponseBody(step.ResponsePayload)
				var result struct {
					OK              bool                   `json:"ok"`
					WorkspaceObject *workspaceproto.Object `json:"workspaceObject"`
					Items           []struct {
						WorkspaceObject *workspaceproto.Object `json:"workspaceObject"`
					} `json:"items"`
				}
				if json.Unmarshal(payload, &result) != nil || !result.OK {
					continue
				}
				objects := []*workspaceproto.Object{result.WorkspaceObject}
				if len(result.Items) > 0 {
					objects = nil
					for _, item := range result.Items {
						objects = append(objects, item.WorkspaceObject)
					}
				}
				for _, object := range objects {
					if object == nil || object.ObjectID == "" || (object.Lifecycle.State != "ready" && object.Lifecycle.State != "opening") || seen[object.ObjectID] {
						continue
					}
					if object.Origin.TurnID != "" && object.Origin.TurnID != turn.TurnID {
						continue
					}
					seen[object.ObjectID] = true
					cloned := *object
					cloned.Origin.TurnID = turn.TurnID
					cloned.Origin.MessageID = ownerID
					label := object.Navigation["label"]
					if label == "" {
						label = object.Content.WindowKey
					}
					if label == "" {
						label = "Workspace"
					}
					attachments = append(attachments, api.WorkspaceAttachment{Kind: "workspaceObject", ObjectID: object.ObjectID, Label: label, Icon: object.Navigation["icon"], WorkspaceObject: &cloned})
				}
			}
		}
		if len(attachments) == 0 {
			continue
		}
		if owner != nil {
			owner.Attachments = attachments
		} else {
			turn.Assistant.Final.Attachments = attachments
		}
	}
}

func workspaceResponseBody(raw json.RawMessage) json.RawMessage {
	var envelope struct {
		InlineBody string `json:"inlineBody"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.InlineBody != "" {
		return json.RawMessage(envelope.InlineBody)
	}
	return raw
}

// Decode binary gzip before JSON serialization can replace non-UTF8 bytes.
func workspaceToolResponsePayload(payload *agconv.ModelCallStreamPayloadView) json.RawMessage {
	if payload == nil {
		return nil
	}
	if payload.InlineBody != nil && strings.EqualFold(payload.Compression, "gzip") {
		decoded := convstore.DecodeInlineBody(*payload.InlineBody, payload.Compression)
		copied := *payload
		copied.InlineBody = &decoded
		copied.Compression = ""
		return marshalToRawJSON(&copied)
	}
	return marshalToRawJSON(payload)
}
