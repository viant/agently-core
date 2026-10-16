package tool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
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
	generation := uint64(0)
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
			snapshot, err := r.cachedManagerCatalogIdentity(ctx, server)
			if err != nil || snapshot == "" {
				return kind, ""
			}
			credential = snapshot
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
	}
	r.mu.RUnlock()
	if kind == "public" && len(catalog) == 0 {
		return kind, ""
	}
	if owner, ok := r.mgr.(interface{ ToolCatalogGeneration(string) uint64 }); ok {
		generation = owner.ToolCatalogGeneration(server)
	}
	raw, err := json.Marshal([]interface{}{server, config, principal, credential, generation, catalog})
	if err != nil {
		return kind, ""
	}
	return kind, fmt.Sprintf("%x", sha256.Sum256(raw))
}

// cachedManagerCatalog uses the exact discovery connection and request options.
// The manager alone decides whether that complete source catalog is reusable.
func (r *Registry) cachedManagerCatalog(ctx context.Context, server string) (string, []mcpschema.Tool, error) {
	owner, ok := r.mgr.(interface {
		ToolCatalogSnapshot(context.Context, string, string, ...mcpclient.RequestOption) (string, []mcpschema.Tool, error)
	})
	if !ok {
		return "", nil, nil
	}
	ctx, conversation, opts, available := r.managerCatalogRequest(ctx, server)
	if !available {
		return "", nil, nil
	}
	return owner.ToolCatalogSnapshot(ctx, conversation, server, opts...)
}

// Cache identities need only the manager's existing page revisions. Alternate
// managers keep the original snapshot compatibility path.
func (r *Registry) cachedManagerCatalogIdentity(ctx context.Context, server string) (string, error) {
	if owner, ok := r.mgr.(interface {
		ToolCatalogIdentity(context.Context, string, string, ...mcpclient.RequestOption) (string, error)
	}); ok {
		ctx, conversation, opts, available := r.managerCatalogRequest(ctx, server)
		if !available {
			return "", nil
		}
		return owner.ToolCatalogIdentity(ctx, conversation, server, opts...)
	}
	identity, _, err := r.cachedManagerCatalog(ctx, server)
	return identity, err
}

// Both consumers retain exactly the same credential and discovery locator.
func (r *Registry) managerCatalogRequest(ctx context.Context, server string) (context.Context, string, []mcpclient.RequestOption, bool) {
	ctx = r.mgr.WithAuthTokenContext(ctx, server)
	useID := r.mgr.UseIDToken(ctx, server)
	token := authctx.MCPAuthToken(ctx, useID)
	if r.isDelegatedAuthServer(ctx, server) {
		token = ""
	}
	scope := ""
	if options, err := r.mgr.Options(ctx, server); err == nil && options != nil {
		scope = r.discoveryClientScope(ctx, server, token, r.isPublicToolCatalog(ctx, server))
	} else {
		scope = requestctx.ConversationIDFromContext(ctx)
	}
	if scope == "" {
		r.mu.RLock()
		var newest discoveryIdentity
		for key, observed := range r.discoveryShared {
			if strings.HasPrefix(key, server+"|") && observed.catalogSeen && observed.userID == authctx.EffectiveUserID(ctx) && observed.tokenFP == tokenFingerprint(token) && observed.useID == useID && observed.seenAt.After(newest.seenAt) {
				scope = strings.TrimPrefix(key, server+"|")
				newest = observed
			}
		}
		r.mu.RUnlock()
	}
	if scope == "" {
		return ctx, "", nil, false
	}
	var opts []mcpclient.RequestOption
	if token != "" {
		opts = append(opts, mcpclient.WithAuthToken(token))
	}
	return ctx, r.metadataConnectionScope(ctx, scope, server), opts, true
}
