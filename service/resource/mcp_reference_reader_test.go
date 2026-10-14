package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	producer "github.com/viant/agently-core/service/resource/provider"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
	tokenstore "github.com/viant/mcp/client/auth/store"
	"golang.org/x/oauth2"
)

type referenceCatalog struct {
	mu                     sync.Mutex
	namespace, owner, kind string
	rows                   map[string]map[string]json.RawMessage
	denied                 map[string]bool
	changes                producer.ResourceChanges
	gets, lists            int
}

func (c *referenceCatalog) Namespaces() []primitive.NamespaceCapabilities {
	return []primitive.NamespaceCapabilities{{Namespace: c.namespace, Kinds: []primitive.KindSupport{{Kind: c.kind, FormatVersions: []int64{1}, Operations: []string{"list", "get"}}}}}
}
func (c *referenceCatalog) binding(raw json.RawMessage, name string) LocalResourceBinding {
	uri := identity.ResourceURI{Kind: c.kind, Namespace: c.namespace, Name: name}
	return LocalResourceBinding{URI: uri.String(), FormatVersion: 1, Resolver: func(ctx context.Context, actor identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
		policy, err := extensionPolicy(ctx, actor, uri, "resource.get")
		if err != nil {
			return nil, err
		}
		return &identity.ResourceResolver{ProviderIdentity: c.owner, Policy: policy, Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return append(json.RawMessage(nil), raw...), nil }}}, nil
	}}
}
func (c *referenceCatalog) visible(ctx context.Context) map[string]json.RawMessage {
	visible := map[string]json.RawMessage{}
	for name, raw := range c.rows[""] {
		visible[name] = raw
	}
	for name, raw := range c.rows[authctx.EffectiveUserID(ctx)] {
		visible[name] = raw
	}
	return visible
}
func (c *referenceCatalog) List(ctx context.Context, _ identity.VerifiedActor, kind, namespace string) ([]LocalResourceBinding, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lists++
	var bindings []LocalResourceBinding
	if kind != c.kind || namespace != "" && namespace != c.namespace {
		return bindings, nil
	}
	for name, raw := range c.visible(ctx) {
		bindings = append(bindings, c.binding(raw, name))
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].URI < bindings[j].URI })
	return bindings, nil
}
func (c *referenceCatalog) Lookup(ctx context.Context, _ identity.VerifiedActor, uri identity.ResourceURI) (LocalResourceBinding, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets++
	if c.denied[uri.Name] {
		return LocalResourceBinding{}, identity.ErrResourceDenied
	}
	raw, found := c.visible(ctx)[uri.Name]
	if !found {
		return LocalResourceBinding{}, producer.ErrCatalogNotFound
	}
	return c.binding(raw, uri.Name), nil
}
func (c *referenceCatalog) set(user, name string, config *cfg.MCPClient) {
	raw, _ := json.Marshal(config)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rows[user] == nil {
		c.rows[user] = map[string]json.RawMessage{}
	}
	c.rows[user][name] = raw
}
func (c *referenceCatalog) remove(user, name string) {
	c.mu.Lock()
	delete(c.rows[user], name)
	c.mu.Unlock()
}
func referenceClient(t *testing.T, c *referenceCatalog) (mcpclient.Interface, error) {
	actor, verify := PublicReadActors()
	// The anchor contributes notification subscription, not MCP definitions.
	anchor := c.binding(json.RawMessage(`{}`), "notification-anchor")
	anchor.URI = "intent://" + c.namespace + "/notification-anchor"
	anchor.Changes = &c.changes
	p, err := NewLocalProvider(LocalConfig{ProviderIdentity: c.owner, Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Catalog: c, Bindings: []LocalResourceBinding{anchor}, Validators: map[string]LocalResourceValidator{"mcp": ValidateMCPDefinition, "intent": func(int64, json.RawMessage) error { return nil }}})
	if err != nil {
		return nil, err
	}
	return NewLocalMCPClient(p)
}
func referenceConfig(name, owner, scope string) *cfg.MCPClient {
	return &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Name: name, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "https://" + name + ".invalid/mcp"}}}, PrimitiveProviderIdentity: owner, ToolsListVisibility: cfg.ToolsListVisibilityPublic, ResourceCacheScopes: map[string]string{"*": scope, "conversation-only": cfg.ResourceCacheConversation}}
}

func TestMCPReferenceReaderUsesExplicitRootsAndScopedNotifications(t *testing.T) {
	actor, verify := PublicReadActors()
	internal := &referenceCatalog{namespace: "workspace", owner: "configuration", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	alpha := &referenceCatalog{namespace: "alpha", owner: "alpha-owner", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	beta := &referenceCatalog{namespace: "beta", owner: "beta-owner", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	partial := &referenceCatalog{namespace: "partial", owner: "partial-owner", kind: "intent", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	internal.set("", "steward", referenceConfig("sdk-client-name-not-service-alias", "", cfg.ResourceCachePublic))
	internal.set("", "mcp_say_hello_auth", referenceConfig("legacy-client", "", cfg.ResourceCachePublic))
	internal.set("", "alpha", referenceConfig("alpha", "alpha-owner", cfg.ResourceCacheUser))
	internal.set("", "beta", referenceConfig("beta", "beta-owner", cfg.ResourceCacheConversation))
	internal.set("", "partial", referenceConfig("partial", "partial-owner", cfg.ResourceCacheUser))
	alpha.set("alice", "alice_only", referenceConfig("alice-client", "", cfg.ResourceCachePublic))
	alpha.set("bob", "bob_only", referenceConfig("bob-client", "", cfg.ResourceCachePublic))
	beta.set("", "beta_target", referenceConfig("beta-client", "", cfg.ResourceCachePublic))
	calls := map[string]int{}
	var callsMu sync.Mutex
	mgr, err := manager.New(manager.NewRepoProvider(manager.WithRepoStateStore(fsstore.NewStateStore(t.TempDir()))), manager.WithClientFactory(func(ctx context.Context, _, name string) (mcpclient.Interface, error) {
		callsMu.Lock()
		calls[name]++
		callsMu.Unlock()
		switch name {
		case "steward":
			return referenceClient(t, partial)
		case "alpha":
			return referenceClient(t, alpha)
		case "beta":
			return referenceClient(t, beta)
		case "partial":
			return referenceClient(t, partial)
		}
		return nil, fmt.Errorf("unselected MCP must not be contacted: %s", name)
	}))
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(context.Background(), "configuration", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: internal.owner, ResourceCacheScopes: map[string]string{"workspace": cfg.ResourceCachePublic}}, func(context.Context) (mcpclient.Interface, error) { return referenceClient(t, internal) }))
	gateway := NewGateway(mgr, actor, verify, "reader")
	defer gateway.Close()
	reader, err := NewMCPReferenceReader(gateway, MCPDefinitionSource{ConnectionName: "configuration", ProviderIdentity: "configuration", Namespace: "workspace"}, []identity.ResourceRef{{URI: "mcp://workspace/alpha"}, {URI: "mcp://workspace/beta"}, {URI: "mcp://workspace/partial"}})
	require.NoError(t, err)
	bound, err := mgr.BindDefinitionReader(reader)
	require.NoError(t, err)
	require.True(t, bound)
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	alice = requestctx.WithConversationID(alice, "alice-conversation")
	bob = requestctx.WithConversationID(bob, "bob-conversation")
	foreignActor := identity.VerifiedActor{Subject: "alice", Issuer: "report-fixture", TenantID: "tenant", AccountID: "account", IdentityRevision: "current", ValidUntil: time.Now().Add(time.Minute)}
	foreign := NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return foreignActor, nil }, func(_ context.Context, a identity.VerifiedActor) error {
		if a != foreignActor || !a.Valid(time.Now()) {
			return identity.ErrResourceDenied
		}
		return nil
	}, "report-operation")
	defer foreign.Close()
	scoped, finish, err := foreign.BeginReadSequence(alice, nil)
	require.NoError(t, err)
	_, err = reader.Load(scoped, "steward")
	require.NoError(t, err, "configuration reads do not borrow report actor or namespace memo")
	_, err = mgr.Options(scoped, "steward")
	require.NoError(t, err)
	require.Empty(t, sequenceFromContext(scoped).reads, "configuration contracts stay outside outer report read selection")
	require.NoError(t, finish())
	original, err := reader.Load(alice, "steward")
	require.NoError(t, err)
	require.Equal(t, cfg.ToolsListVisibilityPublic, original.ToolsListVisibility)
	_, err = reader.Load(alice, "mcp/say/hello/auth")
	require.NoError(t, err)
	callsMu.Lock()
	require.Empty(t, calls, "known internal aliases never enumerate external/unlinked MCPs")
	callsMu.Unlock()
	require.Equal(t, &cfg.ResolvedServiceIdentity{ServiceName: "steward", ResourceURI: "mcp://workspace/steward", ProviderIdentity: "configuration"}, original.ResolvedIdentity)
	firstClient, err := mgr.Get(alice, "session", "steward")
	require.NoError(t, err)
	canonicalClient, err := mgr.Get(alice, "session", "mcp://workspace/steward")
	require.NoError(t, err)
	require.Same(t, firstClient, canonicalClient)
	callsMu.Lock()
	require.Equal(t, 1, calls["steward"])
	callsMu.Unlock()
	aliasOptions, err := mgr.Options(alice, "steward")
	require.NoError(t, err)
	key := tokenstore.TokenKey{Issuer: "https://tokens.example", Scopes: "fixture"}
	require.NoError(t, aliasOptions.Auth.Store.AddToken(key, &oauth2.Token{AccessToken: "fixture-only-token", Expiry: time.Now().Add(time.Minute)}))
	canonicalOptions, err := mgr.Options(alice, "mcp://workspace/steward")
	require.NoError(t, err)
	token, found := canonicalOptions.Auth.Store.LookupToken(key)
	require.True(t, found)
	require.Equal(t, "fixture-only-token", token.AccessToken, "canonical/alias options share persistent token store identity")
	scopedInventory, endInventory, err := foreign.BeginReadSequence(alice, nil)
	require.NoError(t, err)
	_, err = mgr.Names(scopedInventory)
	require.NoError(t, err)
	require.Empty(t, sequenceFromContext(scopedInventory).reads)
	require.NoError(t, endInventory())
	names, err := reader.List(alice)
	require.NoError(t, err)
	require.Contains(t, names, "steward")
	require.Contains(t, names, "alice_only")
	require.Contains(t, names, "beta_target")
	require.NotContains(t, names, "bob_only")
	names, err = reader.List(bob)
	require.NoError(t, err)
	require.Contains(t, names, "bob_only")
	require.NotContains(t, names, "alice_only")
	imported, err := reader.Load(alice, "mcp://alpha/alice_only")
	require.NoError(t, err)
	require.Equal(t, cfg.ToolsListVisibilityPrivate, imported.ToolsListVisibility)
	require.Equal(t, cfg.ResourceCacheUser, imported.ResourceCacheScopes["*"])
	require.Equal(t, cfg.ResourceCacheConversation, imported.ResourceCacheScopes["conversation-only"])
	conversation, err := reader.Load(alice, "beta_target")
	require.NoError(t, err)
	scope, err := conversation.ResourceCacheScope("new-namespace", "mcp")
	require.NoError(t, err)
	require.Equal(t, cfg.ResourceCacheConversation, scope)
	_, err = reader.Load(bob, "mcp://alpha/alice_only")
	require.Error(t, err, "private aliases never widen to another caller")
	before := alpha.gets
	updated := referenceConfig("updated-alice-client", "", cfg.ResourceCachePublic)
	alpha.set("alice", "alice_only", updated)
	cached, err := reader.Load(alice, "alice_only")
	require.NoError(t, err)
	require.Equal(t, "alice-client", cached.Name)
	require.Equal(t, before, alpha.gets, "existing Get cache remains until owning notification")
	inventoryMemo := manager.WithNamesMemo(alice)
	memoNames, err := mgr.Names(inventoryMemo)
	require.NoError(t, err)
	require.NotContains(t, memoNames, "new_alias")
	alpha.set("alice", "new_alias", referenceConfig("added-client", "", cfg.ResourceCacheUser))
	alpha.changes.Publish(alice)
	memoNames, err = mgr.Names(inventoryMemo)
	require.NoError(t, err)
	require.Contains(t, memoNames, "new_alias", "resource notification must invalidate the same-request name snapshot")
	names, err = reader.List(alice)
	require.NoError(t, err)
	require.Contains(t, names, "new_alias")
	fresh, err := reader.Load(alice, "alice_only")
	require.NoError(t, err)
	require.Equal(t, "updated-alice-client", fresh.Name)
	memoAlice := manager.WithNamesMemo(alice)
	firstOptions, err := mgr.Options(memoAlice, "steward")
	require.NoError(t, err)
	internal.mu.Lock()
	firstGets, firstLists := internal.gets, internal.lists
	internal.mu.Unlock()
	firstOptions.Name = "caller-mutation"
	againOptions, err := mgr.Options(memoAlice, "steward")
	require.NoError(t, err)
	require.Equal(t, "sdk-client-name-not-service-alias", againOptions.Name)
	internal.mu.Lock()
	require.Equal(t, firstGets, internal.gets, "one request must not reread the same MCP definition")
	require.Equal(t, firstLists, internal.lists, "one request must not rescan the MCP inventory")
	internal.mu.Unlock()
	_, err = mgr.Options(authctx.WithUserInfo(memoAlice, &authctx.UserInfo{Subject: "bob"}), "alice_only")
	require.Error(t, err, "request-local definition reuse must not cross principals")
	internal.set("", "steward", referenceConfig("updated-steward", "", cfg.ResourceCachePublic))
	internal.changes.Publish(memoAlice)
	changedOptions, err := mgr.Options(memoAlice, "steward")
	require.NoError(t, err)
	require.Equal(t, "updated-steward", changedOptions.Name, "owner notification invalidates same-request definition reuse")
	internal.set("", "steward", referenceConfig("sdk-client-name-not-service-alias", "", cfg.ResourceCachePublic))
	internal.changes.Publish(memoAlice)
	alpha.remove("alice", "new_alias")
	alpha.changes.Publish(alice)
	_, err = reader.Load(alice, "new_alias")
	require.Error(t, err, "removed alias has no raw workspace fallback")
	alpha.mu.Lock()
	alpha.denied["alice_only"] = true
	alpha.mu.Unlock()
	alpha.changes.Publish(alice)
	_, err = reader.Load(alice, "alice_only")
	require.ErrorIs(t, err, identity.ErrResourceDenied, "selected owning denial is terminal")
	alpha.set("alice", "configuration", referenceConfig("collision", "", cfg.ResourceCacheUser))
	alpha.changes.Publish(alice)
	_, err = reader.List(alice)
	require.ErrorIs(t, err, ErrCollision, "reserved local aliases cannot be published")
	alpha.remove("alice", "configuration")
	alpha.set("alice", "beta_target", referenceConfig("collision", "", cfg.ResourceCacheUser))
	alpha.changes.Publish(alice)
	_, err = reader.List(alice)
	require.ErrorIs(t, err, ErrCollision, "two external roots cannot publish ambiguous aliases")
}
