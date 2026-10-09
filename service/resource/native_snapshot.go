package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/forge/backend/types"
)

// NativeSnapshotDefinition declares a deterministic authored-content loader.
// Load must not read actor/roles/grants/request data or make authorization
// decisions. File/import bytes and static native enrichment are its only input.
// Every authorization/lease decision remains in LocalProvider and its policy.
type NativeSnapshotDefinition struct {
	URI           string
	Title         string
	FormatVersion int64
	File          string
	Load          ExtensionLoader
}

// StaticWorkspaceWindowEnricher promises actor-independent authored enrichment.
// Mutable registry state must be refreshed for the current asset generation in
// BeforeCompile, or captured as an immutable registry for each rebuild.
type StaticWorkspaceWindowEnricher func(context.Context, *types.Window) error
type NativeSnapshotOptions struct {
	// AssetPaths is a trusted set of relative files/directories to monitor.
	// Nil preserves the workspace extension/forge inventory. Imported files
	// read by the loader remain tracked even outside these initial paths.
	AssetPaths []string
	// ImmutableUntilRestart denies changed assets rather than refreshing content
	// under host visibility/group configuration captured at startup.
	ImmutableUntilRestart bool
	// BeforeCompile refreshes only static authored dependencies, never authority.
	// It is invoked once per startup/rebuild before any definition is compiled.
	BeforeCompile func(context.Context, *ExtensionReader) error
}
type nativeFileMetadata struct {
	info  os.FileInfo
	size  int64
	mtime time.Time
	ctime string
}

func metadataOf(info os.FileInfo) nativeFileMetadata {
	change := ""
	value := reflect.ValueOf(info.Sys())
	if value.IsValid() {
		if value.Kind() == reflect.Pointer {
			value = value.Elem()
		}
		if value.IsValid() && value.Kind() == reflect.Struct {
			for _, field := range []string{"Ctim", "Ctimespec"} {
				stamp := value.FieldByName(field)
				if stamp.IsValid() && stamp.CanInterface() {
					change = fmt.Sprint(stamp.Interface())
					break
				}
			}
		}
	}
	return nativeFileMetadata{info: info, size: info.Size(), mtime: info.ModTime(), ctime: change}
}
func (a nativeFileMetadata) same(info os.FileInfo) bool {
	b := metadataOf(info)
	return os.SameFile(a.info, info) && a.info.Mode() == info.Mode() && a.size == b.size && a.mtime.Equal(b.mtime) && a.ctime == b.ctime
}

func nativeMetadataMatches(name string, expected nativeFileMetadata, info os.FileInfo) bool {
	if name == "." {
		return os.SameFile(expected.info, info) && expected.info.Mode() == info.Mode()
	}
	return expected.same(info)
}

type nativeSnapshotValue struct {
	raw         json.RawMessage
	fingerprint string
	kind        string
	version     int64
}

// NativeAssetSnapshot preloads immutable compiled content and checks cheap file
// metadata on every serve. It caches no actors, policies, grants or leases. The
// complete Forge tree (including fragments/imports/directories) is invalidated
// on changes; extra files read through reader are also tracked.
type NativeAssetSnapshot struct {
	root         string
	anchor       os.FileInfo
	definitions  []NativeSnapshotDefinition
	options      NativeSnapshotOptions
	mu           sync.Mutex
	manifest     map[string]nativeFileMetadata
	values       map[string]nativeSnapshotValue
	compilations atomic.Uint64
}

func NewNativeAssetSnapshot(ctx context.Context, root string, definitions []NativeSnapshotDefinition, options ...NativeSnapshotOptions) (*NativeAssetSnapshot, error) {
	if ctx == nil || ctx.Err() != nil || len(definitions) == 0 || len(options) > 1 {
		return nil, fmt.Errorf("native snapshot requires deterministic definitions")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	opened, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, err
	}
	anchor, err := opened.Stat(".")
	opened.Close()
	if err != nil {
		return nil, err
	}
	snapshot := &NativeAssetSnapshot{root: absolute, anchor: anchor, definitions: append([]NativeSnapshotDefinition(nil), definitions...)}
	if len(options) == 1 {
		snapshot.options = options[0]
		if options[0].AssetPaths != nil {
			if len(options[0].AssetPaths) == 0 || len(options[0].AssetPaths) > 128 {
				return nil, fmt.Errorf("native snapshot requires bounded asset paths")
			}
			seenPaths := map[string]bool{}
			for _, asset := range options[0].AssetPaths {
				if !extensionPath(asset) || seenPaths[asset] {
					return nil, fmt.Errorf("invalid or duplicate native asset path")
				}
				seenPaths[asset] = true
			}
			snapshot.options.AssetPaths = append([]string(nil), options[0].AssetPaths...)
		}
	}
	seen := map[string]bool{}
	for _, definition := range snapshot.definitions {
		uri, err := identity.ParseResourceURI(definition.URI)
		if err != nil || definition.Load == nil || !extensionPath(definition.File) || seen[definition.URI] || !(uri.Kind == "window" && definition.FormatVersion == 2 || uri.Kind == "report" && definition.FormatVersion == 1) {
			return nil, fmt.Errorf("unsupported or duplicate native snapshot definition")
		}
		seen[definition.URI] = true
	}
	if err = snapshot.rebuild(ctx); err != nil {
		return nil, err
	}
	return snapshot, nil
}
func (s *NativeAssetSnapshot) CompileCount() uint64 {
	if s == nil {
		return 0
	}
	return s.compilations.Load()
}
func (s *NativeAssetSnapshot) scan(ctx context.Context) (map[string]nativeFileMetadata, error) {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	current, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(s.anchor, current) {
		return nil, identity.ErrResourceStale
	}
	result := map[string]nativeFileMetadata{".": metadataOf(current)}
	assets := s.options.AssetPaths
	if assets == nil {
		assets = []string{"extension/forge"}
	}
	for _, asset := range assets {
		// WalkDir does not reject symlinks in the ancestors of its start path.
		// Validate each original prefix through the confined root first.
		prefix := ""
		for _, segment := range strings.Split(asset, "/") {
			if prefix != "" {
				prefix += "/"
			}
			prefix += segment
			info, err := root.Lstat(prefix)
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
				return nil, identity.ErrResourceDenied
			}
			result[prefix] = metadataOf(info)
		}
		err = fs.WalkDir(root.FS(), asset, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
				return identity.ErrResourceDenied
			}
			result[name] = metadataOf(info)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return result, err
}
func (s *NativeAssetSnapshot) changed(ctx context.Context, manifest map[string]nativeFileMetadata) (bool, error) {
	for name, expected := range manifest {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		location := s.root
		if name != "." {
			location = filepath.Join(s.root, filepath.FromSlash(name))
		}
		current, err := os.Lstat(location)
		if err != nil {
			if os.IsNotExist(err) {
				return true, nil
			}
			return false, err
		}
		if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() && !current.Mode().IsRegular() {
			return false, identity.ErrResourceDenied
		}
		if !nativeMetadataMatches(name, expected, current) {
			return true, nil
		}
	}
	return false, nil
}
func (s *NativeAssetSnapshot) rebuild(ctx context.Context) error {
	// Static compilation receives no caller values, actor, grants or request data.
	compileCtx, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(ctx, cancel)
	defer func() { stop(); cancel() }()
	ctx = compileCtx

	before, err := s.scan(ctx)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	reader := &ExtensionReader{root: root}
	defer reader.close()
	if s.options.BeforeCompile != nil {
		if err = s.options.BeforeCompile(ctx, reader); err != nil {
			return err
		}
	}
	values := map[string]nativeSnapshotValue{}
	for _, definition := range s.definitions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		raw, err := definition.Load(ctx, reader, definition.File)
		if err != nil {
			return err
		}
		uri, _ := identity.ParseResourceURI(definition.URI)
		validator := ValidateWindowBundle
		if uri.Kind == "report" {
			validator = ValidateReportEnvelope
		}
		if err = validator(definition.FormatVersion, raw); err != nil {
			return err
		}
		values[definition.URI] = nativeSnapshotValue{raw: append(json.RawMessage(nil), raw...), fingerprint: identity.ContentFingerprint(raw), kind: uri.Kind, version: definition.FormatVersion}
		s.compilations.Add(1)
	}
	// Outside-Forge imports and their parent directories are part of the same
	// immutable source generation. Reader records metadata without caching bytes.
	reader.mu.Lock()
	for name, info := range reader.dependencies {
		if expected, ok := before[name]; ok {
			if !nativeMetadataMatches(name, expected, info) {
				reader.mu.Unlock()
				return identity.ErrResourceStale
			}
		} else {
			before[name] = metadataOf(info)
		}
	}
	reader.mu.Unlock()
	if changed, err := s.changed(ctx, before); err != nil {
		return err
	} else if changed {
		return identity.ErrResourceStale
	}
	s.manifest, s.values = before, values
	return nil
}
func (s *NativeAssetSnapshot) value(ctx context.Context, uri identity.ResourceURI) (nativeSnapshotValue, error) {
	if s == nil || ctx == nil || ctx.Err() != nil || !uri.Valid() {
		return nativeSnapshotValue{}, identity.ErrResourceDenied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx); err != nil {
		return nativeSnapshotValue{}, err
	}
	value, ok := s.values[uri.String()]
	if !ok {
		return nativeSnapshotValue{}, identity.ErrResourceDenied
	}
	return value, nil
}
func (s *NativeAssetSnapshot) refreshLocked(ctx context.Context) error {
	changed, err := s.changed(ctx, s.manifest)
	if err != nil {
		return err
	}
	if changed {
		if s.options.ImmutableUntilRestart {
			return identity.ErrResourceStale
		}
		return s.rebuild(ctx)
	}
	return nil
}

// WindowIndex returns trusted metadata without loading/resolving any definition
// or permission. The caller applies declared group visibility and namespace/
// private-owner isolation freshly, then checks this shared index again.
func (s *NativeAssetSnapshot) WindowIndex(ctx context.Context) ([]primitive.ResourceState, error) {
	if s == nil || ctx == nil || ctx.Err() != nil {
		return nil, identity.ErrResourceDenied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx); err != nil {
		return nil, err
	}
	result := []primitive.ResourceState{}
	for _, definition := range s.definitions {
		uri, _ := identity.ParseResourceURI(definition.URI)
		if uri.Kind != "window" {
			continue
		}
		value := s.values[definition.URI]
		result = append(result, primitive.ResourceState{Kind: "window", Namespace: uri.Namespace, Name: uri.Name, URI: uri.String(), Title: definition.Title, Lifecycle: identity.WorkingCandidate, Revision: identity.WorkingCandidate, FormatVersion: definition.FormatVersion, ContentFingerprint: value.fingerprint})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].URI < result[j].URI })
	return result, nil
}
func (s *NativeAssetSnapshot) Candidates(ctx context.Context, uri identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	value, err := s.value(ctx, uri)
	if err != nil {
		return nil, err
	}
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: value.fingerprint}}, nil
}
func (s *NativeAssetSnapshot) ReadCandidate(ctx context.Context, uri identity.ResourceURI, candidate identity.ResourceCandidate) (json.RawMessage, error) {
	if !candidate.Valid() || candidate.Kind != identity.WorkingCandidate {
		return nil, identity.ErrResourceDenied
	}
	value, err := s.value(ctx, uri)
	if err != nil {
		return nil, err
	}
	if candidate.ContentFingerprint != value.fingerprint {
		return nil, identity.ErrResourceStale
	}
	return append(json.RawMessage(nil), value.raw...), nil
}
func (s *NativeAssetSnapshot) localDefinitionCurrent(ctx context.Context, uri identity.ResourceURI, candidate identity.ResourceCandidate) error {
	value, err := s.value(ctx, uri)
	if err != nil {
		return err
	}
	if candidate.Kind != identity.WorkingCandidate || candidate.ContentFingerprint != value.fingerprint {
		return identity.ErrResourceStale
	}
	return nil
}
func (s *NativeAssetSnapshot) localDefinitionValidated(kind string, version int64, fingerprint string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, value := range s.values {
		if value.kind == kind && value.version == version && value.fingerprint == fingerprint {
			return true
		}
	}
	return false
}
func (s *NativeAssetSnapshot) Bindings(provider string, policy LocalRevisionPolicyFactory) ([]LocalResourceBinding, error) {
	if s == nil || provider == "" || policy == nil {
		return nil, fmt.Errorf("native bindings require provider and host policy")
	}
	result := make([]LocalResourceBinding, 0, len(s.definitions))
	for _, definition := range s.definitions {
		uri, _ := identity.ParseResourceURI(definition.URI)
		result = append(result, LocalResourceBinding{URI: definition.URI, Title: definition.Title, FormatVersion: definition.FormatVersion, Resolver: func(ctx context.Context, actor identity.VerifiedActor, action string) (*identity.ResourceResolver, error) {
			p, err := policy(ctx, actor, uri, action)
			if err != nil {
				return nil, err
			}
			if p == nil {
				return nil, identity.ErrResourceDenied
			}
			return &identity.ResourceResolver{ProviderIdentity: provider, Source: s, Policy: p}, nil
		}})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].URI < result[j].URI })
	return result, nil
}

// SnapshotWindowDefinitions uses explicit native logical entries; fragments and
// target branches never become independent resources.
func SnapshotWindowDefinitions(ctx context.Context, root string, bindings []workspacewindow.ResourceBinding, enrich StaticWorkspaceWindowEnricher) ([]NativeSnapshotDefinition, error) {
	local, err := ConfinedWindowBindings(ctx, root, bindings, func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) (identity.ResourceRevisionPolicy, error) {
		return nil, nil
	}, workspacewindow.WorkspaceWindowEnricher(enrich))
	if err != nil {
		return nil, err
	}
	result := make([]NativeSnapshotDefinition, 0, len(local))
	for _, binding := range local {
		// The supplied policy factory is deliberately non-authorizing; expose the
		// already trusted entry point directly rather than fabricating an actor.
		var original workspacewindow.ResourceBinding
		for _, candidate := range bindings {
			if candidate.URI == binding.URI {
				original = candidate
				break
			}
		}
		rootHandle, err := os.OpenRoot(root)
		if err != nil {
			return nil, err
		}
		reader := &ExtensionReader{root: rootHandle}
		file := ""
		for _, candidate := range []string{filepath.ToSlash(filepath.Join("extension/forge/windows", original.WindowKey+".yaml")), filepath.ToSlash(filepath.Join("extension/forge/windows", original.WindowKey, "main.yaml")), filepath.ToSlash(filepath.Join("extension/forge/windows", original.WindowKey, "shared/main.yaml"))} {
			if err := reader.check(candidate); err == nil {
				file = candidate
				break
			} else if !os.IsNotExist(err) {
				reader.close()
				rootHandle.Close()
				return nil, err
			}
		}
		reader.close()
		rootHandle.Close()
		if file == "" {
			return nil, identity.ErrResourceDenied
		}
		result = append(result, NativeSnapshotDefinition{URI: binding.URI, Title: binding.Title, FormatVersion: 2, File: file, Load: NativeWindowLoader([]workspacewindow.ResourceBinding{original}, workspacewindow.WorkspaceWindowEnricher(enrich))})
	}
	return result, nil
}

func pureLocalValidator(kind string, validator LocalResourceValidator) bool {
	if validator == nil {
		return false
	}
	pointer := reflect.ValueOf(validator).Pointer()
	return kind == "window" && pointer == reflect.ValueOf(ValidateWindowBundle).Pointer() || kind == "report" && pointer == reflect.ValueOf(ValidateReportEnvelope).Pointer()
}
