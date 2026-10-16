package executor

import (
	"github.com/stretchr/testify/require"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/agent/finder"
	llmagents "github.com/viant/agently-core/protocol/tool/service/llm/agents"
	"testing"
)

func TestConfiguredAgentDirectoryPreservesHostAssembly(t *testing.T) {
	local := finder.New(finder.WithInitial(&agentmodel.Agent{Identity: agentmodel.Identity{ID: "local", Name: "Local"}}, &agentmodel.Agent{Identity: agentmodel.Identity{ID: "internal"}, Internal: true}))
	provider := localAgentDirectory(local, func() []llmagents.ListItem {
		return []llmagents.ListItem{{ID: "external", Name: "Remote", Source: "external", Capabilities: map[string]interface{}{"mcp": true}}, {ID: "local", Name: "Host local", Source: "internal"}}
	})
	entries := provider()
	require.Len(t, entries, 3)
	byID := map[string]llmagents.ListItem{}
	for _, v := range entries {
		byID[v.ID] = v
	}
	require.Equal(t, "Host local", byID["local"].Name)
	require.Equal(t, "external", byID["external"].Source)
	require.Equal(t, true, byID["external"].Capabilities["mcp"])
	require.True(t, byID["internal"].Internal)
	require.Empty(t, localAgentDirectory(finder.New(), nil)(), "empty configuration must not invent fallback agents")
}

func TestConfiguredDirectoryIncludesNameOnlyIdentityWithoutMutation(t *testing.T) {
	original := &agentmodel.Agent{Identity: agentmodel.Identity{Name: "Name only"}}
	entries := localAgentDirectory(finder.New(finder.WithInitial(original)), nil)()
	require.Len(t, entries, 1)
	require.Equal(t, "Name only", entries[0].ID)
	require.Empty(t, original.ID)
}
