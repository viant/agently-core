package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	_ "github.com/viant/afs/embed"
	"github.com/viant/afs/file"
	"github.com/viant/afs/url"
)

const defaultConfigYAML = "models: []\nagents: []\n"

// BootstrapHook customizes workspace bootstrap behavior.
// When configured via SetBootstrapHook, it receives the resolved workspace root
// and can decide what (if anything) to populate.
type BootstrapHook func(ctx context.Context, fs afs.Service, root string)

// SetBootstrapHook sets a process-wide workspace bootstrap hook.
// When set, default bootstrapping is skipped and the hook is responsible for
// creating any required workspace files/directories.
func SetBootstrapHook(hook BootstrapHook) {
	defaultsMu.Lock()
	bootstrapHook = hook
	defaultsMu.Unlock()
}

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

// IsEmptyWorkspace reports whether the active workspace contains no meaningful files.
// It ignores placeholder .keep files and empty directories.
func IsEmptyWorkspace(fs afs.Service) (bool, error) {
	return IsEmptyWorkspaceAt(context.Background(), fs, Root())
}

// IsEmptyWorkspaceAt reports whether the supplied workspace root contains no
// meaningful files. It ignores placeholder .keep files and empty directories.
func IsEmptyWorkspaceAt(ctx context.Context, fs afs.Service, root string) (bool, error) {
	if fs == nil || strings.TrimSpace(root) == "" {
		return true, nil
	}
	root = abs(root)
	if root == "" {
		return true, nil
	}
	if ok, err := fs.Exists(ctx, root); err != nil {
		return false, err
	} else if !ok {
		return true, nil
	}

	return isDirEffectivelyEmpty(ctx, fs, root)
}

func isDirEffectivelyEmpty(ctx context.Context, fs afs.Service, dir string) (bool, error) {
	objects, err := fs.List(ctx, dir)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	for _, object := range objects {
		name := filepath.Base(object.Name())
		if name == ".keep" {
			continue
		}
		if object.IsDir() && name == filepath.Base(dir) {
			// afs may include the directory itself in listing results.
			continue
		}
		if !object.IsDir() {
			return false, nil
		}
		nextDir := object.URL()
		if strings.TrimSpace(nextDir) == "" {
			nextDir = object.Name()
		}
		if !filepath.IsAbs(nextDir) && !strings.Contains(nextDir, "://") {
			nextDir = filepath.Join(dir, filepath.Base(nextDir))
		}
		empty, err := isDirEffectivelyEmpty(ctx, fs, nextDir)
		if err != nil {
			return false, err
		}
		if !empty {
			return false, nil
		}
	}
	return true, nil
}
