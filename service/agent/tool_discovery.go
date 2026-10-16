package agent

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	toolmatcher "github.com/viant/agently-core/internal/tool/matcher"
	"github.com/viant/agently-core/protocol/mcpname"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	toolservice "github.com/viant/agently-core/protocol/tool/service/llm/tools"
	"github.com/viant/agently-core/runtime/toolselection"
	agenttool "github.com/viant/agently-core/service/agent/tool"
)

func hasToolDiscoverySelection(ctx context.Context, input *QueryInput) bool {
	_, state := toolselection.FromContext(ctx)
	if profile := selectedPromptProfileFromContext(ctx); profile != nil {
		for _, name := range profile.PreferredTools {
			if !strings.ContainsAny(name, "*?[]") && strings.TrimSpace(name) != "" {
				return true
			}
		}
	}
	return state || intakeToolSelectionHint(input) != nil
}

// Discovery uses the existing principal-scoped catalog and its normal warm
// cache, independently of which workflow's bundles are currently selected.
func (s *Service) discoverableToolDefinitions(ctx context.Context, input *QueryInput) ([]llm.ToolDefinition, error) {
	if s == nil || s.registry == nil || input == nil || input.Agent == nil {
		return nil, nil
	}
	profile, err := s.selectedPromptProfileAny(ctx, input)
	if err != nil {
		return nil, err
	}
	if profile != nil && profile.Execution != nil && profile.Execution.DisableTools {
		return nil, nil
	}
	if profile != nil {
		ctx = withSelectedPromptProfile(ctx, profile)
	}
	definitions, err := s.callerScopedToolDefinitions(ctx, input, s.definitions(ctx))
	if err != nil {
		return nil, err
	}
	bundles, err := s.discoveryBundles(ctx, definitions)
	if err != nil {
		return nil, err
	}
	match := func(pattern string) []*llm.ToolDefinition {
		var matches []*llm.ToolDefinition
		for i := range definitions {
			if toolmatcher.Match(pattern, definitions[i].Name) {
				matches = append(matches, &definitions[i])
			}
		}
		return matches
	}
	// Bundle membership is not discovery authority. Keep every connected
	// definition unless an explicit exclusion removes it without another trusted
	// rule admitting it. Resolve rule metadata through the canonical resolver.
	allowed := map[string]bool{}
	denied := map[string]bool{}
	var annotated []llm.ToolDefinition
	ruleSets := make([][]llm.Tool, 0, len(bundles)+1)
	for _, bundle := range bundles {
		ruleSets = append(ruleSets, bundle.Match)
	}
	direct := agenttool.Merge(agenttool.FromPromptProfile(profile), runtimeToolRequirements(input))
	directRules := append([]llm.Tool(nil), agentToolRules(input)...)
	for _, pattern := range direct.Tools {
		directRules = append(directRules, llm.Tool{Name: pattern})
	}
	ruleSets = append(ruleSets, directRules)
	for _, rules := range ruleSets {
		resolved := toolbundle.ResolveDefinitionsWithOptions(&toolbundle.Bundle{Match: rules}, match)
		annotated = append(annotated, resolved.Definitions...)
		for _, definition := range resolved.Definitions {
			allowed[mcpname.Canonical(definition.Name)] = true
		}
		for _, rule := range rules {
			if len(rule.Exclude) == 0 {
				continue
			}
			included := toolbundle.ResolveDefinitionsWithOptions(&toolbundle.Bundle{Match: []llm.Tool{rule}}, match)
			retained := map[string]bool{}
			for _, definition := range included.Definitions {
				retained[mcpname.Canonical(definition.Name)] = true
			}
			unexcluded := rule
			unexcluded.Exclude = nil
			all := toolbundle.ResolveDefinitionsWithOptions(&toolbundle.Bundle{Match: []llm.Tool{unexcluded}}, match)
			for _, definition := range all.Definitions {
				name := mcpname.Canonical(definition.Name)
				if !retained[name] {
					denied[name] = true
				}
			}
		}
	}
	definitions = dedupeDefinitions(append(definitions, annotated...))
	var admitted []llm.ToolDefinition
	for _, definition := range definitions {
		name := mcpname.Canonical(definition.Name)
		if !denied[name] || allowed[name] {
			admitted = append(admitted, definition)
		}
	}
	var metadata []llm.ToolDefinition
	for _, definition := range admitted {
		if definition.Name == mcpname.Canonical(toolservice.Name+":list") || definition.Name == mcpname.Canonical(toolservice.Name+":activate") {
			continue
		}
		metadata = append(metadata, definition)
	}
	return s.modelVisibleDefinitions(ctx, dedupeDefinitions(metadata))
}

// callerToolScope preserves the existing explicit contract: exact caller
// names UNION the normally resolved explicit runtime bundles. Agent defaults,
// unselected bundles and global retention rules cannot enlarge this boundary.
func (s *Service) callerToolScope(ctx context.Context, input *QueryInput) (map[string]bool, error) {
	if input == nil || len(input.ToolsAllowed) == 0 {
		return nil, nil
	}
	allowed := map[string]bool{}
	for _, name := range input.ToolsAllowed {
		allowed[mcpname.Canonical(strings.TrimSpace(name))] = true
	}
	if len(input.ToolBundles) > 0 {
		runtime, err := s.resolveBundleResult(ctx, input.ToolBundles)
		if err != nil {
			return nil, err
		}
		for _, definition := range runtime.Definitions {
			allowed[mcpname.Canonical(definition.Name)] = true
		}
	}
	return allowed, nil
}
func (s *Service) callerScopedToolDefinitions(ctx context.Context, input *QueryInput, definitions []llm.ToolDefinition) ([]llm.ToolDefinition, error) {
	allowed, err := s.callerToolScope(ctx, input)
	if err != nil {
		return nil, err
	}
	result := make([]llm.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		definition.Name = mcpname.Canonical(definition.Name)
		if allowed == nil || allowed[definition.Name] {
			result = append(result, definition)
		}
	}
	return result, nil
}

// promoteDiscoveredTools resolves only exact chosen candidates. Discovering a
// workspace bundle does not make that bundle's other tools required schemas.
func (s *Service) promoteDiscoveredTools(ctx context.Context, definitions, candidates []llm.ToolDefinition, names []string) ([]llm.ToolDefinition, error) {
	available := map[string]llm.ToolDefinition{}
	for _, definition := range candidates {
		available[mcpname.Canonical(definition.Name)] = definition
	}
	present := map[string]bool{}
	for _, definition := range definitions {
		present[mcpname.Canonical(definition.Name)] = true
	}
	var extra []llm.ToolDefinition
	for _, name := range names {
		canonical := mcpname.Canonical(strings.TrimSpace(name))
		definition, found := available[canonical]
		if !found || present[canonical] {
			continue
		}
		present[canonical] = true
		extra = append(extra, cloneToolDefinition(definition))
	}
	if len(extra) == 0 {
		return definitions, nil
	}
	indexed, err := s.discoveryBundles(ctx, candidates)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(indexed))
	for id := range indexed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	entry := &resolvedToolSurface{Definitions: extra, ApprovalByID: map[string]*llm.ApprovalConfig{}, AsyncByID: map[string]*llm.Tool{}}
	for _, id := range ids {
		resolved := toolbundle.ResolveDefinitionsWithOptions(indexed[id], func(pattern string) []*llm.ToolDefinition {
			var matched []*llm.ToolDefinition
			for i := range extra {
				if toolmatcher.Match(pattern, extra[i].Name) {
					matched = append(matched, &extra[i])
				}
			}
			return matched
		})
		for name, approval := range resolved.ApprovalByID {
			if prior := entry.ApprovalByID[name]; prior != nil && !reflect.DeepEqual(prior, approval) {
				return nil, fmt.Errorf("discovered tool %q has conflicting bundle approval requirements", name)
			}
			entry.ApprovalByID[name] = approval
		}
		for name, async := range resolved.AsyncByID {
			if prior := entry.AsyncByID[name]; prior != nil && !reflect.DeepEqual(prior.Async, async.Async) {
				return nil, fmt.Errorf("discovered tool %q has conflicting bundle async requirements", name)
			}
			entry.AsyncByID[name] = async
		}
	}
	entry.Definitions = augmentPromptApprovalReviewDefinitions(entry.Definitions, entry.ApprovalByID)
	s.applyResolvedToolSurfaceMetadata(ctx, entry)
	return append(append([]llm.ToolDefinition(nil), definitions...), entry.Definitions...), nil
}

// discoveryBundles uses the same authored/default bundle authority as ordinary
// resolution, while making every available workflow discoverable for this turn.
func (s *Service) discoveryBundles(ctx context.Context, definitions []llm.ToolDefinition) (map[string]*toolbundle.Bundle, error) {
	var bundles []*toolbundle.Bundle
	var err error
	if s.toolBundles != nil {
		bundles, err = s.toolBundles(ctx)
	} else if s.toolBundleRepo != nil {
		bundles, err = s.toolBundleRepo.LoadAll(ctx)
	}
	if err != nil && !s.toolBundlesConfigured && s.toolBundleRepo != nil && s.toolBundleRepo.Repository != nil {
		// Only the default optional repository may be absent. Errors from an
		// explicitly injected provider or existing authored directory stay fatal.
		if exists, checkErr := s.toolBundleRepo.DirExists(ctx); checkErr == nil && !exists {
			err = nil
		}
	}

	if err != nil {
		return nil, err
	}
	indexed := indexBundlesByID(bundles)
	if profile := selectedPromptProfileFromContext(ctx); profile != nil {
		for id, bundle := range indexBundlesByID(profile.ToolBundleDefinitions) {
			indexed[id] = bundle
		}
	}
	if len(indexed) == 0 {
		indexed = indexBundlesByID(toolbundle.DeriveBundles(definitions))
	}
	return indexed, nil
}
