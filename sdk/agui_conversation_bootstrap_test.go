package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	convstore "github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type bootstrapDatlyClient struct {
	*datlyObservedClient
	reads   atomic.Int32
	live    atomic.Int32
	input   *GetTranscriptInput
	options transcriptOptions
}

func (c *bootstrapDatlyClient) GetConversation(ctx context.Context, id string) (*conversation.Conversation, error) {
	return c.native.GetConversation(ctx, id)
}
func (c *bootstrapDatlyClient) GetTranscript(ctx context.Context, input *GetTranscriptInput, options ...TranscriptOption) (*ConversationStateResponse, error) {
	c.reads.Add(1)
	copy := *input
	c.input = &copy
	c.options = transcriptOptions{}
	for _, option := range options {
		option(&c.options)
	}
	return c.native.GetTranscript(ctx, input, options...)
}
func (c *bootstrapDatlyClient) GetLiveState(ctx context.Context, id string, options ...TranscriptOption) (*ConversationStateResponse, error) {
	c.live.Add(1)
	return c.native.GetLiveState(ctx, id, options...)
}
func (c *bootstrapDatlyClient) aguiConversationBootstrapAvailable() bool { return true }

func newBootstrapDatlyServer(t *testing.T) (*bootstrapDatlyClient, *httptest.Server) {
	t.Helper()
	c := &bootstrapDatlyClient{datlyObservedClient: newDatlyObservedClient(t, 32)}
	handler := handleAGUIRun(c, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := r.Header.Get("X-Test-Principal")
		if principal == "" {
			principal = "owner"
		}
		handler(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: principal})))
	}))
	t.Cleanup(server.Close)
	return c, server
}
func bootstrapRequest(run string, payload any) string {
	return string(rawAGUI(map[string]any{"threadId": "thread", "runId": run, "messages": []any{}, "state": map[string]any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "operation": "conversation.bootstrap", "requestId": run, "payload": payload}}}))
}
func bootstrapWireResult(t *testing.T, wire string) *AGUIConversationBootstrapResult {
	t.Helper()
	for _, line := range strings.Split(wire, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type   string          `json:"type"`
			Result json.RawMessage `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
		if event.Type != "RUN_FINISHED" {
			continue
		}
		var result AGUIConversationBootstrapResult
		require.NoError(t, json.Unmarshal(event.Result, &result))
		require.NoError(t, extensions.ValidateConversationResult(event.Result))
		return &result
	}
	t.Fatalf("bootstrap result unavailable: %s", wire)
	return nil
}
func TestAGUIConversationBootstrapDatlyHTTPPreservesCanonicalHistoryAndReadOnlyReplay(t *testing.T) {
	c, server := newBootstrapDatlyServer(t)
	ctx := recoveryContext()
	require.NoError(t, nativeTestTurn(ctx, c.datlyObservedClient, "saved-turn", "succeeded"))
	require.NoError(t, nativeTestMessage(ctx, c.datlyObservedClient, "saved-turn", "native-user", "user", "saved user"))
	rich := "saved assistant\n```forge-data\n{\"id\":\"rows\",\"data\":[{\"n\":9007199254740991}]}\n```\n```forge-ui\n{\"version\":1,\"blocks\":[]}\n```"
	require.NoError(t, nativeTestMessage(ctx, c.datlyObservedClient, "saved-turn", "native-assistant", "assistant", rich))
	expected, err := c.native.GetTranscript(ctx, &GetTranscriptInput{ConversationID: "thread", IncludeModelCalls: true, IncludeToolCalls: true}, WithIncludeFeeds(), WithIncludeModelCalls(), WithIncludeToolCalls())
	require.NoError(t, err)
	request := bootstrapRequest("bootstrap", map[string]any{})
	status, wire := durablePost(t, server, request, nil)
	require.Equal(t, 200, status, wire)
	result := bootstrapWireResult(t, wire)
	require.NotNil(t, result.Transcript.AguiThreadID)
	require.Equal(t, "thread", *result.Transcript.AguiThreadID)
	expected.AguiThreadID = func() *string { value := "thread"; return &value }()
	require.JSONEq(t, string(rawAGUI(expected)), string(rawAGUI(result.Transcript)))
	require.Contains(t, string(rawAGUI(result.Transcript)), "native-user")
	require.Contains(t, string(rawAGUI(result.Transcript)), "native-assistant")
	require.Contains(t, string(rawAGUI(result.Transcript)), "9007199254740991")
	require.False(t, result.Projection.Lossless, "native presentation reconstruction is explicitly incomplete")
	require.Contains(t, result.Projection.UnavailableMessageIDs, "native-assistant")
	require.Contains(t, string(rawAGUI(result.Messages)), "agently.rendered-content")
	for _, message := range result.Messages {
		if message["role"] == "assistant" {
			require.NotContains(t, string(rawAGUI(message["content"])), "forge-data")
		}
	}
	require.Empty(t, result.Runs, "the bootstrap command itself is not an active execution")
	require.Zero(t, c.queries.Load())
	require.Zero(t, c.resumes.Load())
	require.Zero(t, c.completions.Load())
	require.EqualValues(t, 1, c.reads.Load())
	require.True(t, c.input.IncludeModelCalls)
	require.True(t, c.input.IncludeToolCalls)
	require.True(t, c.options.includeFeeds)
	thread, err := c.store.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(thread.Messages))
	require.JSONEq(t, `null`, string(thread.State))
	status, replay := durablePost(t, server, request, nil)
	require.Equal(t, 200, status, replay)
	require.Equal(t, wire, replay)
	require.EqualValues(t, 1, c.reads.Load())
	after, err := c.native.GetTranscript(ctx, &GetTranscriptInput{ConversationID: "thread", IncludeModelCalls: true, IncludeToolCalls: true}, WithIncludeFeeds(), WithIncludeModelCalls(), WithIncludeToolCalls())
	require.NoError(t, err)
	require.JSONEq(t, string(rawAGUI(expected)), string(rawAGUI(after)))
}
func TestAGUIConversationBootstrapDatlyHTTPJournalMediaStateAndSanitizedRuns(t *testing.T) {
	c, server := newBootstrapDatlyServer(t)
	ctx := recoveryContext()
	require.NoError(t, nativeTestTurn(ctx, c.datlyObservedClient, "saved-turn", "succeeded"))
	require.NoError(t, nativeTestMessage(ctx, c.datlyObservedClient, "saved-turn", "assistant", "assistant", "new saved content"))
	record, _, err := c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: "active", TurnID: "private-native-turn", Input: json.RawMessage(`{"threadId":"thread","runId":"active","messages":[],"forwardedProps":{"private":"accepted-input-secret"}}`)})
	require.NoError(t, err)
	record, err = c.store.Claim(ctx, "owner", "thread", record.RunID, record.Revision, "private-lease", time.Minute)
	require.NoError(t, err)
	thread, err := c.store.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	messages := json.RawMessage(`[{"id":"media","role":"user","content":[{"type":"text","text":"hello"},{"type":"image","source":{"type":"url","value":"https://example.com/public.png"}}],"metadata":{"keep":false}},{"id":"assistant","role":"assistant","content":"stale text","metadata":{"opaque":"keep"}},{"id":"app","role":"activity","activityType":"mcp-apps","content":{"result":{"_meta":{"hostOnly":"private-host-receipt"}},"_agentlyApp":{"nativeServerId":"private-server"}}}]`)
	record, err = c.store.Append(ctx, "owner", "thread", record.RunID, record.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": "active"})}, &aguistore.Change{LeaseOwner: record.LeaseOwner, Messages: messages, State: json.RawMessage(`{"number":9007199254740991,"false":false}`), Pending: json.RawMessage(`{"private":"pending-secret"}`), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	status, wire := durablePost(t, server, bootstrapRequest("bootstrap-rich", map[string]any{}), nil)
	require.Equal(t, 200, status, wire)
	result := bootstrapWireResult(t, wire)
	require.Contains(t, string(rawAGUI(result.State)), "9007199254740991")
	require.Contains(t, string(rawAGUI(result.Messages)), "public.png")
	require.Contains(t, string(rawAGUI(result.Messages)), `"keep":false`)
	require.Contains(t, string(rawAGUI(result.Messages)), "new saved content")
	require.Contains(t, string(rawAGUI(result.Messages)), `"opaque":"keep"`)
	require.NotContains(t, string(rawAGUI(result.Messages)), "stale text")
	require.NotContains(t, wire, "private-host-receipt")
	require.NotContains(t, wire, "accepted-input-secret")
	require.NotContains(t, wire, "pending-secret")
	require.NotContains(t, wire, "private-lease")
	require.NotContains(t, string(rawAGUI(result.Runs)), "private-native-turn")
	require.Len(t, result.Runs, 1)
	require.Equal(t, "active", result.Runs[0].RunID)
	require.Contains(t, result.Projection.UnavailableMessageIDs, "app")
	after, err := c.store.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	require.JSONEq(t, string(messages), string(after.Messages))
	require.JSONEq(t, `{"number":9007199254740991,"false":false}`, string(after.State))
}
func TestAGUIConversationBootstrapHTTPForeignScopeAndInvalidPayloadRejectedBeforeAdmission(t *testing.T) {
	c, server := newBootstrapDatlyServer(t)
	status, wire := durablePost(t, server, bootstrapRequest("foreign", map[string]any{}), map[string]string{"X-Test-Principal": "foreign"})
	require.Equal(t, 403, status, wire)
	for _, payload := range []any{map[string]any{"conversationId": "foreign"}, map[string]any{"userId": "foreign"}, map[string]any{"mode": "live", "since": "old"}, map[string]any{"selectors": map[string]any{"ExecutionGroup": map[string]any{"offset": -1}}}} {
		status, wire = durablePost(t, server, bootstrapRequest("invalid", payload), nil)
		require.Equal(t, 400, status, wire)
	}
	missing := strings.Replace(bootstrapRequest("missing", map[string]any{}), `"threadId":"thread"`, `"threadId":"missing"`, 1)
	status, wire = durablePost(t, server, missing, nil)
	require.Equal(t, 403, status, wire)
	require.Zero(t, c.queries.Load())
	require.Zero(t, c.reads.Load())
}
func TestAGUIConversationBootstrapDatlyHTTPLiveAndSelectorsUseExistingOptions(t *testing.T) {
	c, server := newBootstrapDatlyServer(t)
	status, wire := durablePost(t, server, bootstrapRequest("live", map[string]any{"mode": "live", "includeModelCalls": false, "includeToolCalls": false, "includeFeeds": false}), nil)
	require.Equal(t, 200, status, wire)
	result := bootstrapWireResult(t, wire)
	require.NotEmpty(t, result.Transcript.EventCursor)
	require.EqualValues(t, 1, c.live.Load())
	require.Zero(t, c.reads.Load())
	status, wire = durablePost(t, server, bootstrapRequest("selected", map[string]any{"includeModelCalls": false, "includeToolCalls": false, "includeFeeds": false, "selectors": map[string]any{"ExecutionGroup": map[string]any{"limit": 1, "offset": 0}}}), nil)
	require.Equal(t, 200, status, wire)
	bootstrapWireResult(t, wire)
	require.False(t, c.input.IncludeModelCalls)
	require.False(t, c.options.includeFeeds)
	require.Equal(t, 1, c.options.selectors["ExecutionGroup"].Limit)
}
func TestAGUIConversationBootstrapProjectionDoesNotFlattenMultipartNativeUpdate(t *testing.T) {
	transcript := &ConversationStateResponse{Conversation: &ConversationState{ConversationID: "thread", Turns: []*TurnState{{User: &UserMessageState{MessageID: "media", Content: "new text"}}}}}
	messages, quality := aguiBootstrapMessages(context.Background(), nil, transcript, []aguistate.Object{{"id": "media", "role": "user", "content": []any{map[string]any{"type": "text", "text": "old"}, map[string]any{"type": "image", "source": map[string]any{"type": "url", "value": "https://example.com/image"}}}}})
	require.False(t, quality.Lossless)
	require.Equal(t, []string{"media"}, quality.UnavailableMessageIDs)
	require.Contains(t, string(rawAGUI(messages)), "image")
}

func TestAGUIConversationBootstrapProjectionReplacesStaleRichPresentationAndPreservesOpaqueFields(t *testing.T) {
	transcript := &ConversationStateResponse{Conversation: &ConversationState{ConversationID: "thread", Turns: []*TurnState{{Assistant: &AssistantState{Final: &AssistantMessageState{MessageID: "assistant", Content: "new plain response"}}}}}}
	messages, _ := aguiBootstrapMessages(context.Background(), nil, transcript, []aguistate.Object{{"id": "assistant", "role": "assistant", "content": "old response", "metadata": map[string]any{"keep": true}}, {"id": "assistant/activity", "role": "activity", "activityType": "agently.rendered-content", "content": map[string]any{"private": "old report"}}})
	require.Len(t, messages, 1)
	require.Equal(t, "new plain response", messages[0]["content"])
	require.Contains(t, string(rawAGUI(messages)), `"keep":true`)
}

func TestAGUIConversationBootstrapActiveDiscoveryAcrossDatlyStoreInstancesKeepsContinuationReaderUnchanged(t *testing.T) {
	c, server := newBootstrapDatlyServer(t)
	ctx := recoveryContext()
	admit := func(id string) *aguistore.Run {
		run, _, err := c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: id, TurnID: "native-" + id, Input: rawAGUI(map[string]any{"threadId": "thread", "runId": id, "messages": []any{}})})
		require.NoError(t, err)
		return run
	}
	admit("admitted")
	running := admit("running")
	_, err := c.store.Append(ctx, "owner", "thread", running.RunID, running.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": "running"})}, nil)
	require.NoError(t, err)
	interrupted := admit("interrupted")
	_, err = c.store.Append(ctx, "owner", "thread", interrupted.RunID, interrupted.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": "interrupted"}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "thread", "runId": "interrupted", "outcome": map[string]any{"type": "interrupt", "interrupts": []any{map[string]any{"id": "ask", "reason": "approval"}}}})}, &aguistore.Change{Pending: json.RawMessage(`{"interrupts":[{"id":"ask","reason":"approval"}]}`)})
	require.NoError(t, err)
	second := aguistore.New(c.native.goalInvoker)
	active, err := second.ListActive(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Len(t, active, 2)
	ids := []string{active[0].RunID, active[1].RunID}
	require.Equal(t, []string{"admitted", "running"}, ids)
	pending, err := second.ListPending(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "interrupted", pending[0].RunID)
	foreign, err := second.ListActive(ctx, "foreign", "thread")
	require.Error(t, err)
	require.Empty(t, foreign)
	status, wire := durablePost(t, server, bootstrapRequest("read", map[string]any{}), nil)
	require.Equal(t, 200, status, wire)
	result := bootstrapWireResult(t, wire)
	require.Len(t, result.Runs, 3)
	require.Equal(t, []string{"admitted", "interrupted", "running"}, []string{result.Runs[0].RunID, result.Runs[1].RunID, result.Runs[2].RunID})
	require.NotContains(t, string(rawAGUI(result.Runs)), "native-")
	require.NotContains(t, string(rawAGUI(result.Runs)), "ask")
}

func TestAGUIConversationBootstrapCorrelatesProtocolUserWithDifferentPersistedNativeUserByDurableIdentity(t *testing.T) {
	c, server := newBootstrapDatlyServer(t)
	nativeUserID := "native-user-row"
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		if err := nativeTestTurn(ctx, c.datlyObservedClient, in.MessageID, "running"); err != nil {
			return nil, err
		}
		if err := nativeTestMessage(ctx, c.datlyObservedClient, in.MessageID, nativeUserID, "user", "same user"); err != nil {
			return nil, err
		}
		turn := conversation.NewTurn()
		turn.SetId(in.MessageID)
		turn.SetConversationID("thread")
		turn.SetStartedByMessageID(nativeUserID)
		if err := c.conv.PatchTurn(ctx, turn); err != nil {
			return nil, err
		}
		if err := c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeModelStarted, ConversationID: "thread", TurnID: in.MessageID, ParentMessageID: nativeUserID, AssistantMessageID: "assistant", PageID: "page", ModelCallID: "model"}); err != nil {
			return nil, err
		}
		if err := nativeTestTurn(ctx, c.datlyObservedClient, in.MessageID, "succeeded"); err != nil {
			return nil, err
		}
		return &agentsvc.QueryOutput{Content: "done"}, nil
	}
	input := `{"threadId":"thread","runId":"identity-chat","messages":[{"id":"protocol-user","role":"user","content":[{"type":"text","text":"same user"},{"type":"image","source":{"type":"url","value":"https://example.com/media.png"}}]}]}`
	status, wire := durablePost(t, server, input, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "agently.user-identity")
	require.Contains(t, wire, nativeUserID)
	status, wire = durablePost(t, server, bootstrapRequest("identity-bootstrap", map[string]any{}), nil)
	require.Equal(t, 200, status, wire)
	result := bootstrapWireResult(t, wire)
	users := []aguistate.Object{}
	for _, message := range result.Messages {
		if message["role"] == "user" {
			users = append(users, message)
		}
	}
	require.Len(t, users, 1)
	require.Equal(t, "protocol-user", users[0]["id"])
	require.Contains(t, string(rawAGUI(users[0])), "media.png")
	require.Contains(t, string(rawAGUI(result.Transcript)), nativeUserID)
	require.EqualValues(t, 1, c.queries.Load())
}

func TestAGUIConversationBootstrapInternalModesAndIdentityDedupe(t *testing.T) {
	transcript := &ConversationStateResponse{Conversation: &ConversationState{ConversationID: "thread", Turns: []*TurnState{{TurnID: "turn", Messages: []*TurnMessageState{
		{MessageID: "router", Role: "assistant", Mode: "router", Content: `{"classification":true}`},
		{MessageID: "chain", Role: "assistant", Mode: "chain", Content: "internal prose"},
		{MessageID: "interim", Role: "assistant", Mode: "task", Content: "same prose"},
		{MessageID: "final", Role: "assistant", Mode: "task", Content: "same prose"},
	}, Assistant: &AssistantState{Final: &AssistantMessageState{MessageID: "final", Content: "same prose"}}}}}}
	journal := []aguistate.Object{{"id": "router", "role": "assistant", "content": `{"classification":true}`}, {"id": "router/activity", "role": "activity", "activityType": "agently.rendered-content", "content": map[string]any{"private": true}}, {"id": "interim", "role": "assistant", "content": "same prose"}}
	messages, quality := aguiBootstrapMessages(context.Background(), nil, transcript, journal)
	require.False(t, quality.Lossless)
	require.Equal(t, []string{"final"}, quality.UnavailableMessageIDs)
	ids := map[string]int{}
	for _, message := range messages {
		ids[message["id"].(string)]++
	}
	require.NotContains(t, ids, "router")
	require.NotContains(t, ids, "router/activity")
	require.NotContains(t, ids, "chain")
	require.Equal(t, 1, ids["interim"])
	require.Equal(t, 1, ids["final"], "same canonical final ID appears once across message and aggregate")
}

func TestAGUIBootstrapFiltersActualModelCallInclusiveCanonicalHistory(t *testing.T) {
	for _, internalMode := range []string{"router", "chain"} {
		t.Run(internalMode, func(t *testing.T) {
			routerMode, taskMode := internalMode, "task"
			routerBody, taskBody := `{"classification":true}`, "Preliminary findings"
			transcript := convstore.Transcript{&convstore.Turn{Id: "turn", ConversationId: "thread", Message: []*conversationmodel.MessageView{
				{Id: "router", Role: "assistant", Mode: &routerMode, Content: &routerBody, ModelCall: &conversationmodel.ModelCallView{MessageId: "router", Status: "completed"}},
				{Id: "task", Role: "assistant", Mode: &taskMode, Content: &taskBody, ModelCall: &conversationmodel.ModelCallView{MessageId: "task", Status: "completed"}},
			}}}
			canonical := BuildCanonicalState("thread", transcript)
			require.NotEmpty(t, canonical.Turns[0].Execution.Pages)
			require.Len(t, canonical.Turns[0].Messages, 2, "model-call-bearing messages retain their canonical IDs and bodies")
			require.Equal(t, "task", canonical.Turns[0].Messages[1].MessageID)
			require.Equal(t, taskBody, canonical.Turns[0].Messages[1].Content)
			journal := []aguistate.Object{{"id": "router", "role": "assistant", "content": routerBody}, {"id": "router/activity", "role": "activity", "activityType": "agently.rendered-content", "content": map[string]any{"private": true}}, {"id": "task", "role": "assistant", "content": taskBody}}
			messages, _ := aguiBootstrapMessages(context.Background(), nil, &ConversationStateResponse{Conversation: canonical}, journal)
			ids := map[string]bool{}
			for _, message := range messages {
				ids[message["id"].(string)] = true
			}
			require.False(t, ids["router"])
			require.False(t, ids["router/activity"])
			require.True(t, ids["task"])
			require.Len(t, canonical.Turns[0].Execution.Pages[0].ModelSteps, 2, "mixed-iteration model lifecycle is preserved")
		})
	}
}

func TestAGUIBootstrapEnvelopeAcceptsOptionalCompactModelPayloads(t *testing.T) {
	c, server := newBootstrapDatlyServer(t)
	status, wire := durablePost(t, server, bootstrapRequest("compact-schema", map[string]any{"mode": "transcript", "includeModelCalls": true, "includeToolCalls": true, "includeModelPayloads": false}), nil)
	require.Equal(t, 200, status, wire)
	require.NotNil(t, bootstrapWireResult(t, wire))
	require.NotNil(t, c.options.includeModelPayloads)
	require.False(t, *c.options.includeModelPayloads)
	status, _ = durablePost(t, server, bootstrapRequest("bad-compact-schema", map[string]any{"includeModelPayloads": "false"}), nil)
	require.Equal(t, 400, status, "the optional contract remains typed")
}

func TestNativeBootstrapMarksOnlyCanonicalStandalonePublicMessagesForReattachment(t *testing.T) {
	noteID := "61d8d98c-1b0f-4250-a4c8-da464ddc7fb4"
	finalID := "f93b137d-8342-4ed8-95e5-72178cab91ce"
	transcript := &ConversationStateResponse{Conversation: &ConversationState{ConversationID: "conversation", Turns: []*TurnState{{TurnID: "turn", Messages: []*TurnMessageState{{MessageID: noteID, Role: "assistant", Mode: "task", Content: "Owned findings"}, {MessageID: finalID, Role: "assistant", Mode: "task", Content: "Owned final report"}}, Assistant: &AssistantState{Final: &AssistantMessageState{MessageID: finalID, Content: "Owned final report"}}, Execution: &ExecutionState{Pages: []*ExecutionPageState{{PageID: "final-page", FinalAssistantMessageID: finalID, FinalResponse: true, ModelSteps: []*ModelStepState{{AssistantMessageID: finalID}}}}}}}}}
	messages, _ := aguiBootstrapMessages(context.Background(), nil, transcript, nil)
	for _, message := range messages {
		if message["id"] != noteID && message["id"] != finalID {
			continue
		}
		encoded := rawAGUI(message)
		var raw map[string]any
		require.NoError(t, json.Unmarshal(encoded, &raw))
		metadata, _ := raw["metadata"].(map[string]any)
		namespace, _ := metadata["agently"].(map[string]any)
		presentation, _ := namespace["presentation"].(map[string]any)
		if message["id"] == noteID {
			require.Equal(t, "standalone", presentation["messageKind"])
		} else {
			require.NotEqual(t, "standalone", presentation["messageKind"])
		}
	}
}

func TestAGUIConversationBootstrapDoesNotReattachInterruptedRunForTerminalNativeTurn(t *testing.T) {
	c, server := newBootstrapDatlyServer(t)
	ctx := recoveryContext()
	require.NoError(t, nativeTestTurn(ctx, c.datlyObservedClient, "finished-turn", "succeeded"))
	require.NoError(t, nativeTestTurn(ctx, c.datlyObservedClient, "waiting-turn", "waiting_for_user"))
	require.NoError(t, nativeTestTurn(ctx, c.datlyObservedClient, "finished-mcp-turn", "succeeded"))
	for _, id := range []string{"finished", "waiting", "unknown"} {
		run, _, err := c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: id, TurnID: id + "-turn", Input: rawAGUI(map[string]any{"threadId": "thread", "runId": id, "messages": []any{}})})
		require.NoError(t, err)
		_, err = c.store.Append(ctx, "owner", "thread", id, run.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": id}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "thread", "runId": id, "outcome": map[string]any{"type": "interrupt", "interrupts": []any{map[string]any{"id": "ask", "reason": "elicitation"}}}})}, &aguistore.Change{Pending: json.RawMessage(`{"interrupts":[{"id":"ask","reason":"elicitation"}]}`)})
		require.NoError(t, err)
	}
	for _, id := range []string{"mcp", "resource"} {
		input := map[string]any{"threadId": "thread", "runId": id, "messages": []any{}}
		turnID := ""
		if id == "mcp" {
			turnID = "finished-mcp-turn"
			input["forwardedProps"] = map[string]any{"__proxiedMCPRequest": map[string]any{}}
		}
		_, _, err := c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: id, TurnID: turnID, Input: rawAGUI(input)})
		require.NoError(t, err)
	}
	status, wire := durablePost(t, server, bootstrapRequest("read-terminal", map[string]any{}), nil)
	require.Equal(t, 200, status, wire)
	result := bootstrapWireResult(t, wire)
	require.Len(t, result.Runs, 4)
	require.Equal(t, []string{"mcp", "resource", "unknown", "waiting"}, []string{result.Runs[0].RunID, result.Runs[1].RunID, result.Runs[2].RunID, result.Runs[3].RunID})
	pending, err := c.store.ListPending(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Len(t, pending, 3, "read-only bootstrap preserves original journals and pending controls")
}
