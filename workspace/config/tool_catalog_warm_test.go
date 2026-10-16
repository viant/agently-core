package config

import (
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"testing"
)

func TestToolCatalogWarmupExcludedServers(t *testing.T) {
	var root Root
	require.NoError(t, yaml.Unmarshal([]byte("toolCatalogWarmup:\n  excludedServers: [\" slow-service \", slow-service, browser-service, \"\"]\n"), &root))
	require.Equal(t, []string{"slow-service", "browser-service"}, root.ToolCatalogWarmupExcludedServers())
	first := root.ToolCatalogWarmupExcludedServers()
	first[0] = "mutated"
	require.Equal(t, "slow-service", root.ToolCatalogWarmupExcludedServers()[0])
	require.Empty(t, (&Root{}).ToolCatalogWarmupExcludedServers())
	require.Empty(t, (*Root)(nil).ToolCatalogWarmupExcludedServers())
}
