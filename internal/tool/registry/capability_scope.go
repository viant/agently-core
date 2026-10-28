package tool

import (
	"context"

	"github.com/viant/agently-core/genai/llm"
)

// CapabilityCacheScopes exports only the host's existing catalog visibility.
// Provider metadata cannot opt a private catalog into a shared embedding cache.
func (r *Registry) CapabilityCacheScopes(ctx context.Context, definitions []llm.ToolDefinition) map[string]string {
	scopes := make(map[string]string, len(definitions))
	servers := map[string]string{}
	for _, definition := range definitions {
		r.mu.RLock()
		_, virtual := r.virtualDefs[definition.Name]
		r.mu.RUnlock()
		if virtual {
			scopes[definition.Name] = "conversation"
			continue
		}
		server := definition.SourceMCPServer
		if server == "" {
			scopes[definition.Name] = "public"
			continue
		}
		scope, ok := servers[server]
		if !ok {
			scope = "user"
			if r.isInternalServer(server) || r.isPublicToolCatalog(ctx, server) {
				scope = "public"
			}
			servers[server] = scope
		}
		scopes[definition.Name] = scope
	}
	return scopes
}
