package sdk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	messagemodel "github.com/viant/agently-core/model/message"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
)

const aguiChatRequest = `{"threadId":"thread","runId":"external-run","messages":[{"id":"user-message","role":"user","content":"hello"}]}`

type aguiTestClient struct {
	Client
	bus              *streaming.MemoryBus
	subscribed       atomic.Bool
	queries          atomic.Int32
	query            func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error)
	owner            string
	conversationErr  error
	payloads         map[string]*conversation.Payload
	requestPayloadID string
	messagePage      *MessagePage
	messagesInput    *GetMessagesInput
}

func newAGUITestClient() *aguiTestClient { return &aguiTestClient{bus: streaming.NewMemoryBus(64)} }
func (c *aguiTestClient) Query(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
	c.queries.Add(1)
	return c.query(ctx, in)
}
func (c *aguiTestClient) StreamEvents(ctx context.Context, in *StreamEventsInput) (streaming.Subscription, error) {
	sub, err := c.bus.Subscribe(ctx, in.Filter)
	c.subscribed.Store(err == nil)
	return sub, err
}
func (c *aguiTestClient) GetConversation(_ context.Context, id string) (*conversation.Conversation, error) {
	if c.conversationErr != nil {
		return nil, c.conversationErr
	}
	if c.owner == "" {
		return nil, nil
	}
	return &conversation.Conversation{Id: id, CreatedByUserId: &c.owner}, nil
}
func (c *aguiTestClient) GetMessages(_ context.Context, input *GetMessagesInput) (*MessagePage, error) {
	copy := *input
	c.messagesInput = &copy
	if c.messagePage != nil {
		return c.messagePage, nil
	}
	return &MessagePage{}, nil
}
func (c *aguiTestClient) GetPayloads(context.Context, []string) (map[string]*conversation.Payload, error) {
	return c.payloads, nil
}
func (c *aguiTestClient) aguiToolRequestPayload(context.Context, string, string, string) (string, error) {
	return c.requestPayloadID, nil
}
func (c *aguiTestClient) publish(typ streaming.EventType, content string) {
	_ = c.bus.Publish(context.Background(), &streaming.Event{Type: typ, ConversationID: "thread", TurnID: "user-message", MessageID: "assistant", Content: content})
}
func aguiServer(t *testing.T, c *aguiTestClient, principal string, authCfg *svcauth.Config) *httptest.Server {
	t.Helper()
	h := handleAGUIRun(c, authCfg)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal != "" {
			r = r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: principal}))
		}
		h(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}
func aguiPost(t *testing.T, s *httptest.Server, body string) *http.Response {
	t.Helper()
	client := s.Client()
	client.Timeout = 5 * time.Second
	r, err := client.Post(s.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func decodeAGUISSE(t *testing.T, body io.Reader) []agui.Event {
	t.Helper()
	var events []agui.Event
	s := bufio.NewScanner(body)
	for s.Scan() {
		if !strings.HasPrefix(s.Text(), "data: ") {
			continue
		}
		var e agui.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(s.Text(), "data: ")), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}
func assertAGUITerminal(t *testing.T, events []agui.Event, want string) {
	t.Helper()
	count := 0
	for _, e := range events {
		if e.Type == "RUN_FINISHED" || e.Type == "RUN_ERROR" {
			count++
			if e.Type != want {
				t.Fatalf("terminal=%s want=%s events=%+v", e.Type, want, events)
			}
		}
	}
	if count != 1 || events[0].Type != "RUN_STARTED" || events[len(events)-1].Type != want {
		t.Fatalf("invalid lifecycle %+v", events)
	}
}
func TestAGUIRunStartsBeforeExecutionAndUsesUserTurnIdentity(t *testing.T) {
	c := newAGUITestClient()
	receivedStart := make(chan struct{})
	queryStarted := make(chan *agentsvc.QueryInput, 1)
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		if !c.subscribed.Load() {
			return nil, errors.New("not subscribed")
		}
		queryStarted <- in
		<-receivedStart
		c.publish(streaming.EventTypeTextDelta, "answer")
		c.publish(streaming.EventTypeAssistant, "answer")
		c.publish(streaming.EventTypeTurnCompleted, "")
		return &agentsvc.QueryOutput{Content: "answer"}, nil
	}
	s := aguiServer(t, c, "principal", nil)
	r := aguiPost(t, s, aguiChatRequest)
	defer r.Body.Close()
	scanner := bufio.NewScanner(r.Body)
	if !scanner.Scan() {
		t.Fatal("missing immediate start")
	}
	var first agui.Event
	if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &first); err != nil {
		t.Fatal(err)
	}
	if first.Type != "RUN_STARTED" || first.RunID != "external-run" {
		t.Fatal(first)
	}
	close(receivedStart)
	in := <-queryStarted
	if in.MessageID != "user-message" || in.ConversationID != "thread" || in.UserId != "principal" {
		t.Fatal(in)
	}
	var rest strings.Builder
	for scanner.Scan() {
		rest.WriteString(scanner.Text() + "\n")
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	events := append([]agui.Event{first}, decodeAGUISSE(t, strings.NewReader(rest.String()))...)
	assertAGUITerminal(t, events, "RUN_FINISHED")
	text := ""
	for _, e := range events {
		if e.Type == "TEXT_MESSAGE_CONTENT" {
			text += e.Delta
		}
	}
	if text != "answer" {
		t.Fatalf("snapshot/fallback duplicated text %q", text)
	}
}
func TestAGUIPresetPublishedAnswerDoesNotRepeatFallback(t *testing.T) {
	c := newAGUITestClient()
	c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		c.publish(streaming.EventTypeAssistant, "preset")
		return &agentsvc.QueryOutput{Content: "preset"}, nil
	}
	s := aguiServer(t, c, "principal", nil)
	r := aguiPost(t, s, aguiChatRequest)
	defer r.Body.Close()
	events := decodeAGUISSE(t, r.Body)
	assertAGUITerminal(t, events, "RUN_FINISHED")
	starts, content := 0, ""
	for _, e := range events {
		if e.Type == "TEXT_MESSAGE_START" {
			starts++
		}
		if e.Type == "TEXT_MESSAGE_CONTENT" {
			content += e.Delta
		}
	}
	if starts != 1 || content != "preset" {
		t.Fatal(events)
	}
}
func TestAGUIQueuedReturnWaitsForMatchingTerminal(t *testing.T) {
	c := newAGUITestClient()
	release := make(chan struct{})
	c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		c.publish(streaming.EventTypeTurnQueued, "")
		go func() {
			<-release
			_ = c.bus.Publish(context.Background(), &streaming.Event{Type: streaming.EventTypeTurnFailed, ConversationID: "other", TurnID: "user-message", Error: "wrong conversation"})
			_ = c.bus.Publish(context.Background(), &streaming.Event{Type: streaming.EventTypeTurnCompleted, ConversationID: "thread", TurnID: "different-turn"})
			c.publish(streaming.EventTypeAssistant, "queued answer")
			c.publish(streaming.EventTypeTurnCompleted, "")
		}()
		return &agentsvc.QueryOutput{}, nil
	}
	s := aguiServer(t, c, "principal", nil)
	r := aguiPost(t, s, aguiChatRequest)
	defer r.Body.Close()
	done := make(chan []agui.Event, 1)
	go func() { done <- decodeAGUISSE(t, r.Body) }()
	select {
	case events := <-done:
		t.Fatalf("queued response finished early %+v", events)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	events := <-done
	assertAGUITerminal(t, events, "RUN_FINISHED")
	found := false
	for _, e := range events {
		if e.Name == "agently.queue" {
			found = true
		}
	}
	if !found {
		t.Fatal(events)
	}
}
func TestAGUIFailureCancellationAndQueryError(t *testing.T) {
	for _, mode := range []string{"failed", "cancelled", "query-error"} {
		t.Run(mode, func(t *testing.T) {
			c := newAGUITestClient()
			c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
				switch mode {
				case "failed":
					_ = c.bus.Publish(context.Background(), &streaming.Event{Type: streaming.EventTypeTurnFailed, ConversationID: "thread", TurnID: "user-message", Error: "broken"})
				case "cancelled":
					c.publish(streaming.EventTypeTurnCanceled, "")
				case "query-error":
					return nil, errors.New("query failed")
				}
				return &agentsvc.QueryOutput{}, nil
			}
			s := aguiServer(t, c, "principal", nil)
			r := aguiPost(t, s, aguiChatRequest)
			defer r.Body.Close()
			events := decodeAGUISSE(t, r.Body)
			want := "RUN_ERROR"
			if mode == "cancelled" {
				want = "RUN_FINISHED"
			}
			assertAGUITerminal(t, events, want)
			if mode == "cancelled" && events[len(events)-1].Outcome.Type != "cancelled" {
				t.Fatal(events)
			}
		})
	}
}
func TestAGUIRejectsInvalidInputBeforeQuery(t *testing.T) {
	bodies := []string{`{}`, `{"threadId":"t","runId":"r"}`, strings.Replace(aguiChatRequest, `"hello"`, `[]`, 1), strings.Replace(aguiChatRequest, `"messages":`, `"unexpected":true,"messages":`, 1), strings.TrimSuffix(aguiChatRequest, "}") + `,"tools":[{"name":"tool","description":"desc","parameters":{}}]}`, strings.TrimSuffix(aguiChatRequest, "}") + `,"resume":[]}`, aguiChatRequest + ` {}`, strings.TrimSuffix(aguiChatRequest, "}") + `,"forwardedProps":{"agently":{"version":"2","operation":"chat"}}}`}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			c := newAGUITestClient()
			s := aguiServer(t, c, "principal", nil)
			r := aguiPost(t, s, body)
			r.Body.Close()
			if r.StatusCode != 400 || c.queries.Load() != 0 || c.subscribed.Load() {
				t.Fatalf("status=%d queries=%d subscribed=%v", r.StatusCode, c.queries.Load(), c.subscribed.Load())
			}
		})
	}
}
func TestAGUIAuthorizationAndThreadOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, principal, owner string
		want                   int
	}{{"missing principal", "", "", 401}, {"another owner", "principal", "other", 403}, {"matching owner", "principal", "principal", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			c := newAGUITestClient()
			c.owner = tc.owner
			c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
				return &agentsvc.QueryOutput{}, nil
			}
			s := aguiServer(t, c, tc.principal, &svcauth.Config{Enabled: true})
			r := aguiPost(t, s, aguiChatRequest)
			defer r.Body.Close()
			io.Copy(io.Discard, r.Body)
			if r.StatusCode != tc.want {
				t.Fatalf("status=%d", r.StatusCode)
			}
			if tc.want != 200 && (c.queries.Load() != 0 || c.subscribed.Load()) {
				t.Fatal("unauthorized backend execution")
			}
		})
	}
}
func TestAGUIToolPayloadHydration(t *testing.T) {
	c := newAGUITestClient()
	args, result := []byte(`{"q":"abc"}`), []byte(`{"found":true}`)
	c.payloads = map[string]*conversation.Payload{"args": {InlineBody: &args, Compression: "none"}, "result": {InlineBody: &result, Compression: "none"}}
	c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		_ = c.bus.Publish(context.Background(), &streaming.Event{Type: streaming.EventTypeToolCallCompleted, ConversationID: "thread", TurnID: "user-message", ToolCallID: "call", ToolMessageID: "tool-result", AssistantMessageID: "assistant", ToolName: "search", RequestPayloadID: "args", ResponsePayloadID: "result", Status: "completed"})
		c.publish(streaming.EventTypeTurnCompleted, "")
		return &agentsvc.QueryOutput{}, nil
	}
	s := aguiServer(t, c, "principal", nil)
	r := aguiPost(t, s, aguiChatRequest)
	defer r.Body.Close()
	events := decodeAGUISSE(t, r.Body)
	assertAGUITerminal(t, events, "RUN_FINISHED")
	seenArgs, seenResult := false, false
	for _, e := range events {
		if e.Type == "TOOL_CALL_ARGS" {
			seenArgs = e.Delta == string(args)
		}
		if e.Type == "TOOL_CALL_RESULT" {
			seenResult = e.Content != nil && *e.Content == string(result)
		}
	}
	if !seenArgs || !seenResult {
		t.Fatal(events)
	}
}

func TestAGUIToolMissingPayloadFailsRun(t *testing.T) {
	c := newAGUITestClient()
	c.payloads = map[string]*conversation.Payload{}
	c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		_ = c.bus.Publish(context.Background(), &streaming.Event{Type: streaming.EventTypeToolCallCompleted, ConversationID: "thread", TurnID: "user-message", ToolCallID: "call", ToolMessageID: "result", ResponsePayloadID: "missing", Status: "completed"})
		return &agentsvc.QueryOutput{}, nil
	}
	s := aguiServer(t, c, "principal", nil)
	r := aguiPost(t, s, aguiChatRequest)
	defer r.Body.Close()
	events := decodeAGUISSE(t, r.Body)
	assertAGUITerminal(t, events, "RUN_ERROR")
	if events[len(events)-1].Code != "PAYLOAD_UNAVAILABLE" {
		t.Fatal(events)
	}
}
func TestAGUIServerOwnedHistoryQueriesOnlyNewestMessage(t *testing.T) {
	c := newAGUITestClient()
	observed := make(chan *agentsvc.QueryInput, 1)
	c.query = func(_ context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		observed <- in
		return &agentsvc.QueryOutput{}, nil
	}
	s := aguiServer(t, c, "principal", nil)
	body := `{"threadId":"thread","runId":"external-run","messages":[{"id":"earlier-user","role":"user","content":"old question"},{"id":"earlier-assistant","role":"assistant","content":"old answer"},{"id":"user-message","role":"user","content":"new question"}]}`
	r := aguiPost(t, s, body)
	defer r.Body.Close()
	events := decodeAGUISSE(t, r.Body)
	assertAGUITerminal(t, events, "RUN_FINISHED")
	in := <-observed
	if in.Query != "new question" || in.MessageID != "user-message" {
		t.Fatal(in)
	}
}
func TestAGUICapabilitiesOperationDoesNotExecute(t *testing.T) {
	c := newAGUITestClient()
	s := aguiServer(t, c, "principal", nil)
	body := `{"threadId":"thread","runId":"caps","messages":[],"forwardedProps":{"agently":{"version":"1","operation":"capabilities"}}}`
	r := aguiPost(t, s, body)
	defer r.Body.Close()
	events := decodeAGUISSE(t, r.Body)
	assertAGUITerminal(t, events, "RUN_FINISHED")
	if len(events) != 3 || events[1].Name != "agently.capabilities" || c.queries.Load() != 0 || c.subscribed.Load() {
		t.Fatal(events)
	}
}

func TestAGUIDisconnectRetainsActiveReservationUntilQueryReturns(t *testing.T) {
	c := newAGUITestClient()
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		close(entered)
		<-release
		defer close(returned)
		return &agentsvc.QueryOutput{}, nil
	}
	h := handleAGUIRun(c, &svcauth.Config{Enabled: true})
	ctx, cancel := context.WithCancel(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "principal"}))
	defer cancel()
	first := httptest.NewRequest(http.MethodPost, "/v1/ag-ui/run", strings.NewReader(aguiChatRequest)).WithContext(ctx)
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); h(httptest.NewRecorder(), first) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("query not entered")
	}
	cancel()
	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("observation did not detach")
	}
	second := httptest.NewRequest(http.MethodPost, "/v1/ag-ui/run", strings.NewReader(aguiChatRequest)).WithContext(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "principal"}))
	w := httptest.NewRecorder()
	h(w, second)
	close(release)
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("query did not return")
	}
	if w.Code != http.StatusConflict || c.queries.Load() != 1 {
		t.Fatalf("duplicate admitted after disconnect: status=%d queryCount=%d body=%s", w.Code, c.queries.Load(), w.Body.String())
	}
}
func TestAGUIToolTerminalLooksUpPersistedRequestPayload(t *testing.T) {
	c := newAGUITestClient()
	c.requestPayloadID = "persisted-args"
	args, result := []byte(`{"names":["AGENTLY_AGUI_FIXTURE_VALUE"]}`), []byte(`{"AGENTLY_AGUI_FIXTURE_VALUE":"fixture-value"}`)
	c.payloads = map[string]*conversation.Payload{"persisted-args": {InlineBody: &args, Compression: "none"}, "result": {InlineBody: &result, Compression: "none"}}
	c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		_ = c.bus.Publish(context.Background(), &streaming.Event{Type: streaming.EventTypeToolCallCompleted, ConversationID: "thread", TurnID: "user-message", ToolCallID: "call", ToolMessageID: "tool-result", AssistantMessageID: "assistant", ToolName: "system/os:getEnv", ResponsePayloadID: "result", Status: "completed"})
		c.publish(streaming.EventTypeTurnCompleted, "")
		return &agentsvc.QueryOutput{}, nil
	}
	s := aguiServer(t, c, "principal", nil)
	r := aguiPost(t, s, aguiChatRequest)
	defer r.Body.Close()
	events := decodeAGUISSE(t, r.Body)
	assertAGUITerminal(t, events, "RUN_FINISHED")
	seenArgs, seenResult := false, false
	for _, e := range events {
		if e.Type == "TOOL_CALL_ARGS" {
			seenArgs = e.Delta == string(args)
		}
		if e.Type == "TOOL_CALL_RESULT" {
			seenResult = e.Content != nil && *e.Content == string(result)
		}
	}
	if !seenArgs || !seenResult {
		t.Fatal(events)
	}
}

func TestAGUIReplayChecksPersistedTurnIdentity(t *testing.T) {
	c := newAGUITestClient()
	c.owner = "principal"
	c.messagePage = &MessagePage{Rows: []*messagemodel.MessageRowsView{{Id: "persisted-user-message"}}}
	s := aguiServer(t, c, "principal", nil)
	r := aguiPost(t, s, aguiChatRequest)
	defer r.Body.Close()
	io.Copy(io.Discard, r.Body)
	if r.StatusCode != http.StatusConflict || c.queries.Load() != 0 || c.subscribed.Load() {
		t.Fatalf("replay status=%d queries=%d", r.StatusCode, c.queries.Load())
	}
	if c.messagesInput == nil || c.messagesInput.TurnID != "user-message" || c.messagesInput.ID != "" || c.messagesInput.ConversationID != "thread" {
		t.Fatalf("wrong replay identity %+v", c.messagesInput)
	}
}
