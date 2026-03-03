package sdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
