package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestHydrateSelectedURIBackedTool(t *testing.T) {
	s, ctx, reg, _ := retainedCatalogService(t, nil)
	full, err := reg.MatchDefinitionWithContextResult(ctx, "mcp://catalog/source:*")
	require.NoError(t, err)
	require.Len(t, full, 2)
	var read *llm.ToolDefinition
	for _, def := range full {
		if def.Name == "mcp://catalog/source/Read" {
			read = def
		}
	}
	require.NotNil(t, read)
	require.NotEmpty(t, read.Parameters)
	selected := llm.ToolDefinition{Name: modelDefinitionName(*read), SourceMCPServer: read.SourceMCPServer}
	actual, err := s.hydrateSelectedDefinitions(ctx, []llm.ToolDefinition{selected})
	require.NoError(t, err)
	require.Equal(t, selected.Name, actual[0].Name)
	require.Equal(t, read.Parameters, actual[0].Parameters)
}
