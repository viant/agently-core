package meta

import (
	"context"
	"path"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/storage"
	wscodec "github.com/viant/agently-core/workspace/codec"
)

// Service provides minimal meta loading and listing with a base directory.
type Service struct {
	fs      afs.Service
	base    string
	options []storage.Option
}

// New constructs a meta Service with the given filesystem and base directory/URL.
// Optional storage.Option values (e.g. an *embed.FS) are forwarded to every
// afs call so that scheme-specific managers receive them.
func New(fs afs.Service, base string, options ...storage.Option) *Service {
	return &Service{fs: fs, base: base, options: options}
}

// resolve joins base with a relative path, otherwise returns the path as-is.
func (s *Service) resolve(p string) string {
	if p == "" {
		return s.base
	}
	if strings.Contains(p, "://") || filepath.IsAbs(p) {
		return p
	}
	if strings.TrimSpace(s.base) == "" {
		return p
	}
	// When base is a URL, prefer URL-style join to avoid OS path quirks.
	if strings.Contains(s.base, "://") {
		base := strings.TrimRight(s.base, "/")
		rel := strings.TrimLeft(p, "/")
		return base + "/" + rel
	}
	return filepath.Join(s.base, p)
}

// Load reads URL and unmarshals into v. Supports *yaml.Node or a struct pointer.
func (s *Service) Load(ctx context.Context, URL string, v interface{}) error {
	URL = s.resolve(URL)
	return wscodec.DecodeURL(ctx, s.fs, URL, v, s.options...)
}

// List returns YAML candidates under a directory or the file itself when URL points to a file.
func (s *Service) List(ctx context.Context, URL string) ([]string, error) {
	URL = s.resolve(URL)
	if ext := strings.ToLower(path.Ext(URL)); ext == ".yaml" || ext == ".yml" {
		return []string{URL}, nil
	}
	objs, err := s.fs.List(ctx, URL, s.options...)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(o.Name()))
		if ext == ".yaml" || ext == ".yml" {
			out = append(out, s.resolve(filepath.Join(URL, filepath.Base(o.Name()))))
		}
	}
	return out, nil
}

// Exists checks if the resolved URL exists.
func (s *Service) Exists(ctx context.Context, URL string) (bool, error) {
	return s.fs.Exists(ctx, s.resolve(URL), s.options...)
}

// GetURL returns the resolved absolute URL/path for a possibly relative path.
func (s *Service) GetURL(p string) string { return s.resolve(p) }
