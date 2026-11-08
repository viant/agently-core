package agent

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	skillproto "github.com/viant/agently-core/protocol/skill"
	"testing"
)

func TestActiveInlineCanonicalRemoteSkillHasOneOwningRestore(t *testing.T) {
	canonical := (skillproto.Ref{ServerID: "source", Name: "review-guide", ResourceURI: "skill://vendor/review-guide/SKILL.md"}).URI()
	for _, test := range []struct {
		name   string
		alias  bool
		denied bool
		reads  int
	}{
		{"canonical", false, false, 1},
		{"revoked canonical keeps restrictions", false, true, 1},
		{"document alias preserves normalization", true, false, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newLoadedTestSkillService(t, t.TempDir(), "review-guide")
			name := canonical
			if test.alias {
				name += "/SKILL.md"
			}
			owner := &exactSkillControlCatalog{item: &skillproto.Skill{CatalogURI: canonical, ServerID: "source", Body: "remote synthetic body", Frontmatter: skillproto.Frontmatter{Name: "review-guide"}}}
			if test.denied {
				owner.readErr = fmt.Errorf("synthetic owner denial")
			}
			service.SetDelivery(skillControlSources{}, owner)
			history := &binding.History{Current: &binding.Turn{Messages: []*binding.Message{{Kind: binding.MessageKindToolResult, ToolName: "llm/skills:activate", ToolArgs: map[string]interface{}{"name": name}}}}}
			state := resolveActiveInlineSkillState(history, nil, service, &agentmdl.Agent{Skills: []string{"*"}}, context.Background())
			require.Equal(t, []string{name}, state.Names)
			require.Equal(t, test.reads, owner.reads)
			if test.denied {
				require.Empty(t, state.Skills)
				require.Nil(t, state.Primary)
			} else {
				require.Len(t, state.Skills, 1)
				require.Equal(t, "remote synthetic body", state.Primary.Body)
			}
		})
	}
}
