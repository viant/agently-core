package sdk

import (
	"context"
	"encoding/json"
	"errors"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/forge/backend/requestctx"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type streamToolClient struct {
	Client
	execute func(context.Context) (string, error)
}

func (c streamToolClient) ExecuteTool(ctx context.Context, _ string, _ map[string]interface{}) (string, error) {
	return c.execute(ctx)
}
func TestToolExecuteStreamRoutes(t *testing.T) {
	for _, byName := range []bool{false, true} {
		t.Run(map[bool]string{false: "path", true: "body"}[byName], func(t *testing.T) {
			calls := 0
			client := streamToolClient{execute: func(ctx context.Context) (string, error) {
				calls++
				requestctx.EmitToolProgress(ctx, map[string]interface{}{"phase": "started", "completed": 0, "total": 1})
				return `{"sample":true}`, nil
			}}
			r := httptest.NewRequest("POST", "/v1/tools/sample/execute", strings.NewReader(`{"name":"sample","args":{}}`))
			r.SetPathValue("name", "sample")
			r.Header.Set("Accept", "text/event-stream")
			w := httptest.NewRecorder()
			if byName {
				handleExecuteToolByName(client)(w, r)
			} else {
				handleExecuteTool(client)(w, r)
			}
			body := w.Body.String()
			if calls != 1 || w.Header().Get("Content-Type") != "text/event-stream" || !strings.HasPrefix(body, "event: progress\n") || !strings.Contains(body, "event: result\ndata: {\"result\":") {
				t.Fatalf("invalid stream %d %s", calls, body)
			}
		})
	}
}
func TestToolExecuteStreamAuthorizationErrors(t *testing.T) {
	for _, progress := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", nil)
		executeToolStream(w, r, r.Context(), func(ctx context.Context) (string, error) {
			if progress {
				requestctx.EmitToolProgress(ctx, map[string]string{"phase": "started"})
			}
			return "restricted-result", identity.ErrResourceDenied
		}, func(http.ResponseWriter) {})
		if strings.Contains(w.Body.String(), "restricted-result") {
			t.Fatal("denial exposed result")
		}
		if progress {
			if !strings.Contains(w.Body.String(), "event: error\n") || !strings.Contains(w.Body.String(), `"status":403`) {
				t.Fatal(w.Body.String())
			}
		} else if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}
func TestToolExecuteStreamFlushesBeforeExecutionCompletes(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		executeToolStream(w, r, r.Context(), func(ctx context.Context) (string, error) {
			requestctx.EmitToolProgress(ctx, map[string]string{"phase": "started"})
			<-release
			return "done", nil
		}, func(http.ResponseWriter) {})
	}))
	defer server.Close()
	response, err := http.Post(server.URL, "application/json", nil)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	prefix := make([]byte, len("event: progress\n"))
	_, err = io.ReadFull(response.Body, prefix)
	close(release)
	if err != nil || string(prefix) != "event: progress\n" {
		t.Fatalf("not flushed: %s %v", prefix, err)
	}
	_ = response.Body.Close()
}
func TestToolExecuteStreamRetainsJSONAndBoundsMetadata(t *testing.T) {
	calls := 0
	client := streamToolClient{execute: func(ctx context.Context) (string, error) {
		calls++
		requestctx.EmitToolProgress(ctx, strings.Repeat("x", maxToolProgressFrame))
		return "done", nil
	}}
	for _, stream := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/", nil)
		r.SetPathValue("name", "sample")
		if stream {
			r.Header.Set("Accept", "text/event-stream")
		}
		w := httptest.NewRecorder()
		handleExecuteTool(client)(w, r)
		if strings.Contains(w.Body.String(), "event: progress") {
			t.Fatal("oversize metadata exposed")
		}
		if !stream {
			var result map[string]string
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || result["result"] != "done" {
				t.Fatal(w.Body.String())
			}
		}
	}
	if calls != 2 {
		t.Fatal("execution replayed")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	executeToolStream(w, r, r.Context(), func(context.Context) (string, error) { return "", errors.New("synthetic failure") }, func(http.ResponseWriter) {})
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
}

func TestToolExecuteStreamUpdatesRequireExplicitOptIn(t *testing.T) {
	for _, optIn := range []bool{false, true} {
		request := httptest.NewRequest("POST", "/", nil)
		if optIn {
			request.Header.Set("X-Agently-Tool-Updates", "true")
		}
		response := httptest.NewRecorder()
		executeToolStream(response, request, request.Context(), func(ctx context.Context) (string, error) {
			if requestctx.HasToolUpdates(ctx) != optIn {
				t.Error("unexpected update capability")
			}
			if err := requestctx.EmitToolUpdate(ctx, map[string]interface{}{"kind": "sampleReady", "rows": []map[string]int{{"value": 7}}}); err != nil {
				return "", err
			}
			return "complete", nil
		}, func(http.ResponseWriter) {})
		body := response.Body.String()
		if optIn {
			if !strings.Contains(body, "event: update\n") || strings.Index(body, "event: update") > strings.Index(body, "event: result") {
				t.Fatal(body)
			}
		} else if strings.Contains(body, "sampleReady") {
			t.Fatal("unsolicited intermediate data")
		}
		if !strings.Contains(body, "event: result\n") {
			t.Fatal("update replaced terminal result")
		}
	}
}
func TestToolExecuteStreamUpdateFailureIsTerminal(t *testing.T) {
	request := httptest.NewRequest("POST", "/", nil)
	request.Header.Set("X-Agently-Tool-Updates", "true")
	response := httptest.NewRecorder()
	executeToolStream(response, request, request.Context(), func(ctx context.Context) (string, error) {
		requestctx.EmitToolProgress(ctx, map[string]int{"total": 2})
		if err := requestctx.EmitToolUpdate(ctx, map[string]string{"kind": "sampleReady"}); err != nil {
			return "", err
		}
		return "restricted-result", identity.ErrResourceDenied
	}, func(http.ResponseWriter) {})
	body := response.Body.String()
	if !strings.Contains(body, "event: update\n") || !strings.Contains(body, "event: error\n") || strings.Contains(body, "restricted-result") {
		t.Fatal(body)
	}
}
