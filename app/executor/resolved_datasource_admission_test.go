package executor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/datasource"
	ui "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/forge/backend/types"
	mcp "github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type forwardedManagingAccount struct {
	calls, executions int
	seen              map[string]any
}

func (m *forwardedManagingAccount) Execute(_ context.Context, name string, args map[string]any) (string, error) {
	m.calls++
	m.seen = args
	if name != "managing:read" {
		return "", errors.New("wrong managing method")
	}
	// This directory belongs to the managing endpoint. No account argument can
	// authorize a foreign entity or parent, and no local host reader role exists.
	owners := map[string]string{"owned-entity": "account", "owned-parent": "account", "foreign": "other-account"}
	id, _ := args["Object"].(string)
	parent, _ := args["Parent"].(string)
	if owners[id] != "account" || owners[parent] != "account" {
		return "", &dsproto.UpstreamAuthorizationError{Status: 403}
	}
	m.executions++
	return `{"data":[{"id":"owned-entity"}]}`, nil
}

func TestBuilderRemoteDatasourceForwardsWithoutHostBusinessAdmission(t *testing.T) {
	ctx := context.Background()
	actor := identity.VerifiedActor{Subject: "authenticated-no-reader-role", Issuer: "issuer", TenantID: "tenant", AccountID: "account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Minute)}
	revoked := false
	actorSource := func(context.Context) (identity.VerifiedActor, error) { return actor, nil }
	verify := func(context.Context, identity.VerifiedActor) error {
		if revoked {
			return identity.ErrResourceDenied
		}
		return nil
	}
	descriptor := dsproto.DataSource{ID: "records", DataSource: types.DataSource{Selectors: &types.Selectors{Data: "data"}}, Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "managing", Method: "read"}}
	raw, _ := json.Marshal(descriptor)
	raw, err := types.CanonicalWindowDescriptor(raw)
	require.NoError(t, err)
	variant := types.WindowResourceVariant{Window: &types.Window{WindowKey: "workspace", View: types.View{Content: &types.Container{ID: "authored"}}, DataSource: map[string]types.DataSource{"records": descriptor.DataSource}, ResourceDependencies: map[string]string{"records": identity.ContentFingerprint(raw)}}, DataSources: map[string]json.RawMessage{"records": raw}}
	fingerprint, err := types.WindowVariantFingerprint(variant)
	require.NoError(t, err)
	encoded, err := json.Marshal(types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Target: types.WindowTarget{}, Variant: fingerprint}}, Variants: map[string]types.WindowResourceVariant{fingerprint: variant}})
	require.NoError(t, err)
	source := &targetPipelineSource{raw: encoded, allowed: true}
	uri := "window://provider/workspace"
	provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: "provider", Actor: actorSource, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Validators: map[string]resources.LocalResourceValidator{"window": resources.ValidateWindowBundle}, Bindings: []resources.LocalResourceBinding{{URI: uri, FormatVersion: 2, Resolver: func(context.Context, identity.VerifiedActor, string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: "provider", Source: source, Policy: internalMountPolicy{actor}}, nil
	}}}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, "provider", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "provider"}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(provider) }))
	proof, err := types.NewWindowTargetHMAC([]byte(strings.Repeat("p", 32)))
	require.NoError(t, err)
	runtime := &Runtime{MCPManager: mgr, UIBridge: ui.NewService(&ui.Config{WindowDefinitions: primitiveLocalCatalog{}})}
	builder := NewBuilder().WithPrimitiveAuthority(actorSource, verify, "host").WithPrimitiveWindows(proof)
	require.NoError(t, builder.configurePrimitiveProviders(runtime))
	defer runtime.PrimitiveProviders.Close()
	opened, err := runtime.PrimitiveWindows.Remote.Get(ctx, &ui.WindowDefinitionGetInput{WindowID: uri, Target: &types.WindowTarget{}})
	require.NoError(t, err)
	managing := &forwardedManagingAccount{}
	svc := datasource.New(datasource.Options{DisableCache: true, Executor: managing, ReadSequence: runtime.DatasourceReadSequence, ReadContext: resources.ReadContext, ResolveResource: runtime.DatasourceResourceRevalidator, ResolveDefinition: runtime.DatasourceDefinitionResolver, AuthorizeDefinition: runtime.DatasourceDefinitionAuthorizer})
	opts := datasource.FetchOptions{Resource: opened.Definition.Resource, Target: opened.Definition.ResourceTarget, WindowParameters: map[string]any{"Selected": []any{"original-open-selection"}}}
	inputs := map[string]any{"Object": "owned-entity", "Parent": "owned-parent", "filters": map[string]any{"limit": json.Number("9007199254740993"), "enabled": false}}
	before, _ := json.Marshal(inputs)
	_, err = svc.Fetch(ctx, "records", inputs, opts)
	require.NoError(t, err)
	require.Equal(t, 1, managing.calls)
	require.Equal(t, 1, managing.executions)
	seen, _ := json.Marshal(managing.seen)
	require.Equal(t, string(before), string(seen))
	after, _ := json.Marshal(inputs)
	require.Equal(t, string(before), string(after))
	denied := map[string]any{"Object": "foreign", "Parent": "owned-parent", "accountId": "account"}
	_, err = svc.Fetch(ctx, "records", denied, opts)
	var upstream *dsproto.UpstreamAuthorizationError
	require.ErrorAs(t, err, &upstream)
	require.Equal(t, 403, upstream.Status)
	require.Equal(t, 2, managing.calls)
	require.Equal(t, 1, managing.executions)
	revoked = true
	_, err = svc.Fetch(ctx, "records", inputs, opts)
	require.Error(t, err)
	require.Equal(t, 2, managing.calls, "revoked identity reached managing endpoint")
}
