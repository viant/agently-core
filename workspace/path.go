package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveChildPath accepts only a file beneath the workspace root, including
// after symlinks are resolved. A missing file can be returned for callers that
// intentionally fall back to an embedded default.
func ResolveChildPath(root, ref string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(ref))
	if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace reference escapes root")
	}
	rootPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, clean)
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		if os.IsNotExist(err) {
			return target, nil
		}
		return "", err
	}
	relative, err := filepath.Rel(rootPath, realTarget)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace reference escapes root")
	}
	return target, nil
}
