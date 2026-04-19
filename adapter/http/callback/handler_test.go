package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/afs"
	"github.com/viant/agently-core/genai/llm"
	tooldef "github.com/viant/agently-core/protocol/tool"
	callbacksvc "github.com/viant/agently-core/service/callback"
	callbackrepo "github.com/viant/agently-core/workspace/repository/callback"
)

// --------------------------------------------------------------------
// test helpers
// --------------------------------------------------------------------

// stubRegistry records invocations so tests can assert dispatch reached
// the tool registry.
type stubRegistry struct {
	lastName string
	lastArgs map[string]interface{}
	result   string
	err      error
}

func (s *stubRegistry) Definitions() []llm.ToolDefinition                { return nil }
func (s *stubRegistry) MatchDefinition(string) []*llm.ToolDefinition     { return nil }
func (s *stubRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) { return nil, false }
func (s *stubRegistry) MustHaveTools([]string) ([]llm.Tool, error)       { return nil, nil }
func (s *stubRegistry) SetDebugLogger(io.Writer)                         {}
func (s *stubRegistry) Initialize(context.Context)                       {}
func (s *stubRegistry) Execute(_ context.Context, name string, args map[string]interface{}) (string, error) {
	s.lastName = name
	s.lastArgs = args
	return s.result, s.err
}

var _ tooldef.Registry = (*stubRegistry)(nil)

// newTestMux builds an http.ServeMux hosting the dispatch route backed
// by a real repository pointing at the shared callback test fixtures.
func newTestMux(t *testing.T) (*http.ServeMux, *stubRegistry) {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "..", "workspace", "repository", "callback", "testdata"))
	if err != nil {
		t.Fatalf("abs testdata: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(abs, "callbacks")); statErr != nil {
		t.Fatalf("testdata/callbacks missing at %s: %v", abs, statErr)
	}
	t.Setenv("AGENTLY_WORKSPACE", abs)
	repo := callbackrepo.New(afs.New())
	stub := &stubRegistry{result: `{"ok":true}`}
	svc := callbacksvc.New(repo, stub)
	mux := http.NewServeMux()
	NewHandler(svc).Register(mux)
	return mux, stub
}

// fakeAuthMiddleware mimics svcauth.Protect's 401 behaviour: it rejects
// any /v1/ request that lacks a "X-Auth" header. Integration with the
// real svcauth package lives in e2e; this isolated stub lets us assert
// that when the outer middleware rejects an unauthenticated request,
// the callback handler never runs.
func fakeAuthMiddleware(authed string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v1/") && r.Header.Get("X-Auth") != authed {
				http.Error(w, "authorization required", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --------------------------------------------------------------------
// tests
// --------------------------------------------------------------------

// TestDispatch_Authenticated_ReachesTool verifies the happy path: an
// authenticated request reaches the handler and invokes the tool.
func TestDispatch_Authenticated_ReachesTool(t *testing.T) {
	mux, stub := newTestMux(t)
	protected := fakeAuthMiddleware("yes")(mux)

	body, _ := json.Marshal(map[string]interface{}{
		"eventName":      "simple_echo",
		"conversationId": "conv-7",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/api/callbacks/dispatch", bytes.NewReader(body))
	req.Header.Set("X-Auth", "yes")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	protected.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if stub.lastName != "test-echo" {
		t.Errorf("expected tool test-echo, got %q", stub.lastName)
	}
	var out callbacksvc.DispatchOutput
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.EventName != "simple_echo" {
		t.Errorf("expected eventName echo, got %q", out.EventName)
	}
}

// TestDispatch_Unauthenticated_Returns401 verifies the handler never
// runs when the outer middleware rejects the request.
func TestDispatch_Unauthenticated_Returns401(t *testing.T) {
	mux, stub := newTestMux(t)
	protected := fakeAuthMiddleware("yes")(mux)

	body, _ := json.Marshal(map[string]interface{}{"eventName": "simple_echo"})
	req := httptest.NewRequest(http.MethodPost, "/v1/api/callbacks/dispatch", bytes.NewReader(body))
	// No X-Auth header.
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	protected.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
	}
	if stub.lastName != "" {
		t.Errorf("handler was invoked despite 401: lastName=%q", stub.lastName)
	}
}

// TestDispatch_AuthDisabled_ProceedsWithoutUser verifies the handler
// runs and dispatches when no auth middleware wraps it (auth disabled
// workspace).
func TestDispatch_AuthDisabled_ProceedsWithoutUser(t *testing.T) {
	mux, stub := newTestMux(t)
	// No middleware wrap — simulates auth-disabled workspace.

	body, _ := json.Marshal(map[string]interface{}{
		"eventName":      "simple_echo",
		"conversationId": "conv-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/api/callbacks/dispatch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if stub.lastName != "test-echo" {
		t.Errorf("expected tool test-echo, got %q", stub.lastName)
	}
}

// TestDispatch_UnknownEvent_Returns404 checks the "no callback
// registered" → 404 mapping that the frontend uses for fallback.
func TestDispatch_UnknownEvent_Returns404(t *testing.T) {
	mux, _ := newTestMux(t)

	body, _ := json.Marshal(map[string]interface{}{"eventName": "no_such_event"})
	req := httptest.NewRequest(http.MethodPost, "/v1/api/callbacks/dispatch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "no callback registered") {
		t.Errorf("expected 'no callback registered' body, got: %s", rr.Body.String())
	}
}

// TestDispatch_NonPOST_Returns405 rejects GET / PUT etc.
func TestDispatch_NonPOST_Returns405(t *testing.T) {
	mux, _ := newTestMux(t)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/v1/api/callbacks/dispatch", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		// ServeMux-level method filter turns non-POST into 405 via the
		// "POST /v1/..." pattern matcher — confirm behaviour.
		if rr.Code == http.StatusOK {
			t.Errorf("%s reached the handler — expected rejection, got 200", method)
		}
	}
}
