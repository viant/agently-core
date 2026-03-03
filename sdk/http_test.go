package sdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/viant/agently-core/app/store/conversation"
	agentsvc "github.com/viant/agently-core/service/agent"
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

func TestHTTPClient_UpdateConversationVisibility(t *testing.T) {
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
	out, err := c.UpdateConversationVisibility(context.Background(), &UpdateConversationVisibilityInput{
		ConversationID: "c1",
		Visibility:     "public",
		Shareable:      &shareable,
	})
	if err != nil {
		t.Fatalf("UpdateConversationVisibility: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotPath != "/v1/conversations/c1/visibility" {
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
		AgentID: "agent-1",
		Query:   "favorite color",
		Status:  "active",
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
	if gotQuery.Get("agentId") != "agent-1" || gotQuery.Get("q") != "favorite color" || gotQuery.Get("status") != "active" {
		t.Fatalf("unexpected query values: %#v", gotQuery)
	}
	if gotQuery.Get("limit") != "5" || gotQuery.Get("cursor") != "c-2" || gotQuery.Get("direction") != "after" {
		t.Fatalf("unexpected page query values: %#v", gotQuery)
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
