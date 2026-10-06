package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	exportrequestmodel "github.com/viant/agently-core/model/exportrequest"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/agui"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/mcpapps"
	"github.com/viant/agently-core/runtime/requestctx"
	agentsvc "github.com/viant/agently-core/service/agent"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

type proxyApprovalRegistry struct {
	integrationToolRegistry
	count   atomic.Int64
	started chan struct{}
	release chan struct{}
}

func (r *proxyApprovalRegistry) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	r.count.Add(1)
	if r.started != nil {
		close(r.started)
		select {
		case <-r.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	turn, ok := requestctx.TurnMetaFromContext(ctx)
	if !ok || turn.ConversationID != "native-host" {
		return "", fmt.Errorf("wrong guest scope")
	}
	var result mcpschema.CallToolResult
	if err := json.Unmarshal([]byte(`{"resultType":"complete","content":[{"type":"text","text":"safe host result"}],"structuredContent":{"answer":42},"_meta":{"private":"host-only"}}`), &result); err != nil {
		return "", err
	}
	if err := mcpapps.Record(ctx, "native-server", "tool", exportrequestmodel.ID(ctx), &result); err != nil {
		return "", err
	}
	return "safe host result", nil
}

type proxyApprovalFixture struct {
	backend   *backendClient
	store     *aguistore.ComponentStore
	original  *aguistore.Run
	app       AGUIMCPAppBinding
	proxy     *MCPAppsProxyRequest
	pending   aguiMCPProxyPending
	registry  *proxyApprovalRegistry
	workspace string
	revoked   *atomic.Bool
	close     func()
}

func openProxyApprovalBackend(t *testing.T, workspace string, registry *proxyApprovalRegistry, revoked *atomic.Bool) (*backendClient, *aguistore.ComponentStore, func()) {
	t.Helper()
	ctx := recoveryContext()
	runtime, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	conv, err := convservice.New(ctx, runtime)
	require.NoError(t, err)
	ds := data.NewService(runtime)
	agent := agentsvc.New(nil, nil, nil, registry, nil, conv, agentsvc.WithDataService(ds), agentsvc.WithToolBundles(func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "queue", Match: []llm.Tool{{Name: "native-server/tool", Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue}}}}}, nil
	}))
	backend := &backendClient{agent: agent, conv: conv, data: ds, goalInvoker: runtime, registry: registry}
	backend.approvalMCPHost = &AGUIMCPAppsHost{Authorize: func(_ context.Context, app AGUIMCPAppBinding) error {
		if revoked.Load() || app.ThreadID != "native-host" || app.ServerID != "native-server" {
			return fmt.Errorf("app revoked")
		}
		return nil
	}, AuthorizeTool: func(_ context.Context, app AGUIMCPAppBinding, name string) error {
		if revoked.Load() || app.ServerID != "native-server" || name != "tool" {
			return fmt.Errorf("tool revoked")
		}
		return nil
	}, ToolCaller: func(ctx context.Context, input *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) {
		prepared, err := agent.PrepareMCPAppToolContext(ctx, &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"queue"}}}, input.ToolName)
		if err != nil {
			return nil, err
		}
		return backend.ExecuteMCPUIToolCall(prepared, input)
	}}
	return backend, aguistore.New(runtime), func() { require.NoError(t, runtime.Shutdown(ctx)) }
}
func newProxyApprovalFixture(t *testing.T) *proxyApprovalFixture {
	t.Helper()
	ctx := recoveryContext()
	workspace := t.TempDir()
	registry := &proxyApprovalRegistry{integrationToolRegistry: integrationToolRegistry{defs: []llm.ToolDefinition{{Name: "native-server/tool"}}}}
	revoked := &atomic.Bool{}
	backend, store, close := openProxyApprovalBackend(t, workspace, registry, revoked)
	t.Cleanup(close)
	root := apiconv.NewConversation()
	root.SetId("native-host")
	root.SetCreatedByUserID("owner")
	root.SetVisibility("private")
	root.SetStatus("active")
	require.NoError(t, backend.conv.PatchConversations(ctx, root))
	registration, _, err := store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "native-host", RunID: "registration", TurnID: "registration-turn", Input: json.RawMessage(`{"threadId":"native-host","runId":"registration","messages":[{"id":"user","role":"user","content":"register host"}]}`)})
	require.NoError(t, err)
	app := AGUIMCPAppBinding{ThreadID: "native-host", ServerID: "native-server", ServerHash: "server-hash", ResourceURI: "ui://host"}
	raw, err := MCPAppsActivity("registered-host", app, json.RawMessage(`{"content":[{"type":"text","text":"host"}]}`), json.RawMessage(`{}`))
	require.NoError(t, err)
	registered, err := registerMCPAppActivity(raw, registration, 2)
	require.NoError(t, err)
	var activity struct {
		Content map[string]json.RawMessage `json:"content"`
	}
	require.NoError(t, json.Unmarshal(registered, &activity))
	require.NoError(t, json.Unmarshal(activity.Content["_agentlyApp"], &app))
	thread, err := store.GetThread(ctx, "owner", "native-host")
	require.NoError(t, err)
	_, err = store.Append(ctx, "owner", "native-host", registration.RunID, registration.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "native-host", "runId": registration.RunID}), registered, rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "native-host", "runId": registration.RunID, "outcome": map[string]any{"type": "success"}})}, &aguistore.Change{Messages: rawAGUI([]any{map[string]any{"id": "registered-host", "role": "activity", "activityType": "mcp-apps", "content": activity.Content}}), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	proxy := &MCPAppsProxyRequest{ServerID: app.PublicServerID, ServerHash: app.ServerHash, Method: "tools/call", Params: map[string]json.RawMessage{"name": json.RawMessage(`"tool"`), "arguments": json.RawMessage(`{}`)}}
	input := &agui.RunAgentInput{ThreadID: "synthetic-proxy", RunID: "proxy-original", Messages: []agui.Message{}, ForwardedProps: rawAGUI(map[string]any{"__proxiedMCPRequest": proxy})}
	original, _, err := store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: "host-native-turn", Input: rawAGUI(input)})
	require.NoError(t, err)
	bound := WithAGUIMCPAppsBindings(ctx, backend.aguiMCPAppsHost().Bind(ctx, app))
	require.NoError(t, runAGUIMCPProxyWorker(bound, backend, store, original, input, proxy, nil))
	original, err = store.GetRun(ctx, "owner", input.ThreadID, input.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusInterrupted, original.Status)
	var pending aguiMCPProxyPending
	require.NoError(t, json.Unmarshal(original.Pending, &pending))
	require.Len(t, pending.Interrupts, 1)
	return &proxyApprovalFixture{backend: backend, store: store, original: original, app: app, proxy: proxy, pending: pending, registry: registry, workspace: workspace, revoked: revoked, close: close}
}
func TestMCPProxyInboxDecisionUsesGenuineSuccessorFullHostResult(t *testing.T) {
	for _, action := range []string{"approve", "reject", "cancel"} {
		t.Run(action, func(t *testing.T) {
			f := newProxyApprovalFixture(t)
			out, err := f.backend.DecideToolApproval(recoveryContext(), &DecideToolApprovalInput{ID: f.pending.Interrupts[0].ID, Action: action})
			require.NoError(t, err)
			require.Equal(t, "mcp-app", out.Protocol.Kind)
			require.Equal(t, "synthetic-proxy", out.Protocol.ThreadID)
			require.Equal(t, "native-host", out.Protocol.NativeConversationID)
			require.Equal(t, "native-host", out.Outcome.ConversationID)
			successor, err := f.store.GetRun(recoveryContext(), "owner", out.Protocol.ThreadID, out.Protocol.ContinuationRunID)
			require.NoError(t, err)
			events, err := aguiRecoveryJournal(recoveryContext(), f.store, successor)
			require.NoError(t, err)
			if action == "approve" {
				require.Equal(t, aguistore.StatusFinished, successor.Status)
				require.Contains(t, string(events[len(events)-1]), `"structuredContent":{"answer":42}`)
				require.Contains(t, string(events[len(events)-1]), `"private":"host-only"`)
				require.Equal(t, int64(1), f.registry.count.Load())
			} else {
				require.Equal(t, aguistore.StatusError, successor.Status)
				require.Zero(t, f.registry.count.Load())
			}
			nativeThread, err := f.store.GetThread(recoveryContext(), "owner", "native-host")
			require.NoError(t, err)
			var graph []aguistate.Object
			require.NoError(t, json.Unmarshal(nativeThread.Messages, &graph))
			for _, message := range graph {
				if message["role"] != "activity" {
					require.NotContains(t, string(rawAGUI(message)), "host-only")
				}
			}
		})
	}
}

func TestMCPProxyInboxTimeoutIsRealAndDoesNotExecuteTool(t *testing.T) {
	f := newProxyApprovalFixture(t)
	ctx := recoveryContext()
	id := f.pending.Interrupts[0].ID
	past := time.Now().UTC().Add(-time.Hour)
	patch := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	patch.SetId(id)
	patch.SetUserId("owner")
	patch.SetExpiresAt(past)
	require.NoError(t, f.backend.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(ctx, patch))
	out, err := f.backend.DecideToolApproval(ctx, &DecideToolApprovalInput{ID: id, Action: "approve"})
	require.NoError(t, err)
	require.Equal(t, "timed_out", out.Outcome.Status)
	require.Equal(t, "timeout", out.Outcome.Action)
	require.Zero(t, f.registry.count.Load())
	require.Equal(t, "mcp-app", out.Protocol.Kind)
	successor, err := f.store.GetRun(ctx, "owner", out.Protocol.ThreadID, out.Protocol.ContinuationRunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusError, successor.Status)
}
func TestMCPProxyInboxIdenticalReplayAfterRuntimeRestartKeepsFullReceipt(t *testing.T) {
	f := newProxyApprovalFixture(t)
	ctx := recoveryContext()
	input := &DecideToolApprovalInput{ID: f.pending.Interrupts[0].ID, Action: "approve"}
	first, err := f.backend.DecideToolApproval(ctx, input)
	require.NoError(t, err)
	f.close()
	restarted, store, closeRestarted := openProxyApprovalBackend(t, f.workspace, f.registry, f.revoked)
	defer closeRestarted()
	second, err := restarted.DecideToolApproval(ctx, input)
	require.NoError(t, err)
	require.JSONEq(t, string(rawAGUI(first)), string(rawAGUI(second)))
	require.Equal(t, int64(1), f.registry.count.Load())
	successor, err := store.GetRun(ctx, "owner", second.Protocol.ThreadID, second.Protocol.ContinuationRunID)
	require.NoError(t, err)
	journal, err := aguiRecoveryJournal(ctx, store, successor)
	require.NoError(t, err)
	require.Contains(t, string(journal[len(journal)-1]), `"private":"host-only"`)
	control := &aguistore.Run{Principal: "owner", ThreadID: second.Protocol.ThreadID, RunID: "control"}
	result, err := dispatchAGUIRunCommand(ctx, restarted, store, control, "run.get", rawAGUI(map[string]any{"runId": f.original.RunID}))
	require.NoError(t, err)
	require.Equal(t, successor.RunID, result.(map[string]any)["resumedByRunId"])
}
func TestMCPProxyInboxRevokedBindingPreventsDecisionAndContinuation(t *testing.T) {
	f := newProxyApprovalFixture(t)
	f.revoked.Store(true)
	_, err := f.backend.DecideToolApproval(recoveryContext(), &DecideToolApprovalInput{ID: f.pending.Interrupts[0].ID, Action: "approve"})
	require.ErrorContains(t, err, "revoked")
	require.Zero(t, f.registry.count.Load())
	original, err := f.store.GetRun(recoveryContext(), "owner", f.original.ThreadID, f.original.RunID)
	require.NoError(t, err)
	require.Empty(t, original.ResumedByRunID)
	row, err := f.backend.aguiApprovalRow(recoveryContext(), "native-host", f.pending.Interrupts[0])
	require.NoError(t, err)
	require.Equal(t, "pending", row.Status)
}
func TestMCPProxyDirectResumeAndInboxCoordinateOneSuccessorAndEffect(t *testing.T) {
	f := newProxyApprovalFixture(t)
	ctx := recoveryContext()
	answer := approvalAnswer(f.pending.Interrupts[0].ID, "approve")
	var input agui.RunAgentInput
	require.NoError(t, json.Unmarshal(f.original.Input, &input))
	input.RunID = "direct-proxy-successor"
	input.Resume = rawAGUI([]agui.WireResumeEntry{answer})
	direct, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: f.original.TurnID, PriorRunID: f.original.RunID, ExpectedPriorRevision: f.original.Revision, Input: rawAGUI(&input)})
	require.NoError(t, err)
	bound := WithAGUIMCPAppsBindings(ctx, f.backend.aguiMCPAppsHost().Bind(ctx, f.app))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = runAGUIMCPProxyWorker(bound, f.backend, f.store, direct, &input, f.proxy, f.original)
	}()
	out, err := f.backend.DecideToolApproval(ctx, &DecideToolApprovalInput{ID: f.pending.Interrupts[0].ID, Action: "approve"})
	wg.Wait()
	require.NoError(t, err)
	require.Equal(t, direct.RunID, out.Protocol.ContinuationRunID)
	require.Equal(t, int64(1), f.registry.count.Load())
	runs, err := f.store.ListRunsByNativeTurn(ctx, "owner", f.pending.NativeTurnID)
	require.NoError(t, err)
	require.Len(t, runs, 2)
}
func TestMCPProxyCapturedHostWithoutNativeOutcomeIsNeverRepeated(t *testing.T) {
	f := newProxyApprovalFixture(t)
	ctx := recoveryContext()
	answer := approvalAnswer(f.pending.Interrupts[0].ID, "approve")
	row, err := f.backend.aguiApprovalRow(ctx, "native-host", f.pending.Interrupts[0])
	require.NoError(t, err)
	metadata := aguiApprovalMetadata(row)
	metadata["aguiDecision"], err = canonicalJSONValue(rawAGUI(answer))
	require.NoError(t, err)
	blob := []byte(rawAGUI(metadata))
	row.Metadata = &blob
	require.NoError(t, f.backend.conv.(interface {
		ClaimToolApprovalDecision(context.Context, *toolapprovalqueuemodel.QueueRowView, string, string) error
	}).ClaimToolApprovalDecision(ctx, row, "owner", "approve"))
	var input agui.RunAgentInput
	require.NoError(t, json.Unmarshal(f.original.Input, &input))
	input.RunID = "captured-uncertain"
	input.Resume = rawAGUI([]agui.WireResumeEntry{answer})
	successor, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: f.original.TurnID, PriorRunID: f.original.RunID, ExpectedPriorRevision: f.original.Revision, Input: rawAGUI(&input)})
	require.NoError(t, err)
	op := *f.pending.Interrupts[0].ToolCallID
	_, err = f.store.Append(ctx, "owner", successor.ThreadID, successor.RunID, successor.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": successor.ThreadID, "runId": successor.RunID})}, &aguistore.Change{Pending: rawAGUI(aguiMCPProxyPending{NativeTurnID: f.pending.NativeTurnID, NativeOperationID: op, HostResult: json.RawMessage(`{"content":[{"type":"text","text":"definite captured result"}],"_meta":{"secret":"captured"}}`)})})
	require.NoError(t, err)
	require.NoError(t, f.backend.ReconcileAGUIApprovals(ctx))
	require.Eventually(t, func() bool {
		record, err := f.store.GetRun(ctx, "owner", successor.ThreadID, successor.RunID)
		return err == nil && record.Status == aguistore.StatusError
	}, 3*time.Second, 10*time.Millisecond)
	require.Zero(t, f.registry.count.Load())
	original, err := f.backend.aguiApprovalRow(ctx, "native-host", f.pending.Interrupts[0])
	require.NoError(t, err)
	require.Equal(t, "approved", original.Status)
	require.Empty(t, aguiApprovalMetadata(original)["aguiOutcome"])
}

func TestMCPProxyInboxObservesInFlightNativeClaimWithoutRepeatingEffect(t *testing.T) {
	f := newProxyApprovalFixture(t)
	ctx := recoveryContext()
	f.registry.started, f.registry.release = make(chan struct{}), make(chan struct{})
	var input agui.RunAgentInput
	require.NoError(t, json.Unmarshal(f.original.Input, &input))
	input.RunID = "inflight-direct"
	input.Resume = rawAGUI([]agui.WireResumeEntry{approvalAnswer(f.pending.Interrupts[0].ID, "approve")})
	owned, err := f.backend.IsAGUIApprovalRecoveryOwnedTurn(ctx, "native-host", f.original.TurnID)
	require.NoError(t, err)
	require.True(t, owned)
	direct, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: f.original.TurnID, PriorRunID: f.original.RunID, ExpectedPriorRevision: f.original.Revision, Input: rawAGUI(&input)})
	require.NoError(t, err)
	bound := WithAGUIMCPAppsBindings(ctx, f.backend.aguiMCPAppsHost().Bind(ctx, f.app))
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- runAGUIMCPProxyWorker(bound, f.backend, f.store, direct, &input, f.proxy, f.original)
	}()
	<-f.registry.started // The native claim is now committed, outcome still absent.
	owned, err = f.backend.IsAGUIApprovalRecoveryOwnedTurn(ctx, "native-host", f.original.TurnID)
	require.NoError(t, err)
	require.True(t, owned)
	var output *DecideToolApprovalOutput
	decisionDone := make(chan error, 1)
	go func() {
		var decisionErr error
		output, decisionErr = f.backend.DecideToolApproval(ctx, &DecideToolApprovalInput{ID: f.pending.Interrupts[0].ID, Action: "approve"})
		decisionDone <- decisionErr
	}()
	select {
	case err := <-decisionDone:
		close(f.registry.release)
		t.Fatalf("inbox returned before real native outcome: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(f.registry.release)
	require.NoError(t, <-workerDone)
	require.NoError(t, <-decisionDone)
	require.Equal(t, direct.RunID, output.Protocol.ContinuationRunID)
	require.Equal(t, int64(1), f.registry.count.Load())
}

func TestMCPProxyInboxDispatchesPreviouslyAdmittedSuccessor(t *testing.T) {
	f := newProxyApprovalFixture(t)
	ctx := recoveryContext()
	var input agui.RunAgentInput
	require.NoError(t, json.Unmarshal(f.original.Input, &input))
	input.RunID = "admitted-without-worker"
	input.Resume = rawAGUI([]agui.WireResumeEntry{approvalAnswer(f.pending.Interrupts[0].ID, "approve")})
	direct, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: f.original.TurnID, PriorRunID: f.original.RunID, ExpectedPriorRevision: f.original.Revision, Input: rawAGUI(&input)})
	require.NoError(t, err)
	out, err := f.backend.DecideToolApproval(ctx, &DecideToolApprovalInput{ID: f.pending.Interrupts[0].ID, Action: "approve"})
	require.NoError(t, err)
	require.Equal(t, direct.RunID, out.Protocol.ContinuationRunID)
	require.Equal(t, int64(1), f.registry.count.Load())
}

func TestMCPProxyInboxCannotReplaceAdmittedAnswer(t *testing.T) {
	f := newProxyApprovalFixture(t)
	ctx := recoveryContext()
	var input agui.RunAgentInput
	require.NoError(t, json.Unmarshal(f.original.Input, &input))
	input.RunID = "admitted-rejection"
	input.Resume = rawAGUI([]agui.WireResumeEntry{approvalAnswer(f.pending.Interrupts[0].ID, "reject")})
	_, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: f.original.TurnID, PriorRunID: f.original.RunID, ExpectedPriorRevision: f.original.Revision, Input: rawAGUI(&input)})
	require.NoError(t, err)
	_, err = f.backend.DecideToolApproval(ctx, &DecideToolApprovalInput{ID: f.pending.Interrupts[0].ID, Action: "approve"})
	require.ErrorContains(t, err, "different request or answer")
	require.Zero(t, f.registry.count.Load())
	foreign, err := f.store.ListRunsByNativeTurn(ctx, "foreign-principal", f.original.TurnID)
	require.NoError(t, err)
	require.Empty(t, foreign)
}

func TestMCPProxyWatchdogTimesOutUnansweredApprovalWithoutBrowser(t *testing.T) {
	f := newProxyApprovalFixture(t)
	ctx := recoveryContext()
	id := f.pending.Interrupts[0].ID
	patch := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	patch.SetId(id)
	patch.SetUserId("owner")
	patch.SetExpiresAt(time.Now().UTC().Add(-time.Hour))
	require.NoError(t, f.backend.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(ctx, patch))
	require.NoError(t, f.backend.ReconcileAGUIApprovals(ctx))
	original, err := f.store.GetRun(ctx, "owner", f.original.ThreadID, f.original.RunID)
	require.NoError(t, err)
	require.NotEmpty(t, original.ResumedByRunID)
	require.Eventually(t, func() bool {
		successor, err := f.store.GetRun(ctx, "owner", original.ThreadID, original.ResumedByRunID)
		return err == nil && successor.Status == aguistore.StatusError
	}, 3*time.Second, 10*time.Millisecond)
	_, outcome, err := f.backend.aguiCompletedApprovalReceipt(ctx, "native-host", f.pending.Interrupts[0])
	require.NoError(t, err)
	require.Equal(t, "timed_out", outcome.Status)
	require.Zero(t, f.registry.count.Load())
}

// Simulate a process loss after the host recorder and native outcome commit,
// but before the final protocol event commits. All persistence is real Datly.
type proxyTerminalLossStore struct{ aguistore.Store }

func (s proxyTerminalLossStore) Claim(ctx context.Context, principal, threadID, runID string, revision int64, owner string, _ time.Duration) (*aguistore.Run, error) {
	return s.Store.Claim(ctx, principal, threadID, runID, revision, owner, time.Minute)
}
func (s proxyTerminalLossStore) Append(ctx context.Context, principal, threadID, runID string, revision int64, events []json.RawMessage, change *aguistore.Change) (*aguistore.Run, error) {
	for _, event := range events {
		var header struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(event, &header)
		if header.Type == "RUN_FINISHED" {
			current, err := s.Store.GetRun(ctx, principal, threadID, runID)
			if err != nil {
				return nil, err
			}
			if _, err = s.Store.Renew(ctx, principal, threadID, runID, current.LeaseRevision, current.LeaseOwner, time.Millisecond); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("simulated terminal commit loss")
		}
	}
	return s.Store.Append(ctx, principal, threadID, runID, revision, events, change)
}

func TestMCPProxyRestartRecoversCompletedHostAndNativeReceiptWithoutEffect(t *testing.T) {
	f := newProxyApprovalFixture(t)
	ctx := recoveryContext()
	var input agui.RunAgentInput
	require.NoError(t, json.Unmarshal(f.original.Input, &input))
	input.RunID = "lost-final-host-response"
	input.Resume = rawAGUI([]agui.WireResumeEntry{approvalAnswer(f.pending.Interrupts[0].ID, "approve")})
	successor, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: f.original.TurnID, PriorRunID: f.original.RunID, ExpectedPriorRevision: f.original.Revision, Input: rawAGUI(&input)})
	require.NoError(t, err)
	bound := WithAGUIMCPAppsBindings(ctx, f.backend.aguiMCPAppsHost().Bind(ctx, f.app))
	require.ErrorContains(t, runAGUIMCPProxyWorker(bound, f.backend, proxyTerminalLossStore{f.store}, successor, &input, f.proxy, f.original), "simulated terminal commit loss")
	require.Equal(t, int64(1), f.registry.count.Load())
	receipt, outcome, err := f.backend.aguiCompletedApprovalReceipt(ctx, "native-host", f.pending.Interrupts[0])
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.NotNil(t, outcome)
	f.close()
	restarted, store, closeRestarted := openProxyApprovalBackend(t, f.workspace, f.registry, f.revoked)
	defer closeRestarted()
	require.NoError(t, restarted.ReconcileAGUIApprovals(ctx))
	require.Eventually(t, func() bool {
		record, err := store.GetRun(ctx, "owner", successor.ThreadID, successor.RunID)
		return err == nil && record.Status == aguistore.StatusFinished
	}, 3*time.Second, 10*time.Millisecond)
	recovered, err := store.GetRun(ctx, "owner", successor.ThreadID, successor.RunID)
	require.NoError(t, err)
	journal, err := aguiRecoveryJournal(ctx, store, recovered)
	require.NoError(t, err)
	require.Contains(t, string(journal[len(journal)-1]), `"private":"host-only"`)
	require.Equal(t, int64(1), f.registry.count.Load())
}

func TestMCPProxyRestartRecoversTimeoutCommandAfterReceiptBeforeResponse(t *testing.T) {
	f := newProxyApprovalFixture(t)
	ctx := recoveryContext()
	id := f.pending.Interrupts[0].ID
	patch := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	patch.SetId(id)
	patch.SetUserId("owner")
	patch.SetExpiresAt(time.Now().UTC().Add(-time.Hour))
	require.NoError(t, f.backend.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(ctx, patch))
	payload := AGUIApprovalDecideInput{OriginalThreadID: f.original.ThreadID, OriginalRunID: f.original.RunID, ApprovalID: id, Answer: approvalAnswer(id, "approve")}
	body := rawAGUI(map[string]any{"threadId": "native-host", "runId": "timeout-command-response-loss", "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "operation": "approval.decide", "requestId": "timeout-command-response-loss", "payload": payload}}})
	command, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "native-host", RunID: "timeout-command-response-loss", Input: body})
	require.NoError(t, err)
	claimed, err := f.store.Claim(ctx, "owner", command.ThreadID, command.RunID, command.Revision, "lost-command-owner", time.Minute)
	require.NoError(t, err)
	require.ErrorContains(t, f.backend.aguiRunProxyApprovalCommand(ctx, proxyTerminalLossStore{f.store}, claimed, payload), "simulated terminal commit loss")
	f.close()
	restarted, store, closeRestarted := openProxyApprovalBackend(t, f.workspace, f.registry, f.revoked)
	defer closeRestarted()
	require.NoError(t, restarted.ReconcileAGUIApprovals(ctx))
	require.Eventually(t, func() bool {
		recovered, err := store.GetRun(ctx, "owner", command.ThreadID, command.RunID)
		return err == nil && recovered.Status == aguistore.StatusFinished
	}, 3*time.Second, 10*time.Millisecond)
	recovered, err := store.GetRun(ctx, "owner", command.ThreadID, command.RunID)
	require.NoError(t, err)
	journal, err := aguiRecoveryJournal(ctx, store, recovered)
	require.NoError(t, err)
	require.Contains(t, string(journal[len(journal)-1]), `"status":"timed_out"`)
	require.Zero(t, f.registry.count.Load())
}
