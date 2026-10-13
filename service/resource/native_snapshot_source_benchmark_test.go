package resource

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

// The former per-read manifest check is reproduced ONLY as a benchmark baseline.
// Both modes use the same captured, validated original window and actual
// LocalProvider -> SDK in-memory client -> Manager -> Gateway Get call path.
type nativeManifestBaselineSource struct {
	*NativeAssetSnapshot
	paths  []string
	checks atomic.Int64
}

func (s *nativeManifestBaselineSource) check() error {
	for _, path := range s.paths {
		s.checks.Add(1)
		if _, err := os.Lstat(path); err != nil {
			return err
		}
	}
	return nil
}
func (s *nativeManifestBaselineSource) Candidates(ctx context.Context, uri identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	return s.NativeAssetSnapshot.Candidates(ctx, uri)
}
func (s *nativeManifestBaselineSource) ReadCandidate(ctx context.Context, uri identity.ResourceURI, candidate identity.ResourceCandidate) (json.RawMessage, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	return s.NativeAssetSnapshot.ReadCandidate(ctx, uri, candidate)
}
func (s *nativeManifestBaselineSource) CheckCandidate(ctx context.Context, uri identity.ResourceURI, candidate identity.ResourceCandidate) error {
	if err := s.check(); err != nil {
		return err
	}
	return s.NativeAssetSnapshot.CheckCandidate(ctx, uri, candidate)
}

func TestNativeSnapshotOriginalWorkspaceSourceCost(t *testing.T) {
	root := os.Getenv("AGENTLY_NATIVE_SOURCE_BENCH_ROOT")
	if root == "" {
		t.Skip("opt-in readonly original workspace source benchmark")
	}
	ctx := context.Background()
	digest := stewardAssetDigest(t, root)
	defer func() { require.Equal(t, digest, stewardAssetDigest(t, root), "original assets must remain unchanged") }()
	var paths []string
	var files int
	require.NoError(t, filepath.WalkDir(filepath.Join(root, "extension/forge"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		if !entry.IsDir() {
			files++
		}
		return nil
	}))
	binding := workspacewindow.ResourceBinding{WindowKey: "advertiserList", URI: "window://workspace/advertiserList"}
	defs, err := SnapshotWindowDefinitions(ctx, root, []workspacewindow.ResourceBinding{binding}, nil)
	require.NoError(t, err)
	snapshot, err := NewNativeAssetSnapshot(ctx, root, defs)
	require.NoError(t, err)
	uri, err := identity.ParseResourceURI(binding.URI)
	require.NoError(t, err)
	candidates, err := snapshot.Candidates(ctx, uri)
	require.NoError(t, err)
	original, err := snapshot.ReadCandidate(ctx, uri, candidates[0])
	require.NoError(t, err)
	t.Logf("ORIGINAL_SOURCE files=%d formerInventoryEntries=%d payloadBytes=%d startupCompiles=%d", files, len(paths), len(original), snapshot.CompileCount())
	actor, verify := PublicReadActors()
	for _, mode := range []string{"former-manifest-baseline", "captured-map"} {
		baseline := &nativeManifestBaselineSource{NativeAssetSnapshot: snapshot}
		if mode == "former-manifest-baseline" {
			baseline.paths = paths
		}
		bindings, err := snapshot.Bindings("source-benchmark", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
			return authoredTestPolicy{a}, nil
		})
		require.NoError(t, err)
		resolve := bindings[0].Resolver
		bindings[0].Resolver = func(ctx context.Context, a identity.VerifiedActor, action string) (*identity.ResourceResolver, error) {
			r, err := resolve(ctx, a, action)
			if err == nil {
				r.Source = baseline
			}
			return r, err
		}
		provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "source-benchmark", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]LocalResourceValidator{"window": ValidateWindowBundle}})
		require.NoError(t, err)
		cli, err := NewLocalMCPClient(provider)
		require.NoError(t, err)
		mgr, err := manager.New(nil)
		require.NoError(t, err)
		require.NoError(t, mgr.RegisterLocal(ctx, "source-benchmark", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider.ProviderIdentity()}, func(context.Context) (mcpclient.Interface, error) { return cli, nil }))
		gateway := NewGateway(mgr, actor, verify, "consumer")
		connection, err := gateway.ConnectionForProvider(ctx, "source-benchmark")
		require.NoError(t, err)
		for repeat := 1; repeat <= 3; repeat++ {
			baseline.checks.Store(0)
			startCount := snapshot.CompileCount()
			started := time.Now()
			for i := 0; i < 20; i++ {
				got, err := Get[json.RawMessage](ctx, gateway, connection, identity.ResourceRef{URI: uri.String()}, nil)
				require.NoError(t, err)
				require.Equal(t, original, got.Definition)
			}
			elapsed := time.Since(started)
			record, _ := json.Marshal(map[string]any{"mode": mode, "repeat": repeat, "gets": 20, "elapsedMS": float64(elapsed) / float64(time.Millisecond), "averageMS": float64(elapsed) / float64(time.Millisecond) / 20, "filesystemMetadataCalls": baseline.checks.Load(), "recompiles": snapshot.CompileCount() - startCount, "files": files, "formerInventoryEntries": len(paths), "definitionBytes": len(original)})
			t.Log("NATIVE_SOURCE_BENCH " + string(record))
			require.Equal(t, startCount, snapshot.CompileCount())
			if mode == "captured-map" {
				require.Zero(t, baseline.checks.Load())
			}
		}
		gateway.Close()
		mgr.CloseConversation("")
	}
}
