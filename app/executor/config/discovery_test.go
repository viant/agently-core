package config

import (
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"testing"
)

func TestDiscoveryDefaultsDecodeWorkspaceConfiguration(t *testing.T) {
	var defaults Defaults
	require.NoError(t, yaml.Unmarshal([]byte("embedder: fixture\ndiscovery:\n  candidateLimit: 40\n  defaultNamespaceDescription: Workspace discovery guidance\n"), &defaults))
	require.Equal(t, 40, defaults.Discovery.CandidateLimit)
	require.Equal(t, "Workspace discovery guidance", defaults.Discovery.DefaultNamespaceDescription)
	require.Equal(t, "fixture", defaults.Embedder)
}
