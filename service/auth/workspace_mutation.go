package auth

import (
	"encoding/json"
	"fmt"
)

// WorkspaceMutation is a single presentation edit shared by the UI and tools.
// It is applied to the current stored document inside the preferences CAS path.
type WorkspaceMutation struct {
	MenuID        string                  `json:"menuId,omitempty" description:"Exact menu identity as JSON [providerIdentity, applicationId, ...menuIds] from authorized navigation metadata. Used by rename_menu and set_menu_visibility."`
	Presentation  string                  `json:"presentation,omitempty" description:"Shortcut presentation for set_presentation: menu, tiles or both. This changes display only."`
	Default       *WorkspaceReportDefault `json:"default,omitempty"`
	Action        string                  `json:"action" description:"One of pin, unpin, rename_item, move_item, create_group, rename_group, remove_group, move_group, set_enabled, set_presentation, set_title, rename_application, rename_menu, set_menu_visibility, set_application_visibility, set_default, clear_default."`
	GroupID       string                  `json:"groupId,omitempty" description:"Required stable ID for create_group. For a pin in a new group supply groupId and label together in the pin action; the writer creates the group and shortcut together."`
	Label         string                  `json:"label,omitempty" description:"Group label for pin/create_group/rename_group, menu alias for rename_item/rename_menu, application alias for rename_application, or workspace title for set_title. Empty clears a display alias or workspace title. Does not rename the underlying resource."`
	ItemID        string                  `json:"itemId,omitempty"`
	TargetGroupID string                  `json:"targetGroupId,omitempty"`
	Position      *int                    `json:"position,omitempty" description:"Zero-based final position for move_item or move_group."`
	Pin           *WorkspacePin           `json:"pin,omitempty"`
	Enabled       *bool                   `json:"enabled,omitempty"`
	ApplicationID string                  `json:"applicationId,omitempty" description:"Exact application identity from the current navigation metadata, encoded as the JSON pair [providerIdentity, applicationId]. Do not guess it from a display label."`
	Hidden        *bool                   `json:"hidden,omitempty"`
}

func applyWorkspaceMutation(existing any, mutation *WorkspaceMutation) (*WorkspacePreferences, error) {
	return applyWorkspaceMutationWithChange(existing, mutation, nil)
}

func applyWorkspaceMutationWithChange(existing any, mutation *WorkspaceMutation, change *WorkspaceDefaultChange) (result *WorkspacePreferences, err error) {
	if mutation == nil {
		return nil, fmt.Errorf("workspace mutation required")
	}
	raw, err := json.Marshal(existing)
	if err != nil {
		return nil, err
	}
	candidate := &WorkspacePreferences{}
	if existing != nil {
		if err = json.Unmarshal(raw, candidate); err != nil {
			return nil, fmt.Errorf("decode workspace preferences: %w", err)
		}
	}
	defer func() {
		if err != nil {
			err = &WorkspacePreferenceError{Cause: err}
		}
	}()

	groupIndex := func(id string) int {
		for i, g := range candidate.Groups {
			if g.ID == id {
				return i
			}
		}
		return -1
	}
	setLabel := func(labels map[string]string, key string) (map[string]string, error) {
		if !workspaceText(key, 4096) {
			return nil, fmt.Errorf("display identity required")
		}
		if labels == nil {
			labels = map[string]string{}
		}
		if mutation.Label == "" {
			delete(labels, key)
		} else {
			labels[key] = mutation.Label
		}
		return labels, nil
	}
	switch mutation.Action {
	case "set_default", "clear_default":
		if mutation.Default == nil {
			return nil, fmt.Errorf("report default reference required")
		}
		previous := candidate.Defaults
		candidate.Defaults = nil
		for _, entry := range previous {
			if entry.SourceProvider == mutation.Default.SourceProvider && entry.SourceURI == mutation.Default.SourceURI {
				if change != nil {
					value := entry
					change.Previous = &value
				}
				continue
			}
			candidate.Defaults = append(candidate.Defaults, entry)
		}
		if mutation.Action == "set_default" {
			value := *mutation.Default
			candidate.Defaults = append(candidate.Defaults, value)
			if change != nil {
				change.Current = &value
			}
		}
	case "pin":
		if mutation.Pin == nil {
			return nil, fmt.Errorf("pin required")
		}
		if err = candidate.Pin(mutation.GroupID, mutation.Label, *mutation.Pin); err != nil {
			return nil, err
		}
	case "set_presentation":
		if mutation.Presentation == "" {
			return nil, fmt.Errorf("workspace presentation required")
		}
		candidate.Presentation = mutation.Presentation
	case "set_enabled":
		if mutation.Enabled == nil {
			return nil, fmt.Errorf("enabled required")
		}
		value := *mutation.Enabled
		candidate.Enabled = &value
	case "set_title":
		candidate.Title = mutation.Label
	case "rename_application":
		candidate.ApplicationLabels, err = setLabel(candidate.ApplicationLabels, mutation.ApplicationID)
		if err != nil {
			return nil, err
		}
	case "rename_menu":
		candidate.MenuLabels, err = setLabel(candidate.MenuLabels, mutation.MenuID)
		if err != nil {
			return nil, err
		}
	case "set_application_visibility", "set_menu_visibility":
		key, limit, target := mutation.ApplicationID, 2048, &candidate.HiddenApplications
		if mutation.Action == "set_menu_visibility" {
			key, limit, target = mutation.MenuID, 4096, &candidate.HiddenMenus
		}
		if !workspaceText(key, limit) || mutation.Hidden == nil {
			return nil, fmt.Errorf("application identity and hidden required")
		}
		values := []string{}
		for _, id := range *target {
			if id != key {
				values = append(values, id)
			}
		}
		if *mutation.Hidden {
			values = append(values, key)
		}
		*target = values
	case "create_group":
		if groupIndex(mutation.GroupID) >= 0 {
			return nil, fmt.Errorf("group already exists")
		}
		if err := workspaceGroupLabelAvailable(candidate.Groups, mutation.Label, ""); err != nil {
			return nil, err
		}
		candidate.Groups = append(candidate.Groups, WorkspaceGroup{ID: mutation.GroupID, Label: mutation.Label})
	case "rename_group", "remove_group", "move_group":
		index := groupIndex(mutation.GroupID)
		if index < 0 {
			return nil, fmt.Errorf("workspace group not found")
		}
		switch mutation.Action {
		case "rename_group":
			if err := workspaceGroupLabelAvailable(candidate.Groups, mutation.Label, mutation.GroupID); err != nil {
				return nil, err
			}
			candidate.Groups[index].Label = mutation.Label
		case "remove_group":
			candidate.Groups = append(candidate.Groups[:index], candidate.Groups[index+1:]...)
		case "move_group":
			if mutation.Position == nil || *mutation.Position < 0 || *mutation.Position >= len(candidate.Groups) {
				return nil, fmt.Errorf("invalid group position")
			}
			group := candidate.Groups[index]
			candidate.Groups = append(candidate.Groups[:index], candidate.Groups[index+1:]...)
			position := *mutation.Position
			candidate.Groups = append(candidate.Groups, WorkspaceGroup{})
			copy(candidate.Groups[position+1:], candidate.Groups[position:])
			candidate.Groups[position] = group
		}
	case "unpin", "rename_item", "move_item":
		gi, ii := -1, -1
		for g, group := range candidate.Groups {
			for i, item := range group.Items {
				if item.ID == mutation.ItemID {
					gi, ii = g, i
					break
				}
			}
		}
		if gi < 0 {
			return nil, fmt.Errorf("workspace item not found")
		}
		item := candidate.Groups[gi].Items[ii]
		if mutation.Action == "rename_item" {
			candidate.Groups[gi].Items[ii].Alias = mutation.Label
			break
		}
		target := gi
		if mutation.Action == "move_item" && mutation.TargetGroupID != "" {
			target = groupIndex(mutation.TargetGroupID)
			if target < 0 {
				return nil, fmt.Errorf("target workspace group not found")
			}
		}
		candidate.Groups[gi].Items = append(candidate.Groups[gi].Items[:ii], candidate.Groups[gi].Items[ii+1:]...)
		if mutation.Action == "unpin" {
			break
		}
		position := len(candidate.Groups[target].Items)
		if mutation.Position != nil {
			position = *mutation.Position
		}
		items := candidate.Groups[target].Items
		if position < 0 || position > len(items) {
			return nil, fmt.Errorf("invalid item position")
		}
		items = append(items, WorkspacePin{})
		copy(items[position+1:], items[position:])
		items[position] = item
		candidate.Groups[target].Items = items
	default:
		return nil, fmt.Errorf("unsupported workspace action %q", mutation.Action)
	}
	if err = candidate.validate(); err != nil {
		return nil, err
	}
	return candidate, nil
}

// WorkspacePreferenceError denotes an invalid presentation edit, not a provider
// or database failure.
type WorkspacePreferenceError struct{ Cause error }

func (e *WorkspacePreferenceError) Error() string { return e.Cause.Error() }
func (e *WorkspacePreferenceError) Unwrap() error { return e.Cause }
