package cohort

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/viant/agently-core/app/executor"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/forge/backend/artifactcohort"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type metadataRoundTripFunc func(*http.Request) (*http.Response, error)

func (f metadataRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAnonymousMetadataTransportNeverBorrowsIncomingCredentials(t *testing.T) {
	var calls int
	base := metadataRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Fatal("anonymous metadata borrowed incoming credentials")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
	r, err := http.NewRequest(http.MethodGet, "http://localhost/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer incoming-user-token")
	if _, err = (artifactHostBearer{base: base}).RoundTrip(r); err != nil {
		t.Fatal(err)
	}
	if r.Header.Get("Authorization") != "Bearer incoming-user-token" {
		t.Fatal("anonymous metadata mutated caller request")
	}
	for _, token := range []HostToken{func(context.Context) (string, error) { return "", nil }, func(context.Context) (string, error) { return "", errors.New("credential unavailable") }} {
		if _, err = (artifactHostBearer{base: base, token: token}).RoundTrip(r); err == nil {
			t.Fatal("configured authentication fell back to anonymous metadata")
		}
	}
	if calls != 1 {
		t.Fatal("failed configured credentials reached producer")
	}
}

func TestArtifactHostStatelessClientSurvivesProducerSessionReset(t *testing.T) {
	var generation atomic.Int32
	generation.Store(1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get("MCP-Protocol-Version") != schema.LatestProtocolVersion || r.Header.Get("Mcp-Session-Id") != "" {
			http.Error(w, "bad authenticated stateless request", http.StatusForbidden)
			return
		}
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch request.Method {
		case schema.MethodServerDiscover:
			result = map[string]any{"resultType": "complete", "cacheScope": "public", "supportedVersions": []string{schema.LatestProtocolVersion}, "capabilities": map[string]any{"tools": map[string]any{}}, "ttlMs": 0}
		case schema.MethodToolsCall:
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": fmt.Sprintf(`{"generation":%d}`, generation.Load())}}, "isError": false}
		default:
			http.Error(w, "unsupported method "+request.Method, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()
	manager, err := mcpmgr.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := server.URL + "/mcp"
	configured := &mcpcfg.MCPClient{PrimitiveProviderIdentity: "studio", ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: endpoint}}}}
	if err := manager.RegisterLocal(context.Background(), "studio", configured, func(context.Context) (mcpclient.Interface, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	client, notifications, err := NewHostClient(context.Background(), &executor.Runtime{MCPManager: manager}, "studio", "studio", endpoint, "", func(context.Context) (string, error) { return "fixture-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	defer notifications.Close()
	defer func() {
		if closer, ok := client.(interface{ Close() }); ok {
			closer.Close()
		}
	}()
	call := func(want int) {
		result, e := client.CallTool(context.Background(), &schema.CallToolRequestParams{Name: artifactcohort.MethodDiscover, Arguments: map[string]any{}}, mcpclient.WithNoRetry())
		if e != nil || result == nil || result.IsError != nil && *result.IsError {
			t.Fatalf("generation %d call: %v", want, e)
		}
		raw, _ := json.Marshal(result.Content)
		if !strings.Contains(string(raw), fmt.Sprintf(`\"generation\":%d`, want)) {
			t.Fatalf("generation %d not returned", want)
		}
	}
	call(1)
	// A replacement producer starts with no old session table; this server
	// switches generation while the authenticated host client stays open.
	generation.Store(2)
	call(2)
	if requests.Load() < 3 {
		t.Fatalf("missing initialize/calls: %d", requests.Load())
	}
}
