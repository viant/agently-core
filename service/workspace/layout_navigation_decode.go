package workspace

import (
	"encoding/json"
	"fmt"

	identity "github.com/viant/agently-core/protocol/resource"
)

// UnmarshalJSON accepts the portable navigation child aliases and emits the
// shell's existing children shape. This adapts syntax, never resource access.
func (m *LayoutMenu) UnmarshalJSON(raw []byte) error {
	type plain LayoutMenu
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if value.Title == "" {
		if label, ok := fields["label"]; ok {
			if err := json.Unmarshal(label, &value.Title); err != nil {
				return err
			}
		}
	}
	childKey := ""
	for _, key := range []string{"children", "menus", "items"} {
		if _, exists := fields[key]; exists {
			if childKey != "" {
				return fmt.Errorf("navigation node has ambiguous child collections")
			}
			childKey = key
		}
	}
	if childKey != "" && childKey != "children" {
		if err := json.Unmarshal(fields[childKey], &value.Children); err != nil {
			return err
		}
	}
	if value.Action != nil && value.Action.Type == "" && value.Action.ResourceURI != "" {
		uri, err := identity.ParseResourceURI(value.Action.ResourceURI)
		if err != nil {
			return err
		}
		if uri.Kind == "window" || uri.Kind == "report" {
			value.Action.Type = uri.Kind
		}
	}
	*m = LayoutMenu(value)
	return nil
}
