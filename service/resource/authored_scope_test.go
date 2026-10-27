package resource

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

func TestAdmittedIntentScopesUnqualifiedTemplateToOwningProvider(t *testing.T) {
	ctx := context.Background()
	actor, verify := PublicReadActors()
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	for _, provider := range []string{"owner", "foreign"} {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "templates/bundles"), 0700))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "intents"), 0700))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "tools/bundles"), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "templates", provider+".yaml"), []byte("id: same-template\nname: Same Template\ninstructions: "+provider+" body\n"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(root, "intents", provider+".yaml"), []byte("id: "+provider+"-intent\ntemplate: same-template\ntemplates: [same-template]\ntoolBundles: [same-tools]\n"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(root, "tools/bundles", "same.yaml"), []byte("id: same-tools\nmatch: [{name: "+provider+"/read}]\n"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(root, "templates/bundles", "shared.yaml"), []byte("id: same-output-bundle\ndescription: "+provider+" bundle\ntemplates: [same-template]\n"), 0600))
		tc := NewAuthoredCompilation("templates")
		ic := NewIntentCompilation("intents", "tools/bundles")
		defs, err := SnapshotExtensionDefinitions(ctx, root, []ExtensionDirectory{tc.TemplateDirectory(provider), ic.IntentDirectory(provider)})
		require.NoError(t, err)
		snapshot, err := NewNativeAssetSnapshot(ctx, root, defs, NativeSnapshotOptions{BeforeCompile: func(ctx context.Context, r *ExtensionReader) error {
			if err := tc.Prepare(ctx, r); err != nil {
				return err
			}
			return ic.Prepare(ctx, r)
		}})
		require.NoError(t, err)
		tc.Release()
		ic.Release()
		bindings, err := snapshot.Bindings(provider, func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
			return authoredTestPolicy{a}, nil
		})
		require.NoError(t, err)
		p, err := NewLocalProvider(LocalConfig{ProviderIdentity: provider, Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]LocalResourceValidator{"template": ValidateTemplateDefinition, "intent": ValidateIntentDefinition}})
		require.NoError(t, err)
		cli, err := NewLocalMCPClient(p)
		require.NoError(t, err)
		require.NoError(t, mgr.RegisterLocal(ctx, provider, &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider}, func(context.Context) (mcpclient.Interface, error) { return cli, nil }))
	}
	g := NewGateway(mgr, actor, verify, "")
	defer g.Close()
	profile, err := NewIntentReader(g).Load(ctx, "owner-intent")
	require.NoError(t, err)
	require.Equal(t, "owner", profile.ResourceProviderIdentity)
	require.Len(t, profile.ToolBundleDefinitions, 1)
	require.Equal(t, "owner/read", profile.ToolBundleDefinitions[0].Match[0].Name)
	parent, _ := identity.ParseResourceURI(profile.ResourceURI)
	scoped := WithDefinitionScope(ctx, DefinitionScope{Namespace: parent.Namespace, ProviderIdentity: profile.ResourceProviderIdentity})
	outputBundles, err := NewTemplateBundleReader(g).LoadAll(scoped)
	require.NoError(t, err)
	require.Len(t, outputBundles, 1)
	require.Equal(t, "owner bundle", outputBundles[0].Description)
	_, err = NewTemplateBundleReader(g).LoadAll(ctx)
	require.ErrorIs(t, err, ErrCollision)
	tr := NewTemplateReader(g)
	ambiguous, err := tr.Load(ctx, "same-template")
	require.ErrorIs(t, err, ErrCollision)
	require.Nil(t, ambiguous)
	selected, err := tr.LoadFrom(ctx, profile.Template, profile.ResourceURI, profile.ResourceProviderIdentity)
	require.NoError(t, err)
	require.Equal(t, "owner body", selected.Instructions)
	explicit, err := tr.LoadFrom(ctx, "template://foreign/foreign", profile.ResourceURI, profile.ResourceProviderIdentity)
	require.NoError(t, err)
	require.Equal(t, "foreign body", explicit.Instructions)
	raw, err := json.Marshal(profile)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "ResourceProviderIdentity")
	// Workspace-relative profile selection must remain usable when an
	// unrelated connector cannot initialize (for example, OAuth discovery).
	offlineCalls := 0
	require.NoError(t, mgr.RegisterLocal(ctx, "offline", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "offline"}, func(context.Context) (mcpclient.Interface, error) {
		offlineCalls++
		return nil, errors.New("unrelated connector offline")
	}))
	reader := NewIntentReader(g)
	reader.Scope = DefinitionScope{Namespace: "owner", ProviderIdentity: "owner"}
	selectedProfile, err := reader.Load(ctx, "owner-intent")
	require.NoError(t, err)
	require.Equal(t, "owner", selectedProfile.ResourceProviderIdentity)
	require.Zero(t, offlineCalls)
}
