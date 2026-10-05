package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/auth/token"
	runmodel "github.com/viant/agently-core/model/run"
	turnmodel "github.com/viant/agently-core/model/turn"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
)

// ReconcileAGUIApprovals is invoked by the existing agent watchdog lifecycle.
// It pages independent durable evidence, rather than relying on native stale
// runs or a browser callback after the last decision.
func (c *backendClient) ReconcileAGUIApprovals(ctx context.Context) error {
	if c == nil || c.goalInvoker == nil {
		return nil
	}
	c.approvalRecoveryMu.Lock()
	defer c.approvalRecoveryMu.Unlock()
	store := aguistore.New(c.goalInvoker)
	discoveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	candidates, err := store.ReadApprovalRecovery(discoveryCtx, c.approvalRecoveryCursor, time.Now().UTC(), 50)
	cancel()
	if err != nil {
		return err
	}
	if len(candidates) == 50 {
		c.approvalRecoveryCursor = candidates[len(candidates)-1].RunKey
	} else {
		c.approvalRecoveryCursor = ""
	}
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	for _, candidate := range candidates {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		wg.Add(1)
		go func(candidate aguistore.ApprovalRecoveryCandidate) {
			defer wg.Done()
			defer func() { <-slots }()
			authorized := iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: candidate.Principal})
			var security *token.SecurityData
			securityContext := candidate.SecurityContext
			if securityContext == "" {
				lookupCtx, stopLookup := context.WithTimeout(authorized, 5*time.Second)
				savedSecurity, lookupErr := c.aguiApprovalCommandSecurity(lookupCtx, store, candidate)
				securityContext = savedSecurity
				stopLookup()
				if lookupErr != nil {
					mu.Lock()
					failures = append(failures, lookupErr)
					mu.Unlock()
					return
				}
			}
			if securityContext != "" {
				restored, restoredSecurity, restoreErr := token.RestoreSecurityContext(authorized, securityContext)
				security = restoredSecurity
				if restoreErr != nil {
					mu.Lock()
					failures = append(failures, restoreErr)
					mu.Unlock()
					return
				}
				if security == nil || security.Subject != candidate.Principal {
					mu.Lock()
					failures = append(failures, fmt.Errorf("approval recovery security identity mismatch"))
					mu.Unlock()
					return
				}
				authorized = restored
			}
			itemCtx, cancel := context.WithTimeout(authorized, 15*time.Second)
			defer cancel()
			itemCtx = context.WithValue(itemCtx, aguiApprovalLifetimeKey{}, authorized)
			if security != nil && c.approvalTokenProvider != nil {
				if _, err := c.approvalTokenProvider.EnsureTokens(itemCtx, token.Key{Subject: security.Subject, Provider: security.Provider}); err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
					return
				}
			}
			if err := c.reconcileAGUIApprovalCandidate(itemCtx, store, candidate); err != nil && !errors.Is(err, aguistore.ErrConflict) {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
		}(candidate)
	}
	wg.Wait()
	return errors.Join(failures...)
}

func (c *backendClient) reconcileAGUIApprovalCandidate(ctx context.Context, store aguistore.Store, candidate aguistore.ApprovalRecoveryCandidate) error {
	record, err := store.GetRun(ctx, candidate.Principal, candidate.ThreadID, candidate.RunID)
	if err != nil {
		return err
	}
	var acceptedInput agui.RunAgentInput
	if err = json.Unmarshal(record.Input, &acceptedInput); err != nil {
		return err
	}
	if proxy, handled, err := ParseMCPAppsProxyRequest(acceptedInput.ForwardedProps); err != nil {
		return err
	} else if handled {
		return c.reconcileAGUIProxyApproval(ctx, store, record, proxy)
	}
	if record.Status == aguistore.StatusInterrupted && record.ResumedByRunID == "" {
		var pending aguiPending
		if err = json.Unmarshal(record.Pending, &pending); err != nil {
			return err
		}
		approvals := false
		for _, interrupt := range pending.Interrupts {
			if interrupt.Reason != "approval" {
				continue
			}
			approvals = true
			row, err := c.aguiApprovalRow(ctx, record.ConversationID, interrupt)
			if err != nil {
				return err
			}
			if row.Status == "pending" && row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) && len(aguiApprovalMetadata(row)["aguiDecision"]) == 0 {
				if _, handled, err := c.routeAGUIApprovalDecision(ctx, &DecideToolApprovalInput{ID: row.Id, Action: "cancel", Reason: "approval request timed out"}, row); err != nil {
					return err
				} else if !handled {
					return fmt.Errorf("protocol timeout has no coordinator")
				}
			}
		}
		if approvals {
			_, _, err = c.aguiApprovalContinuation(ctx, store, record, pending)
			return err
		}
		return nil
	}
	if record.Status != aguistore.StatusAdmitted && record.Status != aguistore.StatusRunning {
		return nil
	}
	if record.LeaseUntil != nil && record.LeaseUntil.After(time.Now()) {
		return nil
	}
	var input agui.RunAgentInput
	if err = json.Unmarshal(record.Input, &input); err != nil {
		return err
	}
	var properties agui.ForwardedProps
	if len(input.ForwardedProps) > 0 {
		if err = json.Unmarshal(input.ForwardedProps, &properties); err != nil {
			return err
		}
	}
	if record.TurnID == "" {
		if properties.Agently == nil || properties.Agently.Operation != "approval.decide" {
			return nil
		}
		var forwarded map[string]json.RawMessage
		var envelope struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err = json.Unmarshal(input.ForwardedProps, &forwarded); err != nil {
			return err
		}
		if err = json.Unmarshal(forwarded["agently"], &envelope); err != nil {
			return err
		}
		go runAGUIResourceWorker(aguiApprovalWorkerContext(ctx), c, store, record, "approval.decide", envelope.Payload)
		return nil
	}
	if record.PriorRunID == "" {
		return nil
	}
	prior, err := store.GetRun(ctx, record.Principal, record.ThreadID, record.PriorRunID)
	if err != nil {
		return err
	}
	var pending aguiPending
	if err = json.Unmarshal(prior.Pending, &pending); err != nil {
		return err
	}
	approvals := false
	for _, interrupt := range pending.Interrupts {
		if interrupt.Reason == "approval" {
			approvals = true
		}
	}
	if !approvals {
		return nil
	}
	if record.Status == aguistore.StatusRunning {
		// Existing replay recovery reattaches; native lease recovery remains active
		// after native execution starts. Never blindly repeat a running worker.
		go func() { _ = recoverAGUIDurable(aguiApprovalWorkerContext(ctx), c, c, store, record) }()
		return nil
	}
	query, err := queryForAGUI(&input, record.Principal, record.TurnID, properties.Agently, prior)
	if err != nil {
		return err
	}
	session, err := aguiClientToolSession(&input)
	if err != nil {
		return err
	}
	workerCtx := clienttool.WithSession(aguiApprovalWorkerContext(ctx), session)
	go func() { _ = runAGUIDurable(workerCtx, c, c, store, record, &input, query, pending, prior) }()
	return nil
}

// This predicate represents recovery responsibility, not protocol ancestry.
// Existing initial/native-started workers retain their native lease recovery.
func (c *backendClient) IsAGUIApprovalRecoveryOwnedTurn(ctx context.Context, conversationID, turnID string) (bool, error) {
	if c == nil || c.goalInvoker == nil || c.conv == nil {
		return false, nil
	}
	conversation, err := c.conv.GetConversation(ctx, conversationID)
	if err != nil {
		return false, err
	}
	if conversation == nil || conversation.CreatedByUserId == nil {
		return false, nil
	}
	principal := *conversation.CreatedByUserId
	authorized := iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: principal})
	store := aguistore.New(c.goalInvoker)
	runs, err := store.ListRunsByTurn(authorized, principal, conversationID, turnID)
	if err != nil {
		return false, err
	}
	if len(runs) == 0 {
		// Host proxies own synthetic protocol threads but retain the canonical
		// globally unique native turn. Subscriber presence is never authority.
		runs, err = store.ListRunsByNativeTurn(authorized, principal, turnID)
		if err != nil {
			return false, err
		}
	}
	for _, run := range runs {
		var pending aguiPending
		if run.Status == aguistore.StatusInterrupted && run.ResumedByRunID == "" {
			if err = json.Unmarshal(run.Pending, &pending); err != nil {
				return false, err
			}
			for _, interrupt := range pending.Interrupts {
				if interrupt.Reason == "approval" {
					return true, nil
				}
			}
		}
		if run.PriorRunID == "" || (run.Status != aguistore.StatusAdmitted && run.Status != aguistore.StatusRunning) {
			continue
		}
		prior, err := store.GetRun(authorized, principal, run.ThreadID, run.PriorRunID)
		if err != nil {
			return false, err
		}
		if err = json.Unmarshal(prior.Pending, &pending); err != nil {
			return false, err
		}
		approval := false
		for _, interrupt := range pending.Interrupts {
			if interrupt.Reason == "approval" {
				approval = true
			}
		}
		if !approval {
			continue
		}
		var originalInput agui.RunAgentInput
		if json.Unmarshal(prior.Input, &originalInput) == nil {
			if proxy, handled, parseErr := ParseMCPAppsProxyRequest(originalInput.ForwardedProps); parseErr != nil {
				return false, parseErr
			} else if handled && proxy.Method == "tools/call" {
				app, bindErr := ResolveAGUIMCPApp(authorized, store, principal, proxy.ServerID, proxy.ServerHash)
				if bindErr != nil {
					return false, bindErr
				}
				if app.ThreadID != conversationID || prior.TurnID != turnID || run.TurnID != turnID {
					return false, fmt.Errorf("proxy approval recovery scope mismatch")
				}
				// The genuine proxy worker owns dispatch/receipt recovery even
				// while the native host effect runs. It never resumes a model.
				return true, nil
			}
		}
		if run.Status == aguistore.StatusAdmitted {
			return true, nil
		}
		if c.data == nil {
			return false, fmt.Errorf("approval recovery native phase unavailable")
		}
		turn, err := c.data.GetTurnByID(authorized, &turnmodel.TurnLookupInput{ID: turnID, ConversationID: conversationID, Has: &turnmodel.TurnLookupInputHas{ID: true, ConversationID: true}}, principalDataOpts(authorized)...)
		if err != nil {
			return false, err
		}
		if turn != nil && (strings.EqualFold(turn.Status, "waiting_for_user") || strings.EqualFold(turn.Status, "queued")) {
			return true, nil
		}
	}
	return false, nil
}

func (c *backendClient) aguiApprovalCommandSecurity(ctx context.Context, store aguistore.Store, candidate aguistore.ApprovalRecoveryCandidate) (string, error) {
	record, err := store.GetRun(ctx, candidate.Principal, candidate.ThreadID, candidate.RunID)
	if err != nil {
		return "", err
	}
	if record.TurnID != "" {
		return "", nil
	}
	var input struct {
		ForwardedProps map[string]json.RawMessage `json:"forwardedProps"`
	}
	if err = json.Unmarshal(record.Input, &input); err != nil {
		return "", err
	}
	var extension struct {
		Operation string                  `json:"operation"`
		Payload   AGUIApprovalDecideInput `json:"payload"`
	}
	if len(input.ForwardedProps["agently"]) == 0 {
		return "", nil
	}
	if err = json.Unmarshal(input.ForwardedProps["agently"], &extension); err != nil {
		return "", err
	}
	if extension.Operation != "approval.decide" {
		return "", nil
	}
	originalThread := record.ThreadID
	if extension.Payload.OriginalThreadID != "" {
		originalThread = extension.Payload.OriginalThreadID
	}
	original, err := store.GetRun(ctx, record.Principal, originalThread, extension.Payload.OriginalRunID)
	if err != nil {
		return "", err
	}
	if original.Principal != record.Principal || original.ThreadID != originalThread || original.TurnID == "" {
		return "", fmt.Errorf("approval recovery original scope mismatch")
	}
	if c.data == nil {
		return "", fmt.Errorf("approval recovery native security store unavailable")
	}
	run, err := c.data.GetRun(ctx, original.TurnID, &runmodel.RunRowsInput{}, principalDataOpts(ctx)...)
	if err != nil {
		return "", err
	}
	if run == nil && extension.Payload.OriginalThreadID != "" {
		return "", nil
	} // guest turns have no model run/security row
	if run == nil || run.ConversationId == nil || *run.ConversationId != original.ConversationID {
		return "", fmt.Errorf("approval recovery native security identity mismatch")
	}
	if run.EffectiveUserId != nil && *run.EffectiveUserId != "" && *run.EffectiveUserId != record.Principal {
		return "", fmt.Errorf("approval recovery native security principal mismatch")
	}
	return valueOrEmpty(run.SecurityContext), nil
}
