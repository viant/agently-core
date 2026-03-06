package resources

import (
	"context"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agmodel "github.com/viant/agently-core/protocol/agent"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpuri "github.com/viant/agently-core/protocol/mcp/uri"
	"github.com/viant/agently-core/runtime/memory"
	mcpfs "github.com/viant/agently-core/service/augmenter/mcpfs"
)

func isAllowedWorkspace(loc string, allowed []string) bool {
	uKey := normalizeWorkspaceKey(loc)
	if uKey == "" {
		return false
	}
	// Compare canonical workspace:// or mcp: prefixes
	for _, a := range allowed {
		aKey := normalizeWorkspaceKey(a)
		if aKey == "" {
			continue
		}
		if strings.HasPrefix(uKey, aKey) {
			return true
		}
	}
	return false
}

func normalizeWorkspaceKey(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	if mcpuri.Is(v) {
		v = mcpuri.NormalizeForCompare(v)
	} else {
		v = toWorkspaceURI(v)
		v = strings.TrimRight(v, "/")
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func (s *Service) agentAllowed(ctx context.Context) []string {
	ag := s.currentAgent(ctx)
	if ag == nil {
		return nil
	}
	expanded := s.agentResources(ctx, ag)
	if len(expanded) == 0 {
		return nil
	}
	out := make([]string, 0, len(expanded))
	for _, e := range expanded {
		if e == nil {
			continue
		}
		if u := strings.TrimSpace(e.URI); u != "" {
			ws, _, err := s.normalizeUserRoot(ctx, u)
			if err != nil || ws == "" {
				continue
			}
			out = append(out, ws)
		}
	}
	return out
}

// semanticAllowedForAgent reports whether semantic match is permitted for the
// given normalized workspace root under the provided agent configuration. When
// no matching resource is found, the effective value defaults to true.
func (s *Service) semanticAllowedForAgent(ctx context.Context, ag *agmodel.Agent, wsRoot string) bool {
	ws := strings.TrimRight(strings.TrimSpace(wsRoot), "/")
	if ws == "" || ag == nil {
		if mcpuri.Is(ws) {
			if meta, ok := s.mcpRootMeta(ctx, ws); ok && meta != nil {
				return meta.Vectorize && meta.Snapshot
			}
			return false
		}
		return true
	}
	for _, r := range s.agentResources(ctx, ag) {
		if r == nil || strings.TrimSpace(r.URI) == "" {
			continue
		}
		norm, _, err := s.normalizeUserRoot(ctx, r.URI)
		if err != nil || strings.TrimSpace(norm) == "" {
			continue
		}
		if strings.TrimRight(strings.TrimSpace(norm), "/") == ws {
			return r.SemanticAllowed()
		}
	}
	if mcpuri.Is(ws) {
		if meta, ok := s.mcpRootMeta(ctx, ws); ok && meta != nil {
			return meta.Vectorize && meta.Snapshot
		}
		return false
	}
	return true
}

// grepAllowedForAgent reports whether grepFiles is permitted for the given
// normalized workspace root under the provided agent configuration. When no
// matching resource is found, the effective value defaults to true.
func (s *Service) grepAllowedForAgent(ctx context.Context, ag *agmodel.Agent, wsRoot string) bool {
	ws := strings.TrimRight(strings.TrimSpace(wsRoot), "/")
	if ws == "" {
		return true
	}
	isMCP := mcpuri.Is(ws)
	// When no agent or resources are present, default to allowing grep on
	// local/workspace roots but require an explicit resource with allowGrep
	// for MCP roots.
	if ag == nil {
		if isMCP {
			if meta, ok := s.mcpRootMeta(ctx, ws); ok && meta != nil {
				return meta.AllowGrep && meta.Snapshot
			}
			return false
		}
		return true
	}
	for _, r := range s.agentResources(ctx, ag) {
		if r == nil || strings.TrimSpace(r.URI) == "" {
			continue
		}
		norm, _, err := s.normalizeUserRoot(ctx, r.URI)
		if err != nil || strings.TrimSpace(norm) == "" {
			continue
		}
		if strings.TrimRight(strings.TrimSpace(norm), "/") == ws {
			return r.GrepAllowed()
		}
	}
	// No matching resource: allow grep by default for local/workspace roots,
	// but require explicit opt-in for MCP roots.
	if isMCP {
		if meta, ok := s.mcpRootMeta(ctx, ws); ok && meta != nil {
			return meta.AllowGrep && meta.Snapshot
		}
		return false
	}
	return true
}

// mcpRootMeta resolves MCP resource metadata for the provided MCP root.
func (s *Service) mcpRootMeta(ctx context.Context, location string) (*mcpcfg.ResourceRoot, bool) {
	if s == nil || s.mcpMgr == nil {
		return nil, false
	}
	server, _ := mcpuri.Parse(location)
	if strings.TrimSpace(server) == "" {
		return nil, false
	}
	opts, err := s.mcpMgr.Options(ctx, server)
	if err != nil || opts == nil {
		return nil, false
	}
	roots := mcpcfg.ResourceRoots(opts.Metadata)
	if len(roots) == 0 {
		return nil, false
	}
	normLoc := strings.TrimRight(strings.TrimSpace(location), "/")
	if mcpuri.Is(normLoc) {
		normLoc = normalizeMCPURI(normLoc)
	}
	for _, root := range roots {
		uri := strings.TrimRight(strings.TrimSpace(root.URI), "/")
		if uri == "" {
			continue
		}
		if mcpuri.Is(uri) {
			uri = normalizeMCPURI(uri)
		}
		if normLoc == uri || strings.HasPrefix(normLoc, uri+"/") {
			r := root
			return &r, true
		}
	}
	return nil, false
}

// mcpSnapshotResolver builds a snapshot resolver based on MCP metadata roots.
func (s *Service) mcpSnapshotResolver(ctx context.Context) mcpfs.SnapshotResolver {
	return func(location string) (snapshotURI, rootURI string, ok bool) {
		root, found := s.mcpRootMeta(ctx, location)
		if !found || root == nil || !root.Snapshot {
			return "", "", false
		}
		rootURI = strings.TrimRight(strings.TrimSpace(root.URI), "/")
		if rootURI == "" {
			return "", "", false
		}
		snapshotURI = strings.TrimSpace(root.SnapshotURI)
		if snapshotURI == "" {
			snapshotURI = rootURI + "/_snapshot.zip"
		}
		return snapshotURI, rootURI, true
	}
}

// mcpSnapshotManifestResolver reports whether snapshot MD5 manifests are enabled for a root.
func (s *Service) mcpSnapshotManifestResolver(ctx context.Context) mcpfs.SnapshotManifestResolver {
	return func(location string) bool {
		root, found := s.mcpRootMeta(ctx, location)
		if !found || root == nil || !root.Snapshot {
			return false
		}
		return root.SnapshotMD5
	}
}

// currentAgent returns the active agent from conversation context, if available.
func (s *Service) currentAgent(ctx context.Context) *agmodel.Agent {
	if s.aFinder == nil {
		return nil
	}
	if tm, ok := memory.TurnMetaFromContext(ctx); ok {
		if agentID := strings.TrimSpace(tm.Assistant); agentID != "" {
			ag, err := s.aFinder.Find(ctx, agentID)
			if err == nil && ag != nil {
				return ag
			}
		}
	}
	if s.conv == nil {
		return nil
	}
	convID := memory.ConversationIDFromContext(ctx)
	if strings.TrimSpace(convID) == "" {
		return nil
	}
	resp, err := apiconv.NewService(s.conv).Get(ctx, apiconv.GetRequest{Id: convID})
	if err != nil || resp == nil || resp.Conversation == nil {
		return nil
	}
	tr := resp.Conversation.GetTranscript()
	var agentID string
	if len(tr) > 0 {
		t := tr[len(tr)-1]
		if t != nil && t.AgentIdUsed != nil && strings.TrimSpace(*t.AgentIdUsed) != "" {
			agentID = strings.TrimSpace(*t.AgentIdUsed)
		}
	}
	if strings.TrimSpace(agentID) == "" {
		// Fallback: use conversation.AgentId when present
		if resp.Conversation.AgentId != nil && strings.TrimSpace(*resp.Conversation.AgentId) != "" {
			agentID = strings.TrimSpace(*resp.Conversation.AgentId)
		} else {
			return nil
		}
	}
	ag, err := s.aFinder.Find(ctx, agentID)
	if err != nil {
		return nil
	}
	return ag
}
