package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/data"
	authctx "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/runtime/streaming"
	goalsys "github.com/viant/agently-core/service/goal"
	dexec "github.com/viant/datly/exec"
)

type goalReadBarrier struct {
	goalsys.Repository
	invoker          dexec.ComponentInvoker
	armed            atomic.Bool
	entered, release chan struct{}
}

func (b *goalReadBarrier) DatlyInvoker() dexec.ComponentInvoker { return b.invoker }
func (b *goalReadBarrier) Get(ctx context.Context, id string) (*goalsys.Record, error) {
	if b.armed.CompareAndSwap(true, false) {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return b.Repository.Get(ctx, id)
}

// Hide SubscribeOpts so the public SDK uses this actual MemoryBus's bounded
// default queue. All events still originate from native command commits.
type boundedGoalBus struct {
	streaming.Bus
	subscriptions chan streaming.Subscription
}

func (b *boundedGoalBus) Subscribe(ctx context.Context, filter streaming.Filter) (streaming.Subscription, error) {
	sub, err := b.Bus.Subscribe(ctx, filter)
	if err == nil {
		b.subscriptions <- sub
	}
	return sub, err
}

func TestAGUIGoalHTTPOverflowReplaysErrorAndFreshSubscriptionRecovers(t *testing.T) {
	enableAtomicGoals(t)
	runtime, repo, db := atomicGoalFixture(t)
	_, err := db.Exec(`UPDATE conversation SET created_by_user_id='owner',visibility='private' WHERE id='goal-thread'`)
	require.NoError(t, err)
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	conv, err := convservice.New(owner, runtime)
	require.NoError(t, err)
	barrier := &goalReadBarrier{Repository: repo, invoker: runtime, entered: make(chan struct{}), release: make(chan struct{})}
	released := false
	bus := &boundedGoalBus{Bus: streaming.NewMemoryBus(1), subscriptions: make(chan streaming.Subscription, 4)}
	// No model/agent executor exists: a resource observer cannot execute Query.
	backend := &backendClient{goalRepo: barrier, goalInvoker: runtime, conv: conv, data: data.NewService(runtime), streaming: bus}
	server := goalTakeoverServer(backend)
	defer func() {
		if !released {
			close(barrier.release)
		}
		server.Close()
	}()
	rawInput := func(op, run string, payload any) []byte {
		raw, err := json.Marshal(map[string]any{"threadId": "goal-thread", "runId": run, "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "requestId": run, "operation": op, "payload": payload}}})
		require.NoError(t, err)
		return raw
	}
	post := func(principal string, raw []byte) *http.Response {
		t.Helper()
		request, err := http.NewRequest("POST", server.URL, bytes.NewReader(raw))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Test-Principal", principal)
		response, err := server.Client().Do(request)
		require.NoError(t, err)
		return response
	}
	command := func(principal, op string, payload any) (int, string) {
		t.Helper()
		response := post(principal, rawInput(op, uuid.NewString(), payload))
		defer response.Body.Close()
		wire, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		return response.StatusCode, string(wire)
	}
	status, wire := command("owner", "goal.create", map[string]any{"objective": "initial bounded observer"})
	require.Equal(t, 200, status, wire)
	store := aguistore.New(runtime)
	before, err := store.GetThread(owner, "owner", "goal-thread")
	require.NoError(t, err)
	type readResult struct {
		wire string
		err  error
	}
	readStream := func(response *http.Response) (chan string, chan readResult) {
		events := make(chan string, 32)
		done := make(chan readResult, 1)
		go func() {
			var wire strings.Builder
			scanner := bufio.NewScanner(response.Body)
			for scanner.Scan() {
				line := scanner.Text()
				wire.WriteString(line)
				wire.WriteByte('\n')
				if strings.HasPrefix(line, "data:") {
					events <- strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				}
			}
			done <- readResult{wire.String(), scanner.Err()}
		}()
		return events, done
	}
	wait := func(events chan string, contains string) string {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case event := <-events:
				if strings.Contains(event, contains) {
					return event
				}
			case <-timer.C:
				t.Fatalf("missing public event %s", contains)
				return ""
			}
		}
	}
	accepted := rawInput("goal.subscribe", uuid.NewString(), map[string]any{"durationSeconds": 30})
	response := post("owner", accepted)
	require.Equal(t, 200, response.StatusCode)
	defer response.Body.Close()
	events, done := readStream(response)
	wait(events, "initial bounded observer")
	sub := <-bus.subscriptions
	barrier.armed.Store(true)
	status, wire = command("owner", "goal.update", map[string]any{"objective": "overflow-one"})
	require.Equal(t, 200, status, wire)
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("observer authoritative read did not reach barrier")
	}
	for _, objective := range []string{"overflow-two", "overflow-three"} {
		status, wire = command("owner", "goal.update", map[string]any{"objective": objective})
		require.Equal(t, 200, status, wire)
		require.NotContains(t, wire, `"type":"RUN_ERROR"`)
	}
	require.Eventually(t, func() bool { return sub.Reason() == streaming.ReasonOverflow }, time.Second, 10*time.Millisecond, "actual MemoryBus bounded queue must overflow")
	close(barrier.release)
	released = true
	terminal := wait(events, "GOAL_SUBSCRIPTION_ERROR")
	require.Contains(t, terminal, "event buffer overflow")
	var original readResult
	select {
	case original = <-done:
		require.NoError(t, original.err)
	case <-time.After(time.Second):
		t.Fatal("overflow stream did not terminate")
	}
	replay := post("owner", accepted)
	require.Equal(t, 200, replay.StatusCode)
	replayed, err := io.ReadAll(replay.Body)
	replay.Body.Close()
	require.NoError(t, err)
	require.Equal(t, original.wire, string(replayed), "identical accepted input replays terminal error, never starts another observer")
	freshID := uuid.NewString()
	freshAccepted := rawInput("goal.subscribe", freshID, map[string]any{"durationSeconds": 30})
	fresh := post("owner", freshAccepted)
	require.Equal(t, 200, fresh.StatusCode)
	defer fresh.Body.Close()
	freshEvents, freshDone := readStream(fresh)
	wait(freshEvents, "overflow-three")
	status, wire = command("owner", "goal.update", map[string]any{"objective": "fresh committed live update"})
	require.Equal(t, 200, status, wire)
	wait(freshEvents, "fresh committed live update")
	status, _ = command("intruder", "run.cancel", map[string]any{"runId": freshID})
	require.Equal(t, 403, status)
	running, err := store.GetRun(owner, "owner", "goal-thread", freshID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusRunning, running.Status)
	status, wire = command("owner", "run.cancel", map[string]any{"runId": freshID})
	require.Equal(t, 200, status, wire)
	require.Contains(t, wait(freshEvents, `"type":"RUN_FINISHED"`), "cancelled")
	select {
	case result := <-freshDone:
		require.NoError(t, result.err)
	case <-time.After(time.Second):
		t.Fatal("fresh observer did not terminate")
	}
	after, err := store.GetThread(owner, "owner", "goal-thread")
	require.NoError(t, err)
	require.Equal(t, before.Messages, after.Messages)
	require.Equal(t, before.State, after.State)
	foreign := post("intruder", accepted)
	require.Equal(t, 403, foreign.StatusCode)
	foreign.Body.Close()
}
