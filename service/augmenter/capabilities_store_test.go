package augmenter

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	auth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/capability"
	"github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

type persistentCapabilityFinder struct {
	capabilityTestFinder
	identity string
}

func (f persistentCapabilityFinder) EmbeddingCacheIdentity(_ context.Context, model string) (string, error) {
	return model + ":" + f.identity, nil
}

type observedCapabilityStateStore struct {
	workspace.StateStore
	mu    sync.Mutex
	reads int
}

func (s *observedCapabilityStateStore) StatePath(ctx context.Context, scope string) (string, error) {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	return s.StateStore.StatePath(ctx, scope)
}

func persistentCapabilityRequest() CapabilityRequest {
	candidates := capabilityTestCandidates()
	candidates[0].CacheScope = "public"
	candidates[2].CacheScope = "public"
	candidates[1].CacheScope = "user"
	candidates[3].CacheScope = "conversation"
	candidates[4].CacheScope = "conversation"
	return CapabilityRequest{Model: "fixture", Query: "calendar", Limit: 2, Candidates: candidates}
}
func newPersistentCapabilityService(store workspace.StateStore, identity string) (*Service, *capabilityTestEmbedder) {
	embedder := &capabilityTestEmbedder{}
	return New(persistentCapabilityFinder{capabilityTestFinder{embedder}, identity}, WithCapabilityStateStore(store)), embedder
}
func capabilitySnapshotFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	}))
	return files
}

func TestCapabilitySnapshotsReuseAcrossRestartAndRespectVisibility(t *testing.T) {
	root := t.TempDir()
	store := &observedCapabilityStateStore{StateStore: fsstore.NewStateStore(root)}
	service, first := newPersistentCapabilityService(store, "model-v1")
	require.Zero(t, store.reads, "service construction must not scan or reconstruct persistent metadata")
	ctx := auth.WithBearer(capabilityTestContext("alice", "one"), "synthetic-credential-never-persist")
	request := persistentCapabilityRequest()
	expected, err := service.RankCapabilities(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 4, first.count())
	files := capabilitySnapshotFiles(t, root)
	require.Len(t, files, 3, "only the three requested metadata partitions are persisted; queries stay ephemeral")
	for _, path := range files {
		require.True(t, strings.HasPrefix(path, filepath.Join(root, "discovery", "embeddings", "v1")))
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, secret := range []string{"alice", "calendar", "scheduling", "synthetic-credential", "fixture", "review", "owner-a"} {
			require.NotContains(t, strings.ToLower(string(raw)), strings.ToLower(secret), "snapshots contain vectors and opaque digests only")
		}
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, fs.FileMode(0600), info.Mode().Perm())
		info, err = os.Stat(filepath.Dir(path))
		require.NoError(t, err)
		require.Equal(t, fs.FileMode(0700), info.Mode().Perm())
	}
	restarted, second := newPersistentCapabilityService(store, "model-v1")
	result, err := restarted.RankCapabilities(ctx, request)
	require.NoError(t, err)
	require.Equal(t, expected, result)
	require.Equal(t, 1, second.count(), "restart embeds only the ephemeral query and loads retained namespace vectors")
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		calls int
	}{
		{"different user", capabilityTestContext("bob", "one"), 3},
		{"new conversation", capabilityTestContext("alice", "two"), 2},
		{"refreshed credential", auth.WithBearer(ctx, "refreshed-synthetic-credential"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh, embedder := newPersistentCapabilityService(store, "model-v1")
			_, err := fresh.RankCapabilities(tc.ctx, request)
			require.NoError(t, err)
			require.Equal(t, tc.calls, embedder.count(), "public snapshots share globally; user and conversation snapshots retain their original visibility fences")
		})
	}
	changed, embedder := newPersistentCapabilityService(store, "model-v2")
	_, err = changed.RankCapabilities(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 4, embedder.count(), "model configuration digest changes must not reuse incompatible vectors")
	request.Candidates[1].Description += " updated"
	changed, embedder = newPersistentCapabilityService(store, "model-v1")
	_, err = changed.RankCapabilities(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 2, embedder.count(), "only changed content is regenerated on restart")
}

func TestCapabilitySnapshotCorruptionRegeneratesOnlyNeededPartition(t *testing.T) {
	for _, corruption := range []string{"truncated", "checksum", "dimensions", "wrong key"} {
		t.Run(corruption, func(t *testing.T) {
			root := t.TempDir()
			store := fsstore.NewStateStore(root)
			service, _ := newPersistentCapabilityService(store, "model-v1")
			ctx := capabilityTestContext("alice", "one")
			request := persistentCapabilityRequest()
			expected, err := service.RankCapabilities(ctx, request)
			require.NoError(t, err)
			files := capabilitySnapshotFiles(t, root)
			require.Len(t, files, 3)
			raw, err := os.ReadFile(files[0])
			require.NoError(t, err)
			if corruption == "truncated" {
				raw = []byte("{unfinished")
			} else {
				var snapshot capabilitySnapshot
				require.NoError(t, json.Unmarshal(raw, &snapshot))
				switch corruption {
				case "checksum":
					snapshot.Digest = "corrupt"
				case "dimensions":
					snapshot.Dimensions++
				case "wrong key":
					snapshot.Key = strings.Repeat("0", 64)
				}
				raw, err = json.Marshal(snapshot)
				require.NoError(t, err)
			}
			require.NoError(t, os.WriteFile(files[0], raw, 0600))
			restarted, embedder := newPersistentCapabilityService(store, "model-v1")
			result, err := restarted.RankCapabilities(ctx, request)
			require.NoError(t, err)
			require.Equal(t, expected, result)
			require.Equal(t, 2, embedder.count(), "one query and one corrupt namespace regenerate; other namespaces reuse their snapshots")
			for _, path := range capabilitySnapshotFiles(t, root) {
				require.NotContains(t, filepath.Base(path), ".vectors-", "atomic temporary files must not remain after publication")
			}
		})
	}
}

func TestCapabilitySnapshotsDefaultToConfiguredRuntimeStateRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_STATE_PATH", root)
	service, embedder := newPersistentCapabilityService(nil, "model-v1")
	_, err := service.RankCapabilities(capabilityTestContext("alice", "one"), persistentCapabilityRequest())
	require.NoError(t, err)
	require.Equal(t, 4, embedder.count())
	require.Len(t, capabilitySnapshotFiles(t, root), 3)
}

type unsupportedCapabilityStateStore struct{ calls atomic.Int64 }

func (s *unsupportedCapabilityStateStore) StatePath(context.Context, string) (string, error) {
	s.calls.Add(1)
	return "object://example.invalid/state", nil
}
func (*unsupportedCapabilityStateStore) StateRoot(context.Context) (string, error) {
	return "object://example.invalid/state", nil
}
func TestUnsupportedCapabilityStateBackendRetainsMemoryCache(t *testing.T) {
	store := &unsupportedCapabilityStateStore{}
	service, embedder := newPersistentCapabilityService(store, "model-v1")
	ctx := capabilityTestContext("alice", "one")
	request := persistentCapabilityRequest()
	_, err := service.RankCapabilities(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 4, embedder.count())
	require.Equal(t, int64(3), store.calls.Load())
	_, err = service.RankCapabilities(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 4, embedder.count(), "unsupported persistence must not discard ordinary retained memory vectors")
}

func BenchmarkCapabilityRankingRestartSnapshots(b *testing.B) {
	root := b.TempDir()
	store := fsstore.NewStateStore(root)
	request := persistentCapabilityRequest()
	request.Candidates = make([]capability.Candidate, 1000)
	for i := range request.Candidates {
		request.Candidates[i] = capability.Candidate{Kind: "tool", Name: fmt.Sprintf("calendar-%d", i), Description: "Synthetic calendar operation", Namespace: fmt.Sprintf("namespace-%d", i%10), CacheScope: "public"}
	}
	ctx := capabilityTestContext("alice", "one")
	service, _ := newPersistentCapabilityService(store, "model-v1")
	if _, err := service.RankCapabilities(ctx, request); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		restarted, embedder := newPersistentCapabilityService(store, "model-v1")
		if _, err := restarted.RankCapabilities(ctx, request); err != nil {
			b.Fatal(err)
		}
		if embedder.count() != 1 {
			b.Fatalf("metadata reembedded after restart: %d calls", embedder.count())
		}
	}
}
