package auth

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWorkspacePinsPreserveExactTargetsAndExplicitDisable(t *testing.T) {
	disabled := false
	p := WorkspacePreferences{Enabled: &disabled, HiddenApplications: []string{"provider/app"}}
	original := WorkspacePin{ID: "original", Kind: "report", Provider: "provider", URI: "report://example/overview"}
	require.NoError(t, p.Pin("weekly", "Weekly", original))
	require.False(t, *p.Enabled)
	require.Equal(t, original, p.Groups[0].Items[0])
	require.NoError(t, p.Pin("weekly", "Weekly", WorkspacePin{ID: "window", Kind: "window", Provider: "provider", URI: "window://example/orders"}))
	require.Len(t, p.Groups[0].Items, 2)
	require.Equal(t, []string{"provider/app"}, p.HiddenApplications)
}
func TestWorkspaceFirstPinAndRetry(t *testing.T) {
	var p WorkspacePreferences
	pin := WorkspacePin{ID: "one", Kind: "report", Provider: "provider", URI: "report://example/overview", Parameters: map[string]json.RawMessage{"b": json.RawMessage(`2`), "a": json.RawMessage(`1`)}}
	require.NoError(t, p.Pin("", "", pin))
	require.True(t, *p.Enabled)
	pin.ID = "retry"
	pin.Parameters = map[string]json.RawMessage{"a": json.RawMessage(`1`), "b": json.RawMessage(`2`)}
	require.NoError(t, p.Pin("", "", pin))
	require.Len(t, p.Groups[0].Items, 1)
}
func TestWorkspaceInvalidPinDoesNotMutatePreferences(t *testing.T) {
	var p WorkspacePreferences
	require.Error(t, p.Pin("", "", WorkspacePin{ID: "bad", Kind: "report", Provider: "provider", URI: "window://example/orders"}))
	require.Empty(t, p.Groups)
	require.Nil(t, p.Enabled)
}
