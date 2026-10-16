package config

import (
	"fmt"

	identity "github.com/viant/agently-core/protocol/resource"
)

// MCPCatalogs identifies configuration catalogs by definitions published by the
// internal source. Endpoint and OAuth settings remain in those definitions.
func (r *Root) MCPCatalogs() ([]identity.ResourceRef, error) {
	if r == nil || r.Raw["mcpCatalogs"] == nil {
		return nil, nil
	}
	var values []interface{}
	switch input := r.Raw["mcpCatalogs"].(type) {
	case []interface{}:
		values = input
	case []string:
		for _, value := range input {
			values = append(values, value)
		}
	default:
		return nil, fmt.Errorf("mcpCatalogs must be a list of canonical MCP resource references")
	}
	result := make([]identity.ResourceRef, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		text, ok := value.(string)
		uri, err := identity.ParseResourceURI(text)
		if !ok || err != nil || uri.Kind != "mcp" || uri.String() != text || seen[text] {
			return nil, fmt.Errorf("mcpCatalogs requires distinct canonical MCP resource references")
		}
		seen[text] = true
		result = append(result, identity.ResourceRef{URI: text})
	}
	return result, nil
}
