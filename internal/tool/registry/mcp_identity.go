package tool

import (
	"context"
	"fmt"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcpname"
)

// splitExplicitMCPTool preserves a namespace MCP resource as the service and
// leaves the remote method literal. Model aliases use catalog identity instead.
func splitExplicitMCPTool(name string) (service, method string, ok bool) {
	start := 0
	if strings.HasPrefix(name, "mcp://") {
		start = len("mcp://")
	}
	delimiter := strings.IndexByte(name[start:], ':')
	if delimiter < 0 || start+delimiter == 0 || start+delimiter == len(name)-1 {
		return "", "", false
	}
	i := start + delimiter
	return name[:i], name[i+1:], true
}

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
	nameMatches := func(server, method string) bool {
		full, colon := server+"/"+method, server+":"+method
		return name == full || name == colon || name == mcpname.Canonical(full) || name == mcpname.Canonical(colon)
	}
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
		// Resolve authority only for aliases that could match this lookup.
		// Checking visibility for every unrelated cached tool reloads the same
		// server configuration thousands of times during binding construction.
		if !nameMatches(server, entry.mcpDef.Name) {
			continue
		}
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
			if owner, ok := r.mgr.(interface{ MayHaveToolCatalogAlias(string, string) bool }); ok && !owner.MayHaveToolCatalogAlias(server, name) {
				continue
			}
			if r.isInternalServer(server) || r.isPublicToolCatalog(ctx, server) {
				continue
			}
			if !r.hasObservedCatalog(ctx, server) {
				continue
			}
			snapshot, tools, listErr := r.cachedManagerCatalog(ctx, server)
			if snapshot != "" {
				candidate := false
				for _, tool := range tools {
					if nameMatches(server, tool.Name) {
						candidate = true
						break
					}
				}
				if !candidate {
					continue
				}
			} else if listErr == nil {
				tools, listErr = r.listServerTools(ctx, server)
			}
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
