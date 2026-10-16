package tool

import (
	"context"

	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	toolschema "github.com/viant/agently-core/protocol/tool/schema"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

// promoteLegacyBodyArgs adapts older callers that wrapped declared MCP inputs
// in Body. Only a previously discovered schema defines the promoted input shape;
// absence or ambiguity leaves the arguments exactly as supplied.
func (r *Registry) promoteLegacyBodyArgs(ctx context.Context, server, method string, args map[string]interface{}) map[string]interface{} {
	if r == nil {
		return args
	}
	if _, ok := args["Body"].(map[string]interface{}); !ok {
		return args
	}
	properties, ok := r.cachedMCPInputProperties(ctx, server, method)
	if !ok {
		return args
	}
	return toolschema.PromoteLegacyBodyArguments(properties, args)
}

func (r *Registry) cachedMCPInputProperties(ctx context.Context, server, method string) (mcpschema.ToolInputSchemaProperties, bool) {
	if server == "" || method == "" {
		return nil, false
	}
	if r.toolCatalogVisibility(ctx, server) == mcpcfg.ToolsListVisibilityPrivate && !r.isInternalServer(server) {
		if r.mgr == nil {
			return nil, false
		}
		snapshot, tools, err := r.cachedManagerCatalog(ctx, server)
		if err != nil || snapshot == "" {
			return nil, false
		}
		for _, tool := range tools {
			if tool.Name == method {
				return tool.InputSchema.Properties, true
			}
		}
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, name := range []string{server + "/" + method, server + ":" + method} {
		if entry := r.cache[name]; entry != nil && entry.mcpDef.Name == method {
			return entry.mcpDef.InputSchema.Properties, true
		}
	}
	return nil, false
}
