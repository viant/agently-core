package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"
	promptdef "github.com/viant/agently-core/protocol/prompt"
)

func TestCatalog(t *testing.T) {
	profiles := []*promptdef.Profile{
		{ID: "repo_analysis", Description: "Analyze repository state", AppliesTo: []string{"repo", "debug"}},
		{ID: "performance_analysis", Description: "Analyze performance", AppliesTo: []string{"performance"}},
	}
	got := Catalog(profiles, []string{"repo_analysis"})
	require.Contains(t, got, "Available scenario priors:")
	require.Contains(t, got, "- repo_analysis: Analyze repository state [repo, debug]")
	require.NotContains(t, got, "performance_analysis")
}
