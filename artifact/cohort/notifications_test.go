package cohort

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/viant/agently-core/protocol/primitive/observe"
	"github.com/viant/forge/backend/artifactcohort"
	"github.com/viant/jsonrpc"
	protoclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
)

type sidecarNotificationProbe struct {
	protoclient.Handler
	count atomic.Int32
}

func (*sidecarNotificationProbe) Implements(string) bool                           { return false }
func (*sidecarNotificationProbe) Init(context.Context, *schema.ClientCapabilities) {}
func (p *sidecarNotificationProbe) OnNotification(_ context.Context, event *jsonrpc.Notification) {
	if event != nil && event.Method == artifactcohort.NotificationListChanged {
		p.count.Add(1)
	}
}

func TestArtifactNotificationSidecarReadmitsAfterProducerSessionReset(t *testing.T) {
	var generation, discovers, getStreams, invalidAuth atomic.Int32
	notify := make(chan struct{}, 1)
	generation.Store(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get("Origin") != "http://localhost" || r.Header.Get("MCP-Protocol-Version") != schema.LegacyProtocolVersion {
			invalidAuth.Add(1)
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		current := fmt.Sprintf("session-%d", generation.Load())
		if r.Method == http.MethodGet {
			if r.Header.Get("Mcp-Session-Id") != current {
				http.Error(w, "session not found", http.StatusNotFound)
				return
			}
			getStreams.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(": connected\n\n"))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-notify:
				_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/resources/list_changed\",\"params\":{}}\n\n"))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || r.Header.Get(schema.HeaderMethod) != request.Method {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if request.Method != schema.MethodInitialize && r.Header.Get("Mcp-Session-Id") != current {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", current)
		var result any
		switch request.Method {
		case schema.MethodInitialize:
			result = map[string]any{"protocolVersion": schema.LegacyProtocolVersion, "serverInfo": map[string]any{"name": "fixture", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}
		case schema.MethodToolsCall:
			if request.Params.Name != artifactcohort.MethodDiscover || r.Header.Get(schema.HeaderName) != artifactcohort.MethodDiscover || request.Params.Arguments["knownHash"] != "fixture-hash" {
				http.Error(w, "wrong tool", http.StatusBadRequest)
				return
			}
			discovers.Add(1)
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": `{"unchanged":true,"hash":"fixture-hash"}`}}, "isError": false}
		case schema.MethodPing:
			result = map[string]any{}
		default:
			http.Error(w, "unsupported", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()
	probe := &sidecarNotificationProbe{}
	handler := artifactcohort.NewNotificationHandler(context.Background(), probe)
	defer handler.Close()
	var timingMu sync.Mutex
	var timings []observe.Event
	traceCtx := observe.WithObserver(context.Background(), observe.ObserverFunc(func(event observe.Event) {
		timingMu.Lock()
		timings = append(timings, event)
		timingMu.Unlock()
	}))
	stop := startArtifactCohortNotificationsWithInterval(traceCtx, server.URL+"/mcp", "http://localhost", func(context.Context) (string, error) { return "fixture-token", nil }, func() string { return "fixture-hash" }, handler, 25*time.Millisecond)
	defer stop()
	waitFor := func(want int32) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for discovers.Load() < want {
			select {
			case <-deadline:
				t.Fatalf("discover after session reset: want %d, got %d", want, discovers.Load())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	waitFor(1)
	notify <- struct{}{}
	deadline := time.After(3 * time.Second)
	for probe.count.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("stateful SSE resource notification did not reach host handler")
		case <-time.After(10 * time.Millisecond):
		}
	}
	generation.Store(2)
	waitFor(2)
	observedDeadline := time.Now().Add(time.Second)
	for {
		timingMu.Lock()
		gotTimings := append([]observe.Event(nil), timings...)
		timingMu.Unlock()
		var opens, subscriptions int
		for _, event := range gotTimings {
			switch event.Phase {
			case "artifact.notification.session.open":
				if event.Outcome == observe.OK && event.Duration >= 0 {
					opens++
				}
			case "artifact.notification.subscribe":
				if event.Outcome == observe.OK && event.Cache == observe.CacheHit && event.Duration >= 0 {
					subscriptions++
				}
			}
		}
		if opens >= 2 && subscriptions >= 2 {
			break
		}
		if time.Now().After(observedDeadline) {
			t.Fatalf("missing successful compact sidecar setup observations: opens=%d subscriptions=%d", opens, subscriptions)
		}
		time.Sleep(time.Millisecond)
	}
	if invalidAuth.Load() != 0 || getStreams.Load() < 1 {
		t.Fatalf("invalid auth or no authenticated stream: auth=%d streams=%d", invalidAuth.Load(), getStreams.Load())
	}
}
