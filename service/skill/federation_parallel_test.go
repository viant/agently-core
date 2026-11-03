package skill

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

// Independent real MCP HTTP providers prove overlap and the existing bound.
func TestRemoteSkillCatalogsOverlapWithBoundedNativeRequests(t *testing.T) {
	testNativeSkillCatalogReads(t, false)
}

func TestRemoteSkillCatalogStaleActorDiscardsConcurrentResults(t *testing.T) {
	testNativeSkillCatalogReads(t, true)
}

func testNativeSkillCatalogReads(t *testing.T, rejectActor bool) {
	ctx, cancel := context.WithTimeout(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), 5*time.Second)
	defer cancel()
	entered := make(chan string, 6)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var active, maximum atomic.Int32
	var stale atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		var result any
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", name)
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"extensions": map[string]any{schema.SkillsExtension: map[string]any{}}}, "serverInfo": map[string]any{"name": name, "version": "1"}}
		case "skills/list":
			now := active.Add(1)
			for prior := maximum.Load(); now > prior; prior = maximum.Load() {
				if maximum.CompareAndSwap(prior, now) {
					break
				}
			}
			entered <- name
			select {
			case <-release:
			case <-r.Context().Done():
				active.Add(-1)
				return
			}
			active.Add(-1)
			fixture := &skillMCPFixture{body: "---\nname: review\ndescription: Synthetic independent provider\n---\nRead carefully."}
			result = &schema.ListSkillsResult{ResultType: "complete", Skills: []schema.Skill{fixture.entry()}}
		default:
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer func() { once.Do(func() { close(release) }); server.Close() }()
	options := querySkillOptions{}
	names := []string{"a", "b", "c", "d", "e", "f"}
	for _, name := range names {
		options[name] = &cfg.MCPClient{SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}, ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL + "/" + name}}}}
	}
	mgr, err := manager.New(options)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	actor := identity.VerifiedActor{Issuer: "fixture", Subject: "alice", TenantID: "team", AccountID: "account", IdentityRevision: "current", ValidUntil: time.Now().Add(time.Minute)}
	gateway := resources.NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error {
		if stale.Load() {
			return identity.ErrResourceStale
		}
		return nil
	}, "aggregate")
	defer gateway.Close()
	service := &Service{mcpSource: &resources.NativeSkillSource{Gateway: gateway}}
	type response struct {
		entries []remoteEntry
		err     error
	}
	done := make(chan response, 1)
	go func() {
		entries, err := service.remoteEntries(ctx, &agentmdl.Agent{Skills: []string{"*"}}, "")
		done <- response{entries, err}
	}()
	for range 4 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("independent native providers did not overlap before the join")
		}
	}
	require.EqualValues(t, 4, maximum.Load())
	select {
	case extra := <-entered:
		t.Fatalf("provider %s exceeded the existing four-read metadata bound", extra)
	default:
	}
	if rejectActor {
		stale.Store(true)
	}
	once.Do(func() { close(release) })
	select {
	case got := <-done:
		if rejectActor {
			require.ErrorIs(t, got.err, identity.ErrResourceStale)
			require.Empty(t, got.entries)
			return
		}
		require.NoError(t, got.err)
		var order []string
		for _, entry := range got.entries {
			order = append(order, entry.server)
		}
		require.Equal(t, names, order, "completion order must not change source precedence")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.LessOrEqual(t, maximum.Load(), int32(4))
}

type federationOrderSource struct {
	skillSourceFixture
	names          []string
	configurations map[string]*cfg.MCPClient
	optionErrors   map[string]error
	optionCalls    []string
	onOption       func(string)
	onGet          func(context.Context, string) (client.Interface, error)
	mu             sync.Mutex
	getCalls       []string
}

func (s *federationOrderSource) Names(context.Context) ([]string, error) {
	return append([]string(nil), s.names...), nil
}
func (s *federationOrderSource) Options(_ context.Context, name string) (*cfg.MCPClient, error) {
	s.optionCalls = append(s.optionCalls, name)
	if s.onOption != nil {
		s.onOption(name)
	}
	return s.configurations[name], s.optionErrors[name]
}
func (s *federationOrderSource) Get(ctx context.Context, _, name string) (client.Interface, error) {
	s.mu.Lock()
	s.getCalls = append(s.getCalls, name)
	s.mu.Unlock()
	if s.onGet != nil {
		return s.onGet(ctx, name)
	}
	return s.f, nil
}
func federationFixture(names ...string) *federationOrderSource {
	source := &federationOrderSource{names: names, configurations: map[string]*cfg.MCPClient{}, optionErrors: map[string]error{}}
	source.f = &skillMCPFixture{body: "---\nname: review\ndescription: Synthetic ordered provider\n---\nRead carefully."}
	for _, name := range names {
		source.configurations[name] = &cfg.MCPClient{SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}
	}
	return source
}
func federationActor() context.Context {
	return authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
}

func TestRemoteSkillCatalogPreflightAndExplicitSelectionRemainOrdered(t *testing.T) {
	source := federationFixture("c", "a", "b")
	service := &Service{mcpSource: source}
	entries, err := service.remoteEntries(federationActor(), &agentmdl.Agent{Skills: []string{"*"}}, "b")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, source.optionCalls)
	require.Equal(t, []string{"b"}, source.getCalls)
	require.Len(t, entries, 1)
}
func TestRemoteSkillCatalogFailuresAndDiagnosticsRetainSourceOrder(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "automatic"}[automatic], func(t *testing.T) {
			source := federationFixture("b", "a")
			first, second := errors.New("synthetic first failure"), errors.New("synthetic second failure")
			completedSecond := make(chan struct{})
			source.onGet = func(ctx context.Context, name string) (client.Interface, error) {
				if name == "b" {
					close(completedSecond)
					return nil, second
				}
				select {
				case <-completedSecond:
					return nil, first
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if automatic {
				for _, name := range source.names {
					source.configurations[name] = &cfg.MCPClient{ResolvedIdentity: &cfg.ResolvedServiceIdentity{ResourceURI: "mcp://example/" + name}}
				}
			}
			ctx, cancel := context.WithTimeout(federationActor(), time.Second)
			defer cancel()
			ctx, diagnostics := withDiscoveryDiagnostics(ctx)
			_, err := (&Service{mcpSource: source}).remoteEntries(ctx, &agentmdl.Agent{Skills: []string{"*"}}, "")
			if !automatic {
				require.ErrorIs(t, err, first)
				return
			}
			require.NoError(t, err)
			require.Len(t, diagnostics.messages, 2)
			firstID := ordinarySkillDiscovery(source.configurations["a"]).SkillDiscovery.CatalogID
			secondID := ordinarySkillDiscovery(source.configurations["b"]).SkillDiscovery.CatalogID
			require.Contains(t, diagnostics.messages[0], firstID)
			require.Contains(t, diagnostics.messages[1], secondID)
		})
	}
}
func TestRemoteSkillCatalogPreflightErrorPreservesEarlierProviderFailure(t *testing.T) {
	source := federationFixture("b", "a", "c")
	first, later := errors.New("synthetic earlier provider failure"), errors.New("synthetic later configuration failure")
	source.optionErrors["b"] = later
	source.onGet = func(context.Context, string) (client.Interface, error) { return nil, first }
	_, err := (&Service{mcpSource: source}).remoteEntries(federationActor(), &agentmdl.Agent{Skills: []string{"*"}}, "")
	require.ErrorIs(t, err, first)
	require.Equal(t, []string{"a", "b"}, source.optionCalls)
	require.Equal(t, []string{"a"}, source.getCalls)
}
func TestRemoteSkillCatalogCancellationBeforeAndDuringPreflight(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before", false: "during"}[before], func(t *testing.T) {
			source := federationFixture("b", "a")
			ctx, cancel := context.WithCancel(federationActor())
			defer cancel()
			if before {
				cancel()
			} else {
				source.onOption = func(string) { cancel() }
			}
			_, err := (&Service{mcpSource: source}).remoteEntries(ctx, &agentmdl.Agent{Skills: []string{"*"}}, "")
			require.ErrorIs(t, err, context.Canceled)
			require.Empty(t, source.getCalls)
			if !before {
				require.Equal(t, []string{"a"}, source.optionCalls)
			}
		})
	}
}
