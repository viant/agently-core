package executor_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	baseembed "github.com/viant/agently-core/genai/embedder/provider/base"
	"github.com/viant/agently-core/runtime/capability"
	"github.com/viant/agently-core/service/augmenter"
	"github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

type stateCapabilityEmbedder struct{}

func (stateCapabilityEmbedder) Embed(_ context.Context, texts []string) ([][]float32, int, error) {
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = []float32{1, 0}
	}
	return vectors, 0, nil
}

type stateCapabilityFinder struct{}

func (stateCapabilityFinder) Find(context.Context, string) (baseembed.Embedder, error) {
	return stateCapabilityEmbedder{}, nil
}
func (stateCapabilityFinder) Ids() []string { return []string{"fixture"} }
func (stateCapabilityFinder) EmbeddingCacheIdentity(context.Context, string) (string, error) {
	return "synthetic-stable-model", nil
}

func TestBuilderUsesConfiguredStateStoreForInjectedCapabilityAugmenter(t *testing.T) {
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	stateRoot := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	aug := augmenter.New(stateCapabilityFinder{})
	runtime, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithAugmenter(aug).WithStateStore(fsstore.NewStateStore(stateRoot)).WithSkipRegistryInitialize(true).Build(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
	_, err = aug.RankCapabilities(context.Background(), augmenter.CapabilityRequest{Model: "fixture", Query: "request", Limit: 1, Candidates: []capability.Candidate{{Kind: "tool", Name: "one", Namespace: "default", CacheScope: "public"}, {Kind: "skill", Name: "two", Namespace: "default", CacheScope: "public"}}})
	require.NoError(t, err)
	paths, err := filepath.Glob(filepath.Join(stateRoot, "discovery", "embeddings", "v1", "*", "*.json"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	require.NoDirExists(t, filepath.Join(root, "state", "discovery", "embeddings"), "the configured StateStore must own persistence instead of a second inferred location")
}
