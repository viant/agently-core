package cohort

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/primitive/observe"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/artifactcohort"
	"github.com/viant/jsonrpc"
	protoclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
)

type sidecarReplicaCaller struct {
	publisher *artifactcohort.Publisher
	blocked   atomic.Bool
	syncs     atomic.Int32
	entered   chan struct{}
}

func (c *sidecarReplicaCaller) Discover(ctx context.Context) (artifactcohort.Manifest, error) {
	return c.DiscoverKnown(ctx, "")
}
func (c *sidecarReplicaCaller) DiscoverKnown(context.Context, string) (artifactcohort.Manifest, error) {
	c.syncs.Add(1)
	if c.blocked.Load() {
		select {
		case c.entered <- struct{}{}:
		default:
		}
		return artifactcohort.Manifest{}, errors.New("synthetic synchronization outage")
	}
	return c.publisher.Snapshot()
}
func (c *sidecarReplicaCaller) Fetch(_ context.Context, input artifactcohort.FetchInput) (*artifactcohort.Artifact, error) {
	return c.publisher.Get(input.Root)
}

type sidecarFacts struct{}

func (sidecarFacts) Resolve(context.Context) (authz.Facts, error) {
	return authz.Facts{Subject: "example-user", Tenant: "example-tenant", Issuer: "verified", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}, nil
}
func sidecarReplica(t *testing.T) (*artifactcohort.Replica, *sidecarReplicaCaller) {
	t.Helper()
	p := artifactcohort.NewPublisher()
	_, _, err := p.Publish(context.Background(), artifactcohort.RootRef{URI: "window://example/main", Kind: "window", Version: 1}, json.RawMessage(`{"window":"synthetic"}`), func(context.Context, json.RawMessage) error { return nil })
	require.NoError(t, err)
	resource := authz.Resource{Kind: "cohort", ID: "readers", Version: "logical", Tenant: "example-tenant"}
	_, err = p.ReplaceCatalog(context.Background(), []authz.Document{{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{artifactcohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}, []artifactcohort.Cohort{{Tenant: "example-tenant", Name: "readers", PolicyResource: resource, Roots: []artifactcohort.RootRef{{URI: "window://example/main", Kind: "window"}}}}, p.Generation())
	require.NoError(t, err)
	caller := &sidecarReplicaCaller{publisher: p, entered: make(chan struct{}, 1)}
	replica, err := artifactcohort.NewReplica(artifactcohort.Config{Enabled: true, Source: "example-publisher", Tenant: "example-tenant", Account: "example-account", Caller: caller, Facts: sidecarFacts{}, SessionKey: func(context.Context) (string, error) { return "verified-session", nil }})
	require.NoError(t, err)
	require.NoError(t, replica.Sync(context.Background()))
	require.NoError(t, replica.Admit(context.Background()))
	return replica, caller
}

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
	var generation, synchronizations, legacyDiscoveries, getStreams, invalidAuth atomic.Int32
	notify := make(chan struct{}, 1)
	drop := make(chan struct{}, 1)
	replica, caller := sidecarReplica(t)
	knownHash := replica.KnownHash()
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
			case <-drop:
			case <-notify:
				_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/resources/list_changed\",\"params\":{}}\n\n"))
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-drop:
				}
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
			if request.Params.Name == artifactcohort.MethodDiscover {
				legacyDiscoveries.Add(1)
			}
			sync, ok := request.Params.Arguments["sync"].(map[string]any)
			if request.Params.Name != "namespaces/get" || r.Header.Get(schema.HeaderName) != "namespaces/get" || request.Params.Arguments["namespace"] != "example" || !ok || sync["knownHash"] != knownHash {
				http.Error(w, "wrong tool", http.StatusBadRequest)
				return
			}
			synchronizations.Add(1)
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": fmt.Sprintf(`{"sync":{"fetchMethod":"artifact/fetch","catalog":{"unchanged":true,"hash":%q}}}`, knownHash)}}, "isError": false}
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
	require.NoError(t, handler.AttachReplica(replica))
	defer handler.Close()
	var timingMu sync.Mutex
	var timings []observe.Event
	traceCtx := observe.WithObserver(context.Background(), observe.ObserverFunc(func(event observe.Event) {
		timingMu.Lock()
		timings = append(timings, event)
		timingMu.Unlock()
	}))
	stop := startArtifactCohortNotificationsWithInterval(traceCtx, server.URL+"/mcp", "http://localhost", "example", func(context.Context) (string, error) { return "fixture-token", nil }, replica.KnownHash, handler, 25*time.Millisecond)
	defer stop()
	waitFor := func(want int32) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for synchronizations.Load() < want {
			select {
			case <-deadline:
				t.Fatalf("namespace synchronization after session reset: want %d, got %d", want, synchronizations.Load())
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
	require.Eventually(t, func() bool {
		_, err := replica.Open(context.Background(), "window://example/main")
		return caller.syncs.Load() >= 2 && err == nil
	}, time.Second, time.Millisecond)
	caller.blocked.Store(true)
	drop <- struct{}{}
	select {
	case <-caller.entered:
	case <-time.After(time.Second):
		t.Fatal("lost SSE stream did not start background reconciliation")
	}
	_, err := replica.Open(context.Background(), "window://example/main")
	require.ErrorIs(t, err, artifactcohort.ErrUnavailable, "stream loss served old local metadata")
	require.Equal(t, knownHash, replica.KnownHash(), "disconnect must retain conditional hash")
	caller.blocked.Store(false)
	require.Eventually(t, func() bool { _, err := replica.Open(context.Background(), "window://example/main"); return err == nil }, 2*time.Second, time.Millisecond)
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
	if legacyDiscoveries.Load() != 0 {
		t.Fatal("namespace notification admission fell back to legacy artifact discovery")
	}
}

func TestStartNotificationsRequiresExplicitNamespaceBeforeNetwork(t *testing.T) {
	handler := artifactcohort.NewNotificationHandler(context.Background(), nil)
	defer handler.Close()
	for _, namespace := range []string{"", "ns://example", " example", "example/other"} {
		stop, err := StartNotifications(context.Background(), "http://127.0.0.1:1/mcp", "", namespace, func(context.Context) (string, error) { t.Fatal("invalid registration used token"); return "", nil }, func() string { return "hash" }, handler)
		require.ErrorIs(t, err, artifactcohort.ErrInvalid)
		require.Nil(t, stop)
	}
}
