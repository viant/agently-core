package layoutgate

import (
	"encoding/json"
)

// WindowVisible preserves the original application ancestry gate for authored
// window actions. A window not mentioned in navigation retains direct OPEN;
// a mentioned window needs at least one currently visible authored action.
func WindowVisible(original, visible json.RawMessage, key string) (bool, error) {
	collect := func(raw json.RawMessage) (map[string]bool, error) {
		var root struct {
			Applications []json.RawMessage `json:"applications"`
		}
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, err
		}
		out := map[string]bool{}
		var walk func([]json.RawMessage) error
		walk = func(nodes []json.RawMessage) error {
			for _, raw := range nodes {
				var node struct {
					Action          *struct{ Type, Provider, WindowKey string }
					Menus, Children []json.RawMessage
				}
				if err := json.Unmarshal(raw, &node); err != nil {
					return err
				}
				if node.Action != nil && node.Action.Type == "window" {
					name := node.Action.WindowKey
					if node.Action.Provider != "" && node.Action.Provider != "workspace" {
						name = "provider:" + node.Action.Provider + ":" + name
					}
					out[name] = true
				}
				if err := walk(node.Menus); err != nil {
					return err
				}
				if err := walk(node.Children); err != nil {
					return err
				}
			}
			return nil
		}
		return out, walk(root.Applications)
	}
	declared, err := collect(original)
	if err != nil {
		return false, err
	}
	if !declared[key] {
		return true, nil
	}
	allowed, err := collect(visible)
	if err != nil {
		return false, err
	}
	return allowed[key], nil
}
