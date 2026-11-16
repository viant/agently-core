package auth

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWorkspaceMutationsOrganizeExactReferencesWithoutChangingInput(t *testing.T) {
	original := WorkspacePreferences{}
	p, err := applyWorkspaceMutation(original, &WorkspaceMutation{Action: "pin", GroupID: "weekly", Label: "Weekly", Pin: &WorkspacePin{ID: "orders", Kind: "window", Provider: "example", URI: "window://example/orders"}})
	require.NoError(t, err)
	require.Empty(t, original.Groups)
	p, err = applyWorkspaceMutation(p, &WorkspaceMutation{Action: "create_group", GroupID: "planning", Label: "Planning"})
	require.NoError(t, err)
	before, _ := json.Marshal(p)
	p, err = applyWorkspaceMutation(p, &WorkspaceMutation{Action: "move_item", ItemID: "orders", TargetGroupID: "planning"})
	require.NoError(t, err)
	require.Empty(t, p.Groups[0].Items)
	require.Equal(t, "window://example/orders", p.Groups[1].Items[0].URI)
	p, err = applyWorkspaceMutation(p, &WorkspaceMutation{Action: "rename_item", ItemID: "orders", Label: "Current orders"})
	require.NoError(t, err)
	require.Equal(t, "Current orders", p.Groups[1].Items[0].Alias)
	require.Equal(t, "window://example/orders", p.Groups[1].Items[0].URI)
	position := 0
	p, err = applyWorkspaceMutation(p, &WorkspaceMutation{Action: "move_group", GroupID: "planning", Position: &position})
	require.NoError(t, err)
	require.Equal(t, "planning", p.Groups[0].ID)
	p, err = applyWorkspaceMutation(p, &WorkspaceMutation{Action: "unpin", ItemID: "orders"})
	require.NoError(t, err)
	require.Empty(t, p.Groups[0].Items)
	var saved WorkspacePreferences
	require.NoError(t, json.Unmarshal(before, &saved))
	require.Len(t, saved.Groups[0].Items, 1)
}
func TestWorkspaceMutationFailureAndLargeParameterIdentity(t *testing.T) {
	original := WorkspacePreferences{Groups: []WorkspaceGroup{{ID: "weekly", Label: "Weekly"}}}
	_, err := applyWorkspaceMutation(original, &WorkspaceMutation{Action: "rename_group", GroupID: "weekly", Label: ""})
	var invalid *WorkspacePreferenceError
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, "Weekly", original.Groups[0].Label)
	a := WorkspacePin{Kind: "report", Provider: "example", URI: "report://example/overview", Parameters: map[string]json.RawMessage{"entityId": json.RawMessage(`9007199254740992`)}}
	b := a
	b.Parameters = map[string]json.RawMessage{"entityId": json.RawMessage(`9007199254740993`)}
	require.False(t, samePin(a, b), "different exact integer parameters must not deduplicate")
}

func TestWorkspaceDefaultChangesPreservePinsAndVisibility(t *testing.T) {
	disabled := false
	p := WorkspacePreferences{Enabled: &disabled, Groups: []WorkspaceGroup{{ID: "weekly", Label: "Weekly"}}}
	entry := &WorkspaceReportDefault{SourceProvider: "published", SourceURI: "report://example/overview", TargetURI: "report://users/alice/weekly"}
	updated, err := applyWorkspaceMutation(p, &WorkspaceMutation{Action: "set_default", Default: entry})
	require.NoError(t, err)
	require.Len(t, updated.Defaults, 1)
	require.False(t, *updated.Enabled)
	require.Equal(t, p.Groups, updated.Groups)
	cleared, err := applyWorkspaceMutation(updated, &WorkspaceMutation{Action: "clear_default", Default: entry})
	require.NoError(t, err)
	require.Empty(t, cleared.Defaults)
	require.Len(t, updated.Defaults, 1, "mutation cannot edit the original preference candidate")
}

func TestWorkspaceMoveCannotDuplicateAnExistingLaunch(t *testing.T) {
	first := WorkspacePin{ID: "first", Kind: "report", Provider: "example", URI: "report://example/overview"}
	second := first
	second.ID = "second"
	second.Parameters = map[string]json.RawMessage{}
	original := WorkspacePreferences{Groups: []WorkspaceGroup{{ID: "weekly", Label: "Weekly", Items: []WorkspacePin{first}}, {ID: "planning", Label: "Planning", Items: []WorkspacePin{second}}}}
	_, err := applyWorkspaceMutation(original, &WorkspaceMutation{Action: "move_item", ItemID: "first", TargetGroupID: "planning"})
	var invalid *WorkspacePreferenceError
	require.ErrorAs(t, err, &invalid)
	require.Len(t, original.Groups[0].Items, 1, "failed move retains the source pin")
	require.Len(t, original.Groups[1].Items, 1)
	// A distinct parameter preset remains a distinct launch of the same resource.
	original.Groups[1].Items[0].Parameters = map[string]json.RawMessage{"period": json.RawMessage(`"monthly"`)}
	moved, err := applyWorkspaceMutation(original, &WorkspaceMutation{Action: "move_item", ItemID: "first", TargetGroupID: "planning"})
	require.NoError(t, err)
	require.Empty(t, moved.Groups[0].Items)
	require.Len(t, moved.Groups[1].Items, 2)
}

func TestWorkspacePinDefaultParametersHaveOneIdentity(t *testing.T) {
	var preferences WorkspacePreferences
	pin := WorkspacePin{ID: "one", Kind: "window", Provider: "example", URI: "window://example/orders"}
	require.NoError(t, preferences.Pin("weekly", "Weekly", pin))
	pin.ID = "retry"
	pin.Parameters = map[string]json.RawMessage{}
	require.NoError(t, preferences.Pin("weekly", "Weekly", pin))
	require.Len(t, preferences.Groups[0].Items, 1)
	require.Equal(t, "one", preferences.Groups[0].Items[0].ID)
	duplicate := preferences.Groups[0].Items[0]
	duplicate.ID = "another"
	preferences.Groups[0].Items = append(preferences.Groups[0].Items, duplicate)
	require.Error(t, preferences.validate(), "bulk preference updates must enforce the same launch identity")
}

func TestWorkspacePresentationChangesDisplayOnly(t *testing.T) {
	enabled := true
	original := WorkspacePreferences{Enabled: &enabled, Groups: []WorkspaceGroup{{ID: "weekly", Label: "Weekly", Items: []WorkspacePin{{ID: "overview", Kind: "report", Provider: "example", URI: "report://example/overview"}}}}, HiddenApplications: []string{"example"}}
	for _, mode := range []string{"menu", "tiles", "both"} {
		changed, err := applyWorkspaceMutation(original, &WorkspaceMutation{Action: "set_presentation", Presentation: mode})
		require.NoError(t, err)
		require.Equal(t, mode, changed.Presentation)
		require.Equal(t, original.Groups, changed.Groups)
		require.Equal(t, original.HiddenApplications, changed.HiddenApplications)
		require.True(t, *changed.Enabled)
	}
	for _, mode := range []string{"", "invalid"} {
		_, err := applyWorkspaceMutation(original, &WorkspaceMutation{Action: "set_presentation", Presentation: mode})
		var invalid *WorkspacePreferenceError
		require.ErrorAs(t, err, &invalid)
	}
	require.Empty(t, original.Presentation, "display edits do not mutate the original preference object")
}

func TestWorkspaceLabelsAndMenuVisibilityArePersonalPresentation(t *testing.T) {
	original := WorkspacePreferences{Groups: []WorkspaceGroup{{ID: "weekly", Label: "Weekly"}}}
	app := `["example","app"]`
	menu := `["example","app","orders"]`
	changed, err := applyWorkspaceMutation(original, &WorkspaceMutation{Action: "set_title", Label: "My desk"})
	require.NoError(t, err)
	changed, err = applyWorkspaceMutation(changed, &WorkspaceMutation{Action: "rename_application", ApplicationID: app, Label: "My application"})
	require.NoError(t, err)
	changed, err = applyWorkspaceMutation(changed, &WorkspaceMutation{Action: "rename_menu", MenuID: menu, Label: "My orders"})
	require.NoError(t, err)
	hidden := true
	changed, err = applyWorkspaceMutation(changed, &WorkspaceMutation{Action: "set_menu_visibility", MenuID: menu, Hidden: &hidden})
	require.NoError(t, err)
	require.Equal(t, "My desk", changed.Title)
	require.Equal(t, "My application", changed.ApplicationLabels[app])
	require.Equal(t, "My orders", changed.MenuLabels[menu])
	require.Equal(t, []string{menu}, changed.HiddenMenus)
	require.Equal(t, original.Groups, changed.Groups)
	require.Empty(t, original.Title)
	require.Empty(t, original.ApplicationLabels)
	reset, err := applyWorkspaceMutation(changed, &WorkspaceMutation{Action: "rename_menu", MenuID: menu, Label: ""})
	require.NoError(t, err)
	require.Empty(t, reset.MenuLabels)
	_, err = applyWorkspaceMutation(changed, &WorkspaceMutation{Action: "set_title", Label: " invalid "})
	require.Error(t, err)
}
