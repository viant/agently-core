package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/viant/agently-core/app/store/conversation"
	toolpolicy "github.com/viant/agently-core/protocol/tool"
	agentsvc "github.com/viant/agently-core/service/agent"
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

type spyTranscriptClient struct {
	*HTTPClient
	gotInput *GetTranscriptInput
}

func (s *spyTranscriptClient) GetTranscript(_ context.Context, input *GetTranscriptInput) (*TranscriptOutput, error) {
	s.gotInput = input
	return &TranscriptOutput{}, nil
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
