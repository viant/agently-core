package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcpname"
)

// ResolveMCPIdentity shares the catalog identity used by dispatch with policy gates.
func (r *Registry) ResolveMCPIdentity(ctx context.Context, name string) (string, string, bool, error) {
	return r.discoveredMCPIdentity(ctx, name)
}

// discoveredMCPIdentity resolves an advertised alias from the existing catalogs.
// A provider name is lossy; the catalog owns the server and literal tool name.
func (r *Registry) discoveredMCPIdentity(ctx context.Context, name string) (server, method string, found bool, err error) {
	name = strings.TrimSpace(name)
	type identity struct{ server, method string }
	matches := map[identity]bool{}
	add := func(server, method string) {
		if server == "" || method == "" {
			return
		}
		full := server + "/" + method
		colon := server + ":" + method
		if name == full || name == colon || name == mcpname.Canonical(full) || name == mcpname.Canonical(colon) {
			matches[identity{server, method}] = true
		}
	}
	r.mu.RLock()
	entries := make([]*toolCacheEntry, 0, len(r.cache))
	for _, entry := range r.cache {
		entries = append(entries, entry)
	}
	// Private catalogs remain in their existing principal/token cache. Snapshot
	// candidates, then check their exact scoped key before inspecting tool names.
	catalogs := make(map[string]discoveryToolsCacheEntry, len(r.discoveryTools))
	for key, entry := range r.discoveryTools {
		catalogs[key] = entry
	}
	r.mu.RUnlock()
	for _, entry := range entries {
		if entry == nil || entry.mcpDef.Name == "" {
			continue
		}
		suffix := "/" + entry.mcpDef.Name
		if !strings.HasSuffix(entry.def.Name, suffix) {
			continue
		}
		server := strings.TrimSuffix(entry.def.Name, suffix)
		if r.isInternalServer(server) || r.isPublicToolCatalog(ctx, server) {
			add(server, entry.mcpDef.Name)
		}
	}
	user := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if user != "" {
		for key, entry := range catalogs {
			if !entry.expiresAt.After(time.Now()) {
				continue
			}
			server := strings.SplitN(key, "\x1f", 2)[0]
			scoped := ctx
			useID := false
			if r.mgr != nil {
				scoped = r.mgr.WithAuthTokenContext(ctx, server)
				useID = r.mgr.UseIDToken(scoped, server)
			}
			token := authctx.MCPAuthToken(scoped, useID)
			if r.isDelegatedAuthServer(scoped, server) {
				token = ""
			}
			if key != r.principalDiscoveryToolsKey(server, user, token, useID) {
				continue
			}
			for _, tool := range entry.tools {
				add(server, tool.Name)
			}
		}
	}
	if len(matches) > 1 {
		return "", "", false, fmt.Errorf("ambiguous advertised MCP tool alias %q", name)
	}
	for match := range matches {
		return match.server, match.method, true, nil
	}
	return "", "", false, nil
}
