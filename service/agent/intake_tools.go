package agent

import (
	"context"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/debugtrace"
	toolmatcher "github.com/viant/agently-core/internal/tool/matcher"
	mcpname "github.com/viant/agently-core/protocol/mcpname"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/runtime/toolselection"
	agenttool "github.com/viant/agently-core/service/agent/tool"
	intake "github.com/viant/agently-core/service/intake"
	"github.com/viant/forge/backend/provider/contract/observe"
)

func intakeSelection(input *QueryInput) *intake.ToolSelection {
	selection := intakeToolSelectionHint(input)
	if selection == nil || !selection.Complete {
		return nil
	}
	return selection
}

func intakeToolSelectionHint(input *QueryInput) *intake.ToolSelection {
	if input == nil {
		return nil
	}
	turn := intake.FromContext(input.Context)
	if turn == nil {
		return nil
	}
	return turn.Prompting.ToolSelection
}

// intakeSelectedToolNames retains an exact, valid intake selection through
// later presentation filters. An invalid batch keeps the ordinary fallback.
func intakeSelectedToolNames(input *QueryInput, definitions []*llm.ToolDefinition) []string {
	selection := intakeSelection(input)
	if selection == nil {
		return nil
	}
	available := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if definition != nil {
			available[mcpname.Canonical(definition.Name)] = true
		}
	}
	names := make([]string, 0, len(selection.Names))
	for _, name := range selection.Names {
		canonical := mcpname.Canonical(strings.TrimSpace(name))
		if canonical == "" || !available[canonical] {
			return nil
		}
		names = append(names, canonical)
	}
	return names
}

func isIntakeClarification(input *QueryInput) bool {
	if input == nil {
		return false
	}
	turn := intake.FromContext(input.Context)
	return turn != nil && turn.Routing.Mode == intake.ModeClarify
}

func intakeNeedsNoTools(ctx context.Context, input *QueryInput) bool {
	if state, ok := toolselection.FromContext(ctx); ok && len(state.SelectedNames()) > 0 {
		return false
	}
	if isIntakeClarification(input) {
		return true
	}
	selection := intakeSelection(input)
	return selection != nil && len(selection.Names) == 0 && len(input.ToolBundles) == 0 && len(input.ToolsAllowed) == 0 && len(input.ResourceURIs) == 0 && input.EffectiveIntentProfileID() == "" && selectedPromptProfileFromContext(ctx) == nil
}

// Selection narrows model exposure, never execution authority. Definitions have
// already passed the existing bundle/visibility resolution; keep their metadata.
func (s *Service) selectIntakeTools(ctx context.Context, input *QueryInput, definitions []llm.ToolDefinition) (result []llm.ToolDefinition, err error) {
	state, hasState := toolselection.FromContext(ctx)
	profile, err := s.selectedPromptProfileAny(ctx, input)
	if err != nil {
		return nil, err
	}
	if profile != nil && profile.Execution != nil && profile.Execution.DisableTools {
		if hasState {
			state.SetCandidates(nil)
		}
		return nil, nil
	}
	if profile != nil {
		ctx = withSelectedPromptProfile(ctx, profile)
	}
	selection := intakeToolSelectionHint(input)
	var exactPreferred []string
	if profile != nil {
		for _, name := range profile.PreferredTools {
			if !strings.ContainsAny(name, "*?[]") {
				exactPreferred = append(exactPreferred, name)
			}
		}
	}
	if hasState || selection != nil || len(exactPreferred) > 0 {
		definitions, err = s.callerScopedToolDefinitions(ctx, input, definitions)
		if err != nil {
			return nil, err
		}
		discoverable, discoveryErr := s.discoverableToolDefinitions(ctx, input)
		if discoveryErr != nil {
			return nil, discoveryErr
		}
		discoverable = dedupeDefinitions(append(discoverable, definitions...))
		if hasState {
			state.SetCandidates(defsToTools(discoverable))
			restoreToolSelection(ctx, input, state)
		}
		names := append([]string(nil), exactPreferred...)
		if selection != nil && selection.Complete {
			available := map[string]bool{}
			for _, definition := range discoverable {
				available[mcpname.Canonical(definition.Name)] = true
			}
			valid := true
			for _, name := range selection.Names {
				if !available[mcpname.Canonical(strings.TrimSpace(name))] {
					valid = false
					break
				}
			}
			if valid {
				names = append(names, selection.Names...)
			}
		}
		if hasState {
			names = append(names, state.SelectedNames()...)
		}
		definitions, err = s.promoteDiscoveredTools(ctx, definitions, discoverable, names)
		if err != nil {
			return nil, err
		}
	}
	if selection == nil {
		return initialToolDefinitions(ctx, input, definitions), nil
	}
	// Message-injection preferences do not remove the selected workflow's
	// requirements from an already available tool surface.
	required := agenttool.Merge(agenttool.FromPromptProfile(profile), runtimeToolRequirements(input))
	// Uncertainty falls back to the chosen workflow's declared tools. If no
	// workflow is known, retain the original surface rather than guessing.
	if !selection.Complete && len(required.Bundles) == 0 && len(required.Tools) == 0 {
		return initialToolDefinitions(ctx, input, definitions), nil
	}
	span := observe.Start(ctx, "intake.tools.select", observe.Labels{})
	started := time.Now()
	defer func() {
		span.End(observe.OutcomeFor(err), observe.CacheBypass)
		debugtrace.Write("agent", "intake_tool_selection", map[string]any{"conversationID": input.ConversationID, "turnID": input.MessageID, "candidateCount": len(definitions), "requestedCount": len(selection.Names), "schemaCount": len(result), "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond), "failed": err != nil})
	}()
	candidates := map[string]bool{}
	for _, d := range definitions {
		candidates[mcpname.Canonical(d.Name)] = true
	}
	selected := map[string]bool{}
	for _, definition := range definitions {
		if retainedToolDefinition(definition) {
			selected[mcpname.Canonical(definition.Name)] = true
		}
	}
	for _, name := range selection.Names {
		canonical := mcpname.Canonical(strings.TrimSpace(name))
		if canonical == "" || !candidates[canonical] {
			appendWarning(ctx, "Intake tool selection did not match the available catalog; retaining configured tools")
			return initialToolDefinitions(ctx, input, definitions), nil
		}
		selected[canonical] = true
	}
	if hasState {
		for _, name := range state.SelectedNames() {
			selected[mcpname.Canonical(name)] = true
		}
	}
	for _, pattern := range required.Tools {
		for _, d := range definitions {
			if toolmatcher.Match(pattern, d.Name) && (d.Tier != llm.ToolTierOnDemand || retainedToolDefinition(d) || mcpname.Canonical(pattern) == mcpname.Canonical(d.Name)) {
				selected[mcpname.Canonical(d.Name)] = true
			}
		}
	}
	if len(required.Bundles) > 0 {
		bundles, err := s.loadBundlesFor(ctx, required.Bundles)
		if err != nil {
			return nil, err
		}
		for _, id := range required.Bundles {
			b := bundles[strings.ToLower(strings.TrimSpace(id))]
			if b == nil {
				continue
			}
			resolved := toolbundle.ResolveDefinitionsWithOptions(b, func(pattern string) []*llm.ToolDefinition {
				var matched []*llm.ToolDefinition
				for i := range definitions {
					if toolmatcher.Match(pattern, definitions[i].Name) {
						matched = append(matched, &definitions[i])
					}
				}
				return matched
			})
			for _, d := range resolved.Definitions {
				if d.Tier == llm.ToolTierOnDemand && !retainedToolDefinition(d) {
					continue
				}
				selected[mcpname.Canonical(d.Name)] = true
			}
		}
	}
	// A complete empty selection explicitly requests a conversational turn.
	// Required profile/runtime tools above still take precedence over that hint.
	if len(selected) == 0 {
		return nil, nil
	}
	result = make([]llm.ToolDefinition, 0, len(selected)+2)
	for _, d := range definitions {
		if selected[mcpname.Canonical(d.Name)] {
			result = append(result, d)
		}
	}
	return result, nil
}
