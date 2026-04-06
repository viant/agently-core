package workspace

import (
	"bytes"
	"context"
	"fmt"
	iofs "io/fs"
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
// It receives a store helper bound to the resolved workspace root.
type BootstrapHook func(store *BootstrapStore) error

// BootstrapStore provides hook helpers expected by bootstrap callers.
// It intentionally exposes only operations needed during bootstrap.
type BootstrapStore struct {
	root string
	fs   afs.Service
}

// Root returns the workspace root path used by this bootstrap store.
func (s *BootstrapStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// SeedFromFS copies files from srcFS/prefix into workspace root when missing.
func (s *BootstrapStore) SeedFromFS(ctx context.Context, srcFS iofs.FS, prefix string) error {
	if s == nil {
		return nil
	}
	return SeedFromFS(ctx, s.fs, s.root, srcFS, prefix)
}

// SetBootstrapHook sets a process-wide workspace bootstrap hook.
// When set, default bootstrapping is skipped and the hook is responsible for
// creating any required workspace files/directories.
func SetBootstrapHook(hook BootstrapHook) {
	defaultsMu.Lock()
	bootstrapHook = hook
	defaultsByRoot = map[string]bool{}
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
	if runBootstrapHook(fs, root) {
		return
	}
	baseURL := url.Normalize(root, file.Scheme)

	// Ensure key workspace directories exist.
	dirs := []string{
		KindAgent,
		KindModel,
		KindTool,
		KindToolBundle,
		KindToolInstructions,
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

func runBootstrapHook(fs afs.Service, root string) bool {
	defaultsMu.Lock()
	hook := bootstrapHook
	defaultsMu.Unlock()

	if hook != nil {
		if err := hook(&BootstrapStore{root: root, fs: fs}); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "workspace bootstrap hook error: %v\n", err)
		}
		return true
	}
	return false
}

// SeedFromFS copies files from srcFS/prefix into workspace root when the target
// file does not already exist. Existing files are never overwritten.
func SeedFromFS(ctx context.Context, afsSvc afs.Service, root string, srcFS iofs.FS, prefix string) error {
	if afsSvc == nil || srcFS == nil || strings.TrimSpace(root) == "" {
		return nil
	}
	root = abs(root)
	start := strings.Trim(strings.TrimSpace(prefix), "/")
	if start == "" {
		start = "."
	}
	if _, err := iofs.Stat(srcFS, start); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("seed source %q: %w", start, err)
	}

	return iofs.WalkDir(srcFS, start, func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, start)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(root, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if exists, err := afsSvc.Exists(ctx, target); err != nil {
			return fmt.Errorf("check target %q: %w", target, err)
		} else if exists {
			return nil
		}
		data, err := iofs.ReadFile(srcFS, path)
		if err != nil {
			return fmt.Errorf("read source %q: %w", path, err)
		}
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create dir for %q: %w", target, err)
		}
		targetURL := url.Normalize(target, file.Scheme)
		if err = afsSvc.Upload(ctx, targetURL, file.DefaultFileOsMode, bytes.NewReader(data)); err != nil {
			return fmt.Errorf("write target %q: %w", target, err)
		}
		return nil
	})
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
		if object.IsDir() && isBootstrapIgnorableDir(name) {
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

func isBootstrapIgnorableDir(name string) bool {
	switch strings.TrimSpace(name) {
	case KindMCP, KindOAuth, KindWorkflow:
		return true
	default:
		return false
	}
}
