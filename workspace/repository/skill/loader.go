package skill

import (
	"os"
	"path/filepath"
	"strings"

	execconfig "github.com/viant/agently-core/app/executor/config"
	skillproto "github.com/viant/agently-core/protocol/skill"
)

type Loader struct {
	roots []string
}

func New(defaults *execconfig.Defaults) *Loader {
	return &Loader{roots: ResolveRoots(defaults)}
}

// Roots is a snapshot owned by this runtime. Watcher reloads must not resolve
// another runtime's mutable process-wide workspace root.
func (l *Loader) Roots() []string { return append([]string(nil), l.roots...) }

func (l *Loader) LoadAll() (*skillproto.Registry, error) {
	reg := skillproto.NewRegistry()
	for _, root := range l.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			reg.Add(nil, []skillproto.Diagnostic{{Level: "warning", Message: "unable to read skill root", Path: root}})
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillRoot := filepath.Join(root, entry.Name())
			skillPath := filepath.Join(skillRoot, "SKILL.md")
			data, err := os.ReadFile(skillPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				reg.Add(nil, []skillproto.Diagnostic{{Level: "warning", Message: "unable to read skill", Path: skillPath}})
				continue
			}
			source := detectSource(root)
			s, diags, err := skillproto.Parse(skillPath, skillRoot, source, string(data))
			if err != nil {
				reg.Add(nil, []skillproto.Diagnostic{{Level: "error", Message: err.Error(), Path: skillPath}})
				continue
			}
			if s != nil && strings.TrimSpace(s.Frontmatter.Name) != "" && filepath.Base(skillRoot) != strings.TrimSpace(s.Frontmatter.Name) {
				diags = append(diags, skillproto.Diagnostic{Level: "error", Message: "skill directory name must match frontmatter name", Path: skillPath})
			}
			reg.Add(s, diags)
		}
	}
	return reg, nil
}

func detectSource(root string) string {
	root = filepath.ToSlash(root)
	switch {
	case strings.Contains(root, "/.claude/skills"):
		return "claude"
	case strings.Contains(root, "/.codex/skills"):
		return "codex"
	case strings.Contains(root, "/.agently/skills"):
		return "agently"
	default:
		return "workspace"
	}
}
