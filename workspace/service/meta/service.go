package meta

import (
	"context"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/storage"
	wscodec "github.com/viant/agently-core/workspace/codec"
	"gopkg.in/yaml.v3"
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
	if _, ok := v.(*yaml.Node); ok {
		if err := wscodec.DecodeURL(ctx, s.fs, URL, v, s.options...); err != nil {
			return err
		}
		if node, ok := v.(*yaml.Node); ok {
			return ResolveImports(ctx, s.fs, node, importBaseDir(URL), s.options...)
		}
		return nil
	}
	var node yaml.Node
	if err := wscodec.DecodeURL(ctx, s.fs, URL, &node, s.options...); err != nil {
		return err
	}
	if err := ResolveImports(ctx, s.fs, &node, importBaseDir(URL), s.options...); err != nil {
		return err
	}
	return node.Decode(v)
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

// ListDefinitions is a scoped variant used by authored resources that also
// accept Markdown. It does not make general YAML metadata discovery treat
// unrelated Markdown knowledge files as definitions.
func (s *Service) ListDefinitions(ctx context.Context, URL string, extensions ...string) ([]string, error) {
	allowed := map[string]bool{}
	for _, extension := range extensions {
		allowed[strings.ToLower(extension)] = true
	}
	URL = s.resolve(URL)
	if allowed[strings.ToLower(path.Ext(URL))] {
		return []string{URL}, nil
	}
	objects, err := s.fs.List(ctx, URL, s.options...)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, object := range objects {
		if !object.IsDir() && allowed[strings.ToLower(filepath.Ext(object.Name()))] {
			result = append(result, s.resolve(filepath.Join(URL, filepath.Base(object.Name()))))
		}
	}
	sort.Strings(result)
	return result, nil
}

// LoadMarkdown reads a Markdown definition through the same filesystem and
// import boundary as YAML metadata, returning its literal role sections.
func (s *Service) LoadMarkdown(ctx context.Context, URL string, node *yaml.Node) (wscodec.MarkdownDocument, error) {
	URL = s.resolve(URL)
	raw, err := s.fs.DownloadWithURL(ctx, URL, s.options...)
	if err != nil {
		return wscodec.MarkdownDocument{}, err
	}
	doc, err := wscodec.ParseMarkdown(raw)
	if err != nil {
		return doc, err
	}
	if len(doc.Metadata) == 0 {
		return doc, nil
	}
	if err := yaml.Unmarshal(doc.Metadata, node); err != nil {
		return doc, err
	}
	return doc, ResolveImports(ctx, s.fs, node, importBaseDir(URL), s.options...)
}

// ListRecursive returns all YAML candidates below a directory. Unlike List,
// it preserves subdirectory paths so callers can organize resources by domain.
func (s *Service) ListRecursive(ctx context.Context, URL string) ([]string, error) {
	return s.ListDefinitionsRecursive(ctx, URL, ".yaml", ".yml")
}

// ListDefinitionsRecursive keeps the caller's explicit definition formats and
// does not turn all Markdown knowledge files into YAML resource definitions.
func (s *Service) ListDefinitionsRecursive(ctx context.Context, URL string, extensions ...string) ([]string, error) {
	URL = s.resolve(URL)
	allowed := map[string]bool{}
	for _, extension := range extensions {
		allowed[strings.ToLower(extension)] = true
	}
	if allowed[strings.ToLower(path.Ext(URL))] {
		return []string{URL}, nil
	}
	var out []string
	seenFiles := map[string]bool{}
	visitedDirs := map[string]bool{}
	if err := s.listRecursive(ctx, URL, &out, seenFiles, visitedDirs, allowed); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func (s *Service) listRecursive(ctx context.Context, URL string, out *[]string, seenFiles, visitedDirs map[string]bool, allowed map[string]bool) error {
	normalizedURL := normalizeResourcePath(URL)
	if visitedDirs[normalizedURL] {
		return nil
	}
	visitedDirs[normalizedURL] = true
	objects, err := s.fs.List(ctx, URL, s.options...)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if err := ctx.Err(); err != nil {
			return err
		}
		candidate := object.URL()
		if sameResourcePath(URL, candidate) {
			continue
		}
		if object.IsDir() {
			if err := s.listRecursive(ctx, candidate, out, seenFiles, visitedDirs, allowed); err != nil {
				return err
			}
			continue
		}
		ext := strings.ToLower(filepath.Ext(object.Name()))
		normalizedCandidate := normalizeResourcePath(candidate)
		if allowed[ext] && !seenFiles[normalizedCandidate] {
			*out = append(*out, candidate)
			seenFiles[normalizedCandidate] = true
		}
	}
	return nil
}

func sameResourcePath(left, right string) bool {
	return normalizeResourcePath(left) == normalizeResourcePath(right)
}

func normalizeResourcePath(candidate string) string {
	candidate = strings.TrimPrefix(candidate, "file://localhost")
	candidate = strings.TrimPrefix(candidate, "file://")
	return filepath.Clean(filepath.FromSlash(candidate))
}

// Exists checks if the resolved URL exists.
func (s *Service) Exists(ctx context.Context, URL string) (bool, error) {
	return s.fs.Exists(ctx, s.resolve(URL), s.options...)
}

// Download returns the raw bytes for the resolved URL.
func (s *Service) Download(ctx context.Context, URL string) ([]byte, error) {
	return s.fs.DownloadWithURL(ctx, s.resolve(URL), s.options...)
}

// GetURL returns the resolved absolute URL/path for a possibly relative path.
func (s *Service) GetURL(p string) string { return s.resolve(p) }
