package agent

import (
	"context"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	toolmatcher "github.com/viant/agently-core/internal/tool/matcher"
	"github.com/viant/agently-core/protocol/mcpname"
	runtimediscovery "github.com/viant/agently-core/runtime/discovery"
	"github.com/viant/agently-core/runtime/toolselection"
	agenttool "github.com/viant/agently-core/service/agent/tool"
)

type selectedSourceResolutionKey struct{}
type automaticToolPolicyKey struct{}

// Automatic/default bundle policies match the authorized candidate catalog.
// Explicit workflow/runtime requirements and valid complete selections still
// resolve their schemas through the source; unrelated discovery cannot gate them.
func (s *Service) resolveTurnToolDefinitions(ctx context.Context, input *QueryInput, control agenttool.Selection) ([]llm.ToolDefinition, error) {
	selection := intakeSelection(input)
	candidates, err := s.discoverableToolDefinitions(ctx, input)
	if err != nil {
		return nil, err
	}
	available := map[string]bool{}
	for _, definition := range candidates {
		available[mcpname.Canonical(definition.Name)] = true
	}
	var selectedNames []string
	if selection != nil {
		valid := true
		for _, name := range selection.Names {
			name = strings.TrimSpace(name)
			if strings.ContainsAny(name, "*?[]") || !available[mcpname.Canonical(name)] {
				valid = false
				break
			}
		}
		if valid {
			selectedNames = append(selectedNames, selection.Names...)
		}
	}
	profile, err := s.selectedPromptProfileAny(ctx, input)
	if err != nil {
		return nil, err
	}
	required := agenttool.Merge(agenttool.FromPromptProfile(profile), runtimeToolRequirements(input), agenttool.Selection{Tools: selectedNames})
	if state, ok := toolselection.FromContext(ctx); ok {
		for _, name := range state.SelectedNames() {
			if available[mcpname.Canonical(name)] {
				selectedNames = append(selectedNames, name)
			}
		}
	}
	strictCtx := runtimediscovery.MergeMode(ctx, runtimediscovery.Mode{ToolSurface: true, Required: true})
	strictCtx = context.WithValue(strictCtx, selectedSourceResolutionKey{}, true)
	explicit, err := s.resolveStructuredToolDefinitions(strictCtx, required)
	if err != nil {
		return nil, err
	}
	// Fresh required definitions take precedence over advisory catalog copies.
	candidates = dedupeDefinitions(append(explicit, candidates...))
	// Exact promotion also applies policies owned by unselected discovery bundles.
	promoted, err := s.promoteDiscoveredTools(ctx, nil, candidates, selectedNames)
	if err != nil {
		return nil, err
	}
	candidates = dedupeDefinitions(append(promoted, candidates...))
	matched, err := s.resolveStructuredToolDefinitionsWithMatcher(context.WithValue(ctx, automaticToolPolicyKey{}, true), control, func(_ context.Context, pattern string) ([]*llm.ToolDefinition, error) {
		var result []*llm.ToolDefinition
		for i := range candidates {
			if toolmatcher.Match(pattern, candidates[i].Name) {
				result = append(result, &candidates[i])
			}
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	return dedupeDefinitions(append(append(matched, promoted...), explicit...)), nil
}
