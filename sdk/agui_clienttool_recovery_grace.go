package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
)

// This is a recovery policy, not an inference about the legacy call's original
// timeout. Only the first successfully committed, verified observation starts it.
const clientToolLegacyRecoveryGrace = 3 * time.Minute
const clientToolRecoveryGracePrefix = "client-tool-recovery-grace-"

type pendingRecoveryStore interface {
	AdmitPendingRecovery(context.Context, aguistore.Admission, []json.RawMessage, json.RawMessage) (*aguistore.Run, bool, error)
}

func (c *backendClient) aguiClientToolRecoveryGrace(ctx context.Context, store aguistore.Store, prior *aguistore.Run, pending aguiPending) (bool, error) {
	input, next, eligible, err := prepareClientToolRecoveryGrace(ctx, c.conv, store, prior, pending, time.Now().UTC())
	if err != nil || !eligible {
		return false, err
	}
	writer, ok := store.(pendingRecoveryStore)
	if !ok {
		return false, fmt.Errorf("client tool recovery requires atomic pending journal")
	}
	outcome := recoveryGraceOutcome(next)
	events := []json.RawMessage{
		rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": prior.ThreadID, "runId": input.RunID}),
		rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": prior.ThreadID, "runId": input.RunID, "outcome": outcome}),
	}
	_, _, err = writer.AdmitPendingRecovery(ctx, aguistore.Admission{Principal: prior.Principal, ThreadID: prior.ThreadID, RunID: input.RunID, ParentRunID: prior.RunID, PriorRunID: prior.RunID, ExpectedPriorRevision: prior.Revision, TurnID: prior.TurnID, Input: rawAGUI(input)}, events, rawAGUI(next))
	if errors.Is(err, aguistore.ErrConflict) {
		return false, nil
	}
	return err == nil, err
}

func prepareClientToolRecoveryGrace(ctx context.Context, conv conversation.Client, store aguistore.Store, prior *aguistore.Run, pending aguiPending, observedAt time.Time) (*agui.RunAgentInput, aguiPending, bool, error) {
	empty := aguiPending{}
	if prior == nil || conv == nil || store == nil || prior.Principal == "" || iauth.EffectiveUserID(ctx) != prior.Principal || prior.TurnID == "" || len(pending.Dependencies) > 0 || prior.ResumedByRunID != "" {
		return nil, empty, false, nil
	}
	legacy := false
	for _, call := range pending.ClientTools {
		if call.DeadlineAt == "" {
			legacy = true
		}
	}
	if !legacy {
		return nil, empty, false, nil
	}
	original, err := store.GetRun(ctx, prior.Principal, prior.ThreadID, prior.RunID)
	if err != nil {
		return nil, empty, false, err
	}
	if original.Revision != prior.Revision || original.TurnID != prior.TurnID || original.ConversationID != prior.ConversationID || original.Principal != prior.Principal || original.Status != aguistore.StatusInterrupted || original.ResumedByRunID != "" || !bytes.Equal(original.Pending, rawAGUI(pending)) {
		return nil, empty, false, aguistore.ErrConflict
	}
	owner, err := conv.GetConversation(ctx, prior.ConversationID)
	if err != nil {
		return nil, empty, false, err
	}
	if owner == nil || owner.CreatedByUserId == nil || *owner.CreatedByUserId != prior.Principal {
		return nil, empty, false, fmt.Errorf("client tool recovery owner mismatch")
	}
	var next aguiPending
	if err = json.Unmarshal(original.Pending, &next); err != nil {
		return nil, empty, false, err
	}
	seen := map[string]bool{}
	changed := map[string]string{}
	for index := range next.ClientTools {
		call := &next.ClientTools[index]
		id := aguiPendingCallID(*call)
		if id == "" || seen[id] || call.ID == "" || call.ToolMessageID == "" || call.ConversationID != prior.ConversationID || call.TurnID != prior.TurnID {
			return nil, empty, false, fmt.Errorf("client tool recovery identity mismatch")
		}
		seen[id] = true
		if call.DeadlineAt != "" {
			continue
		}
		if call.RecoveryObservedAt != "" {
			return nil, empty, false, fmt.Errorf("client tool recovery provenance incomplete")
		}
		message, e := conv.GetMessage(ctx, call.ToolMessageID, conversation.WithIncludeToolCall(true))
		if e != nil {
			return nil, empty, false, e
		}
		if message == nil || message.ConversationId != call.ConversationID || message.TurnId == nil || *message.TurnId != call.TurnID || message.Role != "tool" || message.MessageToolCall == nil || message.MessageToolCall.OpId != call.ID || message.MessageToolCall.ToolName != call.Name {
			return nil, empty, false, fmt.Errorf("client tool recovery original call mismatch")
		}
		if call.AssistantMessageID != "" && (message.ParentMessageId == nil || *message.ParentMessageId != call.AssistantMessageID) {
			return nil, empty, false, fmt.Errorf("client tool recovery assistant mismatch")
		}
		if message.MessageToolCall.Status != "waiting_for_user" {
			return nil, empty, false, nil
		}
		call.RecoveryObservedAt = observedAt.UTC().Format(time.RFC3339Nano)
		call.DeadlineAt = observedAt.Add(clientToolLegacyRecoveryGrace).UTC().Format(time.RFC3339Nano)
		changed[id] = call.DeadlineAt
	}
	for index := range next.Interrupts {
		interrupt := &next.Interrupts[index]
		if deadline, ok := changed[interrupt.ID]; ok {
			if interrupt.Reason != "agently.client_tool" {
				return nil, empty, false, fmt.Errorf("client tool recovery interrupt mismatch")
			}
			interrupt.ExpiresAt = &deadline
		}
	}
	var accepted agui.RunAgentInput
	if json.Unmarshal(prior.Input, &accepted) != nil {
		return nil, empty, false, fmt.Errorf("client tool recovery original input invalid")
	}
	// Keep original selection metadata for later ordinary continuation. This
	// recovery journal contains no tool definitions and executes no model/tool.
	var properties map[string]json.RawMessage
	if len(accepted.ForwardedProps) > 0 && json.Unmarshal(accepted.ForwardedProps, &properties) != nil {
		return nil, empty, false, fmt.Errorf("client tool recovery selection invalid")
	}
	if properties == nil {
		properties = map[string]json.RawMessage{}
	}
	properties["clientToolRecoveryGrace"] = rawAGUI(map[string]any{"version": 1, "observedAt": observedAt.UTC().Format(time.RFC3339Nano), "graceSeconds": int(clientToolLegacyRecoveryGrace / time.Second)})
	id := aguiApprovalIdentity(clientToolRecoveryGracePrefix, []string{prior.Principal, prior.ThreadID, prior.RunID})
	input := &agui.RunAgentInput{ThreadID: prior.ThreadID, RunID: id, ParentRunID: prior.RunID, Messages: []agui.Message{}, ForwardedProps: rawAGUI(properties)}
	return input, next, true, nil
}

func recoveryGraceOutcome(pending aguiPending) map[string]any {
	if len(pending.Interrupts) > 0 {
		return map[string]any{"type": "interrupt", "interrupts": pending.Interrupts}
	}
	ids := make([]string, 0, len(pending.ClientTools))
	for _, call := range pending.ClientTools {
		ids = append(ids, aguiPendingCallID(call))
	}
	return map[string]any{"type": "success", "pendingToolCallIds": ids}
}
