package resources

import (
	"context"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"strings"
	"time"

	"github.com/viant/afs"
	"github.com/viant/afs/url"
	mcpuri "github.com/viant/agently-core/protocol/mcp/uri"
	svc "github.com/viant/agently-core/protocol/tool/service"
	mcpfs "github.com/viant/agently-core/service/augmenter/mcpfs"
)

type ListInput struct {
	// RootURI is the normalized or user-provided root URI. Prefer using
	// RootID when possible; RootURI is retained for backward compatibility
	// but hidden from public schemas.
	RootURI string `json:"root,omitempty" internal:"true" description:"normalized or user-provided root URI; prefer rootId when available"`
	// RootID is a stable identifier corresponding to a root returned by
	// roots. When provided, it is resolved to the underlying
	// normalized URI before enforcement and listing.
	RootID string `json:"rootId,omitempty" description:"resource root id returned by roots"`
	// Path is an optional subpath under the selected root. When empty, the
	// root itself is listed. Paths may be relative to the root or
	// absolute-like; absolute-like paths must remain under the root.
	Path string `json:"path,omitempty" description:"optional subpath under the root to list"`
	// Recursive controls whether the listing should walk the subtree under
	// Path. When false, only immediate children are returned.
	Recursive bool `json:"recursive,omitempty" description:"when true, walk recursively under path"`
	// Include defines optional file or path globs to include. When provided,
	// only items whose relative path or base name matches at least one
	// pattern are returned. Globs use path-style matching rules and support
	// globstar ("**") to match any directory depth.
	Include []string `json:"include,omitempty" description:"optional file/path globs to include (relative to root+path); supports ** for any depth"`
	// Exclude defines optional file or path globs to exclude. When provided,
	// any item whose relative path or base name matches a pattern is
	// filtered out.
	Exclude []string `json:"exclude,omitempty" description:"optional file/path globs to exclude; supports ** for any depth"`
	// MaxItems caps the number of items returned. When zero or negative, no
	// explicit limit is applied.
	MaxItems int `json:"maxItems,omitempty" description:"maximum number of items to return; 0 means no limit"`
}

type ListItem struct {
	URI      string    `json:"uri"`
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	RootID   string    `json:"rootId,omitempty"`
}

type ListOutput struct {
	Items []ListItem `json:"items"`
	Total int        `json:"total"`
}

// normalizeListGlobs trims whitespace and removes empty patterns.
func normalizeListGlobs(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// listGlobMatch performs a best-effort path-style glob match. It uses
// path.Match and treats any pattern error as a non-match.
func listGlobMatch(pattern, value string) bool {
	if pattern == "" || value == "" {
		return false
	}
	// path.Match does not support "**" (globstar). Implement a minimal globstar
	// matcher where a full path segment equal to "**" can match zero or more
	// path segments.
	if strings.Contains(pattern, "**") {
		return listGlobStarMatch(pattern, value)
	}
	ok, err := pathpkg.Match(pattern, value)
	return err == nil && ok
}

func listGlobStarMatch(pattern, value string) bool {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if pattern == "" || value == "" {
		return false
	}

	pattern = strings.TrimPrefix(pattern, "./")
	pattern = strings.TrimPrefix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")

	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	value = strings.TrimSuffix(value, "/")

	pSegs := splitListGlob(pattern)
	vSegs := splitListGlob(value)

	type state struct{ i, j int }
	seen := make(map[state]bool, len(pSegs)*len(vSegs))
	memo := make(map[state]bool, len(pSegs)*len(vSegs))
	var match func(i, j int) bool
	match = func(i, j int) bool {
		s := state{i: i, j: j}
		if seen[s] {
			return memo[s]
		}
		seen[s] = true

		var ok bool
		switch {
		case i >= len(pSegs):
			ok = j >= len(vSegs)
		case pSegs[i] == "**":
			// "**" matches zero segments (advance pattern) or one segment (advance value).
			ok = match(i+1, j) || (j < len(vSegs) && match(i, j+1))
		case j >= len(vSegs):
			ok = false
		default:
			segOK, err := pathpkg.Match(pSegs[i], vSegs[j])
			ok = err == nil && segOK && match(i+1, j+1)
		}
		memo[s] = ok
		return ok
	}

	return match(0, 0)
}

func splitListGlob(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// listMatchesFilters reports whether a candidate with the given relative
// path and base name passes include/exclude filters. When includes are
// non-empty, at least one must match; excludes always take precedence.
func listMatchesFilters(relPath, name string, includes, excludes []string) bool {
	// Exclude has priority.
	for _, pat := range excludes {
		if listGlobMatch(pat, relPath) || listGlobMatch(pat, name) {
			return false
		}
	}
	if len(includes) == 0 {
		return true
	}
	for _, pat := range includes {
		if listGlobMatch(pat, relPath) || listGlobMatch(pat, name) {
			return true
		}
	}
	return false
}

func (s *Service) list(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ListInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	rootCtx, err := s.newRootContext(ctx, input.RootURI, input.RootID, s.agentAllowed(ctx))
	if err != nil {
		fmt.Printf("resources: list resolve error rootId=%q root=%q err=%v\n", input.RootID, input.RootURI, err)
		return err
	}
	rootBase := rootCtx.Base()
	base := rootBase
	if trimmed := strings.TrimSpace(input.Path); trimmed != "" {
		base, err = rootCtx.ResolvePath(trimmed)
		if err != nil {
			return err
		}
	}
	includes := normalizeListGlobs(input.Include)
	excludes := normalizeListGlobs(input.Exclude)
	afsSvc := afs.New()
	mfs := (*mcpfs.Service)(nil)
	if s.mcpMgr != nil {
		var err error
		mfs, err = s.mcpFS(ctx)
		if err != nil {
			return err
		}
	}
	max := input.MaxItems
	if max <= 0 {
		max = 0
	}
	var items []ListItem
	seen := map[string]bool{}
	if mcpuri.Is(base) {
		if mfs == nil {
			return fmt.Errorf("mcp manager not configured")
		}
		objs, err := mfs.List(ctx, base)
		if err != nil {
			return err
		}
		for _, o := range objs {
			if o == nil {
				continue
			}
			uri := o.URL()
			if seen[uri] {
				continue
			}
			rel := relativePath(rootBase, uri)
			if !listMatchesFilters(rel, o.Name(), includes, excludes) {
				continue
			}
			seen[uri] = true
			items = append(items, ListItem{
				URI:      uri,
				Path:     rel,
				Name:     o.Name(),
				Size:     o.Size(),
				Modified: o.ModTime(),
				RootID:   rootCtx.ID(),
			})
			if max > 0 && len(items) >= max {
				break
			}
		}
	} else {
		if input.Recursive {
			err := afsSvc.Walk(ctx, base, func(ctx context.Context, walkBaseURL, parent string, info os.FileInfo, reader io.Reader) (bool, error) {
				if info == nil || info.IsDir() {
					return true, nil
				}
				var uri string
				if parent == "" {
					uri = url.Join(walkBaseURL, info.Name())
				} else {
					uri = url.Join(walkBaseURL, parent, info.Name())
				}
				if seen[uri] {
					return true, nil
				}

				scheme := url.SchemeExtensionURL(walkBaseURL)
				rootBaseNormalised := url.Normalize(rootBase, scheme)
				rel := relativePath(rootBaseNormalised, uri)

				if !listMatchesFilters(rel, info.Name(), includes, excludes) {
					return true, nil
				}
				seen[uri] = true
				items = append(items, ListItem{
					URI:      uri,
					Path:     rel,
					Name:     info.Name(),
					Size:     info.Size(),
					Modified: info.ModTime(),
					RootID:   rootCtx.ID(),
				})
				if max > 0 && len(items) >= max {
					return false, nil
				}
				return true, nil
			})
			if err != nil {
				return err
			}
		} else {
			objs, err := afsSvc.List(ctx, base)
			if err != nil {
				return err
			}

			baseNormalised := base
			if len(objs) > 0 {
				scheme := url.SchemeExtensionURL(objs[0].URL())
				baseNormalised = url.Normalize(base, scheme)
			}

			for _, o := range objs {
				if o == nil {
					continue
				}

				if baseNormalised == o.URL() {
					continue
				}

				uri := url.Join(base, o.Name()) // we don't enforce normalised base here
				if seen[uri] {
					continue
				}
				rel := relativePath(rootBase, uri)
				if !listMatchesFilters(rel, o.Name(), includes, excludes) {
					continue
				}
				seen[uri] = true
				items = append(items, ListItem{
					URI:      uri,
					Path:     rel,
					Name:     o.Name(),
					Size:     o.Size(),
					Modified: o.ModTime(),
					RootID:   rootCtx.ID(),
				})
				if max > 0 && len(items) >= max {
					break
				}
			}
		}
	}
	output.Items = items
	output.Total = len(items)
	return nil
}
