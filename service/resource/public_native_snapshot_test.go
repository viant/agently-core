package resource

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/workspace"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type publicSnapshotPolicy struct{ actor identity.VerifiedActor }

func (p publicSnapshotPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if len(c) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "public-native-definitions", ValidUntil: p.actor.ValidUntil}, nil
}
func TestPublicNativeFeedAndDatasourceShareImmutableStartup(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "feeds"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, workspace.KindForgeDataSource), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "feeds/media.yaml"), []byte("id: media-plan\nmatch: {service: synthetic, method: update}\nui: {draftOnly: true}\n"), 0600))
	file := filepath.Join(root, workspace.KindForgeDataSource, "spend.yaml")
	body := []byte("id: spend\nselectors: {data: data}\nbackend: {kind: mcp_tool, service: original, method: read}\n")
	require.NoError(t, os.WriteFile(file, body, 0600))
	feed := ToolfeedDirectory("workspace", "feeds")
	ds := DatasourceDirectory("workspace", workspace.KindForgeDataSource)
	var feedLoads, dsLoads atomic.Int64
	feed.Load = func(ctx context.Context, r *ExtensionReader, f string) (json.RawMessage, error) {
		feedLoads.Add(1)
		return NativeToolfeedLoader(ctx, r, f)
	}
	ds.Load = func(ctx context.Context, r *ExtensionReader, f string) (json.RawMessage, error) {
		dsLoads.Add(1)
		return NativeDatasourceLoader(ctx, r, f)
	}
	ctx := context.Background()
	defs, err := SnapshotExtensionDefinitions(ctx, root, []ExtensionDirectory{feed, ds})
	require.NoError(t, err)
	require.Len(t, defs, 2)
	snapshot, err := NewNativeAssetSnapshot(ctx, root, defs, NativeSnapshotOptions{AssetPaths: []string{"feeds", workspace.KindForgeDataSource}, ImmutableUntilRestart: true})
	require.NoError(t, err)
	require.EqualValues(t, len(defs), snapshot.CompileCount())
	require.EqualValues(t, 1, feedLoads.Load())
	require.EqualValues(t, 1, dsLoads.Load())
	actor, verify := PublicReadActors()
	bindings, err := snapshot.Bindings("public-native", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return publicSnapshotPolicy{a}, nil
	})
	require.NoError(t, err)
	p, err := NewLocalProvider(LocalConfig{ProviderIdentity: "public-native", Actor: actor, Verify: verify, Authorize: func(_ context.Context, a identity.VerifiedActor, u identity.ResourceURI, _ string) error {
		if !a.PublicRead || !a.ReadValid(time.Now()) || u.Namespace != "workspace" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: bindings, Validators: map[string]LocalResourceValidator{primitive.ToolfeedKind: ValidateToolfeedDefinition, "datasource": ValidateDatasourceDefinition}})
	require.NoError(t, err)
	for i := 0; i < 4; i++ {
		for _, d := range defs {
			u, err := identity.ParseResourceURI(d.URI)
			require.NoError(t, err)
			got, err := p.Get(ctx, u.Kind, primitive.GetRequest{URI: d.URI})
			require.NoError(t, err)
			require.NotEmpty(t, got.Resource.Definition)
		}
	}
	require.EqualValues(t, 1, feedLoads.Load())
	require.EqualValues(t, 1, dsLoads.Load(), "warm reads must not reload either authoring kind")
	require.EqualValues(t, len(defs), snapshot.CompileCount())
	require.NoError(t, os.WriteFile(file, append(body, []byte("title: changed\n")...), 0600))
	got, err := p.Get(ctx, "datasource", primitive.GetRequest{URI: "datasource://workspace/spend"})
	require.Error(t, err)
	require.Nil(t, got)
	require.EqualValues(t, 1, dsLoads.Load(), "immutable changes deny rather than rebuild")
}
