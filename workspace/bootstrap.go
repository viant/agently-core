package workspace

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	_ "github.com/viant/afs/embed"
	"github.com/viant/afs/file"
	"github.com/viant/afs/url"
	"github.com/viant/afs"
)

const defaultConfigYAML = "models: []\nagents: []\n"

// EnsureDefault bootstraps minimal workspace defaults.
func EnsureDefault(fs afs.Service) {
	EnsureDefaultAt(context.Background(), fs, Root())
}

// EnsureDefaultAt ensures workspace root exists with a baseline config file.
// It intentionally does not seed agent/model catalogs yet.
func EnsureDefaultAt(ctx context.Context, fs afs.Service, root string) {
	if fs == nil || strings.TrimSpace(root) == "" {
		return
	}
	root = abs(root)
	baseURL := url.Normalize(root, file.Scheme)

	// Ensure key workspace directories exist.
	dirs := []string{
		KindAgent,
		KindModel,
		KindTool,
		KindToolBundle,
		KindToolHints,
		KindWorkflow,
		KindMCP,
		KindEmbedder,
		KindOAuth,
		KindFeeds,
		KindA2A,
	}
	for _, kind := range dirs {
		dirURL := url.Join(baseURL, filepath.ToSlash(kind))
		_ = fs.Upload(ctx, url.Join(dirURL, ".keep"), file.DefaultFileOsMode, bytes.NewReader(nil))
	}

	// Seed minimal config only when absent.
	configURL := url.Join(baseURL, "config.yaml")
	if ok, _ := fs.Exists(ctx, configURL); !ok {
		_ = fs.Upload(ctx, configURL, file.DefaultFileOsMode, bytes.NewReader([]byte(defaultConfigYAML)))
	}
}
