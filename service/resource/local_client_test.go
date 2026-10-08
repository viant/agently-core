package resource

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type localActorKey struct{}

func TestLocalSDKClientExactNamesAndRequestAuthority(t *testing.T) {
	source := &localFixtureSource{raw: json.RawMessage(`{"native":"template"}`)}
	var revoked atomic.Bool
	actor := func(ctx context.Context) (identity.VerifiedActor, error) {
		a, ok := ctx.Value(localActorKey{}).(identity.VerifiedActor)
		if !ok {
			return a, identity.ErrResourceDenied
		}
		return a, nil
	}
	binding := func(namespace string) LocalResourceBinding {
		return LocalResourceBinding{URI: "template://" + namespace + "/nested/doc", FormatVersion: 1, Resolver: func(_ context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
			return &identity.ResourceResolver{ProviderIdentity: "internal", Source: source, Policy: localFixturePolicy{actor: a}}, nil
		}}
	}
	provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "internal", Actor: actor, Verify: func(_ context.Context, a identity.VerifiedActor) error {
		if revoked.Load() {
			return identity.ErrResourceDenied
		}
		return nil
	}, Authorize: func(_ context.Context, a identity.VerifiedActor, uri identity.ResourceURI, _ string) error {
		if uri.Namespace != a.Subject {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: []LocalResourceBinding{binding("alice"), binding("bob")}, Validators: map[string]LocalResourceValidator{"template": func(int64, json.RawMessage) error { return nil }}})
	require.NoError(t, err)
	client, err := NewLocalMCPClient(provider)
	require.NoError(t, err)
	listed, err := client.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := []string{}
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		meta, ok := tool.Meta[primitive.AuthoringExtension].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "internal", meta["providerIdentity"])
	}
	require.Contains(t, names, "templates/list")
	require.Contains(t, names, "templates/get")
	require.Contains(t, names, "namespaces/list")
	require.Contains(t, names, "namespaces/get")
	makeContext := func(subject string, expiry time.Time) context.Context {
		return context.WithValue(context.Background(), localActorKey{}, identity.VerifiedActor{Subject: subject, Issuer: "https://idp.example", TenantID: "team", AccountID: subject, IdentityRevision: "r1", ValidUntil: expiry})
	}
	var wg sync.WaitGroup
	for _, subject := range []string{"alice", "bob"} {
		wg.Add(1)
		go func(subject string) {
			defer wg.Done()
			ctx := makeContext(subject, time.Now().Add(time.Minute))
			for i := 0; i < 5; i++ {
				result, err := client.CallTool(ctx, &schema.CallToolRequestParams{Name: "templates/get", Arguments: map[string]interface{}{"uri": "template://" + subject + "/nested/doc"}})
				if err != nil || result == nil || result.IsError != nil && *result.IsError {
					t.Errorf("own authority failed: %s %v", subject, err)
					return
				}
				other := "alice"
				if subject == "alice" {
					other = "bob"
				}
				denied, err := client.CallTool(ctx, &schema.CallToolRequestParams{Name: "templates/get", Arguments: map[string]interface{}{"uri": "template://" + other + "/nested/doc"}})
				if err != nil || denied == nil || denied.IsError == nil || !*denied.IsError {
					t.Errorf("foreign authority allowed: %s %v", subject, err)
					return
				}
			}
		}(subject)
	}
	wg.Wait()
	expired, err := client.CallTool(makeContext("alice", time.Now().Add(-time.Second)), &schema.CallToolRequestParams{Name: "templates/get", Arguments: map[string]interface{}{"uri": "template://alice/nested/doc"}})
	require.NoError(t, err)
	require.NotNil(t, expired.IsError)
	require.True(t, *expired.IsError)
	revoked.Store(true)
	denied, err := client.CallTool(makeContext("alice", time.Now().Add(time.Minute)), &schema.CallToolRequestParams{Name: "templates/get", Arguments: map[string]interface{}{"uri": "template://alice/nested/doc"}})
	require.NoError(t, err)
	require.NotNil(t, denied.IsError)
	require.True(t, *denied.IsError)
}

func TestInProcessProviderGatewayPreservesProvenanceAndDeniesLogicalCollision(t *testing.T) {
	original, source, actor, _ := localFixture(t)
	makeProvider := func(owner string) *LocalProvider {
		binding := original.bindings["window://team/sales"]
		binding.Resolver = func(_ context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
			return &identity.ResourceResolver{ProviderIdentity: owner, Source: source, Policy: localFixturePolicy{actor: a}}, nil
		}
		provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: owner, Actor: original.actor, Verify: original.verify, Authorize: original.authorizer, Bindings: []LocalResourceBinding{binding}, Validators: original.validators})
		require.NoError(t, err)
		return provider
	}
	ctx := context.Background()
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	local := makeProvider("internal")
	require.NoError(t, mgr.RegisterLocal(ctx, "internal", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "internal"}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(local) }))
	gateway := NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return *actor, nil }, original.verify, "aggregate-gateway")
	defer gateway.Close()
	resources, err := gateway.List(ctx, "window", "team")
	require.NoError(t, err)
	require.Len(t, resources, 1)
	require.Equal(t, "internal", resources[0].Connection.ProviderIdentity)
	got, err := gateway.Get(ctx, resources[0].Connection, identity.ResourceRef{URI: resources[0].Resource.URI}, nil)
	require.NoError(t, err)
	require.Equal(t, "internal", got.ResolvedResource.ProviderIdentity)
	peer := makeProvider("external-authority")
	require.NoError(t, mgr.RegisterLocal(ctx, "external", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "external-authority"}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(peer) }))
	resources, err = gateway.List(ctx, "window", "team")
	require.NoError(t, err)
	require.Len(t, resources, 2)
	_, err = ResolveLocator(resources, "window://team/sales", "")
	require.ErrorIs(t, err, ErrCollision)
	selected, err := ResolveLocator(resources, "window://team/sales", "internal")
	require.NoError(t, err)
	require.Equal(t, "internal", selected.ProviderIdentity)
}
