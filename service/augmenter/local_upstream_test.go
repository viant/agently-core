package augmenter

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs/url"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/embedius/indexer/fs"
	"github.com/viant/embedius/indexer/fs/splitter"
	"github.com/viant/embedius/matching"
	"github.com/viant/embedius/metadata"
	"github.com/viant/embedius/vectordb/sqlitevec"
)

func TestUpstreamSyncConfig_LocalRoot(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tempDir)
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")

	matcher := matching.New()
	splitterFactory := splitter.NewFactory(4096)
	fsIndexer := fs.New(tempDir, "test", matcher, splitterFactory)
	aug := &DocsAugmenter{fsIndexer: fsIndexer, store: &sqlitevec.Store{}}

	disabled := false
	svc := New(nil, WithLocalUpstreams(
		[]LocalRoot{{ID: "docs", URI: "workspace://localhost/docs", UpstreamRef: "local"}},
		[]LocalUpstream{{Name: "local", Enabled: &disabled}},
	))

	base := url.Normalize(workspace.Root(), "file")
	location := url.Join(base, "docs")

	cfg := svc.upstreamSyncConfig(context.Background(), location, aug)
	require.NotNil(t, cfg)
	require.False(t, cfg.Enabled)
}

func TestUpstreamSyncConfig_ContextRoots(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tempDir)
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")

	matcher := matching.New()
	splitterFactory := splitter.NewFactory(4096)
	fsIndexer := fs.New(tempDir, "test", matcher, splitterFactory)
	aug := &DocsAugmenter{fsIndexer: fsIndexer, store: &sqlitevec.Store{}}

	disabled := false
	svc := New(nil, WithLocalUpstreams(
		nil,
		[]LocalUpstream{{Name: "local", Enabled: &disabled}},
	))

	base := url.Normalize(workspace.Root(), "file")
	location := url.Join(base, "docs")
	ctx := WithLocalRoots(context.Background(), []LocalRoot{{URI: "workspace://localhost/docs", UpstreamRef: "local"}})

	cfg := svc.upstreamSyncConfig(ctx, location, aug)
	require.NotNil(t, cfg)
	require.False(t, cfg.Enabled)
}

func TestResolveMetadataConfig_LocalRootWithoutUpstream(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tempDir)
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")

	expected := metadata.Config{
		Extractor: "yaml-frontmatter",
		Fields: map[string]string{
			"document.title": "title",
			"source.url":     "sourceUrl",
		},
	}
	svc := New(nil)
	ctx := WithLocalRoots(context.Background(), []LocalRoot{{
		ID:       "docs",
		URI:      "workspace://localhost/docs",
		Metadata: expected,
	}})
	location := url.Join(url.Normalize(workspace.Root(), "file"), "docs/article.md")

	actual := svc.resolveMetadataConfig(ctx, location)
	require.Equal(t, expected, actual)
}

func TestIndexRefreshInterval(t *testing.T) {
	svc := New(nil)
	require.Equal(t, time.Hour, svc.indexRefreshInterval(context.Background(), "workspace://localhost/docs"))

	ctx := WithLocalRoots(context.Background(), []LocalRoot{{
		URI:                    "workspace://localhost/docs",
		RefreshIntervalSeconds: 600,
	}})
	require.Equal(t, 10*time.Minute, svc.indexRefreshInterval(ctx, "workspace://localhost/docs/article.md"))
}
