package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDefaultsUnmarshalYAMLProjection(t *testing.T) {
	input := `
projection:
  relevance:
    enabled: true
    protectedRecentTurns: 1
    tokenThreshold: 42
    model: gpt-5-mini
    prompt:
      text: select relevant turns
      engine: go
  toolCallSupersession:
    enabled: false
    limit:
      history: 3
      turn: 4
`
	var got Defaults
	require.NoError(t, yaml.Unmarshal([]byte(input), &got))
	require.NotNil(t, got.Projection.Relevance)
	require.NotNil(t, got.Projection.Relevance.Enabled)
	require.True(t, *got.Projection.Relevance.Enabled)
	require.NotNil(t, got.Projection.Relevance.ProtectedRecentTurns)
	require.Equal(t, 1, *got.Projection.Relevance.ProtectedRecentTurns)
	require.NotNil(t, got.Projection.Relevance.TokenThreshold)
	require.Equal(t, 42, *got.Projection.Relevance.TokenThreshold)
	require.NotNil(t, got.Projection.Relevance.Model)
	require.Equal(t, "gpt-5-mini", *got.Projection.Relevance.Model)
	require.NotNil(t, got.Projection.Relevance.Prompt)
	require.Equal(t, "select relevant turns", got.Projection.Relevance.Prompt.Text)
	require.Equal(t, "go", got.Projection.Relevance.Prompt.Engine)
	require.NotNil(t, got.Projection.ToolCallSupersession)
	require.NotNil(t, got.Projection.ToolCallSupersession.Enabled)
	require.False(t, *got.Projection.ToolCallSupersession.Enabled)
	require.Equal(t, 3, got.Projection.SupersessionHistoryLimit())
	require.Equal(t, 4, got.Projection.SupersessionTurnLimit())
}

func TestDefaultsUnmarshalYAMLLegacyCompactionAlias(t *testing.T) {
	input := `
compaction:
  toolCallSupersession:
    enabled: false
    limit:
      history: 5
      turn: 6
`
	var got Defaults
	require.NoError(t, yaml.Unmarshal([]byte(input), &got))
	require.NotNil(t, got.Projection.ToolCallSupersession)
	require.NotNil(t, got.Projection.ToolCallSupersession.Enabled)
	require.False(t, *got.Projection.ToolCallSupersession.Enabled)
	require.Equal(t, 5, got.Projection.SupersessionHistoryLimit())
	require.Equal(t, 6, got.Projection.SupersessionTurnLimit())
}

func TestRelevanceProjection_Defaults(t *testing.T) {
	var relevance *RelevanceProjection
	require.True(t, relevance.IsEnabled())
	require.Equal(t, 1, relevance.ProtectedTurns())
	require.Equal(t, 20000, relevance.Threshold())
	require.Equal(t, 0, relevance.Chunk())
	require.Equal(t, 1, relevance.Concurrency())

	relevance = &RelevanceProjection{}
	require.True(t, relevance.IsEnabled())
	require.Equal(t, 1, relevance.ProtectedTurns())
	require.Equal(t, 20000, relevance.Threshold())
}
