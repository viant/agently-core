// Package style compiles workspace-owned theme assets into immutable snapshots.
package style

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/viant/agently-core/protocol/ui/theme"
)

const Directory = "extension/forge/styles"
const MaxCSSBytes = 512 * 1024
const maxFiles = 32
const maxSnapshots = 8
const compilerVersion = 1

type Descriptor struct {
	Version       int    `json:"version"`
	Revision      string `json:"revision"`
	Href          string `json:"href"`
	ThemeRevision string `json:"themeRevision,omitempty"`
}
type Publication struct {
	WorkspaceID string
	Styles      *Descriptor
	Themes      *Descriptor
	Diagnostics []string
}
type snapshot struct {
	revision string
	css      []byte
	catalog  []byte
	warnings []string
}
type Service struct {
	rootFn    func() string
	mu        sync.Mutex
	root      string
	identity  string
	current   *snapshot
	snapshots map[string]*snapshot
	order     []string
}

func New(root func() string) *Service { return &Service{rootFn: root} }

// Current refreshes all inputs before advertising a revision. A failed edit
// preserves only this workspace's last valid snapshot; deletion clears it.
func (s *Service) Current(ctx context.Context) Publication {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := s.selectRoot()
	publication := Publication{}
	if err := ctx.Err(); err != nil {
		publication.Diagnostics = []string{"theme refresh cancelled"}
		return publication
	}
	next, err := compile(ctx, root)
	id, idErr := workspaceIdentity(root)
	if idErr != nil && (next != nil || s.current != nil) {
		publication.Diagnostics = append(publication.Diagnostics, "workspace identity unavailable; preferences will be session-only")
	}
	if id != "" && s.identity != "" && id != s.identity {
		s.current = nil
		s.snapshots = map[string]*snapshot{}
		s.order = nil
	}
	s.identity = id
	publication.WorkspaceID = id
	if err != nil {
		publication.Diagnostics = append(publication.Diagnostics, err.Error())
	} else if next == nil {
		s.current = nil
		// Deletion removes cached assets too; a stale link must not retain deleted customization.
		s.snapshots = map[string]*snapshot{}
		s.order = nil
	} else {
		s.current = next
		if _, ok := s.snapshots[next.revision]; !ok {
			s.snapshots[next.revision] = next
			s.order = append(s.order, next.revision)
			if len(s.order) > maxSnapshots {
				delete(s.snapshots, s.order[0])
				s.order = s.order[1:]
			}
		}
	}
	if s.current == nil {
		return publication
	}
	current := s.current
	publication.Styles = &Descriptor{Version: theme.Version, Revision: current.revision, Href: "/v1/workspace/ui/styles/" + current.revision + ".css"}
	if len(current.catalog) > 0 {
		publication.Themes = &Descriptor{Version: theme.Version, Revision: current.revision, Href: "/v1/workspace/ui/themes/" + current.revision + ".json"}
		publication.Styles.ThemeRevision = current.revision
	}
	publication.Diagnostics = append(publication.Diagnostics, current.warnings...)
	return publication
}
func (s *Service) selectRoot() string {
	root := filepath.Clean(s.rootFn())
	if root != s.root || s.snapshots == nil {
		s.root = root
		s.identity = ""
		s.current = nil
		s.snapshots = map[string]*snapshot{}
		s.order = nil
	}
	return root
}

func compile(ctx context.Context, root string) (*snapshot, error) {
	workspace, err := os.OpenRoot(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot open workspace styles")
	}
	defer workspace.Close()
	assets, err := workspace.OpenRoot(Directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot open workspace styles directory")
	}
	defer assets.Close()
	manifestBytes, err := readFile(assets, "manifest.yaml", theme.MaxManifestBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("manifest.yaml: cannot read manifest")
	}
	manifest, err := theme.Parse(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("manifest.yaml: %w", err)
	}
	catalog, err := theme.Resolve(*manifest)
	if err != nil {
		return nil, err
	}
	// Canonicalize the definition order, while retaining each explicit file order.
	sort.Slice(manifest.Themes, func(i, j int) bool { return manifest.Themes[i].ID < manifest.Themes[j].ID })
	manifest.DefaultMode = catalog.DefaultMode
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "workspace-style:%d:palette:%d\n", compilerVersion, theme.PaletteVersion)
	hashPart(h, canonical)
	var output bytes.Buffer
	result := &snapshot{}
	files := map[string][]byte{}
	appendFiles := func(names []string, ancestors map[string]bool, themeID, mode string) (map[string]bool, error) {
		seen := make(map[string]bool, len(ancestors)+len(names))
		for n := range ancestors {
			seen[n] = true
		}
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("theme refresh cancelled")
			}
			if !validPath(name) {
				return nil, fmt.Errorf("invalid stylesheet path %q", name)
			}
			if seen[name] {
				return nil, fmt.Errorf("duplicate stylesheet reference %q", name)
			}
			seen[name] = true
			data, ok := files[name]
			if !ok {
				if len(files) >= maxFiles {
					return nil, fmt.Errorf("too many stylesheets (maximum %d)", maxFiles)
				}
				data, err = readFile(assets, name, MaxCSSBytes)
				if err != nil {
					return nil, fmt.Errorf("%s: cannot read stylesheet within styles directory", name)
				}
				if !utf8.Valid(data) {
					return nil, fmt.Errorf("%s: stylesheet is not UTF-8", name)
				}
				files[name] = data
			}
			warnings, e := validateCSS(data, themeID, mode)
			if e != nil {
				return nil, fmt.Errorf("%s: %w", name, e)
			}
			for _, w := range warnings {
				result.warnings = append(result.warnings, name+": "+w)
			}
			hashPart(h, []byte(name))
			hashPart(h, data)
			// Newline separators prevent a trailing line comment or token from joining files.
			output.WriteByte('\n')
			output.Write(data)
			output.WriteByte('\n')
			if output.Len() > MaxCSSBytes {
				return nil, fmt.Errorf("compiled CSS exceeds %d bytes", MaxCSSBytes)
			}
		}
		return seen, nil
	}
	shared, err := appendFiles(manifest.Files, nil, "", "")
	if err != nil {
		return nil, err
	}
	for i, t := range manifest.Themes {
		css, e := theme.CSS(&theme.Catalog{Version: theme.Version, Themes: []theme.ResolvedTheme{catalog.Themes[i]}})
		if e != nil {
			return nil, e
		}
		output.WriteString(css)
		inherited, e := appendFiles(t.Files, shared, t.ID, "")
		if e != nil {
			return nil, e
		}
		for _, mode := range []string{"light", "dark"} {
			if v, ok := t.Modes[mode]; ok {
				if _, e = appendFiles(v.Files, inherited, t.ID, mode); e != nil {
					return nil, e
				}
			}
		}
	}
	if _, err = appendFiles(manifest.Overrides, shared, "", ""); err != nil {
		return nil, err
	}
	if output.Len() > MaxCSSBytes {
		return nil, fmt.Errorf("compiled CSS exceeds %d bytes", MaxCSSBytes)
	}
	if len(catalog.Themes) > 0 {
		result.catalog, err = json.Marshal(catalog)
		if err != nil {
			return nil, err
		}
	}
	result.css = bytes.Clone(output.Bytes())
	hashPart(h, result.css)
	hashPart(h, result.catalog)
	result.revision = hex.EncodeToString(h.Sum(nil))
	if ctx.Err() != nil {
		return nil, fmt.Errorf("theme refresh cancelled")
	}
	return result, nil
}
func hashPart(w io.Writer, b []byte) { fmt.Fprintf(w, "%d:", len(b)); _, _ = w.Write(b) }
func validPath(name string) bool {
	return name != "" && len(name) <= 1024 && fs.ValidPath(name) && path.Clean(name) == name && !strings.ContainsAny(name, "\\:%?#\x00") && strings.EqualFold(path.Ext(name), ".css")
}
func readFile(root *os.Root, name string, limit int) ([]byte, error) {
	// Opening a FIFO for reading can block before f.Stat can reject it.
	// Keep the post-open check too, since files can change between operations.
	before, err := root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > int64(limit) {
		return nil, fmt.Errorf("not a regular file within size limit")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, fmt.Errorf("not a regular file within size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("file exceeds size limit")
	}
	return data, nil
}
