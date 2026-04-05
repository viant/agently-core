package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	toolpolicy "github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/scheduler"
)

func TestHTTPClient_Query(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent/query" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(&agentsvc.QueryOutput{ConversationID: "c1", Content: "ok"})
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	out, err := c.Query(context.Background(), &agentsvc.QueryInput{Query: "hi"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if out == nil || out.Content != "ok" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_GetConversation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(&conversation.Conversation{Id: "c1"})
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	out, err := c.GetConversation(context.Background(), "c1")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if out == nil || out.Id != "c1" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_GetPayloads_FallbacksPerID(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/api/payload/p1":
			_ = json.NewEncoder(w).Encode(&conversation.Payload{Id: "p1"})
		case "/v1/api/payload/p2":
			_ = json.NewEncoder(w).Encode(&conversation.Payload{Id: "p2"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	out, err := c.GetPayloads(context.Background(), []string{"p1", "p2", "missing", "p1", ""})
	if err != nil {
		t.Fatalf("GetPayloads: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("unexpected payload count: %d", len(out))
	}
	if out["p1"] == nil || out["p1"].Id != "p1" {
		t.Fatalf("missing p1: %#v", out["p1"])
	}
	if out["p2"] == nil || out["p2"].Id != "p2" {
		t.Fatalf("missing p2: %#v", out["p2"])
	}
}

func TestHTTPClient_UpdateConversation(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody struct {
		Visibility string `json:"visibility"`
		Shareable  *bool  `json:"shareable"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err = json.Unmarshal(data, &gotBody); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(&conversation.Conversation{Id: "c1"})
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	shareable := true
	out, err := c.UpdateConversation(context.Background(), &UpdateConversationInput{
		ConversationID: "c1",
		Visibility:     "public",
		Shareable:      &shareable,
	})
	if err != nil {
		t.Fatalf("UpdateConversation: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotPath != "/v1/conversations/c1" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotBody.Visibility != "public" {
		t.Fatalf("unexpected visibility: %q", gotBody.Visibility)
	}
	if gotBody.Shareable == nil || *gotBody.Shareable != true {
		t.Fatalf("unexpected shareable: %#v", gotBody.Shareable)
	}
	if out == nil || out.Id != "c1" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_ListConversations_QueryParams(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(&ConversationPage{Rows: nil})
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	_, err = c.ListConversations(context.Background(), &ListConversationsInput{
		AgentID:          "agent-1",
		ParentID:         "parent-conv",
		ParentTurnID:     "parent-turn",
		ExcludeScheduled: true,
		Query:            "favorite color",
		Status:           "active",
		Page: &PageInput{
			Limit:     5,
			Cursor:    "c-2",
			Direction: DirectionAfter,
		},
	})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if gotPath != "/v1/conversations" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotQuery.Get("agentId") != "agent-1" || gotQuery.Get("parentId") != "parent-conv" || gotQuery.Get("parentTurnId") != "parent-turn" || gotQuery.Get("q") != "favorite color" || gotQuery.Get("status") != "active" {
		t.Fatalf("unexpected query values: %#v", gotQuery)
	}
	if gotQuery.Get("excludeScheduled") != "true" {
		t.Fatalf("unexpected excludeScheduled query value: %#v", gotQuery)
	}
	if gotQuery.Get("limit") != "5" || gotQuery.Get("cursor") != "c-2" || gotQuery.Get("direction") != "after" {
		t.Fatalf("unexpected page query values: %#v", gotQuery)
	}
}

func TestHTTPClient_ListLinkedConversations_QueryParams(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(&LinkedConversationPage{Rows: nil})
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	_, err = c.ListLinkedConversations(context.Background(), &ListLinkedConversationsInput{
		ParentConversationID: "parent-conv",
		ParentTurnID:         "parent-turn",
		Page: &PageInput{
			Limit:     3,
			Cursor:    "c-9",
			Direction: DirectionBefore,
		},
	})
	if err != nil {
		t.Fatalf("ListLinkedConversations: %v", err)
	}
	if gotPath != "/v1/conversations/linked" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotQuery.Get("parentConversationId") != "parent-conv" || gotQuery.Get("parentTurnId") != "parent-turn" {
		t.Fatalf("unexpected query values: %#v", gotQuery)
	}
	if gotQuery.Get("limit") != "3" || gotQuery.Get("cursor") != "c-9" || gotQuery.Get("direction") != "before" {
		t.Fatalf("unexpected page query values: %#v", gotQuery)
	}
}

func TestHTTPClient_GetTranscript_QueryParamsAndSelectors(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(&ConversationState{})
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	_, err = c.GetTranscript(context.Background(), &GetTranscriptInput{
		ConversationID:    "c1",
		Since:             "m1",
		IncludeModelCalls: true,
		IncludeToolCalls:  true,
	}, WithTranscriptMessageSelector(&QuerySelector{
		Limit:   1,
		Offset:  2,
		OrderBy: "created_at ASC,id ASC",
	}))
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if gotPath != "/v1/conversations/c1/transcript" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotQuery.Get("since") != "m1" || gotQuery.Get("includeModelCalls") != "true" || gotQuery.Get("includeToolCalls") != "true" {
		t.Fatalf("unexpected query values: %#v", gotQuery)
	}
	rawSelectors := gotQuery.Get("selectors")
	if rawSelectors == "" {
		t.Fatalf("expected selectors query param")
	}
	var selectors map[string]*QuerySelector
	if err := json.Unmarshal([]byte(rawSelectors), &selectors); err != nil {
		t.Fatalf("unmarshal selectors: %v", err)
	}
	if selectors["Message"] == nil {
		t.Fatalf("expected Message selector")
	}
	if selectors["Message"].Limit != 1 || selectors["Message"].Offset != 2 || selectors["Message"].OrderBy != "created_at ASC,id ASC" {
		t.Fatalf("unexpected selector: %#v", selectors["Message"])
	}
}

func TestHTTPClient_StreamEvents_DecodesJSONPayloadType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stream" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: model_started\n")
		_, _ = io.WriteString(w, "data:{\"type\":\"model_started\",\"conversationId\":\"c1\",\"streamId\":\"c1\",\"turnId\":\"t1\",\"status\":\"thinking\"}\n\n")
		_, _ = io.WriteString(w, "data:{\"type\":\"assistant_final\",\"conversationId\":\"c1\",\"streamId\":\"c1\",\"turnId\":\"t1\",\"content\":\"done\",\"finalResponse\":true}\n\n")
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	sub, err := c.StreamEvents(context.Background(), &StreamEventsInput{ConversationID: "c1"})
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}
	defer sub.Close()

	var events []*streaming.Event
	timeout := time.After(2 * time.Second)
	for len(events) < 2 {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				t.Fatalf("subscription closed early after %d events", len(events))
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatalf("timed out waiting for events, got %d", len(events))
		}
	}

	if got := events[0].Type; got != streaming.EventTypeModelStarted {
		t.Fatalf("unexpected first event type: %q", got)
	}
	if got := events[0].TurnID; got != "t1" {
		t.Fatalf("unexpected first event turn: %q", got)
	}
	if got := events[1].Type; got != streaming.EventTypeAssistantFinal {
		t.Fatalf("unexpected second event type: %q", got)
	}
	if got := events[1].Content; got != "done" {
		t.Fatalf("unexpected second event content: %q", got)
	}
	if !events[1].FinalResponse {
		t.Fatalf("expected assistant_final finalResponse=true")
	}
}

func TestHandler_Healthz(t *testing.T) {
	handler := NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("unexpected health response: %#v", body)
	}
}

type spyQueryClient struct {
	*HTTPClient
	gotInput *agentsvc.QueryInput
}

func (s *spyQueryClient) Query(_ context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
	s.gotInput = input
	return &agentsvc.QueryOutput{ConversationID: "c1", Content: "ok"}, nil
}

func TestHandler_Query_AssignsAnonymousUserCookie(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyQueryClient{HTTPClient: base}
	handler := NewHandler(spy)

	body := []byte(`{"query":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput == nil {
		t.Fatalf("expected Query to be called")
	}
	if spy.gotInput.UserId == "" {
		t.Fatalf("expected anonymous user id to be assigned")
	}
	if got := rec.Result().Cookies(); len(got) == 0 || got[0].Name != anonymousUserCookieName {
		t.Fatalf("expected anonymous user cookie, got %#v", got)
	}
}

type spyMessagesClient struct {
	*HTTPClient
	gotInput *GetMessagesInput
}

func (s *spyMessagesClient) GetMessages(_ context.Context, input *GetMessagesInput) (*MessagePage, error) {
	s.gotInput = input
	return &MessagePage{}, nil
}

func TestHandler_GetMessages_ParsesPageParams(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyMessagesClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/messages?conversationId=c1&turnId=t1&roles=user,assistant&types=text,tool&limit=3&cursor=m42&direction=before", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput == nil {
		t.Fatalf("expected GetMessages to be called")
	}
	if spy.gotInput.ConversationID != "c1" || spy.gotInput.TurnID != "t1" {
		t.Fatalf("unexpected base filters: %#v", spy.gotInput)
	}
	if len(spy.gotInput.Roles) != 2 || spy.gotInput.Roles[0] != "user" || spy.gotInput.Roles[1] != "assistant" {
		t.Fatalf("unexpected roles: %#v", spy.gotInput.Roles)
	}
	if len(spy.gotInput.Types) != 2 || spy.gotInput.Types[0] != "text" || spy.gotInput.Types[1] != "tool" {
		t.Fatalf("unexpected types: %#v", spy.gotInput.Types)
	}
	if spy.gotInput.Page == nil {
		t.Fatalf("expected page input to be parsed")
	}
	if spy.gotInput.Page.Limit != 3 || spy.gotInput.Page.Cursor != "m42" || spy.gotInput.Page.Direction != DirectionBefore {
		t.Fatalf("unexpected page input: %#v", spy.gotInput.Page)
	}
}

func TestHandler_GetMessages_InvalidLimit(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyMessagesClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/messages?conversationId=c1&limit=abc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput != nil {
		t.Fatalf("GetMessages should not be called on invalid limit")
	}
}

type spyConversationUpdateClient struct {
	*HTTPClient
	gotInput *UpdateConversationInput
	err      error
}

func (s *spyConversationUpdateClient) UpdateConversation(_ context.Context, input *UpdateConversationInput) (*conversation.Conversation, error) {
	s.gotInput = input
	if s.err != nil {
		return nil, s.err
	}
	return &conversation.Conversation{Id: input.ConversationID}, nil
}

func TestHandler_UpdateConversation_ErrorStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "validation", err: errors.New("conversation ID is required"), wantStatus: http.StatusBadRequest},
		{name: "unsupported", err: errors.New("unsupported visibility"), wantStatus: http.StatusBadRequest},
		{name: "not found", err: errors.New("conversation not found"), wantStatus: http.StatusNotFound},
		{name: "conflict", err: newConflictError("conflict"), wantStatus: http.StatusConflict},
		{name: "internal", err: errors.New("db timeout"), wantStatus: http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, err := NewHTTP("http://127.0.0.1")
			if err != nil {
				t.Fatalf("NewHTTP: %v", err)
			}
			spy := &spyConversationUpdateClient{HTTPClient: base, err: tc.err}
			handler := NewHandler(spy)

			req := httptest.NewRequest(http.MethodPatch, "/v1/conversations/c1", strings.NewReader(`{"title":"x"}`))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

type spyQueuedTurnMutationClient struct {
	*HTTPClient
	cancelConversationID string
	cancelTurnID         string
	moveInput            *MoveQueuedTurnInput
	editInput            *EditQueuedTurnInput
}

func (s *spyQueuedTurnMutationClient) CancelQueuedTurn(_ context.Context, conversationID, turnID string) error {
	s.cancelConversationID = conversationID
	s.cancelTurnID = turnID
	return nil
}

func (s *spyQueuedTurnMutationClient) MoveQueuedTurn(_ context.Context, input *MoveQueuedTurnInput) error {
	s.moveInput = input
	return nil
}

func (s *spyQueuedTurnMutationClient) EditQueuedTurn(_ context.Context, input *EditQueuedTurnInput) error {
	s.editInput = input
	return nil
}

func TestHandler_QueuedTurnMutations_ReturnNoContent(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyQueuedTurnMutationClient{HTTPClient: base}
	handler := NewHandler(spy)

	t.Run("delete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/conversations/c1/turns/t1", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
		}
		if spy.cancelConversationID != "c1" || spy.cancelTurnID != "t1" {
			t.Fatalf("unexpected cancel args: %q %q", spy.cancelConversationID, spy.cancelTurnID)
		}
	})

	t.Run("move", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/conversations/c1/turns/t1/move", strings.NewReader(`{"direction":"up"}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
		}
		if spy.moveInput == nil || spy.moveInput.ConversationID != "c1" || spy.moveInput.TurnID != "t1" || spy.moveInput.Direction != "up" {
			t.Fatalf("unexpected move input: %#v", spy.moveInput)
		}
	})

	t.Run("edit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/v1/conversations/c1/turns/t1", strings.NewReader(`{"content":"edited"}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
		}
		if spy.editInput == nil || spy.editInput.ConversationID != "c1" || spy.editInput.TurnID != "t1" || spy.editInput.Content != "edited" {
			t.Fatalf("unexpected edit input: %#v", spy.editInput)
		}
	})
}

type spyToolResourceClient struct {
	*HTTPClient
	executeName string
	resourceRef *ResourceRef
	saveInput   *SaveResourceInput
}

func (s *spyToolResourceClient) ExecuteTool(_ context.Context, name string, args map[string]interface{}) (string, error) {
	s.executeName = name
	return "ok", nil
}

func (s *spyToolResourceClient) GetResource(_ context.Context, ref *ResourceRef) (*GetResourceOutput, error) {
	s.resourceRef = ref
	return &GetResourceOutput{Kind: ref.Kind, Name: ref.Name, Data: []byte("ok")}, nil
}

func (s *spyToolResourceClient) SaveResource(_ context.Context, input *SaveResourceInput) error {
	s.saveInput = input
	return nil
}

func (s *spyToolResourceClient) DeleteResource(_ context.Context, ref *ResourceRef) error {
	s.resourceRef = ref
	return nil
}

func TestHandler_ExecuteTool_EmptyNameIsBadRequest(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyToolResourceClient{HTTPClient: base}
	handler := handleExecuteTool(spy)

	req := httptest.NewRequest(http.MethodPost, "/v1/tools//execute", strings.NewReader(`{}`))
	req.SetPathValue("name", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if spy.executeName != "" {
		t.Fatalf("ExecuteTool should not be called, got %q", spy.executeName)
	}
}

func TestHandler_ResourceHandlers_EmptyPathValuesAreBadRequest(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyToolResourceClient{HTTPClient: base}

	t.Run("get", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/workspace/resources//", nil)
		req.SetPathValue("kind", "")
		req.SetPathValue("name", "")
		rec := httptest.NewRecorder()
		handleGetResource(spy).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})

	t.Run("save", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/v1/workspace/resources//", strings.NewReader("x"))
		req.SetPathValue("kind", "")
		req.SetPathValue("name", "")
		rec := httptest.NewRecorder()
		handleSaveResource(spy).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})

	t.Run("delete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/workspace/resources//", nil)
		req.SetPathValue("kind", "")
		req.SetPathValue("name", "")
		rec := httptest.NewRecorder()
		handleDeleteResource(spy).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})
}

type spyTranscriptClient struct {
	*HTTPClient
	gotInput   *GetTranscriptInput
	gotOptions []TranscriptOption
}

func (s *spyTranscriptClient) GetTranscript(_ context.Context, input *GetTranscriptInput, options ...TranscriptOption) (*ConversationStateResponse, error) {
	s.gotInput = input
	s.gotOptions = options
	return &ConversationStateResponse{SchemaVersion: "2", Conversation: &ConversationState{}}, nil
}

func (s *spyTranscriptClient) GetPayloads(_ context.Context, ids []string) (map[string]*conversation.Payload, error) {
	return nil, nil
}

func (s *spyTranscriptClient) GetLiveState(_ context.Context, conversationID string, options ...TranscriptOption) (*ConversationStateResponse, error) {
	return &ConversationStateResponse{SchemaVersion: "2", Conversation: &ConversationState{ConversationID: conversationID}}, nil
}

func TestHandler_GetTranscript_AcceptsLegacyIncludeToolCallParam(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyTranscriptClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations/c1/transcript?since=m1&includeModelCall=true&includeToolCall=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput == nil {
		t.Fatalf("expected GetTranscript to be called")
	}
	if spy.gotInput.Since != "m1" || !spy.gotInput.IncludeModelCalls || !spy.gotInput.IncludeToolCalls {
		t.Fatalf("unexpected transcript input: %#v", spy.gotInput)
	}
}

func TestHandler_GetTranscript_ParsesSelectors(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyTranscriptClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations/c1/transcript?selectors="+url.QueryEscape(`{"Message":{"limit":1,"offset":2,"orderBy":"created_at ASC,id ASC"}}`), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if len(spy.gotOptions) != 1 {
		t.Fatalf("expected selector option, got %d", len(spy.gotOptions))
	}
	opts := &transcriptOptions{}
	for _, option := range spy.gotOptions {
		option(opts)
	}
	if opts.selectors["Message"] == nil {
		t.Fatalf("expected Message selector")
	}
	if opts.selectors["Message"].Limit != 1 || opts.selectors["Message"].Offset != 2 || opts.selectors["Message"].OrderBy != "created_at ASC,id ASC" {
		t.Fatalf("unexpected selector: %#v", opts.selectors["Message"])
	}
}

type spyExecuteClient struct {
	*HTTPClient
}

func (s *spyExecuteClient) ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if err := toolpolicy.ValidateExecution(ctx, toolpolicy.FromContext(ctx), name, args); err != nil {
		return "", err
	}
	return "ok", nil
}

func TestHandler_ExecuteToolByName_DefaultBestPathBlocksRisky(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyExecuteClient{HTTPClient: base}
	handler := NewHandler(spy)

	body := []byte(`{"name":"system/exec:execute","args":{"commands":["date"],"workdir":"/tmp"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_ExecuteToolByName_DefaultBestPathAllowsSafe(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyExecuteClient{HTTPClient: base}
	handler := NewHandler(spy)

	body := []byte(`{"name":"system/os:getEnv","args":{"names":["USER"]}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
}

type stubSubscription struct {
	id string
	ch chan *streaming.Event
}

func (s *stubSubscription) ID() string                 { return s.id }
func (s *stubSubscription) C() <-chan *streaming.Event { return s.ch }
func (s *stubSubscription) Close() error               { return nil }

type spyStreamClient struct {
	*HTTPClient
	sub streaming.Subscription
}

func (s *spyStreamClient) StreamEvents(_ context.Context, _ *StreamEventsInput) (streaming.Subscription, error) {
	return s.sub, nil
}

func TestHandler_StreamEvents_EmitsKeepaliveComments(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	sub := &stubSubscription{
		id: "sub-1",
		ch: make(chan *streaming.Event),
	}
	spy := &spyStreamClient{HTTPClient: base, sub: sub}
	handler := NewHandler(spy)

	prevInterval := streamKeepaliveInterval
	streamKeepaliveInterval = 10 * time.Millisecond
	defer func() { streamKeepaliveInterval = prevInterval }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/stream?conversationId=c1", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("stream handler did not exit after context cancellation")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, ": keepalive\n\n") {
		t.Fatalf("expected keepalive comment in SSE stream, got %q", got)
	}
}

func TestHTTPClient_GetSchedule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/api/agently/scheduler/schedule/sched-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"data":   &scheduler.Schedule{ID: "sched-1", Name: "daily-report"},
		})
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	out, err := c.GetSchedule(context.Background(), "sched-1")
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if out == nil || out.ID != "sched-1" || out.Name != "daily-report" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_ListSchedules(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/api/agently/scheduler/" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"data": map[string]interface{}{
				"schedules": []*scheduler.Schedule{
					{ID: "s1", Name: "first"},
					{ID: "s2", Name: "second"},
				},
			},
		})
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	out, err := c.ListSchedules(context.Background())
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if len(out) != 2 || out[0].ID != "s1" || out[1].ID != "s2" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_UpsertSchedules(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody struct {
		Schedules []*scheduler.Schedule `json:"schedules"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err = json.Unmarshal(data, &gotBody); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	err = c.UpsertSchedules(context.Background(), []*scheduler.Schedule{
		{ID: "s1", Name: "first", Enabled: true},
	})
	if err != nil {
		t.Fatalf("UpsertSchedules: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotPath != "/v1/api/agently/scheduler/" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if len(gotBody.Schedules) != 1 || gotBody.Schedules[0].ID != "s1" {
		t.Fatalf("unexpected body: %#v", gotBody)
	}
}

func TestHTTPClient_RunScheduleNow(t *testing.T) {
	var gotMethod string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	err = c.RunScheduleNow(context.Background(), "sched-1")
	if err != nil {
		t.Fatalf("RunScheduleNow: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotPath != "/v1/api/agently/scheduler/run-now/sched-1" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
}

func TestResolveQueryUserID_AuthDisabled_AssignsAnonymousCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", nil)

	got := resolveQueryUserID(rec, req, "", nil)
	if got == "" {
		t.Fatal("expected anonymous user id, got empty")
	}
	if !strings.HasPrefix(got, "anonymous:") {
		t.Fatalf("expected anonymous: prefix, got %q", got)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != anonymousUserCookieName {
		t.Fatalf("expected anonymous cookie, got %#v", cookies)
	}
	if !cookies[0].Secure {
		t.Fatalf("expected anonymous cookie to be Secure")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHTTPClient_DownloadFile_RejectsInformationalStatus(t *testing.T) {
	c, err := NewHTTP("http://example.invalid")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	c.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 199,
			Status:     "199 Informational",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("informational")),
			Request:    req,
		}, nil
	})
	_, err = c.DownloadFile(context.Background(), &DownloadFileInput{
		ConversationID: "conv-1",
		FileID:         "file-1",
	})
	if err == nil {
		t.Fatalf("expected error for informational response")
	}
	if !strings.Contains(err.Error(), "download file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveQueryUserID_AuthEnabled_ReturnsEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", nil)
	cfg := &svcauth.Config{Enabled: true}

	got := resolveQueryUserID(rec, req, "", cfg)
	if got != "" {
		t.Fatalf("expected empty user id when auth enabled, got %q", got)
	}
	if cookies := rec.Result().Cookies(); len(cookies) > 0 {
		t.Fatalf("expected no cookies when auth enabled, got %#v", cookies)
	}
}

func TestResolveQueryUserID_AuthEnabled_UsesContextUser(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", nil)
	ctx := iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: "oauth-user-42"})
	req = req.WithContext(ctx)
	cfg := &svcauth.Config{Enabled: true}

	got := resolveQueryUserID(rec, req, "", cfg)
	if got != "oauth-user-42" {
		t.Fatalf("expected context user, got %q", got)
	}
}

func TestResolveQueryUserID_ExplicitUserAlwaysWins(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", nil)
	ctx := iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: "ctx-user"})
	req = req.WithContext(ctx)
	cfg := &svcauth.Config{Enabled: true}

	got := resolveQueryUserID(rec, req, "explicit-user", cfg)
	if got != "explicit-user" {
		t.Fatalf("expected explicit user, got %q", got)
	}
}

func TestHandler_Query_Returns401_WhenAuthEnabledAndNoUser(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyQueryClient{HTTPClient: base}
	sessions := svcauth.NewManager(time.Hour, nil)
	handler, err := NewHandlerWithContext(
		context.Background(),
		spy,
		WithAuth(&svcauth.Config{Enabled: true, IpHashKey: "test-key", CookieName: "sess"}, sessions),
	)
	if err != nil {
		t.Fatalf("NewHandlerWithContext: %v", err)
	}

	body := []byte(`{"query":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput != nil {
		t.Fatal("expected Query NOT to be called when unauthorized")
	}
}

func TestHandler_Query_Succeeds_WhenAuthEnabledAndUserInContext(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyQueryClient{HTTPClient: base}
	authCfg := &svcauth.Config{
		Enabled:    true,
		IpHashKey:  "test-key",
		CookieName: "sess",
		Local:      &svcauth.Local{Enabled: true},
	}
	sessions := svcauth.NewManager(time.Hour, nil)

	// Create a session so the Protect middleware can find it.
	sess := &svcauth.Session{
		ID:        "test-session",
		Username:  "testuser",
		Subject:   "testuser",
		CreatedAt: time.Now(),
	}
	sessions.Put(context.Background(), sess)

	handler, err := NewHandlerWithContext(
		context.Background(),
		spy,
		WithAuth(authCfg, sessions),
	)
	if err != nil {
		t.Fatalf("NewHandlerWithContext: %v", err)
	}

	body := []byte(`{"query":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "sess", Value: "test-session"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput == nil {
		t.Fatal("expected Query to be called")
	}
	if spy.gotInput.UserId != "testuser" {
		t.Fatalf("expected userId=testuser, got %q", spy.gotInput.UserId)
	}
}
