package skill

import (
	"context"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	format "github.com/viant/mcp-protocol/extension/skills"
	"os"
)

// ExportLocal compiles operator-selected local skill trees. Remote skill
// federation is intentionally not re-exported, preventing aggregator cycles.
func (s *Service) ExportLocal(ctx context.Context, patterns []string) ([]*format.Static, error) {
	var out []*format.Static
	for _, item := range s.visibleSkills(&agentmdl.Agent{Skills: patterns}) {
		compiled, err := (format.Compiler{Source: os.DirFS(item.Root)}).Compile(ctx, "skill://agently/"+item.Frontmatter.Name+"/SKILL.md")
		if err != nil {
			return nil, err
		}
		out = append(out, compiled)
	}
	return out, nil
}
