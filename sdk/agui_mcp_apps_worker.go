package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/mcpapps"
	"github.com/viant/agently-core/sdk/api"
)

type aguiMCPProxyPending struct {
	Interrupts        []agui.WireInterrupt `json:"interrupts,omitempty"`
	NativeTurnID      string               `json:"nativeTurnId,omitempty"`
	HostResult        json.RawMessage      `json:"hostResult,omitempty"`
	NativeOperationID string               `json:"nativeOperationId,omitempty"`
}

// runAGUIMCPProxyWorker never routes host envelopes through the model or shared
// projection. A persisted dispatch without a host receipt is uncertain and is
// never retried, preserving at-most-once native effects after a crash.
func runAGUIMCPProxyWorker(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, input *agui.RunAgentInput, proxy *MCPAppsProxyRequest, prior *aguistore.Run) error {
	claimed, err := store.Claim(ctx, record.Principal, record.ThreadID, record.RunID, record.Revision, uuid.NewString(), time.Minute)
	if err != nil {
		return err
	}
	record = claimed
	leaseCtx, stopLease := context.WithCancel(ctx)
	defer stopLease()
	leaseIdentity := *claimed
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		revision := leaseIdentity.LeaseRevision
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewed, e := store.Renew(leaseCtx, leaseIdentity.Principal, leaseIdentity.ThreadID, leaseIdentity.RunID, revision, leaseIdentity.LeaseOwner, time.Minute)
				if e != nil {
					stopLease()
					return
				}
				revision = renewed.LeaseRevision
			}
		}
	}()
	ctx = leaseCtx
	var pending aguiMCPProxyPending
	if err = json.Unmarshal(record.Pending, &pending); err != nil && len(record.Pending) > 0 && !bytes.Equal(bytes.TrimSpace(record.Pending), []byte("[]")) {
		return err
	}
	appendEvents := func(events []json.RawMessage) error {
		next, e := store.Append(ctx, record.Principal, record.ThreadID, record.RunID, record.Revision, events, &aguistore.Change{LeaseOwner: record.LeaseOwner, Pending: rawAGUI(pending)})
		if e == nil {
			record = next
		}
		return e
	}
	finishGuest := func(turnID, status string, cause error) error {
		binding, ok := ctx.Value(mcpAppsBindingKey{}).(AGUIMCPAppsBindings)
		if !ok || proxy.Method != "tools/call" || turnID == "" {
			return nil
		}
		if finalizer, ok := client.(interface {
			aguiFinishMCPGuestTurn(context.Context, string, string, string, error) error
		}); ok {
			return finalizer.aguiFinishMCPGuestTurn(ctx, binding.App.ThreadID, turnID, status, cause)
		}
		return nil
	}
	finish := func(result json.RawMessage, failure error) error {
		if failure != nil {
			return appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_ERROR", "message": failure.Error(), "code": "MCP_PROXY_FAILED"})})
		}
		if len(result) > 0 {
			var hostResult struct {
				IsError bool `json:"isError"`
			}
			if err := json.Unmarshal(result, &hostResult); err != nil {
				return err
			}
			status := "succeeded"
			var toolFailure error
			if hostResult.IsError {
				status = "failed"
				toolFailure = fmt.Errorf("MCP app tool reported failure")
			}
			turnID := pending.NativeTurnID
			if turnID == "" {
				turnID = record.TurnID
			}
			if err := finishGuest(turnID, status, toolFailure); err != nil {
				return err
			}
		}
		return appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": record.ThreadID, "runId": record.RunID, "result": result, "outcome": map[string]any{"type": "success"}})})
	}
	if record.LastSequence > 0 {
		if len(pending.HostResult) > 0 {
			if prior != nil {
				binding, ok := ctx.Value(mcpAppsBindingKey{}).(AGUIMCPAppsBindings)
				if !ok || binding.Authorize == nil || binding.AuthorizeTool == nil {
					return finish(nil, fmt.Errorf("MCP approval recovery binding unavailable"))
				}
				var name string
				_ = json.Unmarshal(proxy.Params["name"], &name)
				if err = binding.Authorize(ctx, binding.App); err != nil {
					return finish(nil, err)
				}
				if err = binding.AuthorizeTool(ctx, binding.App, name); err != nil {
					return finish(nil, err)
				}
				var originalPending aguiMCPProxyPending
				if json.Unmarshal(prior.Pending, &originalPending) != nil || len(originalPending.Interrupts) != 1 {
					return finish(nil, fmt.Errorf("MCP approval recovery identity mismatch"))
				}
				interrupt := originalPending.Interrupts[0]
				if pending.NativeTurnID != record.TurnID || pending.NativeTurnID != originalPending.NativeTurnID || interrupt.ToolCallID == nil || pending.NativeOperationID != *interrupt.ToolCallID {
					return finish(nil, fmt.Errorf("MCP approval recovered host receipt identity mismatch"))
				}
				verifier, ok := client.(interface {
					aguiCompletedApprovalReceipt(context.Context, string, agui.WireInterrupt) (*agui.WireResumeEntry, *api.DecideToolApprovalOutcome, error)
				})
				if !ok {
					return finish(nil, fmt.Errorf("MCP approval receipt recovery unavailable"))
				}
				receipt, outcome, verifyErr := verifier.aguiCompletedApprovalReceipt(ctx, binding.App.ThreadID, originalPending.Interrupts[0])
				if verifyErr != nil || receipt == nil || outcome == nil {
					return finish(nil, fmt.Errorf("MCP host effect captured without complete native approval outcome; effect will not be repeated"))
				}
				var accepted []agui.WireResumeEntry
				if json.Unmarshal(input.Resume, &accepted) != nil || len(accepted) != 1 {
					return finish(nil, fmt.Errorf("MCP approval recovered answer unavailable"))
				}
				proposed, _ := canonicalJSONValue(rawAGUI(accepted[0]))
				completed, _ := canonicalJSONValue(rawAGUI(receipt))
				if !bytes.Equal(proposed, completed) {
					return finish(nil, fmt.Errorf("MCP approval recovered answer mismatch"))
				}
			}
			return finish(pending.HostResult, nil)
		}
		return finish(nil, fmt.Errorf("MCP proxy dispatch interrupted without a durable host receipt; effect will not be repeated"))
	}
	if err = appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": record.ThreadID, "runId": record.RunID, "protocolVersion": "1.0"})}); err != nil {
		return err
	}
	ctx = mcpapps.WithRecorder(ctx, func(result json.RawMessage, native string) error {
		if native == "" {
			return fmt.Errorf("MCP host receipt has no canonical native operation")
		}
		if prior != nil {
			var previous aguiMCPProxyPending
			if json.Unmarshal(prior.Pending, &previous) != nil || len(previous.Interrupts) != 1 || previous.Interrupts[0].ToolCallID == nil || *previous.Interrupts[0].ToolCallID != native {
				return fmt.Errorf("MCP host receipt differs from approved native operation")
			}
		}
		pending.HostResult = append(json.RawMessage(nil), result...)
		pending.NativeTurnID = record.TurnID
		pending.NativeOperationID = native
		return appendEvents(nil)
	})
	ctx = mcpapps.WithTurnID(ctx, record.TurnID)
	if prior != nil {
		var previous aguiMCPProxyPending
		if err = json.Unmarshal(prior.Pending, &previous); err != nil {
			return finish(nil, err)
		}
		var answers []agui.WireResumeEntry
		if err = json.Unmarshal(input.Resume, &answers); err != nil {
			return finish(nil, err)
		}
		if len(previous.Interrupts) != 1 || len(answers) != 1 || previous.Interrupts[0].ID != answers[0].InterruptId {
			return finish(nil, fmt.Errorf("MCP proxy approval answer mismatch"))
		}
		binding, ok := ctx.Value(mcpAppsBindingKey{}).(AGUIMCPAppsBindings)
		if !ok {
			return finish(nil, fmt.Errorf("MCP app binding unavailable"))
		}
		applier, ok := client.(interface {
			aguiApplyInterrupt(context.Context, *aguistore.Run, agui.WireInterrupt, agui.WireResumeEntry) (aguiInterruptDisposition, error)
		})
		if !ok {
			return finish(nil, fmt.Errorf("MCP proxy approval continuation unavailable"))
		}
		var name string
		_ = json.Unmarshal(proxy.Params["name"], &name)
		if binding.Authorize == nil || binding.AuthorizeTool == nil {
			return finish(nil, fmt.Errorf("MCP app authorization unavailable"))
		}
		if err = binding.Authorize(ctx, binding.App); err != nil {
			return finish(nil, err)
		}
		if err = binding.AuthorizeTool(ctx, binding.App, name); err != nil {
			return finish(nil, err)
		}
		captureCtx, capture := mcpapps.WithCapture(ctx, binding.App.ServerID, name, record.RunID)
		native := *prior
		native.ConversationID = binding.App.ThreadID
		native.TurnID = previous.NativeTurnID
		_, err = applier.aguiApplyInterrupt(captureCtx, &native, previous.Interrupts[0], answers[0])
		if err != nil {
			return finish(nil, err)
		}
		answer, decodeErr := decodeAGUIApprovalAnswer(answers[0])
		if decodeErr != nil {
			return finish(nil, decodeErr)
		}
		if answer.Action != "approve" {
			status := "failed"
			if answer.Action == "cancel" {
				status = "canceled"
			}
			if err := finishGuest(previous.NativeTurnID, status, fmt.Errorf("MCP app tool was not approved")); err != nil {
				return err
			}
			return appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_ERROR", "code": "MCP_PROXY_REJECTED", "message": "MCP app tool was not approved"})})
		}
		result, _ := capture.Snapshot()
		if len(result) == 0 && len(pending.HostResult) > 0 {
			result = append(json.RawMessage(nil), pending.HostResult...)
		}
		if len(result) == 0 {
			return finish(nil, fmt.Errorf("approval completed without durable full MCP host result"))
		}
		return finish(result, nil)
	}
	result, output, err := dispatchMCPAppsProxy(ctx, record.RunID, proxy)
	if err != nil {
		return finish(nil, err)
	}
	if output != nil {
		if output.Approval == nil {
			return finish(nil, fmt.Errorf("queued MCP call has no authoritative approval"))
		}
		interrupt, e := aguiApprovalInterrupt(output.Approval)
		if e != nil {
			return finish(nil, e)
		}
		pending.Interrupts = []agui.WireInterrupt{interrupt}
		pending.NativeTurnID = output.TurnID
		return appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": record.ThreadID, "runId": record.RunID, "outcome": map[string]any{"type": "interrupt", "interrupts": pending.Interrupts}})})
	}
	return finish(result, nil)
}
