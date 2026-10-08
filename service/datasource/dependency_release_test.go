package datasource_test

import (
	"context"
	"encoding/json"
	"errors"
	datasource "github.com/viant/agently-core/service/datasource"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/protocol/mcp/config"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	resourcecatalog "github.com/viant/agently-core/service/resource"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type releaseResource struct {
	raw              json.RawMessage
	formatVersion    int64
	revision         string
	authorityBinding string
	validUntil       time.Time
	denied           bool
}
type releaseMCPProvider struct {
	mcpclient.Interface
	owner string
	items map[string]*releaseResource
}

func (p *releaseMCPProvider) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	meta := map[string]any{primitive.AuthoringExtension: map[string]any{"version": 1, "providerIdentity": p.owner, "transport": "tools/call"}}
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "namespaces/list", Meta: meta}, {Name: "namespaces/get", Meta: meta}, {Name: "windows/get", Meta: meta}, {Name: "datasources/get", Meta: meta}}}, nil
}

func (p *releaseMCPProvider) CallTool(_ context.Context, request *schema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	if request == nil {
		return releaseError(), nil
	}
	switch request.Name {
	case "namespaces/list":
		namespaces := map[string]map[string]bool{}
		for uri := range p.items {
			parsed, _ := identity.ParseResourceURI(uri)
			if namespaces[parsed.Namespace] == nil {
				namespaces[parsed.Namespace] = map[string]bool{}
			}
			namespaces[parsed.Namespace][parsed.Kind] = true
		}
		out := []primitive.Namespace{}
		for namespace, kinds := range namespaces {
			values := []string{}
			for kind := range kinds {
				values = append(values, kind)
			}
			out = append(out, primitive.Namespace{Name: namespace, Kinds: values})
		}
		return &schema.CallToolResult{StructuredContent: primitive.NamespaceListResult{ProviderIdentity: p.owner, Namespaces: out, Complete: true}}, nil
	case "namespaces/get":
		namespace, _ := request.Arguments["namespace"].(string)
		kinds := map[string][]int64{}
		for uri, item := range p.items {
			parsed, _ := identity.ParseResourceURI(uri)
			if parsed.Namespace == namespace {
				kinds[parsed.Kind] = append(kinds[parsed.Kind], item.formatVersion)
			}
		}
		caps := []primitive.KindSupport{}
		for kind, versions := range kinds {
			caps = append(caps, primitive.KindSupport{Kind: kind, FormatVersions: versions, Operations: []string{"get"}, Methods: primitive.Methods(kind, []string{"get"})})
		}
		return &schema.CallToolResult{StructuredContent: primitive.NamespaceCapabilities{ProviderIdentity: p.owner, Namespace: namespace, Kinds: caps}}, nil
	default:
		if !strings.HasSuffix(request.Name, "/get") {
			return releaseError(), nil
		}
		uri, _ := request.Arguments["uri"].(string)
		item := p.items[uri]
		if item == nil || item.denied {
			return releaseError(), nil
		}
		requested, _ := request.Arguments["revision"].(string)
		if requested != "" && requested != item.revision {
			return releaseError(), nil
		}
		parsed, _ := identity.ParseResourceURI(uri)
		candidate := identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(item.raw)}
		if item.revision != identity.WorkingCandidate {
			candidate.Kind, candidate.Revision = identity.StampedCandidate, item.revision
		}
		pin := identity.ResolvedResource{ProviderIdentity: p.owner, URI: uri, ResourceCandidate: candidate, AuthorityBinding: item.authorityBinding, ValidUntil: item.validUntil}
		state := &primitive.ResourceState{Kind: parsed.Kind, Namespace: parsed.Namespace, Name: parsed.Name, URI: uri, Title: parsed.Name, Lifecycle: "working", Revision: item.revision, FormatVersion: item.formatVersion, Definition: append(json.RawMessage(nil), item.raw...), DefinitionBytes: append([]byte(nil), item.raw...), ContentFingerprint: candidate.ContentFingerprint}
		return &schema.CallToolResult{StructuredContent: primitive.GetResult{Resource: state, ResolvedResource: &pin}}, nil
	}
}

func releaseError() *schema.CallToolResult {
	failed := true
	return &schema.CallToolResult{IsError: &failed, StructuredContent: map[string]any{"message": "denied"}}
}

type releaseFixtureOptions map[string]*config.MCPClient

func (p releaseFixtureOptions) Options(_ context.Context, name string) (*config.MCPClient, error) {
	return p[name], nil
}
func (p releaseFixtureOptions) Names(context.Context) ([]string, error) {
	result := make([]string, 0, len(p))
	for name := range p {
		result = append(result, name)
	}
	return result, nil
}

type releaseStack struct {
	parentURI string
	parentPin identity.ResolvedResource
	target    types.WindowTarget
	child     *releaseResource
	executor  *releaseToolExecutor
	service   *datasource.Service
}

type releaseToolExecutor struct {
	calls  int
	onCall func()
}

func (e *releaseToolExecutor) Execute(_ context.Context, name string, _ map[string]interface{}) (string, error) {
	if name != "orders-data:fetch" {
		return "", errors.New("unexpected release fixture tool")
	}
	e.calls++
	if e.onCall != nil {
		e.onCall()
	}
	return `{"data":[{"orderId":101,"status":"authorized"}]}`, nil
}

func newReleaseStack(t *testing.T) *releaseStack {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	parentURI, childURI := "window://team/orders", "datasource://team/orders"
	parentValidUntil, childValidUntil := now.Add(5*time.Minute), now.Add(2*time.Minute)
	childDescriptor := dsproto.DataSource{ID: "child-asset-id", DataSource: types.DataSource{Selectors: &types.Selectors{Data: "data"}}, Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "orders-data", Method: "fetch"}}
	childDescriptorRaw, err := json.Marshal(childDescriptor)
	require.NoError(t, err)
	childDefinition, err := json.Marshal(struct {
		SchemaVersion int             `json:"schemaVersion"`
		DataSource    json.RawMessage `json:"dataSource"`
	}{SchemaVersion: 1, DataSource: childDescriptorRaw})
	require.NoError(t, err)
	child := &releaseResource{raw: childDefinition, formatVersion: 1, revision: identity.WorkingCandidate, authorityBinding: "child-binding", validUntil: childValidUntil}

	parentDescriptor := childDescriptor
	parentDescriptor.ID = "orders"
	parentDescriptorRaw, err := json.Marshal(parentDescriptor)
	require.NoError(t, err)
	descriptorFingerprint, err := types.WindowDescriptorFingerprint(parentDescriptorRaw)
	require.NoError(t, err)
	ref := primitive.DataSourceReference{Resource: identity.ResourceRef{URI: childURI, Revision: identity.WorkingCandidate}, ContentFingerprint: identity.ContentFingerprint(childDefinition), ProviderIdentity: "child-provider"}
	window := &types.Window{View: types.View{Content: &types.Container{ID: "orders-root"}}, DataSource: map[string]types.DataSource{"orders": parentDescriptor.DataSource}, ResourceDependencies: map[string]string{"orders": descriptorFingerprint}}
	variant := types.WindowResourceVariant{Window: window, DataSources: map[string]json.RawMessage{"orders": parentDescriptorRaw}, DataSourceResources: map[string]primitive.DataSourceReference{"orders": ref}}
	variantFingerprint, err := types.WindowVariantFingerprint(variant)
	require.NoError(t, err)
	envelope := types.WindowResourceEnvelope{SchemaVersion: 3, Format: types.WindowReferencesFormat, Targets: []types.WindowTargetBinding{{Target: types.WindowTarget{}, Variant: variantFingerprint}}, Variants: map[string]types.WindowResourceVariant{variantFingerprint: variant}}
	require.NoError(t, envelope.Validate())
	parentDefinition, err := json.Marshal(envelope)
	require.NoError(t, err)
	parent := &releaseResource{raw: parentDefinition, formatVersion: 3, revision: identity.WorkingCandidate, authorityBinding: "parent-binding", validUntil: parentValidUntil}
	parentProvider := &releaseMCPProvider{owner: "parent-provider", items: map[string]*releaseResource{parentURI: parent}}
	childProvider := &releaseMCPProvider{owner: "child-provider", items: map[string]*releaseResource{childURI: child}}
	providers := map[string]mcpclient.Interface{"parent": parentProvider, "child": childProvider}
	options := releaseFixtureOptions{"parent": &config.MCPClient{}, "child": &config.MCPClient{}}
	clients, err := manager.New(options, manager.WithClientFactory(func(_ context.Context, _, name string) (mcpclient.Interface, error) { return providers[name], nil }))
	require.NoError(t, err)
	t.Cleanup(func() { clients.CloseConversation("") })
	actor := identity.VerifiedActor{Subject: "orders-user", Issuer: "https://fixture.identity", TenantID: "team", AccountID: "opaque-account", IdentityRevision: "orders-authority-1", ValidUntil: now.Add(10 * time.Minute)}
	gateway := resourcecatalog.NewGateway(clients, func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(_ context.Context, expected identity.VerifiedActor) error {
		if !expected.Valid(time.Now()) || expected.Subject != actor.Subject || expected.AccountID != actor.AccountID || expected.IdentityRevision != actor.IdentityRevision {
			return identity.ErrResourceDenied
		}
		return nil
	}, "host-provider")
	parentConnection, err := gateway.ConnectionForProvider(ctx, "parent-provider")
	require.NoError(t, err)
	parentCandidate := identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(parentDefinition)}
	parentPin := identity.ResolvedResource{ProviderIdentity: "parent-provider", URI: parentURI, ResourceCandidate: parentCandidate, AuthorityBinding: parent.authorityBinding, ValidUntil: childValidUntil}
	childCandidate := identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(childDefinition)}
	childPin := identity.ResolvedResource{ProviderIdentity: "child-provider", URI: childURI, ResourceCandidate: childCandidate, AuthorityBinding: child.authorityBinding, ValidUntil: childValidUntil}
	target := types.WindowTarget{DependencyPins: map[string]identity.ResolvedResource{"orders": childPin}}
	proof, err := types.NewWindowTargetHMAC([]byte(strings.Repeat("release-fixture-key-", 2)))
	require.NoError(t, err)
	target.SelectionToken, err = proof.Sign(ctx, parentPin, target, variantFingerprint)
	require.NoError(t, err)
	catalog := &resourcecatalog.WindowCatalog{Gateway: gateway, TargetProof: proof, Admission: func(_ context.Context, pin identity.ResolvedResource, selected *types.Window) error {
		if pin.ProviderIdentity != "parent-provider" || selected == nil || selected.View.Content == nil || selected.View.Content.ID != "orders-root" {
			return identity.ErrResourceDenied
		}
		return nil
	}}
	executor := &releaseToolExecutor{}
	service := datasource.New(datasource.Options{
		Store:        datasource.NewMemoryStore(),
		Executor:     executor,
		DisableCache: true,
		ResolveResource: func(_ context.Context, supplied identity.ResolvedResource) (*identity.ResolvedResource, error) {
			if supplied.URI != parentPin.URI || supplied.ProviderIdentity != parentPin.ProviderIdentity || supplied.ResourceCandidate != parentPin.ResourceCandidate || supplied.AuthorityBinding != parentPin.AuthorityBinding || !supplied.ValidUntil.After(time.Now()) {
				return nil, identity.ErrResourceDenied
			}
			copy := parentPin
			return &copy, nil
		},
		ResolveDefinition: func(ctx context.Context, pin identity.ResolvedResource, selected *types.WindowTarget, id string) (*dsproto.DataSource, error) {
			if pin != parentPin {
				return nil, identity.ErrResourceDenied
			}
			return catalog.ResolveDatasource(ctx, parentPin, selected, id)
		},
	})
	_ = parentConnection
	return &releaseStack{parentURI: parentURI, parentPin: parentPin, target: target, child: child, executor: executor, service: service}
}

func TestFetchRevalidatesWindowChildAfterRealMCPToolDispatch(t *testing.T) {
	stack := newReleaseStack(t)
	ctx := context.Background()
	fetch := func() (*dsproto.FetchResult, error) {
		return stack.service.Fetch(ctx, "orders", nil, datasource.FetchOptions{Resource: &stack.parentPin, Target: &stack.target, BypassCache: true})
	}
	result, err := fetch()
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	require.Equal(t, 1, stack.executor.calls)

	stack.executor.onCall = func() {
		var envelope struct {
			SchemaVersion int             `json:"schemaVersion"`
			DataSource    json.RawMessage `json:"dataSource"`
		}
		require.NoError(t, json.Unmarshal(stack.child.raw, &envelope))
		var descriptor dsproto.DataSource
		require.NoError(t, json.Unmarshal(envelope.DataSource, &descriptor))
		descriptor.Backend.Method = "drifted-after-dispatch"
		envelope.DataSource, _ = json.Marshal(descriptor)
		stack.child.raw, _ = json.Marshal(envelope)
	}
	result, err = fetch()
	require.Nil(t, result, "no rows may escape after a child datasource changes during dispatch")
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Equal(t, 2, stack.executor.calls, "the test must traverse the datasource backend dispatch")
}
