package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestMCPCatalogsKeepOnlyCanonicalReferences(t *testing.T) {
	var config Root
	require.NoError(t, yaml.Unmarshal([]byte("mcpCatalogs:\n - mcp://workspace/studio\n - mcp://workspace/other\n"), &config))
	refs, err := config.MCPCatalogs()
	require.NoError(t, err)
	require.Len(t, refs, 2)
	require.Equal(t, "mcp://workspace/studio", refs[0].URI)
	for _, input := range []any{true, "studio", []any{"studio"}, []any{"window://workspace/studio"}, []any{"mcp://workspace/studio", "mcp://workspace/studio"}, []any{map[string]any{"url": "https://example.invalid"}}} {
		_, err := (&Root{Raw: map[string]any{"mcpCatalogs": input}}).MCPCatalogs()
		require.Error(t, err)
	}
	refs, err = (&Root{}).MCPCatalogs()
	require.NoError(t, err)
	require.Empty(t, refs)
}
