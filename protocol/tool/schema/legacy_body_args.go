package schema

import mcpschema "github.com/viant/mcp-protocol/schema"

// PromoteLegacyBodyArguments adapts older callers that wrapped top-level MCP
// inputs in Body. The observed tool schema must declare every nested field;
// ambiguous arguments are passed through unchanged.
func PromoteLegacyBodyArguments(properties mcpschema.ToolInputSchemaProperties, args map[string]interface{}) map[string]interface{} {
	if len(args) == 0 || len(properties) == 0 {
		return args
	}
	body, ok := args["Body"].(map[string]interface{})
	if !ok || len(body) == 0 {
		return args
	}
	if _, declared := properties["Body"]; declared {
		return args
	}
	for key := range body {
		if _, declared := properties[key]; !declared {
			return args
		}
		if _, collision := args[key]; collision {
			return args
		}
	}
	promoted := make(map[string]interface{}, len(args)-1+len(body))
	for key, value := range args {
		if key != "Body" {
			promoted[key] = value
		}
	}
	for key, value := range body {
		promoted[key] = value
	}
	return promoted
}
