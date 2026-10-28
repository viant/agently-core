package embedder

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/embedder/provider"
)

func TestEmbeddingCacheIdentityIsStableAcrossRestartAndCredentialRefresh(t *testing.T) {
	makeFinder := func(secret, model string) *Finder {
		finder := New()
		finder.AddConfig("fixture", &provider.Config{ID: "fixture", Options: provider.Options{Provider: "openai", Model: model, URL: "https://example.invalid/embeddings", APIKeyURL: secret}})
		return finder
	}
	first, err := makeFinder("secret://first", "synthetic-model").EmbeddingCacheIdentity(context.Background(), "fixture")
	require.NoError(t, err)
	restarted, err := makeFinder("secret://refreshed", "synthetic-model").EmbeddingCacheIdentity(context.Background(), "fixture")
	require.NoError(t, err)
	require.Equal(t, first, restarted, "credential replacement must not force unchanged model vectors to regenerate")
	changed, err := makeFinder("secret://first", "synthetic-model-v2").EmbeddingCacheIdentity(context.Background(), "fixture")
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
	require.Len(t, first, 64)
	require.NotContains(t, first, "secret")
}
