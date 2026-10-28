package skill

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAuthoredSkillAlwaysIncludeMetadata(t *testing.T) {
	skill, _, err := Parse("SKILL.md", ".", "workspace", "---\nname: review\ndescription: Synthetic review procedure\nmetadata:\n  agently-always-include: \"true\"\n---\nSynthetic body.")
	require.NoError(t, err)
	require.True(t, skill.Frontmatter.AlwaysIncluded())
	require.False(t, (Frontmatter{}).AlwaysIncluded())
}
