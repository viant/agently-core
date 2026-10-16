package config

import "strings"

// ToolCatalogWarmupExcludedServers returns deployment-owned eager discovery
// exclusions. The explicit list has no effect on manual tool use or access.
func (r *Root) ToolCatalogWarmupExcludedServers() []string {
	if r == nil {
		return nil
	}
	section := mapLookup(r.Raw, "toolCatalogWarmup")
	var names []string
	switch values := section["excludedServers"].(type) {
	case []string:
		names = values
	case []interface{}:
		for _, value := range values {
			if name, ok := value.(string); ok {
				names = append(names, name)
			}
		}
	}
	var result []string
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			result = append(result, name)
			seen[name] = true
		}
	}
	return result
}
