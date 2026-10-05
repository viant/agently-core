package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	nativeruntime "github.com/viant/agently-core/app/store/native"
	iauth "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/runtime/streaming"
	authsvc "github.com/viant/agently-core/service/auth"
)

type compatibilityDatlyClient struct {
	*datlyObservedClient
	events []*streaming.Event
	reason string
	closed bool
}

func (c *compatibilityDatlyClient) authorizeCompatibilityConversation(ctx context.Context, id string) error {
	return c.native.authorizeCompatibilityConversation(ctx, id)
}
func (c *compatibilityDatlyClient) compatibilityExecutionProvenance() aguistore.ExecutionProvenanceReader {
	return aguistore.New(c.native.goalInvoker)
}
func (c *compatibilityDatlyClient) StreamEvents(ctx context.Context, input *StreamEventsInput) (streaming.Subscription, error) {
	c.subscribed.Store(true)
	ch := make(chan *streaming.Event, len(c.events))
	for _, event := range c.events {
		if input.Filter == nil || input.Filter(event) {
			ch <- event
		}
	}
	close(ch)
	return &compatibilitySubscription{stubSubscription: stubSubscription{id: "compatibility", ch: ch, reason: c.reason, lastSeq: 99}, close: func() { c.closed = true }}, nil
}

type compatibilitySubscription struct {
	stubSubscription
	close func()
}

func (s *compatibilitySubscription) Close() error { s.close(); return nil }

func compatibilityEvent(typ streaming.EventType, turn, content string) *streaming.Event {
	return &streaming.Event{Type: typ, ConversationID: "thread", StreamID: "thread", TurnID: turn, Content: content, EventSeq: 8}
}
func admitCompatibilityRun(t *testing.T, c *datlyObservedClient, turn string) *aguistore.Run {
	t.Helper()
	run, fresh, err := c.store.Admit(recoveryContext(), aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: "protocol-" + turn, TurnID: turn, Input: rawAGUI(map[string]any{"threadId": "thread", "runId": "protocol-" + turn, "messages": []any{map[string]any{"id": "user-" + turn, "role": "user", "content": "hello"}}})})
	require.NoError(t, err)
	require.True(t, fresh)
	return run
}
func compatibilityHTTP(t *testing.T, c *compatibilityDatlyClient, principal, scope string, config *authsvc.Config) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/stream?conversationId=thread&compatibilityScope="+scope, nil)
	if principal != "" {
		req = req.WithContext(iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: principal}))
	}
	rec := httptest.NewRecorder()
	handleStreamEvents(c, config)(rec, req)
	return rec
}
func compatibilityWireEvents(t *testing.T, wire string) []*streaming.Event {
	t.Helper()
	var result []*streaming.Event
	for _, line := range strings.Split(wire, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		event := &streaming.Event{}
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), event))
		result = append(result, event)
	}
	return result
}

func TestCompatibilityDurableAdmissionPrecedesNativePublicationAndSurvivesRecovery(t *testing.T) {
	c := newDatlyObservedClient(t, 16)
	run := admitCompatibilityRun(t, c, "agui-turn")
	reader := aguistore.New(c.native.goalInvoker)
	before, err := reader.ReadExecutionProvenance(recoveryContext(), "thread", run.TurnID)
	require.NoError(t, err)
	require.True(t, before.AGUIOwned)
	require.False(t, before.NativeTurnFound, "protocol ownership is committed before native turn persistence")
	require.NoError(t, nativeTestTurn(recoveryContext(), c, run.TurnID, "running"))
	o := newCompatibilityObserver(reader, "thread")
	require.Nil(t, o.project(recoveryContext(), compatibilityEvent(streaming.EventTypeTextDelta, run.TurnID, "excluded-live")))
	claimed, err := c.store.Claim(recoveryContext(), "owner", "thread", run.RunID, run.Revision, "worker", time.Minute)
	require.NoError(t, err)
	run, err = c.store.Append(recoveryContext(), "owner", "thread", run.RunID, claimed.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": run.RunID})}, &aguistore.Change{LeaseOwner: "worker"})
	require.NoError(t, err)
	// A new reader/observer has no knowledge of the old subscription or lease.
	replacement := newCompatibilityObserver(aguistore.New(c.native.goalInvoker), "thread")
	require.Nil(t, replacement.project(recoveryContext(), compatibilityEvent(streaming.EventTypeToolCallCompleted, run.TurnID, "excluded-recovered")))
	run, err = c.store.Append(recoveryContext(), "owner", "thread", run.RunID, run.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "thread", "runId": run.RunID, "outcome": map[string]any{"type": "interrupt", "interrupts": []any{map[string]any{"id": "approval", "reason": "approval"}}}})}, &aguistore.Change{LeaseOwner: "worker", Pending: json.RawMessage(`[{"id":"approval"}]`)})
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusInterrupted, run.Status)
	terminal := newCompatibilityObserver(aguistore.New(c.native.goalInvoker), "thread")
	require.Nil(t, terminal.project(recoveryContext(), compatibilityEvent(streaming.EventTypeTurnCompleted, run.TurnID, "legacy approval must not implicitly hand off")))
}

func TestCompatibilityHTTPInterleavedNativeMobileSchedulerAndApplicationEvents(t *testing.T) {
	c := &compatibilityDatlyClient{datlyObservedClient: newDatlyObservedClient(t, 16)}
	admitCompatibilityRun(t, c.datlyObservedClient, "agui-turn")
	require.NoError(t, nativeTestTurn(recoveryContext(), c.datlyObservedClient, "mobile-turn", "running"))
	require.NoError(t, nativeTestTurn(recoveryContext(), c.datlyObservedClient, "scheduler-turn", "queued"))
	c.events = []*streaming.Event{
		compatibilityEvent(streaming.EventTypeTextDelta, "mobile-turn", "mobile text"),
		compatibilityEvent(streaming.EventTypeTextDelta, "agui-turn", "MUST NOT APPEAR"),
		compatibilityEvent(streaming.EventTypeTurnQueued, "scheduler-turn", "scheduled"),
		compatibilityEvent(streaming.EventTypeConversationMetaUpdated, "agui-turn", "title changed"),
		compatibilityEvent(streaming.EventTypeToolCallCompleted, "mobile-turn", "native tool"),
		compatibilityEvent(streaming.EventTypeToolFeedInactive, "", ""),
		compatibilityEvent(streaming.EventTypeTextDelta, "unclassified", "private speculative delta"),
		compatibilityEvent(streaming.EventTypeTextDelta, "unclassified", "private speculative delta 2"),
		{Type: streaming.EventTypeTextDelta, ConversationID: "foreign", TurnID: "mobile-turn", Content: "foreign secret"},
	}
	rec := compatibilityHTTP(t, c, "owner", NativeAndApplicationCompatibilityScope, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	events := compatibilityWireEvents(t, rec.Body.String())
	require.Len(t, events, 6)
	require.Equal(t, []streaming.EventType{streaming.EventTypeTextDelta, streaming.EventTypeTurnQueued, streaming.EventTypeConversationMetaUpdated, streaming.EventTypeToolCallCompleted, streaming.EventTypeToolFeedInactive, CompatibilityReconcileEvent}, []streaming.EventType{events[0].Type, events[1].Type, events[2].Type, events[3].Type, events[4].Type, events[5].Type})
	require.Equal(t, "mobile text", events[0].Content)
	require.Empty(t, events[5].Content)
	require.Empty(t, events[5].ResponsePayload)
	require.Equal(t, "unclassified", events[5].TurnID)
	require.NotContains(t, rec.Body.String(), "private speculative")
	require.NotContains(t, rec.Body.String(), "MUST NOT APPEAR")
	require.NotContains(t, rec.Body.String(), "foreign secret")
	require.True(t, c.closed)
	require.Zero(t, c.queries.Load(), "observation never submits primary chat")
}

func TestCompatibilityHTTPAuthorizationAndSharedVisibility(t *testing.T) {
	c := &compatibilityDatlyClient{datlyObservedClient: newDatlyObservedClient(t, 16)}
	private := conversation.NewConversation()
	private.SetId("thread")
	private.SetVisibility("private")
	require.NoError(t, c.conv.PatchConversations(recoveryContext(), private))
	rec := compatibilityHTTP(t, c, "foreign", NativeAndApplicationCompatibilityScope, nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.False(t, c.subscribed.Load())
	public := conversation.NewConversation()
	public.SetId("thread")
	public.SetVisibility("public")
	require.NoError(t, c.conv.PatchConversations(recoveryContext(), public))
	admitCompatibilityRun(t, c.datlyObservedClient, "agui-turn")
	c.events = []*streaming.Event{compatibilityEvent(streaming.EventTypeTextDelta, "agui-turn", "owner protocol text"), compatibilityEvent(streaming.EventTypeConversationMetaUpdated, "", "visible metadata")}
	rec = compatibilityHTTP(t, c, "foreign", NativeAndApplicationCompatibilityScope, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "owner protocol text")
	require.Contains(t, rec.Body.String(), "visible metadata")
	rec = compatibilityHTTP(t, c, "", NativeAndApplicationCompatibilityScope, &authsvc.Config{Enabled: true})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	rec = compatibilityHTTP(t, c, "owner", "all", nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCompatibilityDisconnectAndOverflowDoNotChangeExecutionAuthority(t *testing.T) {
	c := &compatibilityDatlyClient{datlyObservedClient: newDatlyObservedClient(t, 16), reason: streaming.ReasonOverflow}
	admitCompatibilityRun(t, c.datlyObservedClient, "agui-turn")
	c.events = []*streaming.Event{compatibilityEvent(streaming.EventTypeTextDelta, "agui-turn", "excluded during overflow")}
	rec := compatibilityHTTP(t, c, "owner", NativeAndApplicationCompatibilityScope, nil)
	events := compatibilityWireEvents(t, rec.Body.String())
	require.Len(t, events, 1)
	require.Equal(t, streaming.EventTypeStreamOverflow, events[0].Type)
	require.Equal(t, int64(99), events[0].EventSeq)
	c.reason = streaming.ReasonClosed
	c.closed = false
	rec = compatibilityHTTP(t, c, "owner", NativeAndApplicationCompatibilityScope, nil)
	require.Empty(t, compatibilityWireEvents(t, rec.Body.String()))
	require.True(t, c.closed)
	// The legacy default remains backward compatible for mobile/CLI consumers.
	rec = compatibilityHTTP(t, c, "owner", "", nil)
	require.Contains(t, rec.Body.String(), "excluded during overflow")
}

func TestCompatibilityAGUIDescendantsRemainExcluded(t *testing.T) {
	c := newDatlyObservedClient(t, 16)
	admitCompatibilityRun(t, c, "agui-parent")
	child := conversation.NewConversation()
	child.SetId("child")
	child.SetCreatedByUserID("owner")
	child.SetConversationParentId("thread")
	child.SetConversationParentTurnId("agui-parent")
	require.NoError(t, c.conv.PatchConversations(recoveryContext(), child))
	turn := conversation.NewTurn()
	turn.SetId("child-turn")
	turn.SetConversationID("child")
	turn.SetStatus("running")
	require.NoError(t, c.conv.PatchTurn(recoveryContext(), turn))
	event := &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "child", TurnID: "child-turn", Content: "excluded child"}
	observer := newCompatibilityObserver(aguistore.New(c.native.goalInvoker), "child")
	require.Nil(t, observer.project(recoveryContext(), event))
}

type mutableCompatibilityReader struct {
	evidence *aguistore.ExecutionProvenance
	err      error
	calls    int
}

func (r *mutableCompatibilityReader) ReadExecutionProvenance(context.Context, string, string) (*aguistore.ExecutionProvenance, error) {
	r.calls++
	return r.evidence, r.err
}
func TestCompatibilityUnknownNeverBecomesCachedNativeAuthority(t *testing.T) {
	reader := &mutableCompatibilityReader{err: errors.New("transient read failure")}
	observer := newCompatibilityObserver(reader, "thread")
	event := compatibilityEvent(streaming.EventTypeTextDelta, "turn", "hidden until proven")
	require.Equal(t, CompatibilityReconcileEvent, observer.project(context.Background(), event).Type)
	require.Nil(t, observer.project(context.Background(), event))
	require.Equal(t, 2, reader.calls)
	reader.err = nil
	reader.evidence = &aguistore.ExecutionProvenance{ThreadID: "thread", TurnID: "turn", AGUIOwned: true}
	require.Nil(t, observer.project(context.Background(), event))
	require.Equal(t, 3, reader.calls)
	reader.evidence.AGUIOwned = false
	reader.evidence.NativeTurnFound = true
	require.Nil(t, observer.project(context.Background(), event))
	require.Equal(t, 3, reader.calls, "positive AGUI ownership remains immutable")
}

func TestCompatibilityProvenanceSurvivesNativeRuntimeRestart(t *testing.T) {
	ctx := recoveryContext()
	workspace := t.TempDir()
	server, err := nativeruntime.New(ctx, nativeruntime.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	row := conversation.NewConversation()
	row.SetId("restarted")
	row.SetCreatedByUserID("owner")
	require.NoError(t, conv.PatchConversations(ctx, row))
	repository := aguistore.New(server)
	_, _, err = repository.Admit(ctx, aguistore.Admission{ThreadID: "restarted", RunID: "durable", TurnID: "turn-before-native", Principal: "owner", Input: json.RawMessage(`{"threadId":"restarted","runId":"durable","messages":[{"id":"user","role":"user","content":"hello"}]}`)})
	require.NoError(t, err)
	require.NoError(t, server.Shutdown(ctx))
	server, err = nativeruntime.New(ctx, nativeruntime.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	reader := aguistore.New(server)
	evidence, err := reader.ReadExecutionProvenance(ctx, "restarted", "turn-before-native")
	require.NoError(t, err)
	require.True(t, evidence.AGUIOwned)
	require.False(t, evidence.NativeTurnFound)
	observer := newCompatibilityObserver(reader, "restarted")
	require.Nil(t, observer.project(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "restarted", TurnID: "turn-before-native", Content: "no connection existed in new process"}))
}

func TestCompatibilityRealBusOverflowAndDisconnectNeverTransferOwnership(t *testing.T) {
	c := newDatlyObservedClient(t, 16)
	admitCompatibilityRun(t, c, "agui-turn")
	bus := streaming.NewMemoryBus(1)
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	event := compatibilityEvent(streaming.EventTypeTextDelta, "agui-turn", "no observer-based authority")
	require.NoError(t, bus.Publish(context.Background(), event))
	require.NoError(t, bus.Publish(context.Background(), event))
	require.Equal(t, streaming.ReasonOverflow, sub.Reason())
	observer := newCompatibilityObserver(aguistore.New(c.native.goalInvoker), "thread")
	for delivered := range sub.C() {
		require.Nil(t, observer.project(context.Background(), delivered))
	}
	replacement, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, bus.Publish(context.Background(), event))
	delivered := <-replacement.C()
	require.NoError(t, replacement.Close())
	fresh := newCompatibilityObserver(aguistore.New(c.native.goalInvoker), "thread")
	require.Nil(t, fresh.project(context.Background(), delivered))
}

func TestCompatibilityUnavailableForGenericAdapters(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/v1/stream?conversationId=thread&compatibilityScope=native-and-application", nil)
	req = req.WithContext(iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: "owner"}))
	rec := httptest.NewRecorder()
	handleStreamEvents(base)(rec, req)
	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestCompatibilityMissingNativeTurnCanLaterBecomeAuthoritative(t *testing.T) {
	reader := &mutableCompatibilityReader{evidence: &aguistore.ExecutionProvenance{ThreadID: "thread", TurnID: "turn"}}
	observer := newCompatibilityObserver(reader, "thread")
	event := compatibilityEvent(streaming.EventTypeTextDelta, "turn", "persisted native content")
	require.Equal(t, CompatibilityReconcileEvent, observer.project(context.Background(), event).Type)
	reader.evidence.NativeTurnFound = true
	require.Same(t, event, observer.project(context.Background(), event))
	require.Equal(t, 2, reader.calls)
	require.Same(t, event, observer.project(context.Background(), event))
	require.Equal(t, 2, reader.calls)
}
