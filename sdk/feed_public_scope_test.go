package sdk

import (
	"context"
	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicReadGatewayKeepsProtectedMCPCredentialCatalogsSeparate(t *testing.T) {
	root := t.TempDir()
	var dirs []resources.ExtensionDirectory
	for _, subject := range []string{"alice", "bob"} {
		dir := "feeds/" + subject
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, "feed.yaml"), []byte("id: shared-feed\nmatch: {service: synthetic, method: update}\nui: {title: private-"+subject+"}\n"), 0600))
		dirs = append(dirs, resources.ToolfeedDirectory("user-"+subject, dir))
	}
	var lease atomic.Int64
	provider, err := resources.NewInternalProvider(context.Background(), root, dirs, func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return feedPolicy{a, &lease}, nil
	}, resources.LocalConfig{Actor: feedActor, Verify: func(_ context.Context, a identity.VerifiedActor) error {
		if !a.Valid(time.Now()) {
			return identity.ErrResourceDenied
		}
		return nil
	}, Authorize: func(_ context.Context, a identity.VerifiedActor, u identity.ResourceURI, _ string) error {
		if u.Namespace != "user-"+a.Subject {
			return identity.ErrResourceDenied
		}
		return nil
	}, Validators: map[string]resources.LocalResourceValidator{primitive.ToolfeedKind: resources.ValidateToolfeedDefinition}})
	require.NoError(t, err)
	// This protected endpoint owns its auth decision; workspace public read scope
	// never becomes the managing provider's actor.
	var mu sync.Mutex
	requests := map[string]int{}
	handler, err := resources.NewLocalMCPHandler(provider, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("Authorization")
			subject := ""
			switch token {
			case "Bearer owned-alice":
				subject = "alice"
			case "Bearer owned-bob":
				subject = "bob"
			default:
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			mu.Lock()
			requests[subject]++
			mu.Unlock()
			next.ServeHTTP(w, r.WithContext(feedActorContext(subject)))
		})
	})
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	defer server.Close()
	opts := feedHTTPOptions{"protected": {ClientOptions: &mcp.ClientOptions{Name: "protected", Version: "1", ProtocolVersion: schema.LatestProtocolVersion, Auth: &mcp.ClientAuth{}, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL + "/mcp"}}}}}
	mgr, err := manager.New(opts)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	actor, verify := resources.PublicReadActors()
	g := resources.NewGateway(mgr, actor, verify, "")
	defer g.Close()
	c := &backendClient{primitiveProviders: g}
	// Same workspace effective user, different MCP OAuth credentials: neither
	// the hydrated metadata nor selected full UI may cross the credential scope.
	mkctx := func(tok string) context.Context {
		return authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "workspace-user"}), tok)
	}
	alice := mkctx("owned-alice")
	bob := mkctx("owned-bob")
	for _, test := range []struct {
		ctx     context.Context
		subject string
	}{{alice, "alice"}, {bob, "bob"}, {alice, "alice"}, {bob, "bob"}} {
		ctx, cancel := context.WithTimeout(test.ctx, 5*time.Second)
		specs, err := c.feedSpecs(ctx)
		require.NoError(t, err)
		require.Len(t, specs, 1)
		require.Equal(t, "toolfeed://user-"+test.subject+"/feed", specs[0].URI)
		got, err := c.GetFeedSpecContext(ctx, "shared-feed")
		require.NoError(t, err)
		require.Equal(t, specs[0].URI, got.URI)
		require.Equal(t, "private-"+test.subject, got.UI.(map[string]interface{})["title"])
		summaries, err := c.ListFeedSpecsContext(ctx)
		require.NoError(t, err)
		require.Len(t, summaries, 1)
		require.Nil(t, summaries[0].UI)
		cancel()
	}
	var wg sync.WaitGroup
	for _, test := range []struct {
		ctx     context.Context
		subject string
	}{{alice, "alice"}, {bob, "bob"}} {
		wg.Add(1)
		go func(ctx context.Context, subject string) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				got, e := c.GetFeedSpecContext(ctx, "shared-feed")
				if e != nil || got == nil || got.URI != "toolfeed://user-"+subject+"/feed" {
					t.Errorf("credential-scoped selected GET failed for synthetic actor %s", subject)
					return
				}
			}
		}(test.ctx, test.subject)
	}
	wg.Wait()
	require.Nil(t, c.feeds, "no SDK/global private registry")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := c.GetFeedSpecContext(ctx, "shared-feed")
	require.Error(t, err)
	require.Nil(t, got, "public scope cannot expose protected metadata without MCP credentials")
	mu.Lock()
	require.Positive(t, requests["alice"])
	require.Positive(t, requests["bob"])
	mu.Unlock()
}
func TestPublicReadLocalPrivateProviderRejectsUnverifiedScope(t *testing.T) {
	c, _, _, _, _, _ := feedGatewayFixture(t, 1, false)
	actor, verify := resources.PublicReadActors()
	g := resources.NewGateway(c.primitiveProviders.Manager, actor, verify, "")
	defer g.Close()
	publicClient := &backendClient{primitiveProviders: g}
	got, err := publicClient.GetFeedSpecContext(context.Background(), "shared-feed")
	require.Error(t, err)
	require.Nil(t, got)
}

func TestPublicReadNativeFeedMatchingAndUI(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "feeds"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "feeds/media.yaml"), []byte("id: media-plan\nmatch: {service: synthetic, method: update}\nactivation: {scope: turn}\ndataSource:\n  output: {source: output, exposeAs: output}\n  rows: {dataSourceRef: output, selectors: {data: data}, root: true}\nui: {title: Original, controls: {draftOnly: true}}\n"), 0600))
	dirs := []resources.ExtensionDirectory{resources.ToolfeedDirectory("workspace", "feeds")}
	defs, err := resources.SnapshotToolfeedDefinitions(context.Background(), root, dirs)
	require.NoError(t, err)
	snapshot, err := resources.NewNativeAssetSnapshot(context.Background(), root, defs, resources.NativeSnapshotOptions{})
	require.NoError(t, err)
	actor, verify := resources.PublicReadActors()
	var lease atomic.Int64
	bindings, err := snapshot.Bindings(resources.InternalProviderIdentity, func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return feedPolicy{a, &lease}, nil
	})
	require.NoError(t, err)
	p, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: resources.InternalProviderIdentity, Actor: actor, Verify: verify, Authorize: func(_ context.Context, a identity.VerifiedActor, u identity.ResourceURI, op string) error {
		if !a.PublicRead || u.Namespace != "workspace" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: bindings, Validators: map[string]resources.LocalResourceValidator{primitive.ToolfeedKind: resources.ValidateToolfeedDefinition}})
	require.NoError(t, err)
	local, err := resources.NewLocalMCPClient(p)
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(context.Background(), "internal", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: p.ProviderIdentity()}, func(context.Context) (mcpclient.Interface, error) { return local, nil }))
	g := resources.NewGateway(mgr, actor, verify, "")
	defer g.Close()
	c := &backendClient{primitiveProviders: g}
	ctx := requestctx.WithConversationID(context.Background(), "public-conversation")
	matches, err := c.matchFeedSpecsContext(ctx, "synthetic-update")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, "media-plan", matches[0].ID)
	require.Equal(t, "toolfeed://workspace/media", feedIdentity(matches[0]))
	require.Equal(t, "Original", matches[0].UI.(map[string]interface{})["title"])
	c.conv = newPayloadOnlyConversationClient(map[string]string{"payload": `{"data":[{"id":"alpha","spend":125.5},{"id":"beta","spend":274.5},{"id":"gamma","spend":100}]}`})
	state := &ConversationState{ConversationID: "public-conversation", Turns: []*TurnState{{TurnID: "public-turn", Execution: &ExecutionState{Pages: []*ExecutionPageState{{ToolSteps: []*ToolStepState{{ToolName: "synthetic-update", Status: "completed", ResponsePayloadID: "payload"}}}}}}}}
	feeds := c.resolveActiveFeedsFromState(ctx, state)
	require.Len(t, feeds, 1)
	require.Equal(t, "toolfeed://workspace/media", feeds[0].FeedID)
	require.Equal(t, 3, feeds[0].ItemCount)
	require.Nil(t, c.feeds)
}
