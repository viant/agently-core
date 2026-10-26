package resource

import (
	"context"
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

func TestNativePlaybookMarkdownCompilesOwnedBodyAndRejectsDuplicateFormat(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "playbooks"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "knowledge"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "knowledge", "rules.md"), []byte("Scoped rules"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "review.md"), []byte("---\nid: review\nname: Review\nmodel: gpt-4o\n---\n---\nrole: system\n---\n# Instructions\n$import(../knowledge/rules.md)"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "README.md"), []byte("# Reference material, not a playbook"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "review.user.md"), []byte("# Supporting user fragment"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "reference.md"), []byte("---\ntitle: Reference\n---\n# Notes"), 0600))
	definitions, err := SnapshotExtensionDefinitions(context.Background(), root, []ExtensionDirectory{PlaybookDirectory("team", "playbooks")})
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	snapshot, err := NewNativeAssetSnapshot(context.Background(), root, definitions)
	require.NoError(t, err)
	uri, err := identity.ParseResourceURI("playbook://team/review")
	require.NoError(t, err)
	candidates, err := snapshot.Candidates(context.Background(), uri)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	raw, err := snapshot.ReadCandidate(context.Background(), uri, candidates[0])
	require.NoError(t, err)
	profile, err := DecodePlaybookDefinition(raw)
	require.NoError(t, err)
	require.Equal(t, "review", profile.ID)
	require.Len(t, profile.Messages, 1)
	require.Equal(t, "# Instructions\nScoped rules", profile.Messages[0].Text)
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "review.yaml"), []byte("id: review\n"), 0600))
	_, err = SnapshotExtensionDefinitions(context.Background(), root, []ExtensionDirectory{PlaybookDirectory("team", "playbooks")})
	require.ErrorContains(t, err, "duplicate")
	require.NoError(t, os.Remove(filepath.Join(root, "playbooks", "review.yaml")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "bad.md"), []byte("---\nid: bad\nkind: agent\n---\nbody"), 0600))
	_, err = SnapshotExtensionDefinitions(context.Background(), root, []ExtensionDirectory{PlaybookDirectory("team", "playbooks")})
	require.Error(t, err, "declared root with wrong kind must fail instead of being ignored")
	require.NoError(t, os.Remove(filepath.Join(root, "playbooks", "bad.md")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "bad.yaml"), []byte("id: bad\nkind: intent\n"), 0600))
	definitions, err = SnapshotExtensionDefinitions(context.Background(), root, []ExtensionDirectory{PlaybookDirectory("team", "playbooks")})
	require.NoError(t, err)
	_, err = NewNativeAssetSnapshot(context.Background(), root, definitions)
	require.Error(t, err, "a YAML root cannot declare the legacy kind inside the canonical directory")
}

func TestPlaybookReaderKeepsDistinctLegacyAndCanonicalExactURIs(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"playbooks", "intents"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "review.md"), []byte("---\nid: shared\n---\ncanonical"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "intents", "review.yaml"), []byte("id: shared\ninstructions: legacy\n"), 0600))
	ctx := context.Background()
	definitions, err := SnapshotExtensionDefinitions(ctx, root, []ExtensionDirectory{PlaybookDirectory("team", "playbooks"), IntentDirectory("team", "intents")})
	require.NoError(t, err)
	snapshot, err := NewNativeAssetSnapshot(ctx, root, definitions)
	require.NoError(t, err)
	actor, verify := PublicReadActors()
	bindings, err := snapshot.Bindings("two-kinds", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return authoredTestPolicy{a}, nil
	})
	require.NoError(t, err)
	provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "two-kinds", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]LocalResourceValidator{"playbook": ValidateIntentDefinition, "intent": ValidateIntentDefinition}})
	require.NoError(t, err)
	client, err := NewLocalMCPClient(provider)
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, "two-kinds", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider.ProviderIdentity()}, func(context.Context) (mcpclient.Interface, error) { return client, nil }))
	reader := NewPlaybookReader(NewGateway(mgr, actor, verify, ""))
	profiles, err := reader.LoadAll(ctx)
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	_, err = reader.Load(ctx, "shared")
	require.ErrorIs(t, err, ErrCollision)
	canonical, err := reader.Load(ctx, "playbook://team/review")
	require.NoError(t, err)
	require.Equal(t, "canonical", canonical.Messages[0].Text)
	legacy, err := reader.Load(ctx, "intent://team/review")
	require.NoError(t, err)
	require.Equal(t, "legacy", legacy.Instructions)
}
