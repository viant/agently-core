package sdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/sdk/api"
)

type AGUIApprovalDecideInput struct {
	OriginalThreadID string               `json:"originalThreadId,omitempty"`
	OriginalRunID    string               `json:"originalRunId"`
	ApprovalID       string               `json:"approvalId"`
	Answer           agui.WireResumeEntry `json:"answer"`
}

type AGUIApprovalReconciler interface {
	ReconcileAGUIApprovals(context.Context) error
	IsAGUIApprovalRecoveryOwnedTurn(context.Context, string, string) (bool, error)
}

func aguiApprovalIdentity(label string, value any) string {
	sum := sha256.Sum256(rawAGUI(value))
	return label + hex.EncodeToString(sum[:])
}

func (c *backendClient) aguiApprovalOriginal(ctx context.Context, store aguistore.Store, originalID, approvalID, threadID string) (*aguistore.Run, aguiPending, agui.WireInterrupt, error) {
	record, err := store.GetRun(ctx, iauth.EffectiveUserID(ctx), threadID, originalID)
	if err != nil {
		return nil, aguiPending{}, agui.WireInterrupt{}, err
	}
	if record.TurnID == "" || record.Status != aguistore.StatusInterrupted {
		return nil, aguiPending{}, agui.WireInterrupt{}, fmt.Errorf("original approval run is not interrupted")
	}
	var pending aguiPending
	if err = json.Unmarshal(record.Pending, &pending); err != nil {
		return nil, pending, agui.WireInterrupt{}, err
	}
	for _, interrupt := range pending.Interrupts {
		if interrupt.ID == approvalID && interrupt.Reason == "approval" {
			return record, pending, interrupt, nil
		}
	}
	return nil, pending, agui.WireInterrupt{}, fmt.Errorf("approval does not belong to the original protocol interrupt")
}

// Read a receipt only after the same native identity checks as direct resume.
func (c *backendClient) aguiCompletedApprovalReceipt(ctx context.Context, threadID string, interrupt agui.WireInterrupt) (*agui.WireResumeEntry, *api.DecideToolApprovalOutcome, error) {
	row, err := c.aguiApprovalRow(ctx, threadID, interrupt)
	if err != nil {
		return nil, nil, err
	}
	metadata := aguiApprovalMetadata(row)
	if len(metadata["aguiDecision"]) == 0 {
		return nil, nil, nil
	}
	if len(metadata["aguiOutcome"]) == 0 {
		return nil, nil, fmt.Errorf("approval effect outcome is uncertain; refusing duplicate execution")
	}
	var answer agui.WireResumeEntry
	if err = json.Unmarshal(metadata["aguiDecision"], &answer); err != nil {
		return nil, nil, err
	}
	if answer.InterruptId != interrupt.ID {
		return nil, nil, fmt.Errorf("approval decision receipt identity mismatch")
	}
	if err = c.aguiPreflightApproval(ctx, threadID, interrupt, answer); err != nil {
		return nil, nil, err
	}
	var receipt struct {
		Status string  `json:"status"`
		Result *string `json:"result"`
	}
	if err = json.Unmarshal(metadata["aguiOutcome"], &receipt); err != nil || receipt.Result == nil || (receipt.Status != "completed" && receipt.Status != "failed") {
		return nil, nil, fmt.Errorf("approval completed receipt is invalid")
	}
	action := canonicalDecideAction(valueOrEmpty(row.Decision))
	if action == "" {
		decoded, decodeErr := decodeAGUIApprovalAnswer(answer)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		action = decoded.Action
	}
	outcome := &api.DecideToolApprovalOutcome{ApprovalID: row.Id, Action: action, Decision: valueOrEmpty(row.Decision), Status: row.Status, ConversationID: threadID, TurnID: valueOrEmpty(row.TurnId), MessageID: valueOrEmpty(row.MessageId), ToolName: row.ToolName, Result: *receipt.Result, ErrorMessage: valueOrEmpty(row.ErrorMessage), ExpiresAt: row.ExpiresAt, TimedOutAt: row.TimedOutAt}
	return &answer, outcome, nil
}

func (c *backendClient) aguiCompleteResumeWithApprovalReceipts(ctx context.Context, prior *aguistore.Run, input *agui.RunAgentInput, pending aguiPending) error {
	var originalInput agui.RunAgentInput
	if json.Unmarshal(prior.Input, &originalInput) == nil {
		if _, proxy, err := ParseMCPAppsProxyRequest(originalInput.ForwardedProps); err != nil {
			return err
		} else if proxy {
			return nil
		}
	}
	var answers []agui.WireResumeEntry
	if len(input.Resume) > 0 {
		if err := json.Unmarshal(input.Resume, &answers); err != nil {
			return err
		}
	}
	changed := false
	supplied := map[string]agui.WireResumeEntry{}
	for _, answer := range answers {
		supplied[answer.InterruptId] = answer
	}
	for _, interrupt := range pending.Interrupts {
		if interrupt.Reason != "approval" {
			continue
		}
		receipt, _, err := c.aguiCompletedApprovalReceipt(ctx, prior.ConversationID, interrupt)
		if err != nil {
			return err
		}
		if receipt == nil {
			continue
		}
		if answer, ok := supplied[interrupt.ID]; ok {
			a, _ := canonicalJSONValue(rawAGUI(answer))
			b, _ := canonicalJSONValue(rawAGUI(receipt))
			if !bytes.Equal(a, b) {
				return fmt.Errorf("approval already has a different answer")
			}
			continue
		}
		answers = append(answers, *receipt)
		changed = true
		supplied[interrupt.ID] = *receipt
	}
	if changed {
		input.Resume = rawAGUI(answers)
	}
	return nil
}

func (c *backendClient) aguiApprovalContinuation(ctx context.Context, store aguistore.Store, prior *aguistore.Run, pending aguiPending) (string, []string, error) {
	if prior.ResumedByRunID != "" {
		return prior.ResumedByRunID, []string{}, nil
	}
	var answers []agui.WireResumeEntry
	var remaining []string
	for _, interrupt := range pending.Interrupts {
		if interrupt.Reason != "approval" {
			remaining = append(remaining, interrupt.ID)
			continue
		}
		receipt, _, err := c.aguiCompletedApprovalReceipt(ctx, prior.ConversationID, interrupt)
		if err != nil {
			return "", nil, err
		}
		if receipt == nil {
			remaining = append(remaining, interrupt.ID)
		} else {
			answers = append(answers, *receipt)
		}
	}
	for _, call := range pending.ClientTools {
		id := aguiPendingCallID(call)
		found := false
		for _, value := range remaining {
			if value == id {
				found = true
			}
		}
		if !found {
			remaining = append(remaining, id)
		}
	}
	if len(remaining) > 0 || len(answers) == 0 {
		return "", remaining, nil
	}
	var input agui.RunAgentInput
	if err := json.Unmarshal(prior.Input, &input); err != nil {
		return "", nil, err
	}
	input.RunID = aguiApprovalIdentity("approval-continuation-", []string{prior.ThreadID, prior.RunID})
	input.Resume = rawAGUI(answers)
	var properties agui.ForwardedProps
	if len(input.ForwardedProps) > 0 {
		if err := json.Unmarshal(input.ForwardedProps, &properties); err != nil {
			return "", nil, err
		}
	}
	if properties.Agently == nil {
		properties.Agently = &agui.Extension{Version: "1", Operation: "chat"}
	}
	if properties.Agently != nil {
		serverState := true
		originalPayload := properties.Agently.Payload
		properties.Agently.Payload = &agui.ExecutionPayload{UseServerState: &serverState}
		if originalPayload != nil {
			properties.Agently.Payload.AgentID = originalPayload.AgentID
			properties.Agently.Payload.Model = originalPayload.Model
		}
		properties.Agently.RequestID = input.RunID
		input.ForwardedProps = rawAGUI(properties)
	}
	thread, err := store.GetThread(ctx, prior.Principal, prior.ThreadID)
	if err != nil {
		return "", nil, err
	}
	input.State = append(json.RawMessage(nil), thread.State...)
	if bytes.Equal(bytes.TrimSpace(input.State), []byte("null")) {
		input.State = nil
	}
	if err = json.Unmarshal(thread.Messages, &input.Messages); err != nil {
		return "", nil, err
	}
	if err = preflightAGUIResume(ctx, c, &input, pending); err != nil {
		return "", nil, err
	}
	query, err := queryForAGUI(&input, prior.Principal, prior.TurnID, properties.Agently, prior)
	if err != nil {
		return "", nil, err
	}
	session, err := aguiClientToolSession(&input)
	if err != nil {
		return "", nil, err
	}
	successor, fresh, err := store.Admit(ctx, aguistore.Admission{ThreadID: prior.ThreadID, RunID: input.RunID, Principal: prior.Principal, ParentRunID: input.ParentRunID, PriorRunID: prior.RunID, ExpectedPriorRevision: prior.Revision, TurnID: prior.TurnID, Input: rawAGUI(&input)})
	if err != nil {
		if errors.Is(err, aguistore.ErrConflict) {
			latest, readErr := store.GetRun(ctx, prior.Principal, prior.ThreadID, prior.RunID)
			if readErr == nil && latest.ResumedByRunID != "" {
				return latest.ResumedByRunID, []string{}, nil
			}
		}
		return "", nil, err
	}
	if fresh {
		c.aguiNotifyApprovalUpdated(ctx, prior.ConversationID)
	}
	if fresh || successor.Status == aguistore.StatusAdmitted {
		workerCtx := clienttool.WithSession(aguiApprovalWorkerContext(ctx), session)
		go func() {
			if err := runAGUIDurable(workerCtx, c, c, store, successor, &input, query, pending, prior); err != nil && !errors.Is(err, aguistore.ErrConflict) {
				log.Printf("AG-UI approval continuation failed: %v", err)
			}
		}()
	}
	return successor.RunID, []string{}, nil
}

func (c *backendClient) aguiRunApprovalCommand(ctx context.Context, store aguistore.Store, command *aguistore.Run, payload json.RawMessage) error {
	if err := extensions.ValidateApprovalPayload("approval.decide", payload); err != nil {
		return err
	}
	var input AGUIApprovalDecideInput
	if err := json.Unmarshal(payload, &input); err != nil {
		return err
	}
	if input.ApprovalID != input.Answer.InterruptId || command.TurnID != "" || iauth.EffectiveUserID(ctx) != command.Principal {
		return fmt.Errorf("approval command identity mismatch")
	}
	if input.OriginalThreadID != "" && input.OriginalThreadID != command.ThreadID {
		return c.aguiRunProxyApprovalCommand(ctx, store, command, input)
	}
	original, pending, interrupt, err := c.aguiApprovalOriginal(ctx, store, input.OriginalRunID, input.ApprovalID, command.ThreadID)
	if err != nil {
		return err
	}
	if command.LastSequence == 0 {
		command, err = store.Append(ctx, command.Principal, command.ThreadID, command.RunID, command.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": command.ThreadID, "runId": command.RunID, "protocolVersion": "1.0"})}, &aguistore.Change{LeaseOwner: command.LeaseOwner})
		if err != nil {
			return err
		}
	}
	row, err := c.aguiApprovalRow(ctx, original.ConversationID, interrupt)
	if err != nil {
		return err
	}
	if row.Status == "pending" && row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) && len(aguiApprovalMetadata(row)["aguiDecision"]) == 0 {
		input.Answer = aguiApprovalTimeoutAnswer(row.Id)
		ctx = context.WithValue(ctx, aguiApprovalTimeoutKey{}, true)
	}
	if _, err = c.aguiApplyApproval(ctx, original, interrupt, input.Answer); err != nil {
		return err
	}
	_, outcome, err := c.aguiCompletedApprovalReceipt(ctx, original.ConversationID, interrupt)
	if err != nil {
		return err
	}
	if outcome == nil {
		return fmt.Errorf("approval has no completed outcome")
	}
	continuation, remaining, err := c.aguiApprovalContinuation(ctx, store, original, pending)
	if err != nil {
		return err
	}
	result := &DecideToolApprovalOutput{Status: "ok", Outcome: outcome, Protocol: &api.ApprovalProtocolReferences{Version: "1", ThreadID: original.ThreadID, OriginalRunID: original.RunID, CommandRunID: command.RunID, ContinuationRunID: continuation, RemainingInterruptIDs: remaining}}
	result.Outcome.Protocol = result.Protocol
	if err = c.aguiPersistApprovalPublicOutcome(ctx, interrupt, original.ConversationID, result.Outcome); err != nil {
		return err
	}
	_, err = store.Append(ctx, command.Principal, command.ThreadID, command.RunID, command.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": command.ThreadID, "runId": command.RunID, "outcome": map[string]any{"type": "success"}, "result": result})}, &aguistore.Change{LeaseOwner: command.LeaseOwner})
	return err
}

func (c *backendClient) routeAGUIApprovalDecision(ctx context.Context, input *DecideToolApprovalInput, row *toolapprovalqueuemodel.QueueRowView) (*DecideToolApprovalOutput, bool, error) {
	if c.goalInvoker == nil {
		return nil, false, nil
	}
	store := aguistore.New(c.goalInvoker)
	runs, err := store.ListRunsByTurn(ctx, iauth.EffectiveUserID(ctx), valueOrEmpty(row.ConversationId), valueOrEmpty(row.TurnId))
	if err != nil {
		return nil, true, err
	}
	if len(runs) == 0 {
		proxyOriginal, err := c.aguiFindProxyApprovalOrigin(ctx, store, row)
		if err != nil {
			return nil, true, err
		}
		if proxyOriginal != nil {
			runs = []*aguistore.Run{proxyOriginal}
		}
	}
	if len(runs) == 0 {
		evidence, err := store.ReadExecutionProvenance(ctx, valueOrEmpty(row.ConversationId), valueOrEmpty(row.TurnId))
		if err != nil {
			return nil, true, err
		}
		if evidence.AGUIOwned {
			return nil, true, ErrAGUIProxyApprovalContinuation
		}
		if !evidence.NativeTurnFound {
			return nil, true, fmt.Errorf("approval native execution provenance is unavailable")
		}
		return nil, false, nil
	}
	var original *aguistore.Run
	for _, record := range runs {
		var pending aguiPending
		if json.Unmarshal(record.Pending, &pending) != nil {
			continue
		}
		for _, interrupt := range pending.Interrupts {
			if interrupt.Reason == "approval" && interrupt.ID == row.Id {
				if original != nil {
					return nil, true, fmt.Errorf("approval belongs to more than one protocol boundary")
				}
				original = record
			}
		}
	}
	if original == nil {
		return nil, true, fmt.Errorf("AG-UI approval has not reached a durable interrupt boundary")
	}
	action := canonicalDecideAction(input.Action)
	answer := agui.WireResumeEntry{InterruptId: row.Id, Status: "resolved"}
	if action == "cancel" {
		answer.Status = "cancelled"
	}
	answerPayload := rawAGUI(&aguiApprovalAnswer{Action: action, EditedFields: input.EditedFields, Payload: input.Payload, Reason: input.Reason, Note: input.Note})
	answer.Payload = &answerPayload
	nativeConversationID := valueOrEmpty(row.ConversationId)
	commandThread, err := aguiWireThreadForConversation(ctx, store, original.Principal, nativeConversationID)
	if err != nil {
		return nil, true, err
	}
	originalThreadID := ""
	if original.ThreadID != commandThread {
		originalThreadID = original.ThreadID
	}
	payload := rawAGUI(&AGUIApprovalDecideInput{OriginalRunID: original.RunID, OriginalThreadID: originalThreadID, ApprovalID: row.Id, Answer: answer})
	commandID := aguiApprovalIdentity("approval-decision-", []any{original.ThreadID, original.RunID, json.RawMessage(payload)})
	body := rawAGUI(map[string]any{"threadId": commandThread, "runId": commandID, "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "operation": "approval.decide", "requestId": commandID, "payload": json.RawMessage(payload)}}})
	command, _, err := store.Admit(ctx, aguistore.Admission{Principal: original.Principal, ThreadID: commandThread, RunID: commandID, Input: body})
	if err != nil {
		return nil, true, err
	}
	if !aguiRunTerminal(command.Status) {
		claimed, claimErr := store.Claim(ctx, command.Principal, command.ThreadID, command.RunID, command.Revision, uuid.NewString(), time.Minute)
		if claimErr != nil {
			return nil, true, claimErr
		}
		if err = c.aguiRunApprovalCommand(ctx, store, claimed, payload); err != nil {
			c.aguiJournalApprovalCommandFailure(context.WithoutCancel(ctx), store, claimed, err)
			return nil, true, err
		}
	}
	command, err = store.GetRun(ctx, command.Principal, command.ThreadID, command.RunID)
	if err != nil {
		return nil, true, err
	}
	journal, err := aguiRecoveryJournal(ctx, store, command)
	if err != nil {
		return nil, true, err
	}
	for _, entry := range journal {
		var terminal struct {
			Type    string                    `json:"type"`
			Result  *DecideToolApprovalOutput `json:"result"`
			Message string                    `json:"message"`
		}
		if json.Unmarshal(entry, &terminal) != nil {
			continue
		}
		if terminal.Type == "RUN_FINISHED" && terminal.Result != nil {
			return terminal.Result, true, nil
		}
		if terminal.Type == "RUN_ERROR" {
			return nil, true, fmt.Errorf("%s", terminal.Message)
		}
	}
	return nil, true, fmt.Errorf("approval command has no completed result")
}

func (c *backendClient) aguiVerifiedApprovalReceipt(ctx context.Context, threadID string, interrupt agui.WireInterrupt, answer agui.WireResumeEntry) (bool, error) {
	row, err := c.aguiApprovalRow(ctx, threadID, interrupt)
	if err != nil {
		return false, err
	}
	metadata := aguiApprovalMetadata(row)
	if len(metadata["aguiDecision"]) == 0 {
		return false, nil
	}
	if err = c.aguiPreflightApproval(ctx, threadID, interrupt, answer); err != nil {
		return false, err
	}
	return true, nil
}

type aguiApprovalTimeoutKey struct{}

func aguiApprovalTimeoutAnswer(id string) agui.WireResumeEntry {
	payload := rawAGUI(&aguiApprovalAnswer{Action: "cancel", Reason: api.ApprovalTimeoutErrorMessage})
	return agui.WireResumeEntry{InterruptId: id, Status: "cancelled", Payload: &payload}
}
func aguiTrustedApprovalTimeout(ctx context.Context, row *toolapprovalqueuemodel.QueueRowView, entry agui.WireResumeEntry) bool {
	trusted, _ := ctx.Value(aguiApprovalTimeoutKey{}).(bool)
	if !trusted || row == nil || row.ExpiresAt == nil || row.ExpiresAt.After(time.Now()) {
		return false
	}
	expected, _ := canonicalJSONValue(rawAGUI(aguiApprovalTimeoutAnswer(row.Id)))
	actual, _ := canonicalJSONValue(rawAGUI(entry))
	return bytes.Equal(expected, actual)
}

type aguiApprovalLifetimeKey struct{}

func aguiApprovalWorkerContext(ctx context.Context) context.Context {
	if lifetime, ok := ctx.Value(aguiApprovalLifetimeKey{}).(context.Context); ok {
		return aguiApprovalLifecycleContext{Context: context.WithoutCancel(ctx), lifetime: lifetime}
	}
	return context.WithoutCancel(ctx)
}

var ErrAGUIProxyApprovalContinuation = errors.New("MCP proxy approval requires its original host continuation; native decision remains pending")

func (c *backendClient) RouteToolApprovalTimeout(ctx context.Context, row *toolapprovalqueuemodel.QueueRowView, now time.Time) (*api.DecideToolApprovalOutcome, bool, error) {
	if row == nil || row.ExpiresAt == nil || row.ExpiresAt.After(now) {
		return nil, false, nil
	}
	output, handled, err := c.routeAGUIApprovalDecision(ctx, &DecideToolApprovalInput{ID: row.Id, Action: "cancel", Reason: api.ApprovalTimeoutErrorMessage}, row)
	if errors.Is(err, ErrAGUIProxyApprovalContinuation) {
		return nil, true, nil
	}
	if err != nil || !handled {
		return nil, handled, err
	}
	if output == nil || output.Outcome == nil {
		return nil, true, fmt.Errorf("approval timeout has no completed outcome")
	}
	return output.Outcome, true, nil
}

func (c *backendClient) aguiPersistApprovalPublicOutcome(ctx context.Context, interrupt agui.WireInterrupt, threadID string, outcome *api.DecideToolApprovalOutcome) error {
	row, err := c.aguiApprovalRow(ctx, threadID, interrupt)
	if err != nil {
		return err
	}
	metadata := aguiApprovalMetadata(row)
	metadata["outcome"] = rawAGUI(outcome)
	patch := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	patch.SetId(row.Id)
	patch.SetUserId(row.UserId)
	patch.SetMetadata(rawAGUI(metadata))
	patch.SetUpdatedAt(time.Now().UTC())
	patcher, ok := c.conv.(toolApprovalQueuePatcher)
	if !ok {
		return fmt.Errorf("approval writer unavailable")
	}
	return patcher.PatchToolApprovalQueue(ctx, patch)
}

func (c *backendClient) aguiNotifyApprovalUpdated(ctx context.Context, threadID string) {
	if c.streaming == nil || threadID == "" {
		return
	}
	if err := c.streaming.Publish(context.WithoutCancel(ctx), &streaming.Event{Type: streaming.EventTypeConversationMetaUpdated, ConversationID: threadID, StreamID: threadID, CreatedAt: time.Now().UTC(), Patch: map[string]any{"aguiUpdated": true}}); err != nil {
		log.Printf("AG-UI approval post-commit notification: %v", err)
	}
}

func (c *backendClient) aguiJournalApprovalCommandFailure(ctx context.Context, store aguistore.Store, command *aguistore.Run, failure error) {
	latest, err := store.GetRun(ctx, command.Principal, command.ThreadID, command.RunID)
	if err != nil || aguiRunTerminal(latest.Status) {
		return
	}
	var events []json.RawMessage
	if latest.LastSequence == 0 {
		events = append(events, rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": command.ThreadID, "runId": command.RunID}))
	}
	events = append(events, rawAGUI(map[string]any{"type": "RUN_ERROR", "code": "APPROVAL_COMMAND_FAILED", "message": failure.Error()}))
	if _, err = store.Append(ctx, command.Principal, command.ThreadID, command.RunID, latest.Revision, events, &aguistore.Change{LeaseOwner: command.LeaseOwner}); err != nil {
		log.Printf("AG-UI approval command error journal: %v", err)
	}
}

type aguiApprovalLifecycleContext struct {
	context.Context
	lifetime context.Context
}

func (c aguiApprovalLifecycleContext) Deadline() (time.Time, bool) { return c.lifetime.Deadline() }
func (c aguiApprovalLifecycleContext) Done() <-chan struct{}       { return c.lifetime.Done() }
func (c aguiApprovalLifecycleContext) Err() error                  { return c.lifetime.Err() }
