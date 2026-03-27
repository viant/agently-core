package resources

import (
	"context"
	"fmt"
	"strings"

	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpuri "github.com/viant/agently-core/protocol/mcp/uri"
	embopt "github.com/viant/embedius/matching/option"
	embSchema "github.com/viant/embedius/schema"
)

type searchRootMeta struct {
	id     string
	wsRoot string
}

func (s *Service) selectSearchRoots(ctx context.Context, roots []Root, input *MatchInput) ([]Root, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("no roots configured")
	}
	allowed := s.agentAllowed(ctx)
	var selected []Root
	seen := map[string]struct{}{}
	add := func(candidates []Root) {
		for _, root := range candidates {
			key := strings.ToLower(strings.TrimSpace(root.ID))
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(root.URI))
			}
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok || !root.AllowedSemanticSearch {
				continue
			}
			seen[key] = struct{}{}
			selected = append(selected, root)
		}
	}
	if len(input.RootIDs) > 0 {
		matchedByID := filterRootsByID(roots, input.RootIDs)
		add(matchedByID)
		missing := missingRootIDs(input.RootIDs, matchedByID)
		if len(missing) > 0 {
			matchedByURI := filterRootsByURI(roots, missing)
			add(matchedByURI)
			if remaining := missingRootIDs(input.RootIDs, append(matchedByID, matchedByURI...)); len(remaining) > 0 {
				var unresolved []string
				curAgent := s.currentAgent(ctx)
				for _, id := range remaining {
					if strings.Contains(id, "://") || mcpuri.Is(id) || strings.HasPrefix(id, "workspace://") || strings.HasPrefix(id, "file://") {
						unresolved = append(unresolved, id)
						continue
					}
					uri, err := s.resolveRootID(ctx, id)
					if err != nil || strings.TrimSpace(uri) == "" {
						unresolved = append(unresolved, id)
						continue
					}
					if len(allowed) > 0 && !isAllowedWorkspace(uri, allowed) {
						return nil, fmt.Errorf("rootId not allowed: %s", id)
					}
					if !s.semanticAllowedForAgent(ctx, curAgent, uri) {
						return nil, fmt.Errorf("rootId not semantic-enabled: %s", id)
					}
					add([]Root{{ID: strings.TrimSpace(id), URI: uri, AllowedSemanticSearch: true, Role: "system"}})
				}
				if len(unresolved) > 0 {
					return nil, fmt.Errorf("unknown rootId(s): %s", strings.Join(unresolved, ", "))
				}
			}
		}
	}
	if len(selected) == 0 {
		uris := append([]string(nil), input.RootURI...)
		uris = append(uris, input.Roots...)
		if len(uris) > 0 {
			add(filterRootsByURI(roots, uris))
		}
	}
	if len(selected) == 0 {
		add(roots)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no semantic-enabled roots available")
	}
	return selected, nil
}

func filterRootsByID(roots []Root, ids []string) []Root {
	if len(ids) == 0 {
		return nil
	}
	idSet := map[string]struct{}{}
	for _, raw := range ids {
		if trimmed := normalizeRootID(raw); trimmed != "" {
			idSet[trimmed] = struct{}{}
		}
	}
	var out []Root
	for _, root := range roots {
		id := normalizeRootID(root.ID)
		if id == "" {
			id = normalizeRootID(root.URI)
		}
		if id != "" {
			if _, ok := idSet[id]; ok {
				out = append(out, root)
			}
		}
	}
	return out
}

func filterRootsByURI(roots []Root, uris []string) []Root {
	if len(uris) == 0 {
		return nil
	}
	uriSet := map[string]struct{}{}
	for _, raw := range uris {
		if trimmed := normalizeRootURI(raw); trimmed != "" {
			uriSet[trimmed] = struct{}{}
		}
	}
	var out []Root
	for _, root := range roots {
		uri := normalizeRootURI(root.URI)
		if uri == "" {
			continue
		}
		if _, ok := uriSet[uri]; ok {
			out = append(out, root)
		}
	}
	return out
}

func missingRootIDs(requested []string, matched []Root) []string {
	if len(requested) == 0 {
		return nil
	}
	matchedSet := map[string]struct{}{}
	for _, root := range matched {
		if key := normalizeRootID(root.ID); key != "" {
			matchedSet[key] = struct{}{}
		}
		if key := normalizeRootURI(root.URI); key != "" {
			matchedSet[key] = struct{}{}
		}
	}
	seen := map[string]struct{}{}
	var missing []string
	for _, raw := range requested {
		idKey := normalizeRootID(raw)
		uriKey := normalizeRootURI(raw)
		if idKey == "" && uriKey == "" {
			continue
		}
		if _, ok := matchedSet[idKey]; ok {
			continue
		}
		if _, ok := matchedSet[uriKey]; ok {
			continue
		}
		key := idKey
		if key == "" {
			key = uriKey
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		missing = append(missing, raw)
	}
	return missing
}

func normalizeRootID(value string) string {
	v := strings.TrimSpace(value)
	if mcpuri.Is(v) {
		v = mcpuri.NormalizeForCompare(v)
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func normalizeRootURI(value string) string {
	v := strings.TrimSpace(value)
	if mcpuri.Is(v) {
		v = mcpuri.NormalizeForCompare(v)
	} else {
		v = strings.TrimRight(v, "/")
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func assignRootMetadata(doc *embSchema.Document, roots []searchRootMeta) {
	if doc == nil || len(roots) == 0 {
		return
	}
	path := documentMetadataPath(doc.Metadata)
	if path == "" {
		return
	}
	normalized := normalizeWorkspaceKey(path)
	for _, entry := range roots {
		prefix := normalizeWorkspaceKey(entry.wsRoot)
		if prefix != "" && (normalized == prefix || strings.HasPrefix(normalized, prefix+"/")) {
			doc.Metadata["rootId"] = entry.id
			return
		}
	}
}

func normalizeSearchPath(p string, wsRoot string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return ""
	}
	trimmed = toWorkspaceURI(trimmed)
	root := strings.TrimRight(strings.TrimSpace(wsRoot), "/")
	if root == "" {
		return trimmed
	}
	if strings.EqualFold(strings.TrimRight(trimmed, "/"), root) {
		return ""
	}
	if strings.HasPrefix(trimmed, root+"/") {
		return strings.TrimPrefix(trimmed[len(root):], "/")
	}
	return trimmed
}

func mergeMatchOptions(base *embopt.Options, include, exclude []string, maxSizeBytes int64) *embopt.Options {
	if base == nil && len(include) == 0 && len(exclude) == 0 && maxSizeBytes <= 0 {
		return nil
	}
	out := &embopt.Options{}
	if base != nil {
		*out = *base
	}
	if len(out.Inclusions) == 0 && len(include) > 0 {
		out.Inclusions = append([]string(nil), include...)
	}
	if len(out.Exclusions) == 0 && len(exclude) > 0 {
		out.Exclusions = append([]string(nil), exclude...)
	}
	if out.MaxFileSize == 0 && maxSizeBytes > 0 {
		out.MaxFileSize = int(maxSizeBytes)
	}
	return out
}

func mergeEffectiveMatch(rootMatch, inputMatch *embopt.Options) *embopt.Options {
	if rootMatch == nil && inputMatch == nil {
		return nil
	}
	out := &embopt.Options{}
	if rootMatch != nil {
		*out = *rootMatch
	}
	if inputMatch != nil {
		if len(inputMatch.Inclusions) > 0 {
			out.Inclusions = append([]string(nil), inputMatch.Inclusions...)
		}
		if len(inputMatch.Exclusions) > 0 {
			out.Exclusions = append([]string(nil), inputMatch.Exclusions...)
		}
		if inputMatch.MaxFileSize > 0 {
			out.MaxFileSize = inputMatch.MaxFileSize
		}
	}
	if len(out.Inclusions) == 0 && len(out.Exclusions) == 0 && out.MaxFileSize == 0 {
		return nil
	}
	return out
}

func matchKey(rootMatch, inputMatch *embopt.Options) string {
	eff := mergeEffectiveMatch(rootMatch, inputMatch)
	if eff == nil {
		return ""
	}
	return fmt.Sprintf("max=%d|incl=%s|excl=%s", eff.MaxFileSize, strings.Join(eff.Inclusions, ","), strings.Join(eff.Exclusions, ","))
}

func matchesMCPSelector(selectors []string, root mcpcfg.ResourceRoot) bool {
	if len(selectors) == 0 {
		return true
	}
	rootURI := strings.TrimSpace(root.URI)
	rootID := strings.TrimSpace(root.ID)
	if rootID == "" {
		rootID = rootURI
	}
	for _, sel := range selectors {
		if sel == "" {
			continue
		}
		if normalizeRootID(sel) == normalizeRootID(rootID) || normalizeRootURI(sel) == normalizeRootURI(rootURI) {
			return true
		}
	}
	return false
}

func normalizeMCPURI(value string) string {
	if !mcpuri.Is(value) {
		return value
	}
	server, uri := mcpuri.Parse(value)
	if strings.TrimSpace(server) == "" {
		return value
	}
	normalized := mcpuri.Canonical(server, uri)
	if normalized == "" {
		return value
	}
	return normalized
}
