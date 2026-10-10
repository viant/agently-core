package auth

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/viant/agently-core/service/auth/providerregistry"
)

type mcpConnector struct {
	Provider  string `json:"provider"`
	Server    string `json:"server,omitempty"`
	Connected bool   `json:"connected"`
	Pending   bool   `json:"pending,omitempty"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

func (s *mcpLinkService) connectors(ctx context.Context, userID, sessionID string) ([]mcpConnector, error) {
	refs, err := s.delegated.registry.References(ctx)
	if err != nil {
		return nil, err
	}
	lister, ok := s.configs.(mcpAuthConfigLister)
	if !ok {
		return nil, fmt.Errorf("connector listing unavailable")
	}
	names, err := lister.Names(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	result := make([]mcpConnector, 0)
	seen := map[string]bool{}
	for _, name := range names {
		cfg, err := s.configs.Options(ctx, name)
		if err != nil {
			return nil, err
		}
		if cfg == nil || !cfg.IsDelegatedAuth() {
			continue
		}
		ref := cfg.Auth.ProviderRef
		if cfg.Auth.InlineProvider != nil {
			ref = cfg.Auth.InlineProvider.ID
		}
		if ref == "" {
			continue
		}
		seen[ref] = true
		row := mcpConnector{Provider: ref, Server: name}
		link, err := s.resolveServer(ctx, name)
		if err != nil {
			row.Reason = "Configuration unavailable"
			if providerregistry.IsDisabled(err) || err == errMCPProviderDisabled {
				row.Reason = "Disabled by administrator"
			}
		} else {
			row.Available = true
			status := s.status(ctx, name, userID, sessionID)
			row.Connected, row.Pending = status.Connected, status.Pending
			// A saved refresh grant remains connected even after access expiry.
			if stored, e := s.delegated.resolver.store.GetExact(ctx, userID, link.resolved.storageKey); e == nil && stored != nil && stored.RefreshToken != "" && s.delegated.resolver.validateStored(stored, link.requirement, link.resolved) == nil {
				row.Connected = true
			}
		}
		result = append(result, row)
	}
	for _, ref := range refs {
		if !seen[ref] {
			result = append(result, mcpConnector{Provider: ref, Reason: "No MCP server configured"})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Provider == result[j].Provider {
			return result[i].Server < result[j].Server
		}
		return result[i].Provider < result[j].Provider
	})
	return result, nil
}

func (a *authExtension) handleMCPConnectors() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := a.requireMCPLinkSession(w, r)
		if !ok {
			return
		}
		if !a.mcpLink.userActive(r.Context(), identity.canonicalUserID) {
			runtimeJSON(w, http.StatusForbidden, map[string]string{"code": "oauth_link_unavailable"})
			return
		}
		if !a.mcpLink.limits.statusUser.Allow(identity.canonicalUserID) {
			writeMCPLinkRateLimited(w)
			return
		}
		rows, err := a.mcpLink.connectors(r.Context(), identity.canonicalUserID, identity.session.ID)
		if err != nil {
			runtimeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "connector_list_unavailable"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		runtimeJSON(w, http.StatusOK, map[string]any{"connections": rows})
	}
}
