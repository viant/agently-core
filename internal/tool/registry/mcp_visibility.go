package tool

import (
	"context"
	"fmt"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/mcpapps"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

// MCPToolVisible consults the same principal-scoped advertised catalog used for
// exact dispatch identity. Visibility narrows exposure; it never grants a tool.
func (r *Registry) MCPToolVisible(ctx context.Context, name, audience string) (bool, error) {
	result, err := r.MCPToolsVisible(ctx, []string{name}, audience)
	return result[name], err
}

// MCPToolsVisible reads each relevant catalog once for the entire tool surface,
// rather than decoding and searching every server catalog for every tool.
func (r *Registry) MCPToolsVisible(ctx context.Context, names []string, audience string) (map[string]bool, error) {
	if len(names) == 0 {
		return map[string]bool{}, nil
	}
	type identity struct{ server, method string }
	requested := make(map[string]bool, len(names))
	matches := map[string]map[identity]map[string]interface{}{}
	for _, name := range names {
		requested[strings.TrimSpace(name)] = true
	}
	add := func(server string, tool mcpschema.Tool) {
		if server == "" || tool.Name == "" {
			return
		}
		full, colon := server+"/"+tool.Name, server+":"+tool.Name
		for _, alias := range []string{full, colon, mcpname.Canonical(full), mcpname.Canonical(colon)} {
			if !requested[alias] {
				continue
			}
			if matches[alias] == nil {
				matches[alias] = map[identity]map[string]interface{}{}
			}
			matches[alias][identity{server, tool.Name}] = tool.Meta
		}
	}
	public := map[string]bool{}
	isPublic := func(server string) bool {
		if value, ok := public[server]; ok {
			return value
		}
		value := r.isInternalServer(server) || r.isPublicToolCatalog(ctx, server)
		public[server] = value
		return value
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
		if isPublic(server) {
			add(server, entry.mcpDef)
		}
	}
	if authctx.EffectiveUserID(ctx) != "" && r.mgr != nil {
		servers, err := r.listServers(ctx)
		if err != nil {
			return nil, err
		}
		for _, server := range servers {
			if isPublic(server) || !r.hasObservedCatalog(ctx, server) {
				continue
			}
			if owner, ok := r.mgr.(interface{ MayHaveToolCatalogAlias(string, string) bool }); ok {
				candidate := false
				for name := range requested {
					if owner.MayHaveToolCatalogAlias(server, name) {
						candidate = true
						break
					}
				}
				if !candidate {
					continue
				}
			}
			snapshot, tools, err := r.cachedManagerCatalog(ctx, server)
			if snapshot == "" && err == nil {
				tools, err = r.listServerTools(ctx, server)
			}
			if err != nil {
				return nil, err
			}
			for _, tool := range tools {
				add(server, tool)
			}
		}
	}
	result := make(map[string]bool, len(names))
	for _, name := range names {
		candidates := matches[strings.TrimSpace(name)]
		if len(candidates) > 1 {
			return nil, fmt.Errorf("ambiguous advertised MCP tool alias %q", name)
		}
		result[name] = audience == "model" // native/unknown tools have no app authority
		for _, metadata := range candidates {
			result[name] = mcpapps.Visible(metadata, audience)
		}
	}
	return result, nil
}
