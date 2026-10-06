package toolexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/protocol/mcpname"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/clienttool"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

func deferClientTool(ctx context.Context, session *clienttool.Session, step StepInfo, conv apiconv.Client, turn runtimerequestctx.TurnMeta, startedAt time.Time) (llm.ToolCall, error) {
	if runtimerequestctx.ModelMessageIDFromContext(ctx) == "" {
		if assistantID := runtimerequestctx.TurnModelMessageID(turn.TurnID); assistantID != "" {
			ctx = runtimerequestctx.WithModelMessageID(ctx, assistantID)
		}
	}
	pending, err := session.DeferForTurn(turn.ConversationID, turn.TurnID, step.ID, step.Name, step.Args, func() (clienttool.PendingCall, error) {
		if conv == nil {
			return clienttool.PendingCall{}, fmt.Errorf("client tool persistence requires a conversation store")
		}
		messageID, err := createToolMessage(ctx, conv, turn, startedAt, step.Name)
		if err != nil {
			return clienttool.PendingCall{}, err
		}
		fail := func(err error) (clienttool.PendingCall, error) {
			finCtx, cancel := detachedFinalizeCtx(ctx)
			defer cancel()
			_ = completeToolCall(finCtx, conv, messageID, step.ID, step.Name, "failed", time.Now(), "", err.Error())
			return clienttool.PendingCall{}, err
		}
		if err = initToolCall(ctx, conv, messageID, step.ID, turn, step.Name, startedAt, step.ResponseID); err != nil {
			return fail(err)
		}
		argsJSON, err := json.Marshal(step.Args)
		if err != nil {
			return fail(err)
		}
		requestID, err := createInlinePayload(ctx, conv, "tool_request", "application/json", argsJSON)
		if err != nil {
			return fail(err)
		}
		requestLink := apiconv.NewToolCall()
		requestLink.SetMessageID(messageID)
		requestLink.SetOpID(step.ID)
		requestLink.RequestPayloadID = &requestID
		requestLink.Has.RequestPayloadID = true
		if err = conv.PatchToolCall(ctx, requestLink); err != nil {
			return fail(err)
		}
		// This is an open call, with neither completion timestamp nor response payload.
		update := apiconv.NewToolCall()
		update.SetMessageID(messageID)
		update.SetOpID(step.ID)
		update.SetTurnID(turn.TurnID)
		update.SetStatus("waiting_for_user")
		update.SetToolName(step.Name)
		update.RequestPayloadID = &requestID
		update.Has.RequestPayloadID = true
		if err = conv.PatchToolCall(ctx, update); err != nil {
			return fail(err)
		}
		if err = updateToolMessageStatus(ctx, conv, messageID, "waiting_for_user"); err != nil {
			return fail(err)
		}
		iteration := 0
		if meta, ok := runtimerequestctx.RunMetaFromContext(ctx); ok {
			iteration = meta.Iteration
		}
		return clienttool.PendingCall{ToolMessageID: messageID, AssistantMessageID: runtimerequestctx.ModelMessageIDFromContext(ctx), ConversationID: turn.ConversationID, TurnID: turn.TurnID, Iteration: iteration}, nil
	})
	if err != nil {
		return llm.ToolCall{}, err
	}
	return llm.ToolCall{ID: pending.ID, Name: pending.Name, Arguments: pending.Arguments, ResultMessageID: pending.ToolMessageID}, nil
}

// CompleteClientTool writes the result onto the original deferred tool message.
// The caller must hold its durable protocol continuation claim and verify the
// protocol result hash before calling. Repeated identical completed results are
// accepted; a conflicting result or call identity is rejected.
func CompleteClientTool(ctx context.Context, conv apiconv.Client, pending clienttool.PendingCall, content string) error {
	return completeClientToolContent(ctx, conv, pending, content, "text/plain", "")
}
func completeClientToolContent(ctx context.Context, conv apiconv.Client, pending clienttool.PendingCall, content, mime, toolError string) error {
	if conv == nil || pending.ToolMessageID == "" || pending.ID == "" || pending.TurnID == "" || pending.ConversationID == "" {
		return fmt.Errorf("client tool completion requires durable call identity")
	}
	message, err := conv.GetMessage(ctx, pending.ToolMessageID, apiconv.WithIncludeToolCall(true))
	if err != nil {
		return err
	}
	if message == nil || message.ConversationId != pending.ConversationID || message.TurnId == nil || *message.TurnId != pending.TurnID || message.Role != "tool" {
		return fmt.Errorf("client tool completion message identity mismatch")
	}
	var call *apiconv.ToolCallView
	if raw := message.MessageToolCall; raw != nil {
		call = &apiconv.ToolCallView{OpId: raw.OpId, Status: raw.Status, ErrorMessage: raw.ErrorMessage, ToolName: raw.ToolName}
	}
	if call == nil {
		for _, entry := range message.ToolMessage {
			if entry != nil && entry.ToolCall != nil {
				call = entry.ToolCall
				break
			}
		}
	}
	if pending.AssistantMessageID != "" && (message.ParentMessageId == nil || *message.ParentMessageId != pending.AssistantMessageID) {
		return fmt.Errorf("client tool completion assistant identity mismatch")
	}
	if call == nil || call.OpId != pending.ID {
		return fmt.Errorf("client tool completion call identity mismatch")
	}
	if strings.ToLower(mcpname.Canonical(call.ToolName)) != strings.ToLower(mcpname.Canonical(pending.Name)) {
		return fmt.Errorf("client tool completion tool identity mismatch")
	}
	if call.Status == "completed" || call.Status == "failed" {
		existingError := ""
		if call.ErrorMessage != nil {
			existingError = *call.ErrorMessage
		}
		if message.GetContent() == content && existingError == toolError {
			return nil
		}
		return fmt.Errorf("client tool already completed with a different result")
	}
	if call.Status != "waiting_for_user" {
		return fmt.Errorf("client tool is not waiting for a result: %s", call.Status)
	}
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{ConversationID: pending.ConversationID, TurnID: pending.TurnID})
	responseID, err := createInlinePayload(ctx, conv, "tool_response", mime, []byte(content))
	if err != nil {
		return err
	}
	if mime == clienttool.ContentMIME || toolError != "" {
		marker := apiconv.NewMessage()
		marker.SetId(pending.ToolMessageID)
		mimeMarker := clienttool.ContentMIME
		if mime != clienttool.ContentMIME {
			mimeMarker = clienttool.ToolErrorMIME
		}
		marker.ContextSummary = &mimeMarker
		marker.Has.ContextSummary = true
		if err = conv.PatchMessage(ctx, marker); err != nil {
			return err
		}
	}
	if err = updateToolMessageContent(ctx, conv, pending.ToolMessageID, content); err != nil {
		return err
	}
	status := "completed"
	if toolError != "" {
		status = "failed"
	}
	return completeToolCall(ctx, conv, pending.ToolMessageID, pending.ID, pending.Name, status, time.Now(), responseID, toolError)
}

// CompleteClientToolMessage persists ordered protocol content verbatim. The
// binder maps this MIME type back to ordered native content items on continuation.
func CompleteClientToolMessage(ctx context.Context, conv apiconv.Client, pending clienttool.PendingCall, content json.RawMessage) error {
	return CompleteClientToolResult(ctx, conv, pending, content, "")
}

func CompleteClientToolResult(ctx context.Context, conv apiconv.Client, pending clienttool.PendingCall, content json.RawMessage, toolError string) error {
	if _, err := clienttool.MapContent(content); err != nil {
		return err
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return completeClientToolContent(ctx, conv, pending, text, "text/plain", toolError)
	}
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, content); err != nil {
		return err
	}
	return completeClientToolContent(ctx, conv, pending, compact.String(), clienttool.ContentMIME, toolError)
}

func CompleteDependency(ctx context.Context, conv apiconv.Client, dependency clienttool.Dependency, result clienttool.ChildResult) error {
	content, toolError, err := clienttool.FormatDependencyResult(dependency, result)
	if err != nil {
		return err
	}
	return completeClientToolContent(ctx, conv, dependency.ParentCall, content, "text/plain", toolError)
}
