package workspace

import (
	"bytes"
	"context"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/viant/afs"
	_ "github.com/viant/afs/embed"
	"github.com/viant/afs/file"
	"github.com/viant/afs/url"
)

const defaultConfigYAML = "models: []\nagents: []\n"

var bootstrapAssetsFS iofs.FS = defaultWorkspaceFS
var bootstrapAssetsPrefix = "defaults"
var bootstrapTemplateVars map[string]string

type BootstrapTemplateContext struct {
	WorkspaceRoot string
	RuntimeRoot   string
	StateRoot     string
	HomeDir       string
	TmpDir        string
	OS            string
	Arch          string
	PathSeparator string
	Vars          map[string]string
}

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

// SetBootstrapAssetsFS overrides the embedded bootstrap assets source.
// This keeps bootstrap logic generic while allowing callers to provide their
// own embedded defaults (agents, wrappers, templates, etc.) without changing
// core bootstrap code.
func SetBootstrapAssetsFS(srcFS iofs.FS, prefix string) {
	defaultsMu.Lock()
	defer defaultsMu.Unlock()
	if srcFS == nil {
		bootstrapAssetsFS = defaultWorkspaceFS
		bootstrapAssetsPrefix = "defaults"
		return
	}
	bootstrapAssetsFS = srcFS
	bootstrapAssetsPrefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if bootstrapAssetsPrefix == "" {
		bootstrapAssetsPrefix = "."
	}
}

// SetBootstrapTemplateVars sets caller-provided template variables that are
// available to embedded bootstrap assets via `.Vars`.
func SetBootstrapTemplateVars(vars map[string]string) {
	defaultsMu.Lock()
	defer defaultsMu.Unlock()
	if len(vars) == 0 {
		bootstrapTemplateVars = nil
		return
	}
	cloned := make(map[string]string, len(vars))
	for k, v := range vars {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		cloned[k] = v
	}
	bootstrapTemplateVars = cloned
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
		KindSkill,
		KindTool,
		KindToolBundle,
		KindToolInstructions,
		KindTemplate,
		KindTemplateBundle,
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

	defaultsMu.Lock()
	seedFS := bootstrapAssetsFS
	seedPrefix := bootstrapAssetsPrefix
	vars := cloneBootstrapTemplateVarsLocked()
	defaultsMu.Unlock()
	_ = SeedFromFS(ctx, fs, root, seedFS, seedPrefix, vars)
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
func SeedFromFS(ctx context.Context, afsSvc afs.Service, root string, srcFS iofs.FS, prefix string, vars ...map[string]string) error {
	if afsSvc == nil || srcFS == nil || strings.TrimSpace(root) == "" {
		return nil
	}
	root = abs(root)
	templateVars := map[string]string{}
	if len(vars) > 0 {
		for _, item := range vars {
			for k, v := range item {
				templateVars[k] = v
			}
		}
	}
	renderCtx := bootstrapTemplateContext(root, templateVars)
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
		rel = bootstrapTargetRel(rel)
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
		if strings.HasSuffix(path, ".tmpl") {
			rendered, err := renderBootstrapTemplate(path, data, renderCtx)
			if err != nil {
				return fmt.Errorf("render template %q: %w", path, err)
			}
			data = rendered
		}
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create dir for %q: %w", target, err)
		}
		targetURL := url.Normalize(target, file.Scheme)
		if err = afsSvc.Upload(ctx, targetURL, bootstrapTargetMode(rel), bytes.NewReader(data)); err != nil {
			return fmt.Errorf("write target %q: %w", target, err)
		}
		return nil
	})
}

func cloneBootstrapTemplateVarsLocked() map[string]string {
	if len(bootstrapTemplateVars) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(bootstrapTemplateVars))
	for k, v := range bootstrapTemplateVars {
		cloned[k] = v
	}
	return cloned
}

func bootstrapTemplateContext(root string, vars map[string]string) BootstrapTemplateContext {
	homeDir, _ := os.UserHomeDir()
	tmpDir := os.TempDir()
	cloned := map[string]string{}
	for k, v := range vars {
		cloned[k] = v
	}
	workspaceRoot := abs(root)
	runtimeRoot := workspaceRoot
	if env := os.Getenv("AGENTLY_RUNTIME_ROOT"); strings.TrimSpace(env) != "" {
		runtimeRoot = abs(strings.ReplaceAll(strings.TrimSpace(env), "${workspaceRoot}", workspaceRoot))
	}
	stateRoot := filepath.Join(runtimeRoot, "state")
	if env := os.Getenv("AGENTLY_STATE_PATH"); strings.TrimSpace(env) != "" {
		v := strings.TrimSpace(env)
		v = strings.ReplaceAll(v, "${workspaceRoot}", workspaceRoot)
		v = strings.ReplaceAll(v, "${runtimeRoot}", runtimeRoot)
		stateRoot = abs(v)
	}
	return BootstrapTemplateContext{
		WorkspaceRoot: workspaceRoot,
		RuntimeRoot:   runtimeRoot,
		StateRoot:     stateRoot,
		HomeDir:       strings.TrimSpace(homeDir),
		TmpDir:        strings.TrimSpace(tmpDir),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		PathSeparator: string(os.PathSeparator),
		Vars:          cloned,
	}
}

func bootstrapTargetRel(rel string) string {
	if strings.HasSuffix(rel, ".tmpl") {
		return strings.TrimSuffix(rel, ".tmpl")
	}
	return rel
}

func bootstrapTargetMode(rel string) os.FileMode {
	clean := filepath.ToSlash(strings.TrimSpace(rel))
	if clean == "" {
		return file.DefaultFileOsMode
	}
	if strings.HasPrefix(clean, "bin/") {
		return 0o755
	}
	return file.DefaultFileOsMode
}

func renderBootstrapTemplate(name string, body []byte, ctx BootstrapTemplateContext) ([]byte, error) {
	tpl, err := template.New(filepath.Base(name)).Option("missingkey=error").Parse(string(body))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, ctx); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
