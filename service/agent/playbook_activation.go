package agent

import (
	"context"
	"fmt"
	"github.com/viant/agently-core/genai/llm"
	toolmatcher "github.com/viant/agently-core/internal/tool/matcher"
	"github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/runtime/toolselection"
	agenttool "github.com/viant/agently-core/service/agent/tool"
)

func (s *Service) preparePlaybookTools(ctx context.Context, input *QueryInput, profile *intent.Profile) (func() error, error) {
	control := agenttool.FromPromptProfile(profile)
	if len(control.Tools) == 0 && len(control.Bundles) == 0 {
		return nil, nil
	}
	state, ok := toolselection.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("playbook tool activation requires the current turn")
	}
	ctx = withSelectedPromptProfile(ctx, profile)
	candidates, err := s.discoverableToolDefinitions(ctx, input)
	if err != nil {
		return nil, err
	}
	candidates, err = s.callerScopedToolDefinitions(ctx, input, candidates)
	if err != nil {
		return nil, err
	}
	definitions, err := s.resolveStructuredToolDefinitionsWithMatcher(ctx, control, func(_ context.Context, pattern string) ([]*llm.ToolDefinition, error) {
		var matches []*llm.ToolDefinition
		for i := range candidates {
			if toolmatcher.Match(pattern, candidates[i].Name) {
				matches = append(matches, &candidates[i])
			}
		}
		return matches, nil
	})
	if err != nil {
		return nil, err
	}
	names := agenttool.ToolNames(definitions)
	if len(names) == 0 {
		return nil, fmt.Errorf("playbook declared tools have no visible matches")
	}
	return func() error {
		if current, valid := toolselection.FromContext(ctx); !valid || current != state {
			return fmt.Errorf("playbook tool activation is no longer in the current turn")
		}
		state.SetCandidates(defsToTools(candidates))
		return state.Activate(names)
	}, nil
}
