package convterm

import (
	"context"
	"fmt"
	"strings"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
)

// PatchExecutionTerminal marks non-terminal execution artifacts in the
// supplied conversation transcript with a terminal status. It patches turns,
// the latest assistant message per turn, unfinished model calls, and unfinished
// tool calls. Conversation status and run records remain the caller's
// responsibility.
func PatchExecutionTerminal(ctx context.Context, convClient convcli.Client, conv *convcli.Conversation, status string) error {
	if convClient == nil || conv == nil {
		return nil
	}
	now := time.Now().UTC()
	errs := make([]error, 0)
	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		if !isTerminalExecutionStatus(turn.Status) {
			upd := convcli.NewTurn()
			upd.SetId(strings.TrimSpace(turn.Id))
			upd.SetConversationID(strings.TrimSpace(turn.ConversationId))
			upd.SetStatus(status)
			if err := convClient.PatchTurn(ctx, upd); err != nil {
				errs = append(errs, fmt.Errorf("patch turn %s: %w", strings.TrimSpace(turn.Id), err))
			}
		}
		if assistant := lastAssistantMessage(turn); assistant != nil {
			if assistant.Status == nil || !isTerminalExecutionStatus(*assistant.Status) {
				upd := convcli.NewMessage()
				upd.SetId(strings.TrimSpace(assistant.Id))
				upd.SetConversationID(strings.TrimSpace(assistant.ConversationId))
				if assistant.TurnId != nil && strings.TrimSpace(*assistant.TurnId) != "" {
					upd.SetTurnID(strings.TrimSpace(*assistant.TurnId))
				}
				upd.SetStatus(status)
				if err := convClient.PatchMessage(ctx, upd); err != nil {
					errs = append(errs, fmt.Errorf("patch message %s: %w", strings.TrimSpace(assistant.Id), err))
				}
			}
		}
		for _, msg := range turn.Message {
			if msg == nil {
				continue
			}
			if msg.ModelCall != nil && (msg.ModelCall.CompletedAt == nil || msg.ModelCall.CompletedAt.IsZero()) {
				upd := convcli.NewModelCall()
				upd.SetMessageID(strings.TrimSpace(msg.ModelCall.MessageId))
				if msg.ModelCall.TurnId != nil && strings.TrimSpace(*msg.ModelCall.TurnId) != "" {
					upd.SetTurnID(strings.TrimSpace(*msg.ModelCall.TurnId))
				}
				upd.SetStatus(modelCallFinalStatus(msg.ModelCall.Status))
				upd.SetCompletedAt(now)
				if err := convClient.PatchModelCall(ctx, upd); err != nil {
					errs = append(errs, fmt.Errorf("patch model call %s: %w", strings.TrimSpace(msg.ModelCall.MessageId), err))
				}
			}
			for _, toolMsg := range msg.ToolMessage {
				if toolMsg == nil || toolMsg.ToolCall == nil {
					continue
				}
				if toolMsg.ToolCall.CompletedAt != nil && !toolMsg.ToolCall.CompletedAt.IsZero() {
					continue
				}
				upd := convcli.NewToolCall()
				upd.SetMessageID(strings.TrimSpace(toolMsg.ToolCall.MessageId))
				upd.SetOpID(strings.TrimSpace(toolMsg.ToolCall.OpId))
				if toolMsg.ToolCall.TurnId != nil && strings.TrimSpace(*toolMsg.ToolCall.TurnId) != "" {
					upd.SetTurnID(strings.TrimSpace(*toolMsg.ToolCall.TurnId))
				}
				upd.SetStatus(toolCallFinalStatus(toolMsg.ToolCall.Status))
				upd.CompletedAt = timePtrUTC(now)
				upd.Has.CompletedAt = true
				if err := convClient.PatchToolCall(ctx, upd); err != nil {
					errs = append(errs, fmt.Errorf("patch tool call %s: %w", strings.TrimSpace(toolMsg.ToolCall.MessageId), err))
				}
			}
		}
	}
	return joinErrors(errs...)
}

func lastAssistantMessage(turn *convcli.Turn) *convcli.Message {
	if turn == nil || len(turn.Message) == 0 {
		return nil
	}
	for i := len(turn.Message) - 1; i >= 0; i-- {
		msg := turn.Message[i]
		if msg != nil && strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			return (*convcli.Message)(msg)
		}
	}
	return nil
}

func modelCallFinalStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return "failed"
	case "succeeded", "success", "completed", "done":
		return "succeeded"
	default:
		return "canceled"
	}
}

func toolCallFinalStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return "failed"
	case "succeeded", "success", "completed", "done":
		return "succeeded"
	default:
		return "canceled"
	}
}

func isTerminalExecutionStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "canceled", "cancel", "failed", "error", "succeeded", "success", "completed", "done", "rejected":
		return true
	default:
		return false
	}
}

func timePtrUTC(v time.Time) *time.Time {
	value := v.UTC()
	return &value
}

func joinErrors(errs ...error) error {
	var filtered []error
	for _, err := range errs {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return fmt.Errorf("%v", filtered)
}
