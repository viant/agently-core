package skill

import (
	"os"
	"path/filepath"
	"strings"

	execconfig "github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/workspace"
)

func ResolveRoots(defaults *execconfig.Defaults) []string {
	var roots []string
	if defaults != nil && len(defaults.Skills.Roots) > 0 {
		roots = append(roots, defaults.Skills.Roots...)
	} else {
		roots = []string{
			filepath.Join(workspace.Root(), workspace.KindSkill),
			"./skills",
			"./.claude/skills",
			"./.codex/skills",
		}
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		roots = append(roots,
			filepath.Join(home, ".claude", "skills"),
			filepath.Join(home, ".codex", "skills"),
		)
	}
	seen := map[string]struct{}{}
	var out []string
	for _, root := range roots {
		if v := resolveRoot(root); v != "" {
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func resolveRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	root = strings.ReplaceAll(root, "${AGENTLY_WORKSPACE}", workspace.Root())
	root = strings.ReplaceAll(root, "${workspaceRoot}", workspace.Root())
	if strings.HasPrefix(root, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			root = filepath.Join(home, root[2:])
		}
	}
	if !filepath.IsAbs(root) {
		if cwd, err := os.Getwd(); err == nil && cwd != "" {
			root = filepath.Join(cwd, root)
		} else {
			root = filepath.Join(workspace.Root(), root)
		}
	}
	return filepath.Clean(root)
}
