package tool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
)

// ToolSurfaceCacheIdentity describes a single backend namespace. Configuration
// and catalog content form its generation; credentials are never returned raw.
// Broad patterns stay uncached because they can span public and private servers.
func (r *Registry) ToolSurfaceCacheIdentity(ctx context.Context, pattern string) (kind, identity string) {
	server := serverFromPattern(pattern)
	if server == "" || strings.ContainsAny(server, "*?[]") {
		return "", ""
	}
	kind = "public"
	var config interface{}
	principal := ""
	credential := ""
	if !r.isInternalServer(server) {
		if r.mgr == nil {
			return "", ""
		}
		options, err := r.mgr.Options(ctx, server)
		if err != nil || options == nil || options.IsBrowserExecution() {
			return "", ""
		}
		// Public generations must not depend on per-user token stores attached
		// by RepoProvider.Options. Only schema-affecting server settings belong
		// in the shared generation.
		settings := map[string]interface{}{"visibility": options.ToolsListVisibility, "artifacts": options.OutputArtifacts}
		if options.ClientOptions != nil {
			settings["transport"] = options.ClientOptions.Transport
		}
		config = settings
		if r.toolCatalogVisibility(ctx, server) != mcpcfg.ToolsListVisibilityPublic {
			kind = "private"
			principal = authctx.EffectiveUserID(ctx)
			if principal == "" {
				return kind, ""
			}
			// Delegated credentials are resolved by the manager. Until it exposes
			// a credential generation, do not create a second cache above it.
			if options.IsDelegatedAuth() {
				return kind, ""
			}
			scoped := r.mgr.WithAuthTokenContext(ctx, server)
			useID := r.mgr.UseIDToken(scoped, server)
			credential = r.principalDiscoveryToolsKey(server, principal, authctx.MCPAuthToken(scoped, useID), useID)
		}
	}
	catalog := map[string]llm.ToolDefinition{}
	r.mu.RLock()
	if kind == "public" {
		for _, entry := range r.cache {
			if entry != nil && strings.HasPrefix(entry.def.Name, server+"/") {
				catalog[entry.def.Name] = entry.def
			}
		}
	} else if entry, ok := r.discoveryTools[credential]; ok && entry.expiresAt.After(time.Now()) {
		for _, item := range entry.tools {
			if definition := llm.ToolDefinitionFromMcpTool(&item); definition != nil {
				catalog[item.Name] = *definition
			}
		}
	}
	r.mu.RUnlock()
	if len(catalog) == 0 {
		return kind, ""
	}
	raw, err := json.Marshal([]interface{}{server, config, principal, credential, catalog})
	if err != nil {
		return kind, ""
	}
	return kind, fmt.Sprintf("%x", sha256.Sum256(raw))
}
