package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agent "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	skills "github.com/viant/agently-core/service/skill"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp"
	protocolclient "github.com/viant/mcp-protocol/client"
	format "github.com/viant/mcp-protocol/extension/skills"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	client "github.com/viant/mcp/client"
	server "github.com/viant/mcp/server"
)

// A real native skills-extension handler backs the source. The wrapper counts
// lazy reads and delivers the publisher's standard skills-change notification.
type publishedSkillEndpoint struct {
	client.Interface
	mu            sync.Mutex
	registry      *protocol.DefaultHandler
	notifications protocolclient.Handler
	gets, reads   atomic.Int64
}

func newPublishedSkillEndpoint(t *testing.T) *publishedSkillEndpoint {
	t.Helper()
	endpoint := &publishedSkillEndpoint{}
	srv, err := server.New(server.WithNewHandler(protocol.WithDefaultHandler(context.Background(), func(h *protocol.DefaultHandler) error { endpoint.registry = h; return nil })))
	require.NoError(t, err)
	endpoint.Interface = server.NewAdapter(srv.NewHandler(context.Background(), nil).(*server.Handler))
	return endpoint
}
func (p *publishedSkillEndpoint) publish(t *testing.T, name string) {
	t.Helper()
	compiled, err := (format.Compiler{Source: fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: " + name + "\ndescription: Dynamic publisher skill\n---\nRead the producer-owned " + name + " instructions.")}, "refs/note.txt": {Data: []byte("Producer-owned supporting evidence.")}}}).Compile(context.Background(), "skill://publisher/"+name+"/SKILL.md")
	require.NoError(t, err)
	require.NoError(t, p.registry.RegisterStaticSkill(compiled))
	p.mu.Lock()
	h := p.notifications
	p.mu.Unlock()
	if h != nil {
		h.OnNotification(context.Background(), &jsonrpc.Notification{Jsonrpc: jsonrpc.Version, Method: "notifications/skills/list_changed"})
	}
}
func (p *publishedSkillEndpoint) SetNotificationHandler(h protocolclient.Handler) {
	p.mu.Lock()
	p.notifications = h
	p.mu.Unlock()
}
func (p *publishedSkillEndpoint) GetSkill(ctx context.Context, uri string, opts ...client.RequestOption) (*schema.GetSkillResult, error) {
	p.gets.Add(1)
	return p.Interface.(manager.SkillsClient).GetSkill(ctx, uri, opts...)
}
func (p *publishedSkillEndpoint) ListSkills(ctx context.Context, cursor *string, opts ...client.RequestOption) (*schema.ListSkillsResult, error) {
	return p.Interface.(manager.SkillsClient).ListSkills(ctx, cursor, opts...)
}
func (p *publishedSkillEndpoint) ReadResource(ctx context.Context, in *schema.ReadResourceRequestParams, opts ...client.RequestOption) (*schema.ReadResourceResult, error) {
	p.reads.Add(1)
	return p.Interface.ReadResource(ctx, in, opts...)
}

func TestStudioPublishedMCPDefinitionDiscoversSkillsAndChangesWithoutRestart(t *testing.T) {
	actor, verify := PublicReadActors()
	internal := &referenceCatalog{namespace: "workspace", owner: "configuration", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	metadata := &referenceCatalog{namespace: "studio", owner: "studio-metadata-owner", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	internal.set("", "studio-metadata", referenceConfig("studio-metadata", "studio-metadata-owner", cfg.ResourceCacheUser))
	endpoint := newPublishedSkillEndpoint(t)
	endpoint.publish(t, "first-procedure")
	unsupported := newPublishedSkillEndpoint(t) // No registered skills; no extension capability.
	var opened atomic.Int64
	mgr, err := manager.New(manager.NewRepoProvider(manager.WithRepoStateStore(fsstore.NewStateStore(t.TempDir()))), manager.WithClientFactory(func(_ context.Context, _, name string) (client.Interface, error) {
		switch name {
		case "studio-metadata":
			return referenceClient(t, metadata)
		case "mcp://studio/new-producer":
			opened.Add(1)
			return endpoint, nil
		case "mcp://studio/ordinary-tools":
			return unsupported, nil
		default:
			return nil, fmt.Errorf("unselected connection %s", name)
		}
	}))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation(""); mgr.CloseConversation("skill-catalog") })
	require.NoError(t, mgr.RegisterLocal(context.Background(), "configuration", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "configuration", ResourceCacheScopes: map[string]string{"workspace": cfg.ResourceCachePublic}}, func(context.Context) (client.Interface, error) { return referenceClient(t, internal) }))
	gateway := NewGateway(mgr, actor, verify, "consumer")
	t.Cleanup(gateway.Close)
	reader, err := NewMCPReferenceReader(gateway, MCPDefinitionSource{ConnectionName: "configuration", ProviderIdentity: "configuration", Namespace: "workspace"}, []identity.ResourceRef{{URI: "mcp://workspace/studio-metadata"}})
	require.NoError(t, err)
	bound, err := mgr.BindDefinitionReader(reader)
	require.NoError(t, err)
	require.True(t, bound)
	service := skills.New(nil, nil, nil)
	service.SetMCPSource(&NativeSkillSource{Gateway: gateway, LocalConnection: "configuration"})
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	a := &agent.Agent{Skills: []string{"*"}}
	before, err := service.ListVisiblePage(alice, a, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Empty(t, before.Items)
	// Publish complete native connection definitions, not consumer skill flags or names.
	plain := referenceConfig("new-producer", "", cfg.ResourceCacheUser)
	require.Nil(t, plain.SkillDiscovery)
	metadata.set("alice", "new-producer", plain)
	metadata.set("alice", "ordinary-tools", referenceConfig("ordinary-tools", "", cfg.ResourceCacheUser))
	metadata.changes.Publish(alice)
	roots, err := mgr.CatalogNames(alice)
	require.NoError(t, err)
	require.NotContains(t, roots, "mcp://studio/new-producer", "skill federation must not promote a published backend into a primitive catalog root")
	page, err := service.ListVisiblePage(alice, a, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Zero(t, endpoint.gets.Load())
	require.Zero(t, endpoint.reads.Load())
	require.Zero(t, unsupported.reads.Load(), "a producer without the extension is optional during automatic aggregate discovery")
	endpoint.publish(t, "later-procedure")
	after, err := service.ListVisiblePage(alice, a, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Len(t, after.Items, 2)
	require.Zero(t, endpoint.reads.Load(), "publication refresh is metadata-only")
	var chosen string
	for _, item := range after.Items {
		if item.Name == "later-procedure" {
			chosen = item.URI
		}
	}
	require.NotEmpty(t, chosen)
	selected, err := service.GetVisible(alice, a, chosen)
	require.NoError(t, err)
	require.Contains(t, selected.Body, "later-procedure instructions")
	require.EqualValues(t, 1, opened.Load(), "publication uses the same pooled connection")
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	hidden, err := service.ListVisiblePage(bob, a, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.Empty(t, hidden.Items)
	_, err = service.GetVisible(bob, a, chosen)
	require.Error(t, err)
}
