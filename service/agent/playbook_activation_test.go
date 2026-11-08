package agent

import (
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/runtime/toolselection"
	"testing"
)

func TestPlaybookActivationUsesVisibleWildcardToolsWithoutExecution(t *testing.T) {
	s, ctx, fixture := discoveryService(t)
	input := selectionInput(nil)
	profile := &intent.Profile{ID: "example", PreferredTools: []string{"operations:report*", "operations:report", "operations:appOnly"}}
	activate, err := s.preparePlaybookTools(ctx, input, profile)
	require.NoError(t, err)
	state, ok := toolselection.FromContext(ctx)
	require.True(t, ok)
	require.Empty(t, state.SelectedNames(), "preparation must not activate before messages are injected")
	require.NoError(t, activate())
	require.Equal(t, []string{"operations-report"}, state.SelectedNames(), "duplicate patterns collapse and app-only tools remain hidden")
	require.Zero(t, fixture.calls, "activation must not execute tools")
	definitions, err := s.buildToolSignatures(ctx, input)
	require.NoError(t, err)
	require.Contains(t, signatureNames(definitions), "operations-report", "next model binding sees the selected tool")
}

func TestPlaybookActivationHonorsCallerScope(t *testing.T) {
	s, ctx, fixture := discoveryService(t)
	input := selectionInput(nil)
	input.ToolsAllowed = []string{"operations:configure"}
	_, err := s.preparePlaybookTools(ctx, input, &intent.Profile{ID: "example", PreferredTools: []string{"operations:report*"}})
	require.ErrorContains(t, err, "no visible matches")
	state, _ := toolselection.FromContext(ctx)
	require.Empty(t, state.SelectedNames())
	require.Zero(t, fixture.calls)
}

func TestPlaybookActivationRetainsLegacyBundleDeclarations(t *testing.T) {
	s, ctx, fixture := discoveryService(t)
	input := selectionInput(nil)
	activate, err := s.preparePlaybookTools(ctx, input, &intent.Profile{ID: "example", ToolBundles: []string{"reports"}})
	require.NoError(t, err)
	require.NoError(t, activate())
	state, _ := toolselection.FromContext(ctx)
	require.ElementsMatch(t, []string{"operations-report", "operations-otherReport"}, state.SelectedNames())
	require.Zero(t, fixture.calls)
}
