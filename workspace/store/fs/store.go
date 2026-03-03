package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	afsfile "github.com/viant/afs/file"
	"github.com/viant/agently-core/workspace"
)

// Store is a filesystem-backed implementation of workspace.Store.
type Store struct {
	root string
	fs   afs.Service
}

// New creates an FS-backed Store rooted at root. If root is empty it defaults
// to workspace.Root().
func New(root string, opts ...Option) *Store {
	if strings.TrimSpace(root) == "" {
		root = workspace.Root()
	}
	s := &Store{root: root, fs: afs.New()}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Root returns the workspace root path.
func (s *Store) Root() string { return s.root }

// List returns the names of all resources of the given kind.
func (s *Store) List(ctx context.Context, kind string) ([]string, error) {
	dir := filepath.Join(s.root, kind)
	objs, err := s.fs.List(ctx, dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var res []string
	for _, o := range objs {
		if o.IsDir() {
			dirName := filepath.Base(o.Name())
			nested := filepath.Join(dir, dirName, dirName+".yaml")
			if ok, _ := s.fs.Exists(ctx, nested); ok {
				res = append(res, dirName)
			}
			continue
		}
		base := filepath.Base(o.Name())
		ext := strings.ToLower(filepath.Ext(base))
		if ext == ".yaml" || ext == ".yml" {
			res = append(res, strings.TrimSuffix(base, ext))
		}
	}
	return res, nil
}

// Load returns the raw bytes for a single resource. It tries the flat layout
// first (<root>/<kind>/<name>.yaml), then the nested layout
// (<root>/<kind>/<name>/<name>.yaml). Returns os.ErrNotExist if neither is found.
func (s *Store) Load(ctx context.Context, kind, name string) ([]byte, error) {
	flat := filepath.Join(s.root, kind, name+".yaml")
	if ok, _ := s.fs.Exists(ctx, flat); ok {
		return s.fs.DownloadWithURL(ctx, flat)
	}
	nested := filepath.Join(s.root, kind, name, name+".yaml")
	if ok, _ := s.fs.Exists(ctx, nested); ok {
		return s.fs.DownloadWithURL(ctx, nested)
	}
	return nil, fmt.Errorf("%s/%s: %w", kind, name, os.ErrNotExist)
}

// Save creates or overwrites a resource using the flat layout.
func (s *Store) Save(ctx context.Context, kind, name string, data []byte) error {
	dir := filepath.Join(s.root, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	flat := filepath.Join(dir, name+".yaml")
	return s.fs.Upload(ctx, flat, afsfile.DefaultFileOsMode, strings.NewReader(string(data)))
}

// Delete removes whichever layout exists. No error if absent.
func (s *Store) Delete(ctx context.Context, kind, name string) error {
	flat := filepath.Join(s.root, kind, name+".yaml")
	if ok, _ := s.fs.Exists(ctx, flat); ok {
		return s.fs.Delete(ctx, flat)
	}
	nested := filepath.Join(s.root, kind, name, name+".yaml")
	if ok, _ := s.fs.Exists(ctx, nested); ok {
		return s.fs.Delete(ctx, nested)
	}
	return nil
}

// Exists reports whether a resource exists in either layout.
func (s *Store) Exists(ctx context.Context, kind, name string) (bool, error) {
	flat := filepath.Join(s.root, kind, name+".yaml")
	if ok, _ := s.fs.Exists(ctx, flat); ok {
		return true, nil
	}
	nested := filepath.Join(s.root, kind, name, name+".yaml")
	if ok, _ := s.fs.Exists(ctx, nested); ok {
		return true, nil
	}
	return false, nil
}

// Entries returns metadata-enriched listings for polling watchers.
func (s *Store) Entries(ctx context.Context, kind string) ([]workspace.Entry, error) {
	dir := filepath.Join(s.root, kind)
	objs, err := s.fs.List(ctx, dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []workspace.Entry
	for _, o := range objs {
		if o.IsDir() {
			dirName := filepath.Base(o.Name())
			nested := filepath.Join(dir, dirName, dirName+".yaml")
			if ok, _ := s.fs.Exists(ctx, nested); ok {
				entries = append(entries, workspace.Entry{
					Kind:      kind,
					Name:      dirName,
					UpdatedAt: o.ModTime(),
				})
			}
			continue
		}
		base := filepath.Base(o.Name())
		ext := strings.ToLower(filepath.Ext(base))
		if ext == ".yaml" || ext == ".yml" {
			entries = append(entries, workspace.Entry{
				Kind:      kind,
				Name:      strings.TrimSuffix(base, ext),
				UpdatedAt: o.ModTime(),
			})
		}
	}
	return entries, nil
}
