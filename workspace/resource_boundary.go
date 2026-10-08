package workspace

import (
	"errors"
	"net/url"
	"path/filepath"
	"strings"
)

var ErrCanonicalResourceAccess = errors.New("canonical resource endpoint required")

// RawResourceBoundary protects migrated definitions from unversioned workspace
// CRUD and file tools. It is explicitly installed by a canonical-mode host;
// trusted internal loaders retain their original store.
type RawResourceBoundary struct {
	Root  string
	paths []string
}

func NewRawResourceBoundary(root string) *RawResourceBoundary {
	if u, err := url.Parse(root); err == nil && u.Scheme != "" {
		if u.Scheme == "file" {
			root = u.Path
		} else {
			root = Root()
		}
	}
	return &RawResourceBoundary{Root: root, paths: []string{"window", "windows", KindForgeWindow, "report", "reports", "extension/forge/reporting", "intent", KindIntent, "prompt", KindPrompt, "skill", KindSkill}}
}
func validRawParts(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
func (p *RawResourceBoundary) CheckResource(kind, name string) error {
	if p == nil {
		return nil
	}
	if !validRawParts(kind) || name != "" && !validRawParts(name) {
		return ErrCanonicalResourceAccess
	}
	value := strings.Trim(kind+"/"+name, "/")
	for _, protected := range p.paths {
		if pathWithin(value, protected) {
			return ErrCanonicalResourceAccess
		}
	}
	if name != "" {
		for _, relative := range []string{kind + "/" + name + ".yaml", kind + "/" + name + "/" + filepath.Base(name) + ".yaml"} {
			if err := p.CheckURI(filepath.Join(p.Root, relative), false); err != nil {
				return err
			}
		}
	} else {
		if err := p.CheckURI(filepath.Join(p.Root, kind), false); err != nil {
			return err
		}
	}
	return nil
}
func pathWithin(value, parent string) bool {
	value = strings.ToLower(filepath.Clean(value))
	parent = strings.ToLower(filepath.Clean(parent))
	return value == parent || strings.HasPrefix(value, parent+string(filepath.Separator))
}

// CheckURI rejects both protected paths and directory scans containing them.
// It resolves existing symlink prefixes even when the final write target does
// not yet exist. Other workspace resources and external roots remain usable.
func (p *RawResourceBoundary) CheckURI(location string, recursive bool) error {
	if p == nil {
		return nil
	}
	u, err := url.Parse(location)
	if err != nil {
		return ErrCanonicalResourceAccess
	}
	if u.Scheme != "" && u.Scheme != "file" && u.Scheme != "workspace" {
		return nil
	}
	value := location
	if u.Scheme == "file" {
		value = u.Path
	}
	if u.Scheme == "workspace" {
		if u.Host != "" && u.Host != "localhost" {
			return nil
		}
		value = filepath.Join(p.Root, u.Path)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(p.Root, value)
	}
	root := resolveExistingPath(p.Root)
	target := resolveExistingPath(value)
	for _, protected := range p.paths {
		base := resolveExistingPath(filepath.Join(root, protected))
		if pathWithin(target, base) || recursive && pathWithin(base, target) {
			return ErrCanonicalResourceAccess
		}
	}
	return nil
}
func resolveExistingPath(value string) string {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(value)
	}
	current := absolute
	tail := []string{}
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute
		}
		tail = append(tail, filepath.Base(current))
		current = parent
	}
}
