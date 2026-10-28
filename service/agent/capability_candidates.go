package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/protocol/primitive"
	"github.com/viant/agently-core/runtime/capability"
	"github.com/viant/agently-core/service/augmenter"
	resourcesvc "github.com/viant/agently-core/service/resource"
)

func (s *Service) defaultNamespaceDescription() string {
	if s.defaults != nil && strings.TrimSpace(s.defaults.Discovery.DefaultNamespaceDescription) != "" {
		return s.defaults.Discovery.DefaultNamespaceDescription
	}
	return capability.DefaultNamespaceDescription
}

func intakeCandidate(item intakeCapability) capability.Candidate {
	candidate := capability.Candidate{Kind: item.Kind, Name: item.Name, Description: item.Description, URI: item.URI, Namespace: item.Namespace, Provider: item.ConnectionAlias, CacheScope: item.CacheScope, Retained: item.AlwaysInclude, LookupNames: item.LookupNames}
	if item.Kind == "tool" {
		candidate.Provider = item.Definition.SourceProviderIdentity
		candidate.URI = item.Definition.SourceResourceURI
		candidate.Retained = retainedToolDefinition(item.Definition)
	}
	return candidate
}

func (s *Service) rankIntakeCapabilities(ctx context.Context, input *QueryInput, items []intakeCapability, namespaces []resourcesvc.DiscoveredNamespace) ([]intakeCapability, bool, error) {
	if s.augmenter == nil || strings.TrimSpace(input.Query) == "" {
		return items, false, nil
	}
	model := strings.TrimSpace(input.EmbeddingModel)
	limit := capability.DefaultCandidateLimit
	if s.defaults != nil {
		if model == "" {
			model = strings.TrimSpace(s.defaults.Embedder)
		}
		if s.defaults.Discovery.CandidateLimit > 0 {
			limit = s.defaults.Discovery.CandidateLimit
		}
	}
	if model == "" {
		return items, false, nil
	}
	descriptions := map[string]string{capability.DefaultNamespace: s.defaultNamespaceDescription()}
	for _, namespace := range namespaces {
		if descriptor := describedNamespaceDescriptor(namespace.Namespace, namespaces); descriptor != nil {
			descriptions[namespace.Namespace] = descriptor.Description
		}
	}
	candidates := make([]capability.Candidate, 0, len(items))
	byID := make(map[string]intakeCapability, len(items))
	for _, item := range items {
		candidate := intakeCandidate(item)
		candidates = append(candidates, candidate)
		byID[candidate.ID()] = item
	}
	ranked, err := s.augmenter.RankCapabilities(ctx, augmenter.CapabilityRequest{Model: model, Query: input.Query, Limit: limit, Candidates: candidates, NamespaceDescriptions: descriptions})
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		debugtrace.Write("agent", "capability_ranking_unavailable", map[string]any{"errorType": fmt.Sprintf("%T", err), "candidateCount": len(items)})
		appendWarning(ctx, "Capability ranking is unavailable; full authorized discovery metadata remains available")
		return items, false, nil
	}
	result := make([]intakeCapability, 0, len(ranked))
	for _, candidate := range ranked {
		if item, ok := byID[candidate.ID()]; ok {
			result = append(result, item)
		}
	}
	debugtrace.Write("agent", "capability_candidates", map[string]any{"candidateCount": len(items), "selectedCount": len(result), "budget": limit})
	return result, true, nil
}

// Cards remain discovery guidance alongside selected metadata. They never hide
// the candidates needed by intake or restrict subsequent tools/skills listing.
func (s *Service) rankedIntakeMetadata(ctx context.Context, input *QueryInput, items []intakeCapability, namespaces []resourcesvc.DiscoveredNamespace) ([]any, []string) {
	encoded := make([]any, 0, len(items)+len(namespaces)+1)
	defaultKinds := map[string]bool{}
	for _, item := range items {
		encoded = append(encoded, item)
		if item.Namespace == capability.DefaultNamespace {
			kind := item.Kind
			if kind == "tool" {
				kind = "mcp"
			}
			defaultKinds[kind] = true
		}
	}
	retained := s.retainedNamespaceDiscovery(ctx, input)
	cards := map[string]namespaceCapability{}
	for _, namespace := range namespaces {
		descriptor := describedNamespaceDescriptor(namespace.Namespace, namespaces)
		if descriptor == nil {
			continue
		}
		if !(retained["mcp"] && namespaceSupports(*descriptor, "mcp") || retained["skill"] && namespaceSupports(*descriptor, "skill")) {
			continue
		}
		cards[descriptor.Namespace] = namespaceCapability{Kind: "namespace", Name: descriptor.Namespace, Description: descriptor.Description, UseCases: descriptor.UseCases, SupportedKinds: descriptor.Kinds}
	}
	if len(defaultKinds) > 0 {
		if _, declared := cards[capability.DefaultNamespace]; !declared {
			var supported []primitive.KindSupport
			for _, kind := range []string{"mcp", "skill"} {
				if defaultKinds[kind] {
					supported = append(supported, primitive.KindSupport{Kind: kind})
				}
			}
			cards[capability.DefaultNamespace] = namespaceCapability{Kind: "namespace", Name: capability.DefaultNamespace, Description: s.defaultNamespaceDescription(), SupportedKinds: supported}
		}
	}
	names := make([]string, 0, len(cards))
	for namespace := range cards {
		names = append(names, namespace)
	}
	sort.Strings(names)
	for _, namespace := range names {
		encoded = append(encoded, cards[namespace])
	}
	return encoded, names
}
