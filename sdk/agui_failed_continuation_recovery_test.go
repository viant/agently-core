package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/clienttool"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type failedContinuationClient struct {
	*recoveryAGUIClient
	mu                sync.Mutex
	results           []string
	controlsPreserved bool
}

func (c *failedContinuationClient) aguiCompleteTool(_ context.Context, call clienttool.PendingCall, raw json.RawMessage, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if call.ID != "original-call" || string(raw) != `"already executed fixture result"` {
		return fmt.Errorf("accepted original result changed")
	}
	c.results = append(c.results, call.ID)
	c.completions.Add(1)
	return nil
}

func (c *failedContinuationClient) aguiResume(ctx context.Context, query *agentsvc.QueryInput, turn string, out *agentsvc.QueryOutput) error {
	c.mu.Lock()
	c.controlsPreserved = query.AutoSelectTools != nil && !*query.AutoSelectTools && query.DisableChains && len(query.ToolsAllowed) == 1 && query.ToolsAllowed[0] == "fixture/read" && len(query.ToolBundles) == 1 && query.ToolBundles[0] == "fixture"
	c.mu.Unlock()
	return c.durableAGUIClient.aguiResume(ctx, query, turn, out)
}

func failedContinuationFixture(t *testing.T, extra ...json.RawMessage) (*failedContinuationClient, *aguistore.Run, *aguistore.Run, clienttool.PendingCall) {
	t.Helper()
	c := &failedContinuationClient{recoveryAGUIClient: newRecoveryAGUIClient(t)}
	ctx := recoveryContext()
	call := clienttool.PendingCall{ID: "original-call", Name: "browser", Arguments: map[string]interface{}{}, ConversationID: "thread", TurnID: "native-turn", ToolMessageID: "native-tool", AssistantMessageID: "assistant"}
	rootBody := json.RawMessage(`{"threadId":"thread","runId":"initial","messages":[{"id":"user","role":"user","content":"generic review"}],"forwardedProps":{"agently":{"version":"1","operation":"chat","payload":{"autoSelectTools":false,"disableChains":true,"backendTools":["fixture/read"],"toolBundles":["fixture"]}}}}`)
	root, _, err := c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: "initial", TurnID: "native-turn", Input: rootBody})
	require.NoError(t, err)
	pending := aguiPending{ClientTools: []clienttool.PendingCall{call}}
	root, err = c.store.Append(ctx, "owner", "thread", root.RunID, root.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": "initial"}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "thread", "runId": "initial", "outcome": map[string]any{"type": "success", "pendingToolCallIds": []string{aguiPendingCallID(call)}}})}, &aguistore.Change{Pending: rawAGUI(pending)})
	require.NoError(t, err)
	body := rawAGUI(map[string]any{"threadId": "thread", "runId": "failed-child", "parentRunId": "initial", "messages": []any{map[string]any{"id": "original-result", "role": "tool", "toolCallId": aguiPendingCallID(call), "content": "already executed fixture result"}}, "tools": []any{map[string]any{"name": "browser", "description": "generic fixture", "parameters": map[string]any{"type": "object"}}}})
	failed, _, err := c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: "failed-child", ParentRunID: root.RunID, PriorRunID: root.RunID, ExpectedPriorRevision: root.Revision, TurnID: root.TurnID, Input: body})
	require.NoError(t, err)
	events := []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": "failed-child"})}
	events = append(events, extra...)
	events = append(events, rawAGUI(map[string]any{"type": "RUN_ERROR", "message": "continuation lineage is cyclic or exceeds its bound", "code": "EXECUTION_FAILED"}))
	failed, err = c.store.Append(ctx, "owner", "thread", failed.RunID, failed.Revision, events, nil)
	require.NoError(t, err)
	root, err = c.store.GetRun(ctx, "owner", "thread", root.RunID)
	require.NoError(t, err)
	c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
		return &aguiRecoveredNative{Status: "waiting_for_user", Pending: pending}, nil
	}
	return c, root, failed, call
}

func TestAGUIAttachRecoversLegacyPreRuntimeAcceptedResultThroughOneSuccessor(t *testing.T) {
	c, prior, failed, _ := failedContinuationFixture(t)
	before, err := aguiRecoveryJournal(recoveryContext(), c.store, failed)
	require.NoError(t, err)
	h := handleAGUIRun(c, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h(w, r.WithContext(recoveryContext())) }))
	defer server.Close()
	body := strings.Replace(attachRequest, "external-run", failed.RunID, 1)
	status, wire := durablePost(t, server, body, nil)
	require.Equal(t, http.StatusOK, status, wire)
	require.Contains(t, wire, `"runId":"failed-child"`)
	require.NotContains(t, wire, `"runId":"continuation-recovery-`)
	deadline := time.Now().Add(5 * time.Second)
	var successor *aguistore.Run
	for time.Now().Before(deadline) {
		successor, err = c.store.GetRun(recoveryContext(), "owner", "thread", aguistore.FailedContinuationRecoveryID(failed))
		if err == nil && successor.Status == aguistore.StatusFinished {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NoError(t, err)
	require.NotNil(t, successor)
	require.Equal(t, aguistore.StatusFinished, successor.Status)
	current, err := c.store.GetRun(recoveryContext(), "owner", "thread", failed.RunID)
	require.NoError(t, err)
	require.Equal(t, successor.RunID, current.ResumedByRunID)
	require.Equal(t, failed.Input, current.Input)
	require.Equal(t, aguistore.StatusError, current.Status)
	after, err := aguiRecoveryJournal(recoveryContext(), c.store, current)
	require.NoError(t, err)
	require.Equal(t, before, after)
	original, err := c.store.GetRun(recoveryContext(), "owner", "thread", prior.RunID)
	require.NoError(t, err)
	require.Equal(t, failed.RunID, original.ResumedByRunID)
	require.Equal(t, prior.Pending, original.Pending)
	c.mu.Lock()
	require.True(t, c.controlsPreserved)
	require.Equal(t, []string{"original-call"}, c.results)
	c.mu.Unlock()
	require.Zero(t, c.queries.Load())
	require.EqualValues(t, 1, c.resumes.Load())
	require.EqualValues(t, 1, c.completions.Load())
	status, observed := durablePost(t, server, strings.Replace(attachRequest, "external-run", successor.RunID, 1), nil)
	require.Equal(t, http.StatusOK, status, observed)
	require.Contains(t, observed, `"id":"original-result"`)
	require.Contains(t, observed, `"toolCallId":"original-call"`)
	require.Contains(t, observed, "already executed fixture result")
	_, _, err = c.store.Admit(recoveryContext(), aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: "blind-new-child", PriorRunID: prior.RunID, ExpectedPriorRevision: original.Revision, TurnID: prior.TurnID, Input: json.RawMessage(`{"threadId":"thread","runId":"blind-new-child","messages":[]}`)})
	require.ErrorIs(t, err, aguistore.ErrConflict)
	status, _ = durablePost(t, server, body, nil)
	require.Equal(t, http.StatusOK, status)
	require.EqualValues(t, 1, c.resumes.Load())
	require.EqualValues(t, 1, c.completions.Load())
}

func TestAGUIFailedContinuationConcurrentRecoveryClaimsOneSuccessorAndObserver(t *testing.T) {
	c, _, failed, _ := failedContinuationFixture(t)
	var wg sync.WaitGroup
	errorsOut := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			successor, err := prepareFailedAGUIContinuationRecovery(recoveryContext(), c, c, c.store, failed)
			if err == nil {
				err = runFailedAGUIContinuationRecovery(recoveryContext(), c, c, c.store, successor)
			}
			errorsOut <- err
		}()
	}
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		require.NoError(t, err, "every concurrent recovery follows the one validated successor")
	}
	successor, err := c.store.GetRun(recoveryContext(), "owner", "thread", aguistore.FailedContinuationRecoveryID(failed))
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, successor.Status)
	require.Zero(t, c.queries.Load())
	require.EqualValues(t, 1, c.resumes.Load())
	require.EqualValues(t, 1, c.completions.Load())
}

type delayedRecoveryClaimStore struct {
	aguistore.Store
	entered chan struct{}
	release chan struct{}
}

func (s delayedRecoveryClaimStore) Claim(ctx context.Context, principal, thread, run string, revision int64, owner string, ttl time.Duration) (*aguistore.Run, error) {
	close(s.entered)
	<-s.release
	return s.Store.Claim(ctx, principal, thread, run, revision, owner, ttl)
}

func TestAGUIFailedContinuationLosingClaimFollowsAlreadyCompletedSuccessor(t *testing.T) {
	c, _, failed, _ := failedContinuationFixture(t)
	ctx := recoveryContext()
	successor, err := prepareFailedAGUIContinuationRecovery(ctx, c, c, c.store, failed)
	require.NoError(t, err)
	paused := delayedRecoveryClaimStore{Store: c.store, entered: make(chan struct{}), release: make(chan struct{})}
	loser := make(chan error, 1)
	go func() { loser <- runFailedAGUIContinuationRecovery(ctx, c, c, paused, successor) }()
	select {
	case <-paused.entered:
	case <-time.After(time.Second):
		t.Fatal("follower did not reach observer claim")
	}
	running, err := c.store.GetRun(ctx, "owner", "thread", successor.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusRunning, running.Status)
	err = runFailedAGUIContinuationRecovery(ctx, c, c, c.store, running)
	close(paused.release)
	require.NoError(t, err)
	require.NoError(t, <-loser, "stale claim follows the validated finished successor")
	require.EqualValues(t, 1, c.resumes.Load())
	require.EqualValues(t, 1, c.completions.Load())
	require.Zero(t, c.queries.Load())
}

type invalidRecoveryFollowerStore struct {
	aguistore.Store
	mode  string
	cause error
}

func (s invalidRecoveryFollowerStore) Claim(context.Context, string, string, string, int64, string, time.Duration) (*aguistore.Run, error) {
	return nil, s.cause
}

func (s invalidRecoveryFollowerStore) GetRun(ctx context.Context, principal, thread, id string) (*aguistore.Run, error) {
	run, err := s.Store.GetRun(ctx, principal, thread, id)
	if err != nil || !strings.HasPrefix(id, "continuation-recovery-") {
		return run, err
	}
	copy := *run
	if s.mode == "foreign" {
		copy.Principal = "foreign"
	} else if s.mode == "changedInput" {
		copy.Input = json.RawMessage(`{"changed":true}`)
	}
	return &copy, nil
}

func TestAGUIFailedContinuationFollowerDoesNotMaskUnrelatedOrChangedSuccessorFailure(t *testing.T) {
	for _, mode := range []string{"foreign", "changedInput", "unrelated"} {
		t.Run(mode, func(t *testing.T) {
			c, _, failed, _ := failedContinuationFixture(t)
			ctx := recoveryContext()
			successor, err := prepareFailedAGUIContinuationRecovery(ctx, c, c, c.store, failed)
			require.NoError(t, err)
			cause := aguistore.ErrInvalidTransition
			if mode == "unrelated" {
				cause = errors.New("fixture observer rejection")
			}
			store := invalidRecoveryFollowerStore{Store: c.store, mode: mode, cause: cause}
			err = runFailedAGUIContinuationRecovery(ctx, c, c, store, successor)
			require.ErrorIs(t, err, cause)
			require.Zero(t, c.resumes.Load())
			require.Zero(t, c.completions.Load())
			require.Zero(t, c.queries.Load())
		})
	}
}

type alteredFailedLineageStore struct {
	aguistore.Store
	mode string
}

func (s alteredFailedLineageStore) GetRun(ctx context.Context, principal, thread, id string) (*aguistore.Run, error) {
	run, err := s.Store.GetRun(ctx, principal, thread, id)
	if err != nil {
		return nil, err
	}
	copy := *run
	if id == "initial" {
		if s.mode == "cycle" {
			copy.PriorRunID = "failed-child"
		} else if s.mode == "pendingScope" {
			var pending aguiPending
			_ = json.Unmarshal(copy.Pending, &pending)
			pending.ClientTools[0].ConversationID = "foreign"
			copy.Pending = rawAGUI(pending)
		} else {
			copy.ConversationID = "foreign"
		}
	}
	return &copy, nil
}

func TestAGUIFailedContinuationRecoveryRefusesChangedOrAlreadyStartedBoundaries(t *testing.T) {
	for _, mode := range []string{"owner", "cycle", "scope", "pendingScope", "missingResult", "changedResult", "nativeStarted", "nativeCompleted", "nativeCallChanged", "runtimeSnapshot", "unrelatedError"} {
		t.Run(mode, func(t *testing.T) {
			var extra []json.RawMessage
			if mode == "runtimeSnapshot" {
				extra = []json.RawMessage{rawAGUI(map[string]any{"type": "STATE_SNAPSHOT", "snapshot": map[string]any{}})}
			}
			c, _, failed, call := failedContinuationFixture(t, extra...)
			store := c.store
			ctx := recoveryContext()
			copy := *failed
			switch mode {
			case "owner":
				ctx = iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "other"})
			case "cycle", "scope", "pendingScope":
				store = alteredFailedLineageStore{Store: store, mode: mode}
			case "missingResult":
				copy.Input = json.RawMessage(`{"threadId":"thread","runId":"failed-child","messages":[]}`)
			case "changedResult":
				copy.Input = json.RawMessage(strings.Replace(string(copy.Input), "already executed fixture result", "different result", 1))
			case "nativeStarted":
				c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
					return &aguiRecoveredNative{Status: "running"}, nil
				}
			case "nativeCompleted":
				c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
					return &aguiRecoveredNative{Status: "completed"}, nil
				}
			case "nativeCallChanged":
				call.Arguments = map[string]interface{}{"changed": true}
				c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
					return &aguiRecoveredNative{Status: "waiting_for_user", Pending: aguiPending{ClientTools: []clienttool.PendingCall{call}}}, nil
				}
			case "unrelatedError":
				copy.Status = aguistore.StatusCancelled
			}
			successor, err := prepareFailedAGUIContinuationRecovery(ctx, c, c, store, &copy)
			if mode == "runtimeSnapshot" || mode == "unrelatedError" {
				require.NoError(t, err)
				require.Nil(t, successor)
			} else {
				require.Error(t, err)
			}
			_, err = c.store.GetRun(recoveryContext(), "owner", "thread", aguistore.FailedContinuationRecoveryID(failed))
			require.ErrorIs(t, err, aguistore.ErrNotFound)
			require.Zero(t, c.queries.Load())
			require.Zero(t, c.resumes.Load())
			require.Zero(t, c.completions.Load())
		})
	}
}

func TestAGUIFailedContinuationRecognizesOnlyClosedPreRuntimeRestorationProof(t *testing.T) {
	c, _, failed, _ := failedContinuationFixture(t)
	journal, err := aguiRecoveryJournal(recoveryContext(), c.store, failed)
	require.NoError(t, err)
	require.True(t, aguistore.FailedContinuationRestoration(failed, journal), "the installed legacy bound failure remains recoverable")
	for _, proof := range []struct {
		code, message string
		accepted      bool
	}{
		{aguistore.ContinuationControlRestoreFailedCode, aguistore.ContinuationControlRestoreFailedMessage, true},
		{"EXECUTION_FAILED", "unrelated model execution failure", false},
		{aguistore.ContinuationControlRestoreFailedCode, "PRIVATE_CANARY", false},
		{"OTHER_FAILURE", aguistore.ContinuationControlRestoreFailedMessage, false},
	} {
		copy := append([]json.RawMessage(nil), journal...)
		copy[1] = rawAGUI(map[string]any{"type": "RUN_ERROR", "code": proof.code, "message": proof.message})
		require.Equal(t, proof.accepted, aguistore.FailedContinuationRestoration(failed, copy))
	}
	wrapped := &aguiContinuationControlRestorationFailure{cause: fmt.Errorf("PRIVATE_CANARY")}
	require.Equal(t, aguistore.ContinuationControlRestoreFailedMessage, wrapped.Error())
}

func TestAGUIFailedContinuationStoreRefusesCallerChangedAcceptedAnswers(t *testing.T) {
	c, prior, failed, _ := failedContinuationFixture(t)
	body, err := aguistore.FailedContinuationRecoveryInput(failed)
	require.NoError(t, err)
	for _, change := range []string{"different result", "wrong-call"} {
		modified := strings.Replace(string(body), "already executed fixture result", change, 1)
		if change == "wrong-call" {
			modified = strings.Replace(string(body), "original-call", change, 1)
		}
		_, _, err = c.store.Admit(recoveryContext(), aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: aguistore.FailedContinuationRecoveryID(failed), ParentRunID: failed.RunID, PriorRunID: prior.RunID, ExpectedPriorRevision: prior.Revision, ExpectedFailedRevision: failed.Revision, TurnID: failed.TurnID, Input: json.RawMessage(modified)})
		require.ErrorIs(t, err, aguistore.ErrConflict)
	}
	current, err := c.store.GetRun(recoveryContext(), "owner", "thread", failed.RunID)
	require.NoError(t, err)
	require.Empty(t, current.ResumedByRunID)
	require.Equal(t, failed.Input, current.Input)
}
