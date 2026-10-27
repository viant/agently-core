package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/viant/agently-core/workspace"
)

var authoredExtensions = []string{".md", ".yaml", ".yml"}

func authoredFormatKind(kind string) bool {
	switch kind {
	case workspace.KindAgent, workspace.KindPlaybook, workspace.KindIntent, workspace.KindPrompt:
		return true
	default:
		return false
	}
}

func (s *Store) authoredPath(ctx context.Context, kind, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if name == "" || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("invalid authored resource name %q", name)
	}
	requested := strings.ToLower(filepath.Ext(name))
	base := name
	if requested == ".md" || requested == ".yaml" || requested == ".yml" {
		base = strings.TrimSuffix(name, filepath.Ext(name))
	} else {
		requested = ""
	}
	var found string
	for _, extension := range authoredExtensions {
		for _, candidate := range []string{
			filepath.Join(s.root, kind, base+extension),
			filepath.Join(s.root, kind, base, filepath.Base(base)+extension),
		} {
			if _, err := os.Stat(candidate); err == nil {
				if found != "" && found != candidate {
					return "", fmt.Errorf("duplicate authored definition %s and %s", found, candidate)
				}
				found = candidate
			} else if !os.IsNotExist(err) {
				return "", err
			}
		}
	}
	if found == "" {
		return "", os.ErrNotExist
	}
	if requested != "" && !strings.EqualFold(filepath.Ext(found), requested) {
		return "", os.ErrNotExist
	}
	return found, nil
}

func (s *Store) authoredNames(ctx context.Context, kind string) ([]string, error) {
	dir := filepath.Join(s.root, kind)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if entry.IsDir() {
			for _, extension := range authoredExtensions {
				candidate := filepath.Join(dir, name, name+extension)
				if _, err := os.Stat(candidate); err == nil {
					if first := seen[strings.ToLower(name)]; first != "" {
						return nil, fmt.Errorf("duplicate authored definition %s and %s", first, candidate)
					}
					seen[strings.ToLower(name)] = candidate
				} else if !os.IsNotExist(err) {
					return nil, err
				}
			}
			continue
		}
		extension := strings.ToLower(filepath.Ext(name))
		if extension != ".md" && extension != ".yaml" && extension != ".yml" {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		candidate := filepath.Join(dir, name)
		if first := seen[strings.ToLower(base)]; first != "" {
			return nil, fmt.Errorf("duplicate authored definition %s and %s", first, candidate)
		}
		seen[strings.ToLower(base)] = candidate
	}
	names := make([]string, 0, len(seen))
	for _, full := range seen {
		name := strings.TrimSuffix(filepath.Base(full), filepath.Ext(full))
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
