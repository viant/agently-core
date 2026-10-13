package resource

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	intent "github.com/viant/agently-core/protocol/intent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
	"os"
	"path/filepath"
	"testing"
)

func TestAuthoredReadersShareNativeSnapshotAndFreshSelectedBody(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"templates/bundles", "intents", "support"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0700))
	}
	templateYAML := []byte("id: original-template\nname: Original Template\nformat: json\ninstructions: original instructions\nschema: {opaqueId: 9007199254740993}\n")
	profileYAML := []byte("id: original-profile\nmodel: original-model\ntoolBundles: [original-tools]\ntemplate: original-template\nmessages: [{role: system, uri: support/safe.txt}]\nbootstrap: [{tool: synthetic, args: {mode: original}}]\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "templates/output.yaml"), templateYAML, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "templates/bundles/output.yaml"), []byte("id: original-bundle\ntemplates: [original-template]\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "intents/run.yaml"), profileYAML, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "support/safe.txt"), []byte("Original {{.account}}"), 0600))
	compiler := NewAuthoredCompilation("templates")
	defs, err := SnapshotExtensionDefinitions(context.Background(), root, []ExtensionDirectory{compiler.TemplateDirectory("workspace"), IntentDirectory("workspace", "intents")})
	require.NoError(t, err)
	require.Len(t, defs, 2)
	snapshot, err := NewNativeAssetSnapshot(context.Background(), root, defs, NativeSnapshotOptions{BeforeCompile: compiler.Prepare})
	require.NoError(t, err)
	compiler.Release()
	actor, verify := PublicReadActors()
	bindings, err := snapshot.Bindings("native-authored", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return authoredTestPolicy{a}, nil
	})
	require.NoError(t, err)
	provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "native-authored", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]LocalResourceValidator{"template": ValidateTemplateDefinition, "intent": ValidateIntentDefinition}})
	require.NoError(t, err)
	cli, err := NewLocalMCPClient(provider)
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(context.Background(), "native", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider.ProviderIdentity()}, func(context.Context) (mcpclient.Interface, error) { return cli, nil }))
	g := NewGateway(mgr, actor, verify, "")
	defer g.Close()
	ctx := context.Background()
	tr := NewTemplateReader(g)
	summaries, err := tr.LoadAll(ctx)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.Empty(t, summaries[0].Instructions)
	require.Nil(t, summaries[0].Schema)
	compiled := snapshot.CompileCount()
	selected, err := tr.Load(ctx, "Original Template")
	require.NoError(t, err)
	require.Equal(t, "original-template", selected.ID)
	require.Equal(t, "template://workspace/output", selected.ResourceURI)
	require.Equal(t, "original instructions", selected.Instructions)
	require.Equal(t, json.Number("9007199254740993"), selected.Schema["opaqueId"])
	bundles, err := NewTemplateBundleReader(g).LoadAll(ctx)
	require.NoError(t, err)
	require.Len(t, bundles, 1)
	require.Equal(t, "original-bundle", bundles[0].ID)
	pr := NewIntentReader(g)
	plans, err := pr.LoadAll(ctx)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Empty(t, plans[0].Messages)
	p, err := pr.Load(ctx, "original-profile")
	require.NoError(t, err)
	require.Equal(t, "original-model", p.Model)
	require.Equal(t, []string{"original-tools"}, p.ToolBundles)
	require.Empty(t, p.Messages[0].URI)
	rendered, err := p.Render(ctx, nil, &intent.RenderOptions{Binding: map[string]interface{}{"account": "synthetic"}})
	require.NoError(t, err)
	require.Equal(t, "Original synthetic", rendered[0].Text)
	require.Equal(t, compiled, snapshot.CompileCount(), "warm selected reads never compile or reload authored content")
	bytes, err := os.ReadFile(filepath.Join(root, "templates/output.yaml"))
	require.NoError(t, err)
	require.Equal(t, templateYAML, bytes)
	require.NoError(t, os.WriteFile(filepath.Join(root, "templates/output.yaml"), append(templateYAML, []byte("description: changed\n")...), 0600))
	selected, err = tr.Load(ctx, "original-template")
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, compiled, snapshot.CompileCount())
	require.NoError(t, snapshot.Reload(ctx))
	selected, err = tr.Load(ctx, "original-template")
	require.ErrorIs(t, err, identity.ErrResourceStale, "old catalog pins do not silently adopt a reloaded source")
	currentActor, err := actor(ctx)
	require.NoError(t, err)
	mgr.InvalidateVisibleCatalogPartition(mgr.CatalogPartition(ctx, catalogPrincipalScope(currentActor), "template"))
	selected, err = tr.Load(ctx, "original-template")
	require.NoError(t, err)
	require.NotNil(t, selected)
}

type authoredTestPolicy struct{ actor identity.VerifiedActor }

func (p authoredTestPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if len(c) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "public-authored", ValidUntil: p.actor.ValidUntil}, nil
}

func TestAuthoredStartupRejectsUnknownAuthorityAndEscapingSupportingURI(t *testing.T) {
	for _, bad := range []string{"id: bad\nname: Bad\nunauthenticatedGrant: true\n", "id: bad\nname: Bad\nunknownBusinessRoles: [admin]\n"} {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "templates"), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "templates/bad.yaml"), []byte(bad), 0600))
		compiler := NewAuthoredCompilation("templates")
		defs, err := SnapshotExtensionDefinitions(context.Background(), root, []ExtensionDirectory{compiler.TemplateDirectory("workspace")})
		require.NoError(t, err)
		snap, err := NewNativeAssetSnapshot(context.Background(), root, defs, NativeSnapshotOptions{BeforeCompile: compiler.Prepare})
		require.Error(t, err)
		require.Nil(t, snap)
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "intents"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "intents/bad.yaml"), []byte("id: bad\nmessages: [{role: system, uri: ../outside.txt}]\n"), 0600))
	defs, err := SnapshotExtensionDefinitions(context.Background(), root, []ExtensionDirectory{IntentDirectory("workspace", "intents")})
	require.NoError(t, err)
	snap, err := NewNativeAssetSnapshot(context.Background(), root, defs, NativeSnapshotOptions{})
	require.Error(t, err)
	require.Nil(t, snap)
}

func TestOriginalStewardAuthoredDefinitionsCompileOnNativeMCP(t *testing.T) {
	root := os.Getenv("AGENTLY_AUTHORED_FIXTURE_ROOT")
	if root == "" {
		t.Skip("original Steward authoring fixture not configured")
	}
	ctx := context.Background()
	templates := NewAuthoredCompilation("templates")
	intents := NewIntentCompilation("intents", "tools/bundles")
	defs, err := SnapshotExtensionDefinitions(ctx, root, []ExtensionDirectory{templates.TemplateDirectory("team"), intents.IntentDirectory("team")})
	require.NoError(t, err)
	before := map[string][]byte{}
	for _, d := range defs {
		raw, err := os.ReadFile(filepath.Join(root, d.File))
		require.NoError(t, err)
		before[d.File] = raw
	}
	snap, err := NewNativeAssetSnapshot(ctx, root, defs, NativeSnapshotOptions{BeforeCompile: func(ctx context.Context, r *ExtensionReader) error {
		if err := templates.Prepare(ctx, r); err != nil {
			return err
		}
		return intents.Prepare(ctx, r)
	}})
	require.NoError(t, err)
	templates.Release()
	intents.Release()
	uri, err := identity.ParseResourceURI("intent://team/media_plan_management")
	require.NoError(t, err)
	candidates, err := snap.Candidates(ctx, uri)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	_, err = snap.ReadCandidate(ctx, uri, candidates[0])
	require.NoError(t, err)
	actor, verify := PublicReadActors()
	bindings, err := snap.Bindings("steward-authored-test", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return authoredTestPolicy{a}, nil
	})
	require.NoError(t, err)
	provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "steward-authored-test", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]LocalResourceValidator{"template": ValidateTemplateDefinition, "intent": ValidateIntentDefinition}})
	require.NoError(t, err)
	cli, err := NewLocalMCPClient(provider)
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, "steward-authored-test", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider.ProviderIdentity()}, func(context.Context) (mcpclient.Interface, error) { return cli, nil }))
	gateway := NewGateway(mgr, actor, verify, "")
	defer gateway.Close()
	p, err := NewIntentReader(gateway).Load(ctx, "media_plan_management")
	require.NoError(t, err)
	require.Equal(t, "intent://team/media_plan_management", p.ResourceURI)
	require.Equal(t, "media_plan_management", p.ID)
	require.NotEmpty(t, p.Messages)
	require.NotContains(t, p.Messages[0].Text, "$import(")
	require.NotEmpty(t, p.ToolBundleDefinitions)
	count := snap.CompileCount()
	_, err = snap.ReadCandidate(ctx, uri, candidates[0])
	require.NoError(t, err)
	require.Equal(t, count, snap.CompileCount())
	for file, original := range before {
		current, err := os.ReadFile(filepath.Join(root, file))
		require.NoError(t, err)
		require.Equal(t, original, current)
	}
}
