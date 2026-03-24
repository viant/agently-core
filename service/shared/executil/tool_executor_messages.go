package executil

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	"github.com/viant/agently-core/runtime/memory"
)

// createToolMessage persists a new tool message and returns its ID.
// The tool message's parent_message_id is set to the interim assistant
// message (from ModelMessageIDFromContext) so the UI can group tool calls
// under the correct model-call iteration. Falls back to the turn's
// ParentMessageID when the model message ID is not in context.
func createToolMessage(ctx context.Context, conv apiconv.Client, turn memory.TurnMeta, startedAt time.Time, toolName string) (string, error) {
	toolMsgID := uuid.New().String()
	displayName := mcpname.Display(toolName)
	opts := []apiconv.MessageOption{
		apiconv.WithId(toolMsgID),
		apiconv.WithRole("tool"),
		apiconv.WithType("tool_op"),
		apiconv.WithStatus("running"),
		apiconv.WithCreatedAt(startedAt),
	}
	if name := strings.TrimSpace(displayName); name != "" {
		opts = append(opts, apiconv.WithToolName(name))
	}
	if runMeta, ok := memory.RunMetaFromContext(ctx); ok && runMeta.Iteration > 0 {
		opts = append(opts, apiconv.WithIteration(runMeta.Iteration))
	}
	if IsChainMode(ctx) {
		opts = append(opts, apiconv.WithMode("chain"))
	}
	// Override parent_message_id to point to the assistant message that
	// triggered this tool call. The model message ID is set in context by
	// OnCallStart and propagated via launchPendingSteps. This enables the
	// tool_message.sql JOIN to group tool calls under the correct iteration.
	if id := strings.TrimSpace(memory.ModelMessageIDFromContext(ctx)); id != "" {
		opts = append(opts, apiconv.WithParentMessageID(id))
	}
	msg, err := apiconv.AddMessage(ctx, conv, &turn, opts...)
	if err != nil {
		return "", fmt.Errorf("persist tool message: %w", err)
	}
	return msg.Id, nil
}

func updateToolMessageStatus(ctx context.Context, conv apiconv.Client, toolMsgID, status string) error {
	if conv == nil || strings.TrimSpace(toolMsgID) == "" || strings.TrimSpace(status) == "" {
		return nil
	}
	upd := apiconv.NewMessage()
	upd.SetId(toolMsgID)
	upd.SetStatus(status)
	return conv.PatchMessage(ctx, upd)
}

// initToolCall initializes and persists a new tool call in a 'running' state for the given tool message.
func initToolCall(ctx context.Context, conv apiconv.Client, toolMsgID, opID string, turn memory.TurnMeta, toolName string, startedAt time.Time, traceID string) error {
	displayName := mcpname.Display(toolName)
	tc := apiconv.NewToolCall()
	tc.SetMessageID(toolMsgID)
	if opID != "" {
		tc.SetOpID(opID)
	}
	if turn.TurnID != "" {
		tc.TurnID = &turn.TurnID
		tc.Has.TurnID = true
	}
	tc.SetToolName(displayName)
	tc.SetToolKind("general")
	tc.SetStatus("running")
	if runMeta, ok := memory.RunMetaFromContext(ctx); ok {
		if strings.TrimSpace(runMeta.RunID) != "" {
			tc.SetRunID(runMeta.RunID)
		}
		if runMeta.Iteration > 0 {
			tc.SetIteration(runMeta.Iteration)
		}
	}

	now := startedAt
	tc.StartedAt = &now
	tc.Has.StartedAt = true
	if trace := strings.TrimSpace(traceID); trace != "" {
		tc.TraceID = &trace
		tc.Has.TraceID = true
	} else if trace := strings.TrimSpace(memory.TurnTrace(turn.TurnID)); trace != "" {
		tc.TraceID = &trace
		tc.Has.TraceID = true
	}
	if err := conv.PatchToolCall(ctx, tc); err != nil {
		return fmt.Errorf("persist tool call start: %w", err)
	}

	if err := conv.PatchConversations(ctx, convw.NewConversationStatus(turn.ConversationID, "running")); err != nil {
		return fmt.Errorf("failed to update conversation: %w", err)
	}
	return nil
}

// completeToolCall marks the tool call as finished and attaches the response payload and error message.
func completeToolCall(ctx context.Context, conv apiconv.Client, toolMsgID, opID, toolName, status string, completedAt time.Time, respPayloadID string, errMsg string) error {
	updTC := apiconv.NewToolCall()
	updTC.SetMessageID(toolMsgID)
	if strings.TrimSpace(opID) != "" {
		updTC.SetOpID(opID)
	}
	if strings.TrimSpace(toolName) != "" {
		updTC.SetToolName(toolName)
	}
	// Propagate turn so the SSE event carries it for UI matching.
	if turn, ok := memory.TurnMetaFromContext(ctx); ok && strings.TrimSpace(turn.TurnID) != "" {
		updTC.SetTurnID(turn.TurnID)
	}
	updTC.SetStatus(status)
	if status == "completed" || status == "failed" || status == "canceled" || status == "cancelled" || status == "queued" {
		done := completedAt
		updTC.CompletedAt = &done
		updTC.Has.CompletedAt = true
	}
	if respPayloadID != "" {
		updTC.ResponsePayloadID = &respPayloadID
		updTC.Has.ResponsePayloadID = true
	}
	if errMsg != "" {
		updTC.ErrorMessage = &errMsg
		updTC.Has.ErrorMessage = true
	}
	if err := conv.PatchToolCall(ctx, updTC); err != nil {
		return err
	}
	msgStatus := status
	if status == "waiting_for_user" {
		msgStatus = "pending"
	}
	return updateToolMessageStatus(ctx, conv, toolMsgID, msgStatus)
}
