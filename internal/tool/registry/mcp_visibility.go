package tool

import (
	"context"
	"fmt"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/mcpapps"
)

// MCPToolVisible consults the same principal-scoped advertised catalog used for
// exact dispatch identity. Visibility narrows policy; it never grants a tool.
func (r *Registry) MCPToolVisible(ctx context.Context, name, audience string) (bool, error) {
	server, method, found, err := r.discoveredMCPIdentity(ctx, name)
	if err != nil {
		return false, err
	}
	if !found {
		return audience == "model", nil
	} // native tools have no app authority

	var metadata map[string]interface{}
	foundMetadata := false
	if r.isInternalServer(server) || r.isPublicToolCatalog(ctx, server) {
		r.mu.RLock()
		for _, entry := range r.cache {
			if entry != nil && entry.mcpDef.Name == method && entry.def.Name == server+"/"+method {
				metadata, foundMetadata = entry.mcpDef.Meta, true
				break
			}
		}
		r.mu.RUnlock()
	} else if r.mgr != nil {
		scoped := r.mgr.WithAuthTokenContext(ctx, server)
		useID := r.mgr.UseIDToken(scoped, server)
		token := authctx.MCPAuthToken(scoped, useID)
		if r.isDelegatedAuthServer(scoped, server) {
			token = ""
		}
		tools, ok := r.loadPrincipalDiscoveryTools(server, authctx.EffectiveUserID(scoped), token, useID)
		if ok {
			for _, tool := range tools {
				if tool.Name == method {
					metadata, foundMetadata = tool.Meta, true
					break
				}
			}
		}
	}
	if !foundMetadata {
		return false, fmt.Errorf("MCP tool visibility catalog is unavailable")
	}
	return mcpapps.Visible(metadata, audience), nil
}
