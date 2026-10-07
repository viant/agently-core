package mcpapps

// Visible implements the stable Apps _meta.ui.visibility contract. Missing
// visibility defaults to model and app. Malformed/unknown audiences fail closed.
func Visible(meta map[string]interface{}, audience string) bool {
	if audience != "model" && audience != "app" {
		return false
	}
	raw, exists := meta["ui"]
	if !exists {
		return true
	}
	ui, ok := raw.(map[string]interface{})
	if !ok {
		return false
	}
	raw, exists = ui["visibility"]
	if !exists {
		return true
	}
	var values []string
	switch items := raw.(type) {
	case []string:
		values = items
	case []interface{}:
		for _, item := range items {
			value, ok := item.(string)
			if !ok {
				return false
			}
			values = append(values, value)
		}
	default:
		return false
	}
	allowed := false
	for _, value := range values {
		if value != "model" && value != "app" {
			return false
		}
		if value == audience {
			allowed = true
		}
	}
	return allowed
}
