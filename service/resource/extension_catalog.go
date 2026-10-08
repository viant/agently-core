package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
)

// InternalProviderIdentity is transport provenance, not a namespace default.
const InternalProviderIdentity = "internal"

// ExtensionLoader materializes the native definition (including its imports or
// package files) through reader. It must omit credentials and execution secrets.
// It receives a trusted inventory path, never a caller-selected path. Windows
// must retain their target bundle; intents, templates and skills retain their
// native schemas. Validators remain mandatory in LocalConfig.
type ExtensionLoader func(context.Context, *ExtensionReader, string) (json.RawMessage, error)

// ExtensionDirectory explicitly binds a folder to a logical namespace. Multiple
// directories may contribute to one namespace; collisions are errors, never an
// order-dependent override. Suffixes identify definition entry points, such as
// .yaml/.yml or /SKILL.md. A package suffix uses its containing folder as name.
type ExtensionDirectory struct {
	Kind          string
	Namespace     string
	Directory     string
	FormatVersion int64
	Suffixes      []string
	Load          ExtensionLoader
}

// ExtensionReader confines native loader reads to a trusted workspace directory.
// It deliberately exposes no absolute path, write API or unconstrained fs.FS.
// Import paths use slash-separated canonical relative paths.
type ExtensionReader struct{ root *os.Root }

func extensionPath(value string) bool {
	return value != "" && value != "." && fs.ValidPath(value) && !strings.ContainsAny(value, "\\:\x00")
}

func (r *ExtensionReader) check(name string) error {
	if r == nil || r.root == nil || !extensionPath(name) {
		return identity.ErrResourceDenied
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := r.root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return identity.ErrResourceDenied
		}
		if i < len(parts)-1 && !info.IsDir() {
			return identity.ErrResourceDenied
		}
	}
	return nil
}

// ReadFile rejects links and special files. os.Root also confines a symlink
// replacement between the checks and open, including an escaping parent link.
func (r *ExtensionReader) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, identity.ErrResourceDenied
	}
	if err := r.check(name); err != nil {
		return nil, err
	}
	file, err := r.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, identity.ErrResourceDenied
	}
	// Bound the definition/package read before allocating; this is not a general
	// purpose file server. Native loaders may read multiple bounded package files.
	const maxFileBytes = 16 << 20
	if info.Size() > maxFileBytes {
		return nil, fmt.Errorf("extension file too large")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("extension file too large")
	}
	if err := r.check(name); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

type extensionSource struct {
	root, file, uri string
	load            ExtensionLoader
}

func (s *extensionSource) read(ctx context.Context, uri identity.ResourceURI) (json.RawMessage, error) {
	if ctx == nil || ctx.Err() != nil || uri.String() != s.uri {
		return nil, identity.ErrResourceDenied
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	reader := &ExtensionReader{root: root}
	if err := reader.check(s.file); err != nil {
		return nil, err
	}
	raw, err := s.load(ctx, reader, s.file)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("native extension loader returned invalid JSON")
	}
	if err := reader.check(s.file); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), raw...), nil
}
func (s *extensionSource) Candidates(ctx context.Context, uri identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	raw, err := s.read(ctx, uri)
	if err != nil {
		return nil, err
	}
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(raw)}}, nil
}
func (s *extensionSource) ReadCandidate(ctx context.Context, uri identity.ResourceURI, candidate identity.ResourceCandidate) (json.RawMessage, error) {
	if !candidate.Valid() || candidate.Kind != identity.WorkingCandidate {
		return nil, identity.ErrResourceDenied
	}
	raw, err := s.read(ctx, uri)
	if err != nil {
		return nil, err
	}
	if identity.ContentFingerprint(raw) != candidate.ContentFingerprint {
		return nil, identity.ErrResourceStale
	}
	return raw, nil
}

// ExtensionBindings indexes configured folders once. Bytes are loaded afresh on
// every candidate/read, without a DB stamp or shared user cache. A host rebuilds
// the inventory to add/remove definitions, then atomically replaces its provider.
// This grants no authority: both namespace authorizer and revision policy remain
// trusted host responsibilities. Missing configured folders fail closed.
func ExtensionBindings(ctx context.Context, rootPath string, directories []ExtensionDirectory, policy LocalRevisionPolicyFactory) ([]LocalResourceBinding, error) {
	if ctx == nil || ctx.Err() != nil || strings.TrimSpace(rootPath) == "" || len(directories) == 0 || policy == nil {
		return nil, fmt.Errorf("extension inventory requires root, directories and revision policy")
	}
	rootPath, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	reader := &ExtensionReader{root: root}
	indexed := map[string]LocalResourceBinding{}
	for _, directory := range directories {
		probe := identity.ResourceURI{Kind: directory.Kind, Namespace: directory.Namespace, Name: "probe"}
		if !probe.Valid() || primitive.Plural(directory.Kind) == "" || !extensionPath(directory.Directory) || directory.FormatVersion < 1 || directory.Load == nil {
			return nil, fmt.Errorf("invalid extension directory")
		}
		suffixes := append([]string(nil), directory.Suffixes...)
		if len(suffixes) == 0 {
			suffixes = []string{".yaml", ".yml"}
		}
		for _, suffix := range suffixes {
			if suffix == "" || strings.ContainsAny(suffix, "\\:\x00") || strings.Contains(suffix, "..") || suffix[0] != '.' && suffix[0] != '/' {
				return nil, fmt.Errorf("invalid extension definition suffix")
			}
		}
		sort.Slice(suffixes, func(i, j int) bool {
			if len(suffixes[i]) != len(suffixes[j]) {
				return len(suffixes[i]) > len(suffixes[j])
			}
			return suffixes[i] < suffixes[j]
		})
		if err := reader.check(directory.Directory); err != nil {
			return nil, err
		}
		info, err := root.Stat(directory.Directory)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("extension directory must be a directory")
		}
		err = fs.WalkDir(root.FS(), directory.Directory, func(file string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("extension inventory rejects symbolic links")
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("extension inventory rejects special files")
			}
			relative := strings.TrimPrefix(file, directory.Directory+"/")
			name := ""
			for _, suffix := range suffixes {
				if strings.HasSuffix(relative, suffix) {
					name = strings.TrimSuffix(relative, suffix)
					break
				}
			}
			if name == "" {
				return nil
			}
			uri := identity.ResourceURI{Kind: directory.Kind, Namespace: directory.Namespace, Name: name}
			if !uri.Valid() {
				return fmt.Errorf("invalid extension resource path")
			}
			if _, exists := indexed[uri.String()]; exists {
				return fmt.Errorf("duplicate extension resource %s", uri.String())
			}
			source := &extensionSource{root: rootPath, file: file, uri: uri.String(), load: directory.Load}
			binding := LocalResourceBinding{URI: uri.String(), Title: path.Base(name), FormatVersion: directory.FormatVersion}
			binding.Resolver = func(ctx context.Context, actor identity.VerifiedActor, action string) (*identity.ResourceResolver, error) {
				revisionPolicy, err := policy(ctx, actor, uri, action)
				if err != nil {
					return nil, err
				}
				if revisionPolicy == nil {
					return nil, identity.ErrResourceDenied
				}
				return &identity.ResourceResolver{ProviderIdentity: InternalProviderIdentity, Source: source, Policy: revisionPolicy}, nil
			}
			indexed[uri.String()] = binding
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(indexed))
	for name := range indexed {
		names = append(names, name)
	}
	sort.Strings(names)
	bindings := make([]LocalResourceBinding, 0, len(names))
	for _, name := range names {
		bindings = append(bindings, indexed[name])
	}
	return bindings, nil
}

// NewInternalProvider binds the confined inventory to the ordinary local MCP
// provider. Identity always remains internal while declared namespaces survive.
func NewInternalProvider(ctx context.Context, root string, directories []ExtensionDirectory, policy LocalRevisionPolicyFactory, config LocalConfig) (*LocalProvider, error) {
	if config.ProviderIdentity != "" && config.ProviderIdentity != InternalProviderIdentity || len(config.Bindings) != 0 {
		return nil, fmt.Errorf("internal provider requires its own inventory")
	}
	bindings, err := ExtensionBindings(ctx, root, directories, policy)
	if err != nil {
		return nil, err
	}
	config.ProviderIdentity, config.Bindings = InternalProviderIdentity, bindings
	return NewLocalProvider(config)
}
