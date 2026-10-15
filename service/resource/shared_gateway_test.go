package resource

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/jsonrpc"

	protoclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"

	"sync"
	"testing"
	"time"
)

type fixtureOptions map[string]*cfg.MCPClient

func (p fixtureOptions) Options(_ context.Context, n string) (*cfg.MCPClient, error) {
	return p[n], nil
}

func (p fixtureOptions) Names(context.Context) ([]string, error) {
	var names []string
	for n := range p {
		names = append(names, n)
	}
	return names, nil
}

type fixtureNotificationPort struct {
	notificationMu sync.RWMutex
	handler        protoclient.Handler
}

func (p *fixtureNotificationPort) SetNotificationHandler(h protoclient.Handler) {
	p.notificationMu.Lock()
	p.handler = h
	p.notificationMu.Unlock()
}
func (p *fixtureNotificationPort) resourceChanged() {
	p.notificationMu.RLock()
	h := p.handler
	p.notificationMu.RUnlock()
	if h == nil {
		panic("fixture notification receiver not connected")
	}
	h.OnNotification(context.Background(), &jsonrpc.Notification{Jsonrpc: jsonrpc.Version, Method: "notifications/resources/list_changed"})
}

type fixtureProvider struct {
	fixtureNotificationPort
	mcpclient.Interface
	owner      string
	definition json.RawMessage
	carrier    []byte
	projection json.RawMessage
	lease      time.Time
	mu         sync.Mutex
	calls      map[string]int
	after      func(string)
}

func (p *fixtureProvider) ListResources(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListResourcesResult, error) {
	return nil, jsonrpc.NewMethodNotFound("resources unavailable", nil)
}

func (p *fixtureProvider) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	meta := map[string]any{primitive.AuthoringExtension: map[string]any{"version": 1, "providerIdentity": p.owner, "transport": "tools/call"}}
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "namespaces/list", Meta: meta}, {Name: "namespaces/get", Meta: meta}, {Name: "windows/list", Meta: meta}, {Name: "windows/get", Meta: meta}}}, nil
}
func (p *fixtureProvider) CallTool(ctx context.Context, r *schema.CallToolRequestParams, options ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !mcpclient.NewRequestOptions(options).NoRetry {
		return nil, errors.New("fixture requires no replay")
	}
	p.calls[r.Name]++
	var value any
	switch r.Name {
	case "namespaces/list":
		value = primitive.NamespaceListResult{ProviderIdentity: p.owner, Namespaces: []primitive.Namespace{{Name: "example", Kinds: []string{"window"}}}, Complete: true}
	case "namespaces/get":
		value = primitive.NamespaceCapabilities{ProviderIdentity: p.owner, Namespace: "example", Kinds: []primitive.KindSupport{{Kind: "window", FormatVersions: []int64{2}, Operations: []string{"list", "get"}, Methods: primitive.Methods("window", []string{"list", "get"})}}}
	case "windows/list":
		value = map[string]any{"windows": []primitive.ResourceState{{URI: "window://example/sales", Kind: "window", Namespace: "example", Name: "sales"}}, "complete": true}
	case "windows/get":
		candidate := identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(p.definition)}
		projection := p.definition
		if len(p.projection) > 0 {
			projection = p.projection
		}
		value = primitive.GetResult{Resource: &primitive.ResourceState{URI: "window://example/sales", Definition: projection, DefinitionBytes: p.carrier, ContentFingerprint: candidate.ContentFingerprint}, ResolvedResource: &identity.ResolvedResource{ProviderIdentity: p.owner, URI: "window://example/sales", ResourceCandidate: candidate, AuthorityBinding: "verified-profile", ValidUntil: p.lease}}
	default:
		return nil, ErrUnavailable
	}
	if p.after != nil {
		p.after(r.Name)
	}
	return &schema.CallToolResult{StructuredContent: value}, nil
}
func fixtureGateway(t *testing.T, providers map[string]*fixtureProvider) (*Gateway, *identity.VerifiedActor, *bool) {
	t.Helper()
	options := fixtureOptions{}
	for name := range providers {
		options[name] = &cfg.MCPClient{}
	}
	mgr, e := manager.New(options, manager.WithClientFactory(func(_ context.Context, _, name string) (mcpclient.Interface, error) { return providers[name], nil }))
	require.NoError(t, e)
	t.Cleanup(func() { mgr.CloseConversation("") })
	actor := &identity.VerifiedActor{Subject: "alice", Issuer: "https://idp.example", TenantID: "example", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Minute)}
	revoked := new(bool)
	gateway := NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return *actor, nil }, func(_ context.Context, expected identity.VerifiedActor) error {
		if *revoked || expected.Subject != actor.Subject || expected.Issuer != actor.Issuer || expected.TenantID != actor.TenantID || expected.AccountID != actor.AccountID || expected.IdentityRevision != actor.IdentityRevision {
			return identity.ErrResourceDenied
		}
		return nil
	}, "local-yaml")
	return gateway, actor, revoked
}
func provider(owner string) *fixtureProvider {
	return &fixtureProvider{owner: owner, definition: json.RawMessage(`{"schemaVersion":2,"view":{"title":"Sales"}}`), lease: time.Now().Add(time.Minute), calls: map[string]int{}}
}
