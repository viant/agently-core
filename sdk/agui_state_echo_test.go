package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	store "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/native"
	auth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/aguistate"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type stateAdmissionCollisionStore struct {
	store.Store
	mu            sync.Mutex
	injected      map[string]bool
	peerState     json.RawMessage
	failThread    bool
	textCollision bool
}

func (s *stateAdmissionCollisionStore) GetThread(ctx context.Context, principal, threadID string) (*store.Thread, error) {
	if s.failThread {
		return nil, fmt.Errorf("fixture initialization read failure")
	}
	return s.Store.GetThread(ctx, principal, threadID)
}
func (s *stateAdmissionCollisionStore) Append(ctx context.Context, principal, threadID, runID string, revision int64, events []json.RawMessage, change *store.Change) (*store.Run, error) {
	hasState := false
	hasTextStart := false
	for _, raw := range events {
		var event struct{ Type string }
		_ = json.Unmarshal(raw, &event)
		hasState = hasState || event.Type == "STATE_SNAPSHOT"
		hasTextStart = hasTextStart || event.Type == "TEXT_MESSAGE_START"
	}
	if (hasState || hasTextStart && s.textCollision) && strings.HasPrefix(runID, "fresh") {
		injectionKey := runID
		if !hasState {
			injectionKey = "text-" + runID
		}
		s.mu.Lock()
		inject := !s.injected[injectionKey]
		s.injected[injectionKey] = true
		s.mu.Unlock()
		if inject {
			id := "metadata-" + injectionKey
			input := rawAGUI(map[string]any{"threadId": threadID, "runId": id, "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "operation": "conversation.bootstrap", "requestId": id, "payload": map[string]any{}}}})
			peer, _, err := s.Store.Admit(ctx, store.Admission{Principal: principal, ThreadID: threadID, RunID: id, Input: input})
			if err != nil {
				return nil, err
			}
			if s.peerState != nil {
				thread, err := s.Store.GetThread(ctx, principal, threadID)
				if err != nil {
					return nil, err
				}
				_, err = s.Store.Append(ctx, principal, threadID, id, peer.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": threadID, "runId": id}), rawAGUI(map[string]any{"type": "STATE_SNAPSHOT", "snapshot": s.peerState}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": threadID, "runId": id, "outcome": map[string]any{"type": "success"}})}, &store.Change{State: s.peerState, ExpectedThreadRevision: thread.Revision})
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return s.Store.Append(ctx, principal, threadID, runID, revision, events, change)
}
func stateEchoHTTPFixture(t *testing.T) (*durableAGUIClient, *httptest.Server, *stateAdmissionCollisionStore) {
	t.Helper()
	ctx := context.Background()
	runtime, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Shutdown(ctx)) })
	collision := &stateAdmissionCollisionStore{Store: store.New(runtime), injected: map[string]bool{}}
	client := &durableAGUIClient{aguiTestClient: newAGUITestClient(), store: collision}
	client.owner = "owner"
	client.query = func(_ context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{Content: "answer " + in.Query}, nil
	}
	handler := handleAGUIRun(client, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r.WithContext(auth.WithUserInfo(r.Context(), &auth.UserInfo{Subject: "owner"})))
	}))
	t.Cleanup(server.Close)
	return client, server, collision
}
func freshEchoRequest(id string) string {
	return string(rawAGUI(map[string]any{"threadId": "thread", "runId": id, "messages": []any{map[string]any{"id": "user-" + id, "role": "user", "content": "hello " + id}}, "state": map[string]any{}}))
}
func TestAGUIConcurrentFreshHTTPClientsRebaseInitialStateEchoAgainstExistingHistory(t *testing.T) {
	client, server, collision := stateEchoHTTPFixture(t)
	status, wire := durablePost(t, server, freshEchoRequest("history"), nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "RUN_FINISHED")
	type result struct {
		status int
		wire   string
		err    error
	}
	results := make(chan result, 2)
	for _, id := range []string{"fresh-a", "fresh-b"} {
		go func(id string) {
			requester := &http.Client{Timeout: 3 * time.Second}
			resp, err := requester.Post(server.URL, "application/json", strings.NewReader(freshEchoRequest(id)))
			if err != nil {
				results <- result{err: err}
				return
			}
			defer resp.Body.Close()
			wire, err := io.ReadAll(resp.Body)
			results <- result{status: resp.StatusCode, wire: string(wire), err: err}
		}(id)
	}
	for i := 0; i < 2; i++ {
		out := <-results
		require.NoError(t, out.err, "no lease-expiry wait or hanging stream")
		require.Equal(t, 200, out.status, out.wire)
		require.Contains(t, out.wire, "RUN_FINISHED")
		require.NotContains(t, out.wire, "RUN_ERROR")
	}
	require.EqualValues(t, 3, client.queries.Load(), "one query per distinct input, including history")
	thread, err := collision.Store.GetThread(context.Background(), "owner", "thread")
	require.NoError(t, err)
	require.Contains(t, string(thread.Messages), "answer hello history")
	require.Contains(t, string(thread.Messages), "answer hello fresh-a")
	require.Contains(t, string(thread.Messages), "answer hello fresh-b")
	collision.mu.Lock()
	require.True(t, collision.injected["fresh-a"])
	require.True(t, collision.injected["fresh-b"])
	collision.mu.Unlock()
}
func TestAGUIInitialStateEchoPublishesRebasedPeerStateOnWire(t *testing.T) {
	client, server, collision := stateEchoHTTPFixture(t)
	status, history := durablePost(t, server, freshEchoRequest("history"), nil)
	require.Equal(t, 200, status, history)
	collision.peerState = json.RawMessage(`{"peer":{"n":9007199254740993,"enabled":false}}`)
	client.query = func(_ context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		state := in.Context["agui"].(map[string]any)["state"].(json.RawMessage)
		require.JSONEq(t, string(collision.peerState), string(state))
		return &agentsvc.QueryOutput{Content: "rebased"}, nil
	}
	status, wire := durablePost(t, server, freshEchoRequest("fresh-echo"), nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, `"n":9007199254740993`)
	require.Contains(t, wire, "RUN_FINISHED")
	thread, err := collision.Store.GetThread(context.Background(), "owner", "thread")
	require.NoError(t, err)
	require.JSONEq(t, string(collision.peerState), string(thread.State))
}
func TestAGUIIntentionalStateWriteCannotReplaceConcurrentPeerMutation(t *testing.T) {
	client, server, collision := stateEchoHTTPFixture(t)
	collision.peerState = json.RawMessage(`{"peer":"authoritative"}`)
	body := strings.Replace(freshEchoRequest("fresh-mutation"), `"state":{}`, `"state":{"intent":"new"}`, 1)
	status, wire := durablePost(t, server, body, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "RUN_ERROR")
	require.NotContains(t, wire, "RUN_FINISHED")
	require.Zero(t, client.queries.Load())
	thread, err := collision.Store.GetThread(context.Background(), "owner", "thread")
	require.NoError(t, err)
	require.JSONEq(t, string(collision.peerState), string(thread.State))
}
func TestAGUIStateEchoEqualityPreservesExactNumbersAndNull(t *testing.T) {
	for _, sample := range []struct {
		left, right string
		same        bool
	}{
		{`{"n":1,"a":[1000,-0]}`, `{"a":[1e3,0.0],"n":1.0}`, true},
		{`{"n":9007199254740993}`, `{"n":9007199254740992}`, false},
		{`{"n":9007199254740993}`, `{"n":9007199254740993.0}`, true},
		{`null`, `{}`, false}, {`null`, `null`, true},
	} {
		same, err := aguistate.EqualJSON(json.RawMessage(sample.left), json.RawMessage(sample.right))
		require.NoError(t, err)
		require.Equal(t, sample.same, same)
	}
}
func TestAGUIInitializationFailureIsDurableWithoutQueryOrHangingHTTP(t *testing.T) {
	client, server, collision := stateEchoHTTPFixture(t)
	collision.failThread = true
	body := freshEchoRequest("failed-initialization")
	status, wire := durablePost(t, server, body, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "RUN_STARTED")
	require.Contains(t, wire, "INITIALIZATION_FAILED")
	require.Zero(t, client.queries.Load())
	status, replay := durablePost(t, server, body, nil)
	require.Equal(t, 200, status, replay)
	require.Equal(t, wire, replay)
	require.Zero(t, client.queries.Load())
}
func TestAGUIInitializationFailureCannotTerminalizeAnotherOrCancelledLease(t *testing.T) {
	client, _, collision := stateEchoHTTPFixture(t)
	ctx := context.Background()
	row, _, err := collision.Store.Admit(ctx, store.Admission{Principal: "owner", ThreadID: "thread", RunID: "fenced", Input: json.RawMessage(freshEchoRequest("fenced"))})
	require.NoError(t, err)
	winner, err := collision.Store.Claim(ctx, "owner", "thread", "fenced", row.Revision, "winner", time.Minute)
	require.NoError(t, err)
	loser := *winner
	loser.LeaseOwner = "loser"
	require.NoError(t, journalAGUIInitializationFailure(ctx, client, collision.Store, &loser, fmt.Errorf("lost initialization")))
	require.NoError(t, journalAGUIInitializationFailure(ctx, client, collision.Store, winner, context.Canceled))
	current, err := collision.Store.GetRun(ctx, "owner", "thread", "fenced")
	require.NoError(t, err)
	require.Equal(t, store.StatusAdmitted, current.Status)
	require.Zero(t, current.LastSequence)
}

func TestAGUIMessageStartCASRollbackDoesNotRetainUncommittedOpenLane(t *testing.T) {
	client, server, collision := stateEchoHTTPFixture(t)
	collision.textCollision = true
	status, wire := durablePost(t, server, freshEchoRequest("fresh-text-collision"), nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "RUN_FINISHED")
	require.NotContains(t, wire, "RUN_ERROR")
	require.EqualValues(t, 1, client.queries.Load())
	events := decodeAGUISSE(t, strings.NewReader(wire))
	starts := 0
	for _, event := range events {
		if event.Type == "TEXT_MESSAGE_START" {
			starts++
		}
	}
	require.Equal(t, 1, starts, "failed local START must be rolled back before the same raw batch retries")
	collision.mu.Lock()
	require.True(t, collision.injected["text-fresh-text-collision"])
	collision.mu.Unlock()
}
