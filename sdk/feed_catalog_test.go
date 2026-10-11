package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type feedActorKey struct{}

func feedActorContext(subject string) context.Context {
	return authctx.WithUserInfo(context.WithValue(context.Background(), feedActorKey{}, identity.VerifiedActor{Subject: subject, Issuer: "synthetic", TenantID: "team", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Minute)}), &authctx.UserInfo{Subject: subject})
}
func feedActor(ctx context.Context) (identity.VerifiedActor, error) {
	actor, ok := ctx.Value(feedActorKey{}).(identity.VerifiedActor)
	if !ok {
		return actor, identity.ErrResourceDenied
	}
	return actor, nil
}

type feedPolicy struct {
	actor identity.VerifiedActor
	lease *atomic.Int64
}

func (p feedPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if len(candidates) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	until := p.actor.ValidUntil
	if n := p.lease.Load(); n != 0 && time.Unix(0, n).Before(until) {
		until = time.Unix(0, n)
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "actor:" + p.actor.Subject, ValidUntil: until}, nil
}

type countedFeedClient struct {
	mcpclient.Interface
	mu          sync.Mutex
	gets, lists int
	ordinary    bool
}

func (c *countedFeedClient) CallTool(ctx context.Context, input *schema.CallToolRequestParams, opts ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	c.mu.Lock()
	if input.Name == "toolfeeds/get" {
		c.gets++
	}
	if input.Name == "toolfeeds/list" {
		c.lists++
	}
	c.mu.Unlock()
	return c.Interface.CallTool(ctx, input, opts...)
}
func (c *countedFeedClient) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets, c.lists
}
func (c *countedFeedClient) ListTools(ctx context.Context, in *string, opts ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	got, err := c.Interface.ListTools(ctx, in, opts...)
	if err != nil || !c.ordinary {
		return got, err
	}
	copy := *got
	copy.Tools = nil
	for _, tool := range got.Tools {
		if tool.Name == "toolfeeds/list" || tool.Name == "toolfeeds/get" || tool.Name == "namespaces/list" || tool.Name == "namespaces/get" {
			copy.Tools = append(copy.Tools, tool)
		}
	}
	return &copy, nil
}
func feedGatewayFixture(t *testing.T, n int, ordinary bool, collision ...bool) (*backendClient, *countedFeedClient, *atomic.Bool, *atomic.Int64, string, *resources.LocalProvider) {
	t.Helper()
	root := t.TempDir()
	var dirs []resources.ExtensionDirectory
	subjects := []string{"alice", "bob"}
	if len(collision) > 0 && collision[0] {
		subjects = append(subjects, "team")
	}
	for _, subject := range subjects {
		directory := "feeds/" + subject
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0700))
		dirs = append(dirs, resources.ToolfeedDirectory("user-"+subject, directory))
		for i := 0; i < n; i++ {
			body := fmt.Sprintf("id: feed-%02d\ntitle: private-%s-%02d\nmatch: {service: synthetic, method: action%02d}\nactivation: {scope: turn}\ndataSource:\n  output: {source: output, exposeAs: output}\n  rows: {dataSourceRef: output, selectors: {data: data}, root: true}\nui: {title: private-%s, controls: {draftOnly: true, identity: 9007199254740993}}\n", i, subject, i, i, subject)
			if len(collision) > 1 && collision[1] {
				body = strings.ReplaceAll(body, "identity: 9007199254740993", "identity: '9007199254740993'")
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, directory, fmt.Sprintf("feed-%02d.yaml", i)), []byte(body), 0600))
		}
	}
	denied := new(atomic.Bool)
	lease := new(atomic.Int64)
	verify := func(_ context.Context, a identity.VerifiedActor) error {
		if denied.Load() || a.Subject == "" {
			return identity.ErrResourceDenied
		}
		return nil
	}
	actorSource := feedActor
	if len(collision) > 1 && collision[1] {
		public, _ := feedActor(feedActorContext("alice"))
		actorSource = func(context.Context) (identity.VerifiedActor, error) { return public, nil }
	}
	provider, err := resources.NewInternalProvider(context.Background(), root, dirs, func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return feedPolicy{a, lease}, nil
	}, resources.LocalConfig{Actor: actorSource, Verify: verify, Authorize: func(_ context.Context, a identity.VerifiedActor, u identity.ResourceURI, _ string) error {
		if u.Namespace != "user-"+a.Subject && u.Namespace != "user-team" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Validators: map[string]resources.LocalResourceValidator{primitive.ToolfeedKind: resources.ValidateToolfeedDefinition}})
	require.NoError(t, err)
	local, err := resources.NewLocalMCPClient(provider)
	require.NoError(t, err)
	client := &countedFeedClient{Interface: local, ordinary: ordinary}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(context.Background(), "native", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider.ProviderIdentity()}, func(context.Context) (mcpclient.Interface, error) { return client, nil }))
	g := resources.NewGateway(mgr, feedActor, verify, "host")
	t.Cleanup(func() { g.Close(); mgr.CloseConversation("") })
	return &backendClient{primitiveProviders: g}, client, denied, lease, root, provider
}
func TestFeedGatewayNativeTwentyDefinitionsWarmCountsAndActorIsolation(t *testing.T) {
	c, transport, denied, _, _, _ := feedGatewayFixture(t, 20, false)
	constructed, err := newBackendFromRuntime(&executor.Runtime{PrimitiveProviders: c.primitiveProviders, Agent: &agentsvc.Service{}, Conversation: newPayloadOnlyConversationClient(nil)})
	require.NoError(t, err)
	require.Nil(t, constructed.feeds)
	require.Same(t, c.primitiveProviders, constructed.primitiveProviders)
	alice, bob := feedActorContext("alice"), feedActorContext("bob")
	tools, err := transport.ListTools(context.Background(), nil)
	require.NoError(t, err)
	encoded, _ := json.Marshal(tools)
	require.NotContains(t, string(encoded), "private-alice")
	specs, err := c.ListFeedSpecsContext(alice)
	require.NoError(t, err)
	require.Len(t, specs, 20)
	for _, spec := range specs {
		require.Nil(t, spec.UI)
		require.Nil(t, spec.DataSource)
		require.Contains(t, spec.URI, "user-alice")
	}
	coldGets, _ := transport.counts()
	require.Equal(t, 20, coldGets)
	selected, err := c.GetFeedSpecContext(alice, "feed-00")
	require.NoError(t, err)
	require.Equal(t, "feed-00", selected.ID)
	require.Equal(t, "toolfeed://user-alice/feed-00", feedIdentity(selected))
	require.Contains(t, fmt.Sprint(selected.UI), "9007199254740993")
	warmGets, _ := transport.counts()
	require.Equal(t, coldGets+1, warmGets)
	_, err = c.ListFeedSpecsContext(alice)
	require.NoError(t, err)
	afterList, _ := transport.counts()
	require.Equal(t, warmGets, afterList)
	var wg sync.WaitGroup
	for _, ctx := range []context.Context{alice, bob} {
		wg.Add(1)
		go func(ctx context.Context) {
			defer wg.Done()
			spec, err := c.GetFeedSpecContext(ctx, "feed-00")
			if err != nil {
				t.Errorf("own feed: %v", err)
				return
			}
			a, _ := feedActor(ctx)
			if spec.Title != "private-"+a.Subject+"-00" {
				t.Errorf("cross-user definition")
			}
		}(ctx)
	}
	wg.Wait()
	_, err = c.GetFeedSpecContext(bob, "toolfeed://user-alice/feed-00")
	require.Error(t, err)
	require.Nil(t, c.FeedRegistry())
	require.Empty(t, c.ListFeedSpecs())
	denied.Store(true)
	_, err = c.GetFeedSpecContext(alice, "feed-00")
	require.Error(t, err)
	t.Logf("20-feed coldGet=%d warmSelectedGetDelta=%d warmListGetDelta=%d", coldGets, warmGets-coldGets, afterList-warmGets)
}
func TestFeedGatewayOrdinaryPartialProviderFreshDriftAndNativeCompletedExtraction(t *testing.T) {
	c, transport, _, _, root, _ := feedGatewayFixture(t, 1, true)
	ctx := requestctx.WithConversationID(feedActorContext("alice"), "owned-conversation")
	spec, err := c.GetFeedSpecContext(ctx, "feed-00")
	require.NoError(t, err)
	c.conv = newPayloadOnlyConversationClient(map[string]string{"payload": `{"data":[{"id":"alpha","spend":125.5},{"id":"beta","spend":274.5},{"id":"gamma","spend":100}]}`})
	state := &ConversationState{ConversationID: "owned-conversation", Turns: []*TurnState{{TurnID: "owned-turn", Execution: &ExecutionState{Pages: []*ExecutionPageState{{ToolSteps: []*ToolStepState{{ToolName: "synthetic-action00", Status: "completed", ResponsePayloadID: "payload"}}}}}}}}
	feeds := c.resolveActiveFeedsFromState(ctx, state)
	require.Len(t, feeds, 1)
	require.Equal(t, feedIdentity(spec), feeds[0].FeedID)
	require.Equal(t, 3, feeds[0].ItemCount)
	bus := streaming.NewMemoryBus(4)
	sub, err := bus.Subscribe(ctx, nil)
	require.NoError(t, err)
	defer sub.Close()
	notifier := newFeedNotifier(nil, bus)
	notifier.resolve = c.matchFeedSpecsContext
	before, _ := transport.counts()
	notifier.NotifyToolCompleted(ctx, "synthetic-action00", nil, `{"data":[{"id":"alpha"}]}`)
	select {
	case event := <-sub.C():
		require.Equal(t, feedIdentity(spec), event.FeedID)
	case <-time.After(time.Second):
		t.Fatal("native feed event missing")
	}
	after, _ := transport.counts()
	require.Equal(t, 1, after-before)
	file := filepath.Join(root, "feeds/alice/feed-00.yaml")
	body, err := os.ReadFile(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, append(body, []byte("presentation: {icon: changed}\n")...), 0600))
	_, err = c.GetFeedSpecContext(ctx, "feed-00")
	require.ErrorIs(t, err, identity.ErrResourceStale)
}
func TestFeedGatewayHydratedSourceLeaseExpiresAndCancellation(t *testing.T) {
	c, transport, _, lease, _, _ := feedGatewayFixture(t, 1, false)
	ctx := feedActorContext("alice")
	lease.Store(time.Now().Add(300 * time.Millisecond).UnixNano())
	_, err := c.ListFeedSpecsContext(ctx)
	require.NoError(t, err)
	_, lists := transport.counts()
	time.Sleep(350 * time.Millisecond)
	specs, err := c.ListFeedSpecsContext(ctx)
	if err == nil {
		require.Empty(t, specs)
	}
	_, after := transport.counts()
	require.Greater(t, after, lists, "source lease must expire shared snapshot")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = c.GetFeedSpecContext(canceled, "feed-00")
	require.Error(t, err)
}

func TestFeedGatewayLegacyAliasCollisionDoesNotPickFirst(t *testing.T) {
	c, transport, _, _, _, _ := feedGatewayFixture(t, 1, false, true)
	ctx := feedActorContext("alice")
	_, err := c.GetFeedSpecContext(ctx, "feed-00")
	require.ErrorIs(t, err, resources.ErrCollision)
	cold, _ := transport.counts()
	require.Equal(t, 2, cold)
	spec, err := c.GetFeedSpecContext(ctx, "toolfeed://user-team/feed-00")
	require.NoError(t, err)
	require.Equal(t, "private-team-00", spec.Title)
	require.Equal(t, "feed-00", spec.ID)
	specs, err := c.ListFeedSpecsContext(ctx)
	require.NoError(t, err)
	require.Len(t, specs, 2)
	require.NotEqual(t, specs[0].URI, specs[1].URI)
	c.conv = newPayloadOnlyConversationClient(map[string]string{"payload": `{"data":[{"id":"owned"}]}`})
	state := &ConversationState{ConversationID: "owned", Turns: []*TurnState{{TurnID: "turn", Execution: &ExecutionState{Pages: []*ExecutionPageState{{ToolSteps: []*ToolStepState{{ToolName: "synthetic-action00", Status: "completed", ResponsePayloadID: "payload"}}}}}}}}
	feeds := c.resolveActiveFeedsFromState(ctx, state)
	require.Len(t, feeds, 2)
	require.NotEqual(t, feeds[0].FeedID, feeds[1].FeedID)
}
func TestFeedGatewayExternalPublicMCPHTTPKeepsWorkspaceActorRequired(t *testing.T) {
	_, _, _, _, _, provider := feedGatewayFixture(t, 1, false, false, true)
	sourceContext := feedActorContext("alice")
	handler, err := resources.NewLocalMCPHandler(provider, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				t.Error("public MCP unexpectedly received credentials")
			}
			next.ServeHTTP(w, r.WithContext(sourceContext))
		})
	})
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	defer server.Close()
	options := feedHTTPOptions{"external": {ClientOptions: &mcp.ClientOptions{Name: "external", Version: "1", ProtocolVersion: schema.LatestProtocolVersion, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL + "/mcp"}}}}}
	mgr, err := manager.New(options)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	g := resources.NewGateway(mgr, feedActor, func(context.Context, identity.VerifiedActor) error { return nil }, "host")
	defer g.Close()
	c := &backendClient{primitiveProviders: g}
	ctx, cancel := context.WithTimeout(feedActorContext("alice"), 5*time.Second)
	defer cancel()
	_, discoveryErr := g.Discover(ctx)
	require.NoError(t, discoveryErr, "external namespace discovery")
	spec, err := c.GetFeedSpecContext(ctx, "feed-00")
	require.NoError(t, err)
	require.Equal(t, "toolfeed://user-alice/feed-00", spec.URI)
	_, err = c.GetFeedSpecContext(context.Background(), "feed-00")
	require.Error(t, err, "upstream public access must not grant unsigned workspace authority")
}

type feedHTTPOptions map[string]*mcpcfg.MCPClient

func (o feedHTTPOptions) Options(_ context.Context, name string) (*mcpcfg.MCPClient, error) {
	return o[name], nil
}
func (o feedHTTPOptions) Names(context.Context) ([]string, error) {
	names := make([]string, 0, len(o))
	for name := range o {
		names = append(names, name)
	}
	return names, nil
}
