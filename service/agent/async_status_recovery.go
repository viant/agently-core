package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/logx"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/pkg/mcpname"
	runtimerecovery "github.com/viant/agently-core/runtime/recovery"
)

func (s *Service) repairResumedAsyncStatusRows(ctx context.Context, input *QueryInput) error {
	if s == nil || s.conversation == nil || s.registry == nil || input == nil {
		return nil
	}
	mode, ok := runtimerecovery.ModeFromContext(ctx)
	if !ok || !strings.EqualFold(strings.TrimSpace(mode), runtimerecovery.ModeResume) {
		return nil
	}
	convID := strings.TrimSpace(input.ConversationID)
	if convID == "" {
		return nil
	}
	conv, err := s.conversation.GetConversation(ctx, convID, apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return err
	}
	repaired := 0
	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil || msg.MessageToolCall == nil {
				continue
			}
			tc := msg.MessageToolCall
			if !sameCanonicalTool(tc.ToolName, "llm/agents:status") {
				continue
			}
			if isTerminalToolStatus(strings.TrimSpace(tc.Status)) {
				continue
			}
			childConvID := childConversationIDFromToolMessage(msg)
			if childConvID == "" {
				continue
			}
			raw, execErr := s.registry.Execute(ctx, "llm/agents:status", map[string]interface{}{"conversationId": childConvID})
			if execErr != nil {
				logx.WarnCtxf(ctx, "conversation", "resume async-status repair status call failed parent=%q child=%q err=%v", convID, childConvID, execErr)
				continue
			}
			toolStatus, terminal, errMsg := terminalToolStatusFromStatusResult(raw)
			if !terminal {
				continue
			}
			if err := s.patchRecoveredAsyncStatusMessage(ctx, msg, tc, raw, toolStatus, errMsg); err != nil {
				logx.WarnCtxf(ctx, "conversation", "resume async-status repair patch failed parent=%q child=%q err=%v", convID, childConvID, err)
				continue
			}
			repaired++
		}
	}
	if repaired > 0 {
		logx.Infof("conversation", "agent.resume async-status repaired convo=%q repaired=%d", convID, repaired)
	}
	return nil
}

func sameCanonicalTool(actual, expected string) bool {
	return strings.EqualFold(strings.TrimSpace(mcpname.Canonical(actual)), strings.TrimSpace(mcpname.Canonical(expected)))
}

func isTerminalToolStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "canceled", "cancelled", "rejected":
		return true
	default:
		return false
	}
}

func stringArgValue(args map[string]interface{}, key string) string {
	if len(args) == 0 {
		return ""
	}
	if raw, ok := args[key]; ok && raw != nil {
		return strings.TrimSpace(fmt.Sprint(raw))
	}
	return ""
}

func childConversationIDFromToolMessage(msg *agconv.MessageView) string {
	if msg == nil || msg.MessageToolCall == nil || msg.MessageToolCall.MessageRequestPayload == nil || msg.MessageToolCall.MessageRequestPayload.InlineBody == nil {
		return ""
	}
	raw := strings.TrimSpace(*msg.MessageToolCall.MessageRequestPayload.InlineBody)
	if raw == "" {
		return ""
	}
	args := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return ""
	}
	return strings.TrimSpace(stringArgValue(args, "conversationId"))
}

func terminalToolStatusFromStatusResult(raw string) (status string, terminal bool, errMsg string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, ""
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", false, ""
	}
	state := strings.ToLower(strings.TrimSpace(stringArgValue(payload, "status")))
	switch state {
	case "succeeded", "completed", "success", "done":
		return "completed", true, ""
	case "failed", "error":
		msg := strings.TrimSpace(stringArgValue(payload, "error"))
		if msg == "" {
			msg = strings.TrimSpace(stringArgValue(payload, "message"))
		}
		return "failed", true, msg
	case "canceled", "cancelled":
		return "canceled", true, ""
	default:
		return "", false, ""
	}
}

func (s *Service) patchRecoveredAsyncStatusMessage(ctx context.Context, msg *agconv.MessageView, tc *agconv.MessageToolCallView, raw, toolStatus, errMsg string) error {
	if s == nil || s.conversation == nil || msg == nil || tc == nil {
		return nil
	}
	respID, err := s.persistInlineToolResponsePayload(ctx, raw)
	if err != nil {
		return err
	}
	msgPatch := apiconv.NewMessage()
	msgPatch.SetId(msg.Id)
	msgPatch.SetContent(raw)
	msgPatch.SetStatus(toolStatus)
	if err := s.conversation.PatchMessage(ctx, msgPatch); err != nil {
		return err
	}

	callPatch := apiconv.NewToolCall()
	callPatch.SetMessageID(msg.Id)
	if tc.TurnId != nil && strings.TrimSpace(*tc.TurnId) != "" {
		callPatch.SetTurnID(strings.TrimSpace(*tc.TurnId))
	}
	callPatch.SetOpID(strings.TrimSpace(tc.OpId))
	callPatch.SetAttempt(tc.Attempt)
	callPatch.SetToolName(strings.TrimSpace(tc.ToolName))
	callPatch.SetToolKind(strings.TrimSpace(tc.ToolKind))
	callPatch.SetStatus(toolStatus)
	now := time.Now()
	callPatch.CompletedAt = &now
	callPatch.Has.CompletedAt = true
	if strings.TrimSpace(respID) != "" {
		callPatch.ResponsePayloadID = &respID
		callPatch.Has.ResponsePayloadID = true
	}
	if strings.TrimSpace(errMsg) != "" {
		callPatch.SetErrorMessage(errMsg)
	}
	return s.conversation.PatchToolCall(ctx, callPatch)
}

func (s *Service) persistInlineToolResponsePayload(ctx context.Context, body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" || s == nil || s.conversation == nil {
		return "", nil
	}
	payload := apiconv.NewPayload()
	payload.SetId(uuid.NewString())
	payload.SetKind("tool_response")
	payload.SetMimeType("text/plain")
	payload.SetSizeBytes(len(body))
	payload.SetStorage("inline")
	payload.SetInlineBody([]byte(body))
	if err := s.conversation.PatchPayload(ctx, payload); err != nil {
		return "", err
	}
	return payload.Id, nil
}
