package resources

import (
	"context"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/viant/afs/url"
	mcpuri "github.com/viant/agently-core/protocol/mcp/uri"
	"github.com/viant/agently-core/workspace"
)

type rootContext struct {
	id     string
	alias  string
	wsRoot string
	base   string
}

func (s *Service) newRootContext(ctx context.Context, rootURI, rootID string, allowed []string) (*rootContext, error) {
	root := strings.TrimSpace(rootURI)
	aliasID := strings.TrimSpace(rootID)
	if root == "" && strings.TrimSpace(rootID) != "" {
		var err error
		root, err = s.resolveRootID(ctx, rootID)
		if err != nil {
			return nil, err
		}
	}
	if root == "" {
		root = implicitAllowedRoot(allowed)
	}
	if root == "" {
		return nil, fmt.Errorf("root or rootId is required")
	}
	if aliasID == "" && looksLikeRootAlias(root) {
		if resolved, err := s.resolveRootID(ctx, root); err == nil && strings.TrimSpace(resolved) != "" {
			aliasID = root
			root = resolved
		}
	}
	wsRoot, _, err := s.normalizeUserRoot(ctx, root)
	if err != nil {
		return nil, err
	}
	if len(allowed) > 0 && !isAllowedWorkspace(wsRoot, allowed) {
		if fallback := implicitAllowedRoot(allowed); fallback != "" {
			if normalizedFallback, _, ferr := s.normalizeUserRoot(ctx, fallback); ferr == nil && isAllowedWorkspace(normalizedFallback, allowed) {
				wsRoot = normalizedFallback
				root = fallback
			}
		}
	}
	if len(allowed) > 0 && !isAllowedWorkspace(wsRoot, allowed) {
		return nil, fmt.Errorf("root not allowed: %s", root)
	}
	id := aliasID
	if id == "" {
		id = wsRoot
	}
	base := wsRoot
	if strings.HasPrefix(wsRoot, "workspace://") {
		base = workspaceToFile(wsRoot)
	}
	return &rootContext{
		id:     id,
		alias:  root,
		wsRoot: wsRoot,
		base:   base,
	}, nil
}

func looksLikeRootAlias(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return false
	}
	if strings.Contains(v, "://") || mcpuri.Is(v) {
		return false
	}
	if filepath.IsAbs(v) || isWindowsAbsPath(v) {
		return false
	}
	trimmed := strings.Trim(v, "/")
	if trimmed == "" {
		return false
	}
	// Treat a single segment as an alias (for example: "local", "repo", "knowledge").
	return !strings.Contains(trimmed, "/")
}

func implicitAllowedRoot(allowed []string) string {
	if len(allowed) != 1 {
		return ""
	}
	value := strings.TrimSpace(allowed[0])
	if value == "" || mcpuri.Is(value) {
		return ""
	}
	return value
}

func inferAllowedRootFromPath(path string, allowed []string) string {
	wsPath := toWorkspaceURI(strings.TrimSpace(path))
	if wsPath == "" {
		return ""
	}
	for _, candidate := range allowed {
		if isAllowedWorkspace(wsPath, []string{candidate}) {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

func (rc *rootContext) ResolvePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return rc.base, nil
	}
	return joinBaseWithPath(rc.wsRoot, rc.base, strings.TrimSpace(p), rc.alias)
}

func (rc *rootContext) Base() string {
	return rc.base
}

func (rc *rootContext) ID() string {
	if rc == nil {
		return ""
	}
	return strings.TrimSpace(rc.id)
}

func (rc *rootContext) Workspace() string {
	return rc.wsRoot
}

func (s *Service) normalizeFullURI(ctx context.Context, uri string, allowed []string) (string, error) {
	wsRoot, _, err := s.normalizeUserRoot(ctx, uri)
	if err != nil {
		return "", err
	}
	if len(allowed) > 0 && !isAllowedWorkspace(wsRoot, allowed) {
		return "", fmt.Errorf("resource not allowed: %s", uri)
	}
	if strings.HasPrefix(wsRoot, "workspace://") {
		return workspaceToFile(wsRoot), nil
	}
	return wsRoot, nil
}

func isAbsLikePath(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return true
	}
	if strings.HasPrefix(p, "file://") {
		return true
	}
	if strings.HasPrefix(strings.ToLower(p), "workspace://") {
		return true
	}
	if mcpuri.Is(p) {
		return true
	}
	return false
}

func fileURLToPath(u string) string {
	u = strings.TrimSpace(u)
	if !strings.HasPrefix(u, "file://") {
		return u
	}
	rest := strings.TrimPrefix(u, "file://")
	rest = strings.TrimPrefix(rest, "localhost")
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	return rest
}

func isUnderRootPath(path, root string) bool {
	path = strings.TrimSpace(path)
	root = strings.TrimSpace(root)
	if path == "" || root == "" {
		return false
	}
	if strings.Contains(root, "://") {
		rootPath := pathpkg.Clean("/" + strings.TrimLeft(url.Path(root), "/"))
		pathPath := pathpkg.Clean("/" + strings.TrimLeft(url.Path(path), "/"))
		if rootPath == "/" {
			return true
		}
		if pathPath == rootPath {
			return true
		}
		if !strings.HasSuffix(rootPath, "/") {
			rootPath += "/"
		}
		return strings.HasPrefix(pathPath, rootPath)
	}
	pathFS := filepath.Clean(path)
	rootFS := filepath.Clean(root)
	if rootFS == string(os.PathSeparator) {
		return true
	}
	if pathFS == rootFS {
		return true
	}
	if !strings.HasSuffix(rootFS, string(os.PathSeparator)) {
		rootFS += string(os.PathSeparator)
	}
	return strings.HasPrefix(pathFS, rootFS)
}

func joinBaseWithPath(wsRoot, base, p, rootAlias string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return base, nil
	}
	if p == "/" {
		return base, nil
	}
	if isAbsLikePath(p) {
		if !mcpuri.Is(wsRoot) {
			rootBase := base
			if strings.HasPrefix(rootBase, "file://") {
				rootBase = fileURLToPath(rootBase)
			}
			pathBase := p
			if strings.HasPrefix(pathBase, "file://") {
				pathBase = fileURLToPath(pathBase)
			}
			if !isUnderRootPath(pathBase, rootBase) {
				return "", fmt.Errorf("path %s is outside root %s", p, rootAlias)
			}
		}
		lower := strings.ToLower(p)
		if strings.HasPrefix(lower, "workspace://") {
			return workspaceToFile(p), nil
		}
		return p, nil
	}
	if !mcpuri.Is(wsRoot) {
		rootBase := base
		if strings.HasPrefix(rootBase, "file://") {
			rootBase = fileURLToPath(rootBase)
		}
		if candidate := "/" + strings.TrimLeft(p, "/"); isUnderRootPath(candidate, rootBase) {
			return candidate, nil
		}
	}
	return url.Join(base, strings.TrimPrefix(p, "/")), nil
}

func relativePath(rootURI, fullURI string) string {
	root := strings.TrimSuffix(strings.TrimSpace(rootURI), "/")
	uri := strings.TrimSpace(fullURI)
	if root == "" || uri == "" {
		return ""
	}
	if mcpuri.Is(root) || mcpuri.Is(uri) {
		rootNorm := mcpuri.NormalizeForCompare(root)
		uriNorm := mcpuri.NormalizeForCompare(uri)
		if rootNorm == "" || uriNorm == "" {
			return uri
		}
		if !strings.HasPrefix(uriNorm, rootNorm) {
			return uri
		}
		rel := strings.TrimPrefix(uriNorm[len(rootNorm):], "/")
		return rel
	}
	if !strings.HasPrefix(uri, root) {
		return uri
	}
	rel := strings.TrimPrefix(uri[len(root):], "/")
	return rel
}

func workspaceToFile(ws string) string {
	base := url.Normalize(workspace.Root(), "file")
	rel := strings.TrimPrefix(ws, "workspace://")
	rel = strings.TrimPrefix(rel, "localhost/")
	return url.Join(strings.TrimRight(base, "/")+"/", rel)
}

func fileToWorkspace(file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(file), "file://") {
		file = fileURLToPath(file)
	}
	path := filepath.Clean(file)
	return "workspace://localhost" + url.Path(path)
}

func toWorkspaceURI(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	lower := strings.ToLower(v)
	if strings.HasPrefix(lower, "workspace://") || strings.HasPrefix(lower, "mcp://") {
		return v
	}
	if strings.HasPrefix(lower, "file://") {
		return fileToWorkspace(v)
	}
	if filepath.IsAbs(v) || isWindowsAbsPath(v) {
		return fileToWorkspace(v)
	}
	return v
}

func isWindowsAbsPath(v string) bool {
	if len(v) < 2 {
		return false
	}
	if v[1] != ':' {
		return false
	}
	if v[0] >= 'a' && v[0] <= 'z' || v[0] >= 'A' && v[0] <= 'Z' {
		return true
	}
	return false
}
