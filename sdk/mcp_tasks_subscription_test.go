package sdk

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/protocol/agui"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcp/expose"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/schema"
	tasks "github.com/viant/mcp-tasks"
	taskclient "github.com/viant/mcp-tasks/client"
	taskrpc "github.com/viant/mcp-tasks/rpc"
)

type tasksStreamNext struct{}

func (tasksStreamNext) Serve(_ context.Context, _ *jsonrpc.Request, response *jsonrpc.Response) {
	response.Result = json.RawMessage(`{"resultType":"complete","capabilities":{}}`)
}
func (tasksStreamNext) OnNotification(context.Context, *jsonrpc.Notification) {}
func tasksStreamHTTP(t *testing.T, backend *MCPTasksBackend, interval time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := r.Header.Get("X-Fixture-Subject")
		if subject == "" {
			subject = "owner"
		}
		tenant := r.Header.Get("X-Fixture-Tenant")
		if tenant == "" {
			tenant = "workspace"
		}
		ctx := iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: subject})
		taskrpc.HTTPHandler{Build: func(_ context.Context, notifier transport.Notifier) transport.Handler {
			owner := func(ctx context.Context, _ *jsonrpc.Request) (tasks.Owner, error) {
				return tasks.Owner{Tenant: tenant, Subject: iauth.EffectiveUserID(ctx)}, nil
			}
			wrapper, err := expose.WithTasks(tasksStreamNext{}, backend, owner, func(context.Context, *jsonrpc.Request, *jsonrpc.Response) (*tasks.CreateOptions, error) {
				return nil, nil
			}, expose.WithTaskSubscriptions(notifier, taskrpc.SubscriptionOptions{ReconcileInterval: interval, MaxDuration: 2 * time.Second}))
			require.NoError(t, err)
			return wrapper
		}}.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(server.Close)
	return server
}
func tasksStreamClient(t *testing.T, server *httptest.Server, headers http.Header) *taskclient.Client {
	t.Helper()
	caller, err := taskrpc.NewHTTPCaller(server.URL, server.Client())
	require.NoError(t, err)
	caller.HeaderProvider = func(context.Context) (http.Header, error) { return headers, nil }
	client, err := taskclient.New(caller, taskclient.Options{Tasks: true, Info: schema.Implementation{Name: "native-stream-fixture", Version: "1"}, Capabilities: mcpTasksCaps()})
	require.NoError(t, err)
	return client
}
func TestMCPTasksHTTPSubscriptionUsesNativeAGUIWakeupAndOwnedDetachedChild(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	store := base.store
	ctx := mcpTasksOwnerContext()
	owner := tasks.Owner{Tenant: "workspace", Subject: "owner"}
	parent := mcpTasksRun(t, store, ctx, "stream-parent", "parent", "", "wait")
	child := mcpTasksRun(t, store, ctx, "stream-child", "child", parent.RunID, "detach")
	controls := mcpTasksControls(store, func(context.Context, tasks.Owner, MCPTaskReference, []agui.WireResumeEntry) error { return nil }, func(context.Context, tasks.Owner, MCPTaskReference) error { return nil })
	controls.Watch = mcpTasksAGUIWatch(base)
	backend, err := NewMCPTasksBackend(store, controls, MCPTasksOptions{Tenant: "workspace"})
	require.NoError(t, err)
	created, err := backend.Create(ctx, owner, mcpTasksCaps(), MCPTaskOrigin(MCPTaskReference{ThreadID: child.ThreadID, RunID: child.RunID, ParentRunID: parent.RunID, ExecutionMode: "detach"}))
	require.NoError(t, err)
	server := tasksStreamHTTP(t, backend, time.Hour) // a terminal within 1s proves native event wakeup, not polling
	client := tasksStreamClient(t, server, nil)
	received := make(chan tasks.DetailedTask, 8)
	done := make(chan error, 1)
	observation, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- client.Subscribe(observation, []string{created.TaskID}, taskclient.SubscriptionOptions{OnTask: func(state tasks.DetailedTask) error { received <- state; return nil }})
	}()
	select {
	case initial := <-received:
		require.Equal(t, tasks.Working, initial.Status)
	case <-time.After(time.Second):
		t.Fatal("initial stream snapshot missing")
	}
	require.True(t, base.subscribed.Load(), "actual native event bus must be attached")
	mcpTasksFinish(t, store, ctx, parent, json.RawMessage(`{"resultType":"complete","content":[]}`))
	// Parent termination does not terminate the existing detached child task.
	state, err := backend.Get(ctx, owner, created.TaskID)
	require.NoError(t, err)
	require.Equal(t, tasks.Working, state.Status)
	final := json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"detached child final"}],"isError":false,"_meta":{"fixture":"host-only"}}`)
	mcpTasksFinish(t, store, ctx, child, final)
	require.NoError(t, base.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTurnCompleted, ConversationID: child.ConversationID, TurnID: child.TurnID}))
	select {
	case terminal := <-received:
		require.Equal(t, tasks.Completed, terminal.Status)
		require.JSONEq(t, string(final), string(terminal.Result))
	case <-time.After(time.Second):
		t.Fatal("native AG-UI event did not wake full-state notification")
	}
	select {
	case err = <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("terminal stream did not close gracefully")
	}
	require.Zero(t, base.queries.Load())
	require.Zero(t, base.resumes.Load())
}
func TestMCPTasksStreamLostNativeHintsReconcileAndDisconnectDoesNotCancel(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	store := base.store
	ctx := mcpTasksOwnerContext()
	owner := tasks.Owner{Tenant: "workspace", Subject: "owner"}
	native := mcpTasksRun(t, store, ctx, "lost-hints", "native", "", "wait")
	var cancellations atomic.Int32
	controls := mcpTasksControls(store, func(context.Context, tasks.Owner, MCPTaskReference, []agui.WireResumeEntry) error { return nil }, func(context.Context, tasks.Owner, MCPTaskReference) error { cancellations.Add(1); return nil })
	controls.Watch = mcpTasksAGUIWatch(base)
	backend, err := NewMCPTasksBackend(store, controls, MCPTasksOptions{Tenant: "workspace"})
	require.NoError(t, err)
	created, err := backend.Create(ctx, owner, mcpTasksCaps(), MCPTaskOrigin(MCPTaskReference{ThreadID: native.ThreadID, RunID: native.RunID}))
	require.NoError(t, err)
	server := tasksStreamHTTP(t, backend, 15*time.Millisecond)
	client := tasksStreamClient(t, server, nil)
	initial := make(chan struct{}, 1)
	done := make(chan error, 1)
	observer, stop := context.WithCancel(context.Background())
	go func() {
		done <- client.Subscribe(observer, []string{created.TaskID}, taskclient.SubscriptionOptions{OnTask: func(state tasks.DetailedTask) error { initial <- struct{}{}; return nil }})
	}()
	<-initial
	stop()
	select {
	case err = <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("observer did not detach")
	}
	require.Zero(t, cancellations.Load())
	// Reconnect after a missed terminal event must immediately read canonical state.
	mcpTasksFinish(t, store, ctx, native, json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"missed event terminal"}]}`))
	var terminal tasks.DetailedTask
	err = client.Subscribe(ctx, []string{created.TaskID}, taskclient.SubscriptionOptions{OnTask: func(state tasks.DetailedTask) error { terminal = state; return nil }})
	require.NoError(t, err)
	require.Equal(t, tasks.Completed, terminal.Status)
	for _, headers := range []http.Header{{"X-Fixture-Subject": []string{"foreign"}}, {"X-Fixture-Tenant": []string{"other"}}} {
		foreign := tasksStreamClient(t, server, headers)
		leaked := false
		err = foreign.Subscribe(ctx, []string{created.TaskID}, taskclient.SubscriptionOptions{OnTask: func(tasks.DetailedTask) error { leaked = true; return nil }})
		var protocol *tasks.RPCError
		require.ErrorAs(t, err, &protocol)
		require.Equal(t, -32602, protocol.Code)
		require.False(t, leaked)
	}
}

func TestMCPTasksActiveStreamReconcilesMissingHintsAndStreamsCancellation(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	store := base.store
	ctx := mcpTasksOwnerContext()
	owner := tasks.Owner{Tenant: "workspace", Subject: "owner"}
	native := mcpTasksRun(t, store, ctx, "stream-cancel", "native", "", "wait")
	controls := mcpTasksControls(store, func(context.Context, tasks.Owner, MCPTaskReference, []agui.WireResumeEntry) error { return nil }, func(_ context.Context, _ tasks.Owner, reference MCPTaskReference) error {
		current, err := store.GetRun(ctx, "owner", reference.ThreadID, reference.RunID)
		if err != nil {
			return err
		}
		_, err = store.Append(ctx, "owner", current.ThreadID, current.RunID, current.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": current.ThreadID, "runId": current.RunID, "outcome": map[string]any{"type": "cancelled"}})}, nil)
		return err
	})
	controls.Watch = mcpTasksAGUIWatch(base)
	backend, err := NewMCPTasksBackend(store, controls, MCPTasksOptions{Tenant: "workspace"})
	require.NoError(t, err)
	created, err := backend.Create(ctx, owner, mcpTasksCaps(), MCPTaskOrigin(MCPTaskReference{ThreadID: native.ThreadID, RunID: native.RunID}))
	require.NoError(t, err)
	server := tasksStreamHTTP(t, backend, 10*time.Millisecond)
	client := tasksStreamClient(t, server, nil)
	states := make(chan tasks.DetailedTask, 8)
	done := make(chan error, 1)
	go func() {
		done <- client.Subscribe(ctx, []string{created.TaskID}, taskclient.SubscriptionOptions{OnTask: func(state tasks.DetailedTask) error { states <- state; return nil }})
	}()
	<-states
	// No native event is published: periodic canonical reconciliation must still
	// deliver the terminal cancellation. Its control is explicitly client-driven.
	require.NoError(t, client.Cancel(ctx, created.TaskID))
	select {
	case state := <-states:
		require.Equal(t, tasks.Cancelled, state.Status)
		require.Empty(t, state.Result)
	case <-time.After(time.Second):
		t.Fatal("lost cancellation event was not reconciled")
	}
	select {
	case err = <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("cancelled task stream did not end")
	}
	require.Zero(t, base.queries.Load())
	require.Zero(t, base.resumes.Load())
}

func TestMCPTasksCancellationReceiptDoesNotChangeCanonicalWorkingState(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	store := base.store
	ctx := mcpTasksOwnerContext()
	owner := tasks.Owner{Tenant: "workspace", Subject: "owner"}
	native := mcpTasksRun(t, store, ctx, "cancel-receipt", "native", "", "wait")
	entered, release := make(chan struct{}), make(chan struct{})
	var released atomic.Bool
	releaseControl := func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}
	t.Cleanup(releaseControl)
	var cancellations atomic.Int32
	controls := mcpTasksControls(store, func(context.Context, tasks.Owner, MCPTaskReference, []agui.WireResumeEntry) error { return nil }, func(context.Context, tasks.Owner, MCPTaskReference) error {
		cancellations.Add(1)
		close(entered)
		<-release
		return nil
	})
	createdAt := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	backend, err := NewMCPTasksBackend(store, controls, MCPTasksOptions{Tenant: "workspace", Clock: func() time.Time { return createdAt }})
	require.NoError(t, err)
	created, err := backend.Create(ctx, owner, mcpTasksCaps(), MCPTaskOrigin(MCPTaskReference{ThreadID: native.ThreadID, RunID: native.RunID}))
	require.NoError(t, err)
	before, err := backend.Get(ctx, owner, created.TaskID)
	require.NoError(t, err)
	backend.options.Clock = func() time.Time { return createdAt.Add(time.Minute) }
	done := make(chan error, 1)
	go func() {
		_, err := backend.Cancel(ctx, owner, created.TaskID)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cancellation control did not start")
	}
	// The private dispatch receipt is committed, while the native execution is
	// still working. Full-state subscriptions must not emit a timestamp-only
	// working update before the authoritative native cancellation arrives.
	during, err := backend.Get(ctx, owner, created.TaskID)
	require.NoError(t, err)
	require.Equal(t, tasks.Working, during.Status)
	require.Equal(t, before.DetailedTask, during.DetailedTask)
	_, err = backend.Cancel(ctx, owner, created.TaskID)
	require.NoError(t, err)
	require.EqualValues(t, 1, cancellations.Load(), "same cancellation receipt must not redispatch")
	releaseControl()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("cancellation control did not finish")
	}
}
