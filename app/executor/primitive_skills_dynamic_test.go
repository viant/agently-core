package executor

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agent "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	producer "github.com/viant/agently-core/service/resource/provider"
	skills "github.com/viant/agently-core/service/skill"
	"github.com/viant/mcp"
	protocolclient "github.com/viant/mcp-protocol/client"
	format "github.com/viant/mcp-protocol/extension/skills"
	"github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

// Models a Studio-owned native namespace catalog, including its ordinary MCP
// change notification. Consumers never receive a manually maintained skill list.
type dynamicSkillPublication struct {
	producer.ResourceChanges
	mu          sync.RWMutex
	items       map[string]*format.Static
	gets, reads atomic.Int64
}

func (c *dynamicSkillPublication) Namespaces() []primitive.NamespaceCapabilities {
	return []primitive.NamespaceCapabilities{{Namespace: "studio", Kinds: []primitive.KindSupport{{Kind: "skill", FormatVersions: []int64{1}, Operations: []string{"list", "get"}}}}}
}
func (c *dynamicSkillPublication) binding(uri identity.ResourceURI, compiled *format.Static) resources.LocalResourceBinding {
	raw, _ := json.Marshal(schema.GetSkillResult{ResultType: "complete", Skill: compiled.Metadata()})
	return resources.LocalResourceBinding{URI: uri.String(), Description: "Published synthetic skill", FormatVersion: 1, Resolver: func(ctx context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: "studio-publisher", Policy: skillRevision{uri: uri.String(), actor: a}, Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return append(json.RawMessage(nil), raw...), nil }}}, nil
	}}
}
func (c *dynamicSkillPublication) List(ctx context.Context, a identity.VerifiedActor, kind, namespace string) ([]resources.LocalResourceBinding, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []resources.LocalResourceBinding
	if authctx.EffectiveUserID(ctx) != "alice" || kind != "skill" || namespace != "" && namespace != "studio" {
		return out, nil
	}
	for text, compiled := range c.items {
		uri, _ := identity.ParseResourceURI(text)
		out = append(out, c.binding(uri, compiled))
	}
	return out, nil
}
func (c *dynamicSkillPublication) Lookup(ctx context.Context, a identity.VerifiedActor, uri identity.ResourceURI) (resources.LocalResourceBinding, error) {
	c.gets.Add(1)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if authctx.EffectiveUserID(ctx) != "alice" {
		return resources.LocalResourceBinding{}, identity.ErrResourceDenied
	}
	compiled := c.items[uri.String()]
	if compiled == nil {
		return resources.LocalResourceBinding{}, producer.ErrCatalogNotFound
	}
	return c.binding(uri, compiled), nil
}
func (c *dynamicSkillPublication) publish(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	compiled, err := (format.Compiler{Source: fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: " + name + "\ndescription: Published synthetic skill\n---\nRun the producer-owned " + name + " procedure.")}}}).Compile(ctx, "skill://studio/"+name+"/SKILL.md")
	require.NoError(t, err)
	c.mu.Lock()
	c.items[compiled.Metadata().Uri] = compiled
	c.mu.Unlock()
	c.Publish(ctx)
}

type dynamicSkillPublicationClient struct {
	client.Interface
	source *dynamicSkillPublication
}

func (c *dynamicSkillPublicationClient) SetNotificationHandler(h protocolclient.Handler) {
	c.Interface.(interface{ SetNotificationHandler(protocolclient.Handler) }).SetNotificationHandler(h)
}
func (c *dynamicSkillPublicationClient) ListSkills(ctx context.Context, cursor *string, opts ...client.RequestOption) (*schema.ListSkillsResult, error) {
	return c.Interface.(manager.SkillsClient).ListSkills(ctx, cursor, opts...)
}
func (c *dynamicSkillPublicationClient) GetSkill(ctx context.Context, uri string, opts ...client.RequestOption) (*schema.GetSkillResult, error) {
	return c.Interface.(manager.SkillsClient).GetSkill(ctx, uri, opts...)
}
func (c *dynamicSkillPublicationClient) ReadResource(ctx context.Context, in *schema.ReadResourceRequestParams, _ ...client.RequestOption) (*schema.ReadResourceResult, error) {
	c.source.reads.Add(1)
	if authctx.EffectiveUserID(ctx) != "alice" {
		return nil, identity.ErrResourceDenied
	}
	c.source.mu.RLock()
	compiled := c.source.items[in.Uri]
	c.source.mu.RUnlock()
	if compiled == nil {
		return nil, identity.ErrResourceDenied
	}
	result, e := compiled.ReadResource(ctx, &schema.ReadResourceRequest{Params: *in})
	if e != nil {
		return nil, e
	}
	return result, nil
}

func TestNativeSkillRegistrationAndPublicationRefreshWithoutConsumerRestart(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation(""); mgr.CloseConversation("skill-catalog") })
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(mgr, actor, verify, "consumer")
	t.Cleanup(gateway.Close)
	delivery, err := newPrimitiveSkillDelivery(context.Background(), gateway, nil)
	require.NoError(t, err)
	service := skills.New(nil, nil, nil)
	service.SetDelivery(delivery.source, delivery)
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	a := &agent.Agent{Skills: []string{"*"}}
	initial, err := service.ListVisiblePage(alice, a, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Empty(t, initial.Items)

	source := &dynamicSkillPublication{items: map[string]*format.Static{}}
	source.publish(t, alice, "initial")
	provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: "studio-publisher", Catalog: source, Actor: actor, Verify: verify, Authorize: func(ctx context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) error {
		if authctx.EffectiveUserID(ctx) != "alice" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Validators: map[string]resources.LocalResourceValidator{"skill": func(int64, json.RawMessage) error { return nil }}})
	require.NoError(t, err)
	// A plain native connection has neither SkillDiscovery nor skill-name config.
	options := &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "studio-publisher", ResourceCacheScopes: map[string]string{"studio": cfg.ResourceCacheUser}}
	require.NoError(t, mgr.RegisterLocal(alice, "new-studio-source", options, func(context.Context) (client.Interface, error) {
		base, e := resources.NewLocalMCPClient(provider)
		return &dynamicSkillPublicationClient{Interface: base, source: source}, e
	}))
	first, err := service.ListVisiblePage(alice, a, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	require.Zero(t, source.gets.Load(), "listing metadata must not fetch a skill manifest")
	require.Zero(t, source.reads.Load(), "listing metadata must not fetch the skill body")
	var notifications atomic.Int64
	stop := mgr.OnResourceCatalogChange(func(name string) {
		if name == "new-studio-source" {
			notifications.Add(1)
		}
	})
	defer stop()
	source.publish(t, alice, "added-later")
	require.Eventually(t, func() bool { return notifications.Load() > 0 }, time.Second, time.Millisecond)
	after, err := service.ListVisiblePage(alice, a, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Len(t, after.Items, 2)
	require.Zero(t, source.reads.Load(), "new publication must remain metadata-only until selected")
	selected := after.Items[0]
	if selected.URI == first.Items[0].URI {
		selected = after.Items[1]
	}
	got, err := service.GetVisible(alice, a, selected.URI)
	require.NoError(t, err)
	require.Contains(t, got.Body, "added-later procedure")
	require.Greater(t, source.gets.Load(), int64(0))
	require.Greater(t, source.reads.Load(), int64(0))
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	hidden, err := service.ListVisiblePage(bob, a, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Empty(t, hidden.Items)
	_, err = service.GetVisible(bob, a, selected.URI)
	require.Error(t, err, "another caller cannot borrow Alice's selected skill")
}
