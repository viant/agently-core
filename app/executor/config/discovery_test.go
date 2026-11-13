package config

import (
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"testing"
)

func TestDiscoveryDefaultsDecodeWorkspaceConfiguration(t *testing.T) {
	var defaults Defaults
	require.NoError(t, yaml.Unmarshal([]byte("embedder: fixture\ndiscovery:\n  ranker: bm25\n  candidateLimit: 40\n  defaultNamespaceDescription: Workspace discovery guidance\n"), &defaults))
	require.Equal(t, "bm25", defaults.Discovery.Ranker)
	require.Equal(t, 40, defaults.Discovery.CandidateLimit)
	require.Equal(t, "Workspace discovery guidance", defaults.Discovery.DefaultNamespaceDescription)
	require.Equal(t, "fixture", defaults.Embedder)
}

func TestRetainedToolFailureIsolationIsDefaultWithExplicitStrictOptOut(t *testing.T) {
	var defaults Defaults
	require.NoError(t, yaml.Unmarshal([]byte("discovery: {}\n"), &defaults))
	require.False(t, defaults.Discovery.FailOnUnavailableRetainedTools)
	require.NoError(t, yaml.Unmarshal([]byte("discovery:\n  failOnUnavailableRetainedTools: true\n"), &defaults))
	require.True(t, defaults.Discovery.FailOnUnavailableRetainedTools)
}
