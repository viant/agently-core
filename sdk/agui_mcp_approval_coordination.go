package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/sdk/api"
)

// The original native conversation authorizes this management command. Host
// authority remains the independently resolved proxy/app journal binding.
func (c *backendClient) aguiProxyApprovalBinding(ctx context.Context, store aguistore.Store, original *aguistore.Run, nativeConversationID string) (context.Context, *MCPAppsProxyRequest, aguiMCPProxyPending, error) {
	var input agui.RunAgentInput
	var pending aguiMCPProxyPending
	if original.Principal != iauth.EffectiveUserID(ctx) || json.Unmarshal(original.Input, &input) != nil || input.ThreadID != original.ThreadID || input.RunID != original.RunID {
		return nil, nil, pending, fmt.Errorf("proxy approval original identity mismatch")
	}
	proxy, handled, err := ParseMCPAppsProxyRequest(input.ForwardedProps)
	if err != nil || !handled || proxy.Method != "tools/call" {
		return nil, nil, pending, fmt.Errorf("original approval is not a tools/call proxy")
	}
	if json.Unmarshal(original.Pending, &pending) != nil || pending.NativeTurnID == "" || pending.NativeTurnID != original.TurnID || len(pending.Interrupts) != 1 || pending.Interrupts[0].Reason != "approval" {
		return nil, nil, pending, fmt.Errorf("proxy approval native boundary mismatch")
	}
	app, err := ResolveAGUIMCPApp(ctx, store, original.Principal, proxy.ServerID, proxy.ServerHash)
	if err != nil {
		return nil, nil, pending, err
	}
	if app.ThreadID != nativeConversationID {
		return nil, nil, pending, fmt.Errorf("proxy approval native conversation mismatch")
	}
	bindings, _ := ctx.Value(aguiWorkspaceBindingsKey{}).(AGUIWorkspaceBindings)
	host := bindings.MCPApps
	if host == nil {
		host = c.aguiMCPAppsHost()
	}
	if host == nil {
		return nil, nil, pending, fmt.Errorf("proxy approval host binding unavailable")
	}
	bound := host.Bind(ctx, *app)
	var name string
	if json.Unmarshal(proxy.Params["name"], &name) != nil || name == "" || bound.Authorize == nil || bound.AuthorizeTool == nil {
		return nil, nil, pending, fmt.Errorf("proxy approval operation binding unavailable")
	}
	if err = bound.Authorize(ctx, bound.App); err != nil {
		return nil, nil, pending, err
	}
	if err = bound.AuthorizeTool(ctx, bound.App, name); err != nil {
		return nil, nil, pending, err
	}
	return WithAGUIMCPAppsBindings(ctx, bound), proxy, pending, nil
}

// A proxy has one true approval interrupt. Admit the standard proxy successor
// before execution so direct resume and inbox actors compete on the same CAS.
func (c *backendClient) aguiAdmitProxyApprovalSuccessor(ctx context.Context, store aguistore.Store, original *aguistore.Run, proxy *MCPAppsProxyRequest, pending aguiMCPProxyPending, answer agui.WireResumeEntry) (*aguistore.Run, error) {
	observe := func(runID string) (*aguistore.Run, error) {
		successor, err := store.GetRun(ctx, original.Principal, original.ThreadID, runID)
		if err != nil {
			return nil, err
		}
		var input agui.RunAgentInput
		var answers []agui.WireResumeEntry
		if successor.PriorRunID != original.RunID || successor.TurnID != original.TurnID || json.Unmarshal(successor.Input, &input) != nil || input.ThreadID != successor.ThreadID || input.RunID != successor.RunID || json.Unmarshal(input.Resume, &answers) != nil || len(answers) != 1 {
			return nil, fmt.Errorf("proxy approval successor identity mismatch")
		}
		acceptedProxy, handled, err := ParseMCPAppsProxyRequest(input.ForwardedProps)
		accepted, _ := canonicalJSONValue(rawAGUI(answers[0]))
		proposed, _ := canonicalJSONValue(rawAGUI(answer))
		if err != nil || !handled || string(rawAGUI(acceptedProxy)) != string(rawAGUI(proxy)) || string(accepted) != string(proposed) {
			return nil, fmt.Errorf("proxy approval successor has a different request or answer")
		}
		if successor.Status == aguistore.StatusAdmitted {
			workerCtx := aguiApprovalWorkerContext(ctx)
			go func() { _ = runAGUIMCPProxyWorker(workerCtx, c, store, successor, &input, proxy, original) }()
		}
		return successor, nil
	}
	if original.ResumedByRunID != "" {
		return observe(original.ResumedByRunID)
	}
	var input agui.RunAgentInput
	if err := json.Unmarshal(original.Input, &input); err != nil {
		return nil, err
	}
	input.RunID = aguiApprovalIdentity("mcp-approval-continuation-", []string{original.ThreadID, original.RunID})
	input.Messages = []agui.Message{}
	input.State = nil
	input.Resume = rawAGUI([]agui.WireResumeEntry{answer})
	successor, fresh, err := store.Admit(ctx, aguistore.Admission{Principal: original.Principal, ThreadID: original.ThreadID, RunID: input.RunID, PriorRunID: original.RunID, ExpectedPriorRevision: original.Revision, TurnID: original.TurnID, ParentRunID: input.ParentRunID, Input: rawAGUI(&input)})
	if err != nil {
		latest, readErr := store.GetRun(ctx, original.Principal, original.ThreadID, original.RunID)
		if readErr == nil && latest.ResumedByRunID != "" {
			return observe(latest.ResumedByRunID)
		}
		return nil, err
	}
	if fresh || successor.Status == aguistore.StatusAdmitted {
		workerCtx := aguiApprovalWorkerContext(ctx)
		go func() { _ = runAGUIMCPProxyWorker(workerCtx, c, store, successor, &input, proxy, original) }()
	}
	return successor, nil
}

func (c *backendClient) aguiRunProxyApprovalCommand(ctx context.Context, store aguistore.Store, command *aguistore.Run, input AGUIApprovalDecideInput) error {
	original, err := store.GetRun(ctx, command.Principal, input.OriginalThreadID, input.OriginalRunID)
	if err != nil {
		return err
	}
	bound, proxy, pending, err := c.aguiProxyApprovalBinding(ctx, store, original, command.ConversationID)
	if err != nil {
		return err
	}
	if input.ApprovalID != pending.Interrupts[0].ID || input.Answer.InterruptId != input.ApprovalID {
		return fmt.Errorf("proxy approval answer identity mismatch")
	}
	native := *original
	native.ConversationID = command.ConversationID
	native.TurnID = pending.NativeTurnID
	row, err := c.aguiApprovalRow(bound, native.ConversationID, pending.Interrupts[0])
	if err != nil {
		return err
	}
	if row.Status == "pending" && row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) && len(aguiApprovalMetadata(row)["aguiDecision"]) == 0 {
		input.Answer = aguiApprovalTimeoutAnswer(row.Id)
		bound = context.WithValue(bound, aguiApprovalTimeoutKey{}, true)
	} else if row.Status == "timed_out" {
		// A resource command can lose its response after the server timeout
		// commits. Restore only the proven timeout answer, never a new approval.
		receipt, outcome, receiptErr := c.aguiCompletedApprovalReceipt(bound, native.ConversationID, pending.Interrupts[0])
		if receiptErr != nil {
			return receiptErr
		}
		expected, _ := canonicalJSONValue(rawAGUI(aguiApprovalTimeoutAnswer(row.Id)))
		actual, _ := canonicalJSONValue(rawAGUI(receipt))
		if receipt == nil || outcome == nil || row.TimedOutAt == nil || string(expected) != string(actual) {
			return fmt.Errorf("proxy timeout receipt is not authoritative")
		}
		input.Answer = *receipt
	}
	// An already admitted genuine successor can be between its native claim and
	// outcome commit. Observe that worker rather than treating its in-flight claim
	// as permission to repeat the effect. Its accepted answer is checked below.
	if original.ResumedByRunID == "" {
		if err = c.aguiPreflightApproval(bound, native.ConversationID, pending.Interrupts[0], input.Answer); err != nil {
			latest, readErr := store.GetRun(ctx, original.Principal, original.ThreadID, original.RunID)
			if readErr != nil || latest.ResumedByRunID == "" {
				return err
			}
			original = latest
		}
	}
	if command.LastSequence == 0 {
		command, err = store.Append(ctx, command.Principal, command.ThreadID, command.RunID, command.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": command.ThreadID, "runId": command.RunID})}, &aguistore.Change{LeaseOwner: command.LeaseOwner})
		if err != nil {
			return err
		}
	}
	successor, err := c.aguiAdmitProxyApprovalSuccessor(bound, store, original, proxy, pending, input.Answer)
	if err != nil {
		return err
	}
	c.aguiNotifyApprovalUpdated(ctx, command.ConversationID)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		latest, err := store.GetRun(ctx, successor.Principal, successor.ThreadID, successor.RunID)
		if err != nil {
			return err
		}
		receipt, outcome, receiptErr := c.aguiCompletedApprovalReceipt(bound, native.ConversationID, pending.Interrupts[0])
		if aguiRunTerminal(latest.Status) {
			if receiptErr != nil {
				return receiptErr
			}
			if receipt == nil || outcome == nil {
				return fmt.Errorf("proxy successor has no real completed approval outcome")
			}
			actual, _ := canonicalJSONValue(rawAGUI(input.Answer))
			saved, _ := canonicalJSONValue(rawAGUI(receipt))
			if string(actual) != string(saved) {
				return fmt.Errorf("proxy approval already has a different answer")
			}
			refs := &api.ApprovalProtocolReferences{Version: "1", Kind: "mcp-app", NativeConversationID: command.ConversationID, ThreadID: original.ThreadID, OriginalRunID: original.RunID, CommandRunID: command.RunID, ContinuationRunID: latest.RunID, RemainingInterruptIDs: []string{}}
			outcome.Protocol = refs
			if err = c.aguiPersistApprovalPublicOutcome(bound, pending.Interrupts[0], native.ConversationID, outcome); err != nil {
				return err
			}
			result := &DecideToolApprovalOutput{Status: "ok", Outcome: outcome, Protocol: refs}
			_, err = store.Append(ctx, command.Principal, command.ThreadID, command.RunID, command.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": command.ThreadID, "runId": command.RunID, "outcome": map[string]any{"type": "success"}, "result": result})}, &aguistore.Change{LeaseOwner: command.LeaseOwner})
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *backendClient) aguiFindProxyApprovalOrigin(ctx context.Context, store *aguistore.ComponentStore, row *toolapprovalqueuemodel.QueueRowView) (*aguistore.Run, error) {
	records, err := store.ListRunsByNativeTurn(ctx, iauth.EffectiveUserID(ctx), valueOrEmpty(row.TurnId))
	if err != nil {
		return nil, err
	}
	var result *aguistore.Run
	for _, record := range records {
		var input agui.RunAgentInput
		var pending aguiMCPProxyPending
		if json.Unmarshal(record.Input, &input) != nil {
			continue
		}
		proxy, ok, err := ParseMCPAppsProxyRequest(input.ForwardedProps)
		if err != nil {
			return nil, err
		}
		if !ok || proxy.Method != "tools/call" || json.Unmarshal(record.Pending, &pending) != nil {
			continue
		}
		if pending.NativeTurnID != valueOrEmpty(row.TurnId) {
			continue
		}
		for _, interrupt := range pending.Interrupts {
			if interrupt.ID == row.Id && interrupt.Reason == "approval" {
				if result != nil {
					return nil, fmt.Errorf("proxy approval belongs to multiple original runs")
				}
				result = record
			}
		}
	}
	return result, nil
}

func (c *backendClient) reconcileAGUIProxyApproval(ctx context.Context, store aguistore.Store, record *aguistore.Run, proxy *MCPAppsProxyRequest) error {
	app, err := ResolveAGUIMCPApp(ctx, store, record.Principal, proxy.ServerID, proxy.ServerHash)
	if err != nil {
		return err
	}
	if record.Status == aguistore.StatusInterrupted && record.ResumedByRunID == "" {
		bound, _, pending, err := c.aguiProxyApprovalBinding(ctx, store, record, app.ThreadID)
		if err != nil {
			return err
		}
		row, err := c.aguiApprovalRow(bound, app.ThreadID, pending.Interrupts[0])
		if err != nil {
			return err
		}
		if row.Status == "pending" && row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) {
			_, _, err := c.routeAGUIApprovalDecision(bound, &DecideToolApprovalInput{ID: row.Id, Action: "cancel", Reason: api.ApprovalTimeoutErrorMessage}, row)
			return err
		}
		if len(aguiApprovalMetadata(row)["aguiDecision"]) > 0 {
			return fmt.Errorf("proxy approval decision without its genuine successor requires reconciliation; effect will not be repeated")
		}
		return nil
	}
	if record.PriorRunID == "" || (record.Status != aguistore.StatusAdmitted && record.Status != aguistore.StatusRunning) {
		return nil
	}
	if record.LeaseUntil != nil && record.LeaseUntil.After(time.Now()) {
		return nil
	}
	original, err := store.GetRun(ctx, record.Principal, record.ThreadID, record.PriorRunID)
	if err != nil {
		return err
	}
	bound, originalProxy, _, err := c.aguiProxyApprovalBinding(ctx, store, original, app.ThreadID)
	if err != nil {
		return err
	}
	if string(rawAGUI(proxy)) != string(rawAGUI(originalProxy)) {
		return fmt.Errorf("proxy approval recovery request changed")
	}
	var input agui.RunAgentInput
	if err = json.Unmarshal(record.Input, &input); err != nil {
		return err
	}
	go func() {
		_ = runAGUIMCPProxyWorker(aguiApprovalWorkerContext(bound), c, store, record, &input, originalProxy, original)
	}()
	return nil
}
