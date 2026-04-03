package base

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/file"
	"github.com/viant/agently-core/workspace"
	meta "github.com/viant/agently-core/workspace/service/meta"
	"gopkg.in/yaml.v3"
)

// Repository generic CRUD for YAML/JSON resources stored under
// $AGENTLY_WORKSPACE/<kind>/.
type Repository[T any] struct {
	fs    afs.Service
	meta  *meta.Service
	dir   string
	store workspace.Store
	kind  string
}

// New constructs a repository for a specific workspace kind (e.g. "models").
func New[T any](fs afs.Service, kind string) *Repository[T] {
	dir := workspace.Path(kind)
	return &Repository[T]{fs: fs, meta: meta.New(fs, dir), dir: dir, kind: kind}
}

// NewWithStore constructs a repository backed by a workspace.Store.
func NewWithStore[T any](store workspace.Store, kind string) *Repository[T] {
	dir := filepath.Join(store.Root(), kind)
	return &Repository[T]{store: store, kind: kind, dir: dir}
}

// filename resolves name to absolute path with .yaml default extension.
// It respects caller context so cancellation/timeout can stop filesystem checks.
func (r *Repository[T]) filename(ctx context.Context, name string) (string, error) {
	// Ensure we end with .yaml when extension missing.
	if filepath.Ext(name) == "" {
		name += ".yaml"
	}

	// Prefer the flat layout: <dir>/<name>.yaml
	flat := filepath.Join(r.dir, name)

	// When the flat file exists we always use it.
	ok, err := r.fs.Exists(ctx, flat)
	if err != nil {
		return "", fmt.Errorf("failed to check file existence for %q: %w", flat, err)
	}
	if ok {
		return flat, nil
	}

	// Otherwise attempt the historical nested layout: <dir>/<name>/<name>.yaml
	base := strings.TrimSuffix(name, ".yaml")
	nested := filepath.Join(r.dir, base, name)
	ok, err = r.fs.Exists(ctx, nested)
	if err != nil {
		return "", fmt.Errorf("failed to check file existence for %q: %w", nested, err)
	}
	if ok {
		return nested, nil
	}

	// Neither layout exists – default to the preferred flat path so new files
	// are created in the simplified structure while still remaining backward
	// compatible when reading existing nested files.
	return flat, nil
}

// ResolveFilename resolves a resource path using the caller context.
func (r *Repository[T]) ResolveFilename(ctx context.Context, name string) (string, error) {
	return r.filename(ctx, name)
}

// Filename is a compatibility helper for legacy callers that do not pass context.
// New code should prefer ResolveFilename so lookup respects cancellation.
func (r *Repository[T]) Filename(name string) string {
	filename, err := r.filename(context.Background(), name)
	if err == nil {
		return filename
	}
	if filepath.Ext(name) == "" {
		name += ".yaml"
	}
	return filepath.Join(r.dir, name)
}

// List basenames (without extension).
func (r *Repository[T]) List(ctx context.Context) ([]string, error) {
	if r.store != nil {
		return r.store.List(ctx, r.kind)
	}
	objs, err := r.fs.List(ctx, r.dir)
	if err != nil {
		return nil, err
	}
	var res []string

	for _, o := range objs {
		if o.IsDir() {
			// Handle possible nested layout <dir>/<name>/<name>.yaml>
			dirName := filepath.Base(o.Name())
			nested := filepath.Join(r.dir, dirName, dirName+".yaml")
			if ok, _ := r.fs.Exists(ctx, nested); ok {
				res = append(res, dirName)
			}
			continue
		}
		base := filepath.Base(o.Name())
		ext := strings.ToLower(filepath.Ext(base))
		// Only include supported YAML files; ignore other extensions
		if ext == ".yaml" || ext == ".yml" {
			res = append(res, strings.TrimSuffix(base, ext))
		}
	}
	return res, nil
}

// GetRaw downloads raw bytes.
func (r *Repository[T]) GetRaw(ctx context.Context, name string) ([]byte, error) {
	if r.store != nil {
		return r.store.Load(ctx, r.kind, name)
	}
	filename, err := r.filename(ctx, name)
	if err != nil {
		return nil, err
	}
	return r.fs.DownloadWithURL(ctx, filename)
}

// Load unmarshals YAML/JSON into *T.
func (r *Repository[T]) Load(ctx context.Context, name string) (*T, error) {
	if r.store != nil {
		data, err := r.store.Load(ctx, r.kind, name)
		if err != nil {
			return nil, err
		}
		var v T
		if err := yaml.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return &v, nil
	}
	var v T
	filename, err := r.filename(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := r.meta.Load(ctx, filename, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Save (Add/overwrite) marshals struct to YAML.
func (r *Repository[T]) Save(ctx context.Context, name string, obj *T) error {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}
	return r.Add(ctx, name, data)
}

// Add uploads raw data.
func (r *Repository[T]) Add(ctx context.Context, name string, data []byte) error {
	if r.store != nil {
		return r.store.Save(ctx, r.kind, name, data)
	}
	filename, err := r.filename(ctx, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	return r.fs.Upload(ctx, filename, file.DefaultFileOsMode, bytes.NewReader(data))
}

// Delete removes file.
func (r *Repository[T]) Delete(ctx context.Context, name string) error {
	if r.store != nil {
		return r.store.Delete(ctx, r.kind, name)
	}
	filename, err := r.filename(ctx, name)
	if err != nil {
		return err
	}
	return r.fs.Delete(ctx, filename)
}
