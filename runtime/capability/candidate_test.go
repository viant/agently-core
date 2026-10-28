package capability

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDeclaredNamespacesAndDefaultPartition(t *testing.T) {
	require.Equal(t, "default", ToolNamespace("", "", ""))
	require.Equal(t, "default", ToolNamespace("", "mcp://example/service", ""))
	require.Equal(t, "example", ToolNamespace("", "mcp://example/service", "verified-owner"))
	require.Equal(t, "default", ToolNamespace("", "mcp://user@example/service", "verified-owner"))
	require.Equal(t, "declared", ToolNamespace("declared", "", ""))
	require.Equal(t, "default", SkillNamespace("", "local-skill"))
	require.Equal(t, "example", SkillNamespace("", "skill://example/review/SKILL.md"))
	require.Equal(t, "declared", SkillNamespace("declared", ""))
}
