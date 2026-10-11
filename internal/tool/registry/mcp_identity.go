package tool

import (
	"context"
	"fmt"
	"strings"

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
	if authctx.EffectiveUserID(ctx) != "" {
		servers, listErr := r.listServers(ctx)
		if listErr != nil {
			return "", "", false, listErr
		}
		for _, server := range servers {
			if r.isInternalServer(server) || r.isPublicToolCatalog(ctx, server) {
				continue
			}
			if !r.hasObservedCatalog(ctx, server) {
				continue
			}
			tools, listErr := r.listServerTools(ctx, server)
			if listErr != nil {
				return "", "", false, listErr
			}
			for _, tool := range tools {
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

func (r *Registry) hasObservedCatalog(ctx context.Context, server string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for key, identity := range r.discoveryShared {
		if strings.HasPrefix(key, server+"|") && identity.userID == authctx.EffectiveUserID(ctx) && identity.catalogSeen {
			return true
		}
	}
	return false
}
