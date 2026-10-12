package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
)

const clientToolTimeoutPrefix = "client-tool-timeout-"
const clientToolTimeoutMessage = "Browser/client tool deadline exceeded; outcome is unknown. The original call will not be replayed."

// This is an internal completion producer, never a frontend result-validation
// exception. Deadlines and call ownership come from the original durable run.
func (c *backendClient) aguiClientToolTimeout(ctx context.Context, store aguistore.Store, prior *aguistore.Run, pending aguiPending) (bool, error) {
	input, due, err := prepareClientToolTimeout(ctx, c.conv, store, prior, pending, time.Now().UTC())
	if err != nil || !due {
		return false, err
	}
	if prior.ResumedByRunID != "" && prior.ResumedByRunID != input.RunID {
		return false, nil
	}
	var properties agui.ForwardedProps
	if err = json.Unmarshal(input.ForwardedProps, &properties); err != nil {
		return false, err
	}
	query, err := queryForAGUI(input, prior.Principal, prior.TurnID, properties.Agently, prior)
	if err != nil {
		return false, err
	}
	successor, fresh, err := store.Admit(ctx, aguistore.Admission{Principal: prior.Principal, ThreadID: prior.ThreadID, RunID: input.RunID, ParentRunID: prior.RunID, PriorRunID: prior.RunID, ExpectedPriorRevision: prior.Revision, TurnID: prior.TurnID, Input: rawAGUI(input)})
	if errors.Is(err, aguistore.ErrConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if successor.LeaseUntil != nil && successor.LeaseUntil.After(time.Now()) {
		return true, nil
	}
	workerCtx := aguiApprovalWorkerContext(ctx)
	// The timeout result does not reactivate the expired frontend capability.
	session, err := clienttool.NewSession(nil)
	if err != nil {
		return false, err
	}
	workerCtx = clienttool.WithSession(workerCtx, session)
	if successor.Status == aguistore.StatusRunning {
		go func() { _ = recoverAGUIDurable(workerCtx, c, c, store, successor) }()
	} else if fresh || successor.Status == aguistore.StatusAdmitted {
		go func() { _ = runAGUIDurable(workerCtx, c, c, store, successor, input, query, pending, prior) }()
	}
	return true, nil
}

func prepareClientToolTimeout(ctx context.Context, conv conversation.Client, store aguistore.Store, prior *aguistore.Run, pending aguiPending, now time.Time) (*agui.RunAgentInput, bool, error) {
	if prior == nil || conv == nil || store == nil || iauth.EffectiveUserID(ctx) != prior.Principal || prior.Principal == "" || prior.TurnID == "" || len(pending.ClientTools) == 0 {
		return nil, false, nil
	}
	// Cross-conversation delegated calls belong to the existing dependency
	// coordinator; a root timeout must not replace that graph's ownership.
	if len(pending.Dependencies) > 0 {
		return nil, false, nil
	}
	original, err := store.GetRun(ctx, prior.Principal, prior.ThreadID, prior.RunID)
	if err != nil {
		return nil, false, err
	}
	if original.Revision != prior.Revision || original.Status != aguistore.StatusInterrupted || !bytes.Equal(original.Pending, rawAGUI(pending)) {
		return nil, false, aguistore.ErrConflict
	}
	owner, err := conv.GetConversation(ctx, prior.ConversationID)
	if err != nil {
		return nil, false, err
	}
	if owner == nil || owner.CreatedByUserId == nil || *owner.CreatedByUserId != prior.Principal {
		return nil, false, fmt.Errorf("client tool timeout owner mismatch")
	}
	id := aguiApprovalIdentity(clientToolTimeoutPrefix, []string{prior.Principal, prior.ThreadID, prior.RunID})
	if prior.ResumedByRunID != "" && prior.ResumedByRunID != id {
		return nil, false, nil
	}
	var retainedInput *agui.RunAgentInput
	retainedIDs := map[string]bool{}
	if prior.ResumedByRunID == id {
		retained, e := store.GetRun(ctx, prior.Principal, prior.ThreadID, id)
		if e != nil {
			return nil, false, e
		}
		input := &agui.RunAgentInput{}
		var answers []agui.WireResumeEntry
		if json.Unmarshal(retained.Input, input) != nil || retained.PriorRunID != prior.RunID || input.ThreadID != prior.ThreadID || input.RunID != id || len(input.Tools) != 0 || json.Unmarshal(input.Resume, &answers) != nil || len(answers) == 0 {
			return nil, false, fmt.Errorf("client tool timeout recovery input mismatch")
		}
		for _, answer := range answers {
			if retainedIDs[answer.InterruptId] {
				return nil, false, fmt.Errorf("duplicate retained timeout")
			}
			retainedIDs[answer.InterruptId] = true
		}
		retainedInput = input
	}
	answers := make([]agui.WireResumeEntry, 0, len(pending.ClientTools))
	seen := map[string]bool{}
	for _, call := range pending.ClientTools {
		id := aguiPendingCallID(call)
		if id == "" || seen[id] || call.ID == "" || call.ToolMessageID == "" || call.ConversationID != prior.ConversationID || call.TurnID != prior.TurnID {
			return nil, false, fmt.Errorf("client tool timeout identity mismatch")
		}
		seen[id] = true
		if retainedInput != nil && !retainedIDs[id] {
			continue
		}
		// Legacy calls have no immutable deadline. Do not invent an admission time
		// or substitute the current process's potentially different configuration.
		if call.DeadlineAt == "" {
			continue
		}
		deadline, e := time.Parse(time.RFC3339Nano, call.DeadlineAt)
		if e != nil || deadline.IsZero() {
			return nil, false, fmt.Errorf("client tool timeout deadline invalid")
		}
		if now.Before(deadline) {
			continue
		}
		message, e := conv.GetMessage(ctx, call.ToolMessageID, conversation.WithIncludeToolCall(true))
		if e != nil {
			return nil, false, e
		}
		if message == nil || message.ConversationId != call.ConversationID || message.TurnId == nil || *message.TurnId != call.TurnID || message.Role != "tool" || message.MessageToolCall == nil || message.MessageToolCall.OpId != call.ID || message.MessageToolCall.ToolName != call.Name {
			return nil, false, fmt.Errorf("client tool timeout original call mismatch")
		}
		if call.AssistantMessageID != "" && (message.ParentMessageId == nil || *message.ParentMessageId != call.AssistantMessageID) {
			return nil, false, fmt.Errorf("client tool timeout assistant mismatch")
		}
		state := message.MessageToolCall.Status
		if state != "waiting_for_user" && (state != "failed" || message.MessageToolCall.ErrorMessage == nil || *message.MessageToolCall.ErrorMessage != clientToolTimeoutMessage) {
			return nil, false, nil
		}
		metadata := agui.WireMetadata{}
		if len(call.Metadata) > 0 && json.Unmarshal(call.Metadata, &metadata) != nil {
			return nil, false, fmt.Errorf("client tool timeout metadata invalid")
		}
		content := string(rawAGUI(map[string]string{"code": "clientToolDeadlineExceeded", "dispatchState": "unknown", "outcome": "unknown"}))
		payload := rawAGUI(map[string]any{"content": content, "error": clientToolTimeoutMessage})
		answer := agui.WireResumeEntry{InterruptId: id, Status: "resolved", Payload: &payload, Metadata: &metadata}
		if _, _, e = aguiClientToolAnswer(call, answer); e != nil {
			return nil, false, e
		}
		answers = append(answers, answer)
	}
	if len(answers) == 0 {
		return nil, false, nil
	}
	if retainedInput != nil {
		if len(answers) != len(retainedIDs) || !bytes.Equal(retainedInput.Resume, rawAGUI(answers)) {
			return nil, false, fmt.Errorf("client tool timeout recovery input mismatch")
		}
		return retainedInput, true, nil
	}
	var accepted agui.RunAgentInput
	if json.Unmarshal(prior.Input, &accepted) != nil {
		return nil, false, fmt.Errorf("client tool timeout original input invalid")
	}
	var properties agui.ForwardedProps
	if len(accepted.ForwardedProps) > 0 && json.Unmarshal(accepted.ForwardedProps, &properties) != nil {
		return nil, false, fmt.Errorf("client tool timeout original selection invalid")
	}
	serverState := true
	payload := &agui.ExecutionPayload{UseServerState: &serverState}
	if properties.Agently != nil && properties.Agently.Payload != nil {
		payload.AgentID = properties.Agently.Payload.AgentID
		payload.Model = properties.Agently.Payload.Model
	}
	thread, e := store.GetThread(ctx, prior.Principal, prior.ThreadID)
	if e != nil {
		return nil, false, e
	}
	input := &agui.RunAgentInput{ThreadID: prior.ThreadID, RunID: id, ParentRunID: prior.RunID, Resume: rawAGUI(answers), ForwardedProps: rawAGUI(agui.ForwardedProps{Agently: &agui.Extension{Version: "1", Operation: "chat", RequestID: id, Payload: payload}}), State: thread.State}
	if bytes.Equal(bytes.TrimSpace(input.State), []byte("null")) {
		input.State = nil
	}
	if e = json.Unmarshal(thread.Messages, &input.Messages); e != nil {
		return nil, false, e
	}
	return input, true, nil
}

func isClientToolTimeoutRun(record *aguistore.Run) bool {
	return record != nil && record.PriorRunID != "" && strings.HasPrefix(record.RunID, clientToolTimeoutPrefix)
}

// After terminal timeout receipts are committed, retain unrelated human waits
// and not-yet-due calls on the successor. No approval is answered and no model
// continuation runs until that existing wait boundary is actually complete.
func finishClientToolTimeoutWait(writer *aguiJournalWriter, translator *agui.Translator, input *agui.RunAgentInput, pending aguiPending) (bool, error) {
	if !isClientToolTimeoutRun(writer.run) || len(pending.Dependencies) > 0 {
		return false, nil
	}
	var answers []agui.WireResumeEntry
	if json.Unmarshal(input.Resume, &answers) != nil {
		return false, fmt.Errorf("client tool timeout results invalid")
	}
	done := map[string]bool{}
	for _, answer := range answers {
		done[answer.InterruptId] = true
	}
	remaining := aguiPending{Dependencies: pending.Dependencies}
	for _, call := range pending.ClientTools {
		if !done[aguiPendingCallID(call)] {
			remaining.ClientTools = append(remaining.ClientTools, call)
		}
	}
	for _, interrupt := range pending.Interrupts {
		if !done[interrupt.ID] {
			remaining.Interrupts = append(remaining.Interrupts, interrupt)
		}
	}
	if len(remaining.ClientTools) == 0 && len(remaining.Interrupts) == 0 && len(remaining.Dependencies) == 0 {
		return false, nil
	}
	outcome := map[string]any{"type": "interrupt", "interrupts": remaining.Interrupts}
	if len(remaining.Interrupts) == 0 {
		ids := []string{}
		for _, call := range remaining.ClientTools {
			ids = append(ids, aguiPendingCallID(call))
		}
		outcome = map[string]any{"type": "success", "pendingToolCallIds": ids}
	}
	events := encodeAGUIEvents(translator.Finish("success"))
	var terminal map[string]any
	if len(events) == 0 || json.Unmarshal(events[len(events)-1], &terminal) != nil {
		return false, fmt.Errorf("client tool timeout wait unavailable")
	}
	terminal["outcome"] = outcome
	events[len(events)-1] = rawAGUI(terminal)
	return true, writer.write(events, &remaining)
}
