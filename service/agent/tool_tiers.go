package agent

import (
	"context"

	"github.com/viant/agently-core/genai/llm"
	toolmatcher "github.com/viant/agently-core/internal/tool/matcher"
	"github.com/viant/agently-core/protocol/mcpname"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/runtime/toolselection"
)

func retainedToolDefinition(definition llm.ToolDefinition) bool {
	return definition.AlwaysInclude || definition.Tier == llm.ToolTierCritical
}

func toolDefinitionPointers(definitions []llm.ToolDefinition) []*llm.ToolDefinition {
	result := make([]*llm.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		copy := cloneToolDefinition(definition)
		copy.Normalize()
		result = append(result, &copy)
	}
	return result
}

func agentToolRules(input *QueryInput) []llm.Tool {
	if input == nil || input.Agent == nil {
		return nil
	}
	var rules []llm.Tool
	for _, item := range input.Agent.Tool.Items {
		if item == nil {
			continue
		}
		copy := *item
		if copy.Name == "" {
			copy.Name = copy.Definition.Name
		}
		rules = append(rules, copy)
	}
	return rules
}

// Rule metadata is applied after source catalog/cache reads; producer schemas
// cannot supply it. Matching and exclusions retain the canonical bundle resolver.
func annotateAgentToolRules(input *QueryInput, definitions []llm.ToolDefinition) []llm.ToolDefinition {
	resolved := toolbundle.ResolveDefinitionsWithOptions(&toolbundle.Bundle{Match: agentToolRules(input)}, func(pattern string) []*llm.ToolDefinition {
		var result []*llm.ToolDefinition
		for i := range definitions {
			if toolmatcher.Match(pattern, definitions[i].Name) {
				result = append(result, &definitions[i])
			}
		}
		return result
	})
	return dedupeDefinitions(append(definitions, resolved.Definitions...))
}

// Only explicitly retained rules are resolved on conversational/empty intake
// paths. This uses the normal pattern cache, visibility and approval metadata;
// it neither enumerates unrelated schemas nor grants execution authority.
func (s *Service) resolveAlwaysIncludedTools(ctx context.Context, input *QueryInput) ([]llm.ToolDefinition, error) {
	if s == nil || s.registry == nil || input == nil || input.Agent == nil {
		return nil, nil
	}
	profile, err := s.selectedPromptProfileAny(ctx, input)
	if err != nil {
		return nil, err
	}
	if profile != nil {
		if profile.Execution != nil && profile.Execution.DisableTools {
			return nil, nil
		}
		ctx = withSelectedPromptProfile(ctx, profile)
	}
	bundles, err := s.discoveryBundles(ctx, nil)
	if err != nil {
		return nil, err
	}
	var rules []llm.Tool
	for _, bundle := range bundles {
		for _, rule := range bundle.Match {
			if rule.AlwaysInclude || rule.Tier == llm.ToolTierCritical {
				rules = append(rules, rule)
			}
		}
	}
	for _, rule := range agentToolRules(input) {
		if rule.AlwaysInclude || rule.Tier == llm.ToolTierCritical {
			rules = append(rules, rule)
		}
	}
	if len(rules) == 0 {
		return nil, nil
	}
	scope, err := s.callerToolScope(ctx, input)
	if err != nil {
		return nil, err
	}
	var matchErr error
	matches := map[string][]*llm.ToolDefinition{}
	resolved := toolbundle.ResolveDefinitionsWithOptions(&toolbundle.Bundle{Match: rules}, func(pattern string) []*llm.ToolDefinition {
		if definitions, found := matches[pattern]; found {
			return definitions
		}
		var definitions []*llm.ToolDefinition
		var err error
		if scope != nil {
			for allowed := range scope {
				if toolmatcher.Match(pattern, mcpname.Canonical(allowed)) {
					matched, matchError := s.matchDefinitionsResult(ctx, allowed)
					if matchError != nil {
						err = matchError
						break
					}
					definitions = append(definitions, matched...)
				}
			}
		} else {
			definitions, err = s.matchDefinitionsResult(ctx, pattern)
		}
		if err != nil {
			if matchErr == nil {
				matchErr = err
			}
			return nil
		}
		matches[pattern] = definitions
		return definitions
	})
	if matchErr != nil {
		return nil, matchErr
	}
	definitions, err := s.callerScopedToolDefinitions(ctx, input, resolved.Definitions)
	if err != nil {
		return nil, err
	}
	definitions, err = s.modelVisibleDefinitions(ctx, definitions)
	if err != nil {
		return nil, err
	}
	s.applyResolvedToolSurfaceMetadata(ctx, &resolvedToolSurface{Definitions: definitions, ApprovalByID: resolved.ApprovalByID, AsyncByID: resolved.AsyncByID})
	return cloneToolDefinitions(definitions), nil
}

// On-demand tools remain discoverable, but enter a model request only through
// a valid exact intake selection/activation or a trusted retention requirement.
func initialToolDefinitions(ctx context.Context, input *QueryInput, definitions []llm.ToolDefinition) []llm.ToolDefinition {
	selected := map[string]bool{}
	// Exact trusted caller/profile choices are selections too; wildcard defaults
	// and owning-bundle membership alone never activate on-demand tools.
	exact := append([]string(nil), input.ToolsAllowed...)
	if profile := selectedPromptProfileFromContext(ctx); profile != nil {
		exact = append(exact, profile.PreferredTools...)
	}
	for _, name := range exact {
		selected[mcpname.Canonical(name)] = true
	}
	for _, name := range intakeSelectedToolNames(input, toolDefinitionPointers(definitions)) {
		selected[mcpname.Canonical(name)] = true
	}
	if state, ok := toolselection.FromContext(ctx); ok {
		for _, name := range state.SelectedNames() {
			selected[mcpname.Canonical(name)] = true
		}
	}
	var result []llm.ToolDefinition
	for _, definition := range definitions {
		if definition.Tier != llm.ToolTierOnDemand || retainedToolDefinition(definition) || selected[mcpname.Canonical(definition.Name)] {
			result = append(result, definition)
		}
	}
	return result
}

// Retained helpers use the same authorized discovery state as ordinary turns.
// Business schemas remain pruned until an exact selection/activation admits them.
func (s *Service) prepareRetainedToolDefinitions(ctx context.Context, input *QueryInput, definitions []llm.ToolDefinition) ([]llm.ToolDefinition, error) {
	if len(definitions) == 0 {
		return nil, nil
	}
	if state, ok := toolselection.FromContext(ctx); ok {
		candidates, err := s.discoverableToolDefinitions(ctx, input)
		if err != nil {
			return nil, err
		}
		state.SetCandidates(defsToTools(dedupeDefinitions(append(candidates, definitions...))))
		restoreToolSelection(ctx, input, state)
	}
	return definitions, nil
}
