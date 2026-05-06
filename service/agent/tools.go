package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	toolctx "github.com/viant/agently-core/protocol/tool"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolasyncconfig "github.com/viant/agently-core/protocol/tool/asyncconfig"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	runtimediscovery "github.com/viant/agently-core/runtime/discovery"
	agenttool "github.com/viant/agently-core/service/agent/tool"
)

// Small utilities for tool name resolution and filtering.

// resolveTools resolves tools using the following precedence:
//   - If input.ToolsAllowed is provided and non-empty, resolve exactly those tools by name
//     and do not gate by agent patterns (explicit allow-list).
//   - Otherwise, resolve tools from agent patterns.
func (s *Service) resolveTools(ctx context.Context, qi *QueryInput) ([]llm.Tool, error) {
	// Clear any previous registry warnings before this resolution cycle.
	if w, ok := s.registry.(interface{ ClearWarnings() }); ok {
		w.ClearWarnings()
	}
	// Prefer explicit allow-list when provided and non-empty.

	if len(qi.ToolsAllowed) > 0 {
		var out []llm.Tool
		var missing []string
		for _, n := range qi.ToolsAllowed {
			name := strings.TrimSpace(n)
			if name == "" {
				continue
			}
			if def, ok := s.registry.GetDefinition(name); ok && def != nil {
				canonical := *def
				canonical.Name = mcpname.Canonical(canonical.Name)
				out = append(out, llm.Tool{Type: "function", Definition: canonical})
				continue
			}
			// Allowed tool not found: add a warning to query output via context.
			appendWarning(ctx, fmt.Sprintf("allowed tool not found: %s", name))
			missing = append(missing, name)
		}
		if strictDiscoveryMode(ctx) && len(missing) > 0 {
			return nil, strictToolDiscoveryError(ctx, strings.Join(missing, ", "))
		}
		// Append any registry warnings (e.g., unreachable servers) to output warnings via context.
		if w, ok := s.registry.(interface {
			LastWarnings() []string
			ClearWarnings()
		}); ok {
			for _, msg := range w.LastWarnings() {
				appendWarning(ctx, msg)
			}
			w.ClearWarnings()
		}
		return out, nil
	}

	control, err := s.resolveToolControl(ctx, qi)
	if err != nil {
		return nil, err
	}

	var defs []llm.ToolDefinition
	if len(control.Bundles) > 0 {
		var err error
		defs, err = s.resolveBundleDefinitions(ctx, control.Bundles)
		if err != nil {
			return nil, err
		}
	}
	if len(control.Tools) == 0 && len(defs) == 0 {
		return nil, nil
	}
	defs, err = s.appendToolSelections(ctx, defs, control.Tools)
	if err != nil {
		return nil, err
	}
	defs = dedupeDefinitions(defs)
	out := defsToTools(defs)
	out = s.appendRegistryWarnings(ctx, out)
	return out, nil
}

func (s *Service) resolveToolControl(ctx context.Context, qi *QueryInput) (agenttool.Selection, error) {
	if qi == nil {
		return agenttool.Selection{}, nil
	}
	selections := agenttool.Selections{
		Agent:   agenttool.FromAgent(qi.Agent),
		Runtime: agenttool.Selection{Bundles: append([]string(nil), qi.ToolBundles...)},
	}
	profileDef, err := s.selectedPromptProfile(ctx, qi)
	if err != nil {
		return agenttool.Selection{}, err
	}
	selections.Profile = agenttool.FromPromptProfile(profileDef)
	effective := agenttool.BuildEffective(selections)
	return effective.Final, nil
}

func (s *Service) appendToolSelections(ctx context.Context, defs []llm.ToolDefinition, names []string) ([]llm.ToolDefinition, error) {
	if len(names) == 0 {
		return defs, nil
	}
	for _, name := range names {
		matched := s.matchDefinitions(ctx, name)
		if strictDiscoveryMode(ctx) && len(matched) == 0 {
			return nil, strictToolDiscoveryError(ctx, name)
		}
		for _, def := range matched {
			if def == nil {
				continue
			}
			defs = append(defs, *def)
		}
	}
	return defs, nil
}

func toolsToDefs(in []llm.Tool) []llm.ToolDefinition {
	if len(in) == 0 {
		return nil
	}
	out := make([]llm.ToolDefinition, 0, len(in))
	for _, tool := range in {
		out = append(out, tool.Definition)
	}
	return out
}

func defsToTools(in []llm.ToolDefinition) []llm.Tool {
	if len(in) == 0 {
		return nil
	}
	out := make([]llm.Tool, 0, len(in))
	for i := range in {
		out = append(out, llm.Tool{Type: "function", Definition: in[i]})
	}
	return out
}

func (s *Service) resolveBundleDefinitions(ctx context.Context, bundleIDs []string) ([]llm.ToolDefinition, error) {
	if s == nil || s.registry == nil {
		return nil, nil
	}
	bundles, err := s.loadBundles(ctx)
	if err != nil {
		return nil, err
	}
	var derived map[string]*toolbundle.Bundle
	if len(bundles) == 0 {
		derived = indexBundlesByID(toolbundle.DeriveBundles(s.registry.Definitions()))
		bundles = derived
	}
	var defs []llm.ToolDefinition
	for _, id := range bundleIDs {
		key := strings.ToLower(strings.TrimSpace(id))
		b := bundles[key]
		if b == nil && len(bundles) > 0 {
			// When workspace bundles exist but don't include the requested id,
			// fall back to derived bundles from tool registry.
			if derived == nil {
				derived = indexBundlesByID(toolbundle.DeriveBundles(s.registry.Definitions()))
			}
			b = derived[key]
		}
		if b == nil {
			defs = append(defs, s.resolveDirectBundleDefinitions(ctx, id)...)
			appendWarning(ctx, fmt.Sprintf("unknown tool bundle: %s", id))
			continue
		}
		res := toolbundle.ResolveDefinitionsWithOptions(b, func(pattern string) []*llm.ToolDefinition {
			return s.matchDefinitions(ctx, pattern)
		})
		for _, d := range res.Definitions {
			key := strings.ToLower(mcpname.Canonical(strings.TrimSpace(d.Name)))
			cfg := res.ApprovalByID[key]
			if cfg != nil && (cfg.IsQueue() || cfg.IsPrompt()) {
				toolapprovalqueue.MarkTool(ctx, d.Name, cfg)
			}
			if asyncRule := res.AsyncByID[key]; asyncRule != nil && asyncRule.Async != nil {
				toolasyncconfig.MarkTool(ctx, d.Name, asyncRule.Async)
			}
		}
		defs = append(defs, res.Definitions...)
	}
	return dedupeDefinitions(defs), nil
}

func (s *Service) resolveDirectBundleDefinitions(ctx context.Context, bundleID string) []llm.ToolDefinition {
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return nil
	}
	patterns := []string{bundleID}
	if !strings.Contains(bundleID, "*") && !strings.Contains(bundleID, ":") {
		patterns = append(patterns, bundleID+"/*")
	}
	var out []llm.ToolDefinition
	for _, pattern := range patterns {
		for _, def := range s.matchDefinitions(ctx, pattern) {
			if def == nil {
				continue
			}
			out = append(out, *def)
		}
	}
	return dedupeDefinitions(out)
}

func (s *Service) loadBundles(ctx context.Context) (map[string]*toolbundle.Bundle, error) {
	if s.toolBundles == nil {
		if s.toolBundleRepo == nil {
			return nil, nil
		}
		list, err := s.toolBundleRepo.LoadAll(ctx)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, nil
		}
		return indexBundlesByID(list), nil
	}
	list, err := s.toolBundles(ctx)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return indexBundlesByID(list), nil
}

func indexBundlesByID(in []*toolbundle.Bundle) map[string]*toolbundle.Bundle {
	out := map[string]*toolbundle.Bundle{}
	for _, b := range in {
		if b == nil {
			continue
		}
		id := strings.TrimSpace(b.ID)
		if id == "" {
			continue
		}
		out[strings.ToLower(id)] = b
	}
	return out
}

func dedupeDefinitions(in []llm.ToolDefinition) []llm.ToolDefinition {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]llm.ToolDefinition, 0, len(in))
	for _, d := range in {
		key := strings.ToLower(mcpname.Canonical(strings.TrimSpace(d.Name)))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		d.Name = mcpname.Canonical(d.Name)
		out = append(out, d)
	}
	return out
}

func (s *Service) appendRegistryWarnings(ctx context.Context, tools []llm.Tool) []llm.Tool {
	if w, ok := s.registry.(interface {
		LastWarnings() []string
		ClearWarnings()
	}); ok {
		for _, msg := range w.LastWarnings() {
			appendWarning(ctx, msg)
		}
		w.ClearWarnings()
	}
	return tools
}

func (s *Service) matchDefinitions(ctx context.Context, pattern string) []*llm.ToolDefinition {
	if cm, ok := s.registry.(toolctx.ContextMatcher); ok {
		return cm.MatchDefinitionWithContext(ctx, pattern)
	}
	return s.registry.MatchDefinition(pattern)
}

func strictDiscoveryMode(ctx context.Context) bool {
	mode, ok := runtimediscovery.ModeFromContext(ctx)
	return ok && mode.Scheduler && mode.Strict
}

func strictToolDiscoveryError(ctx context.Context, pattern string) error {
	mode, _ := runtimediscovery.ModeFromContext(ctx)
	pattern = strings.TrimSpace(pattern)
	return fmt.Errorf("strict tool discovery: required scheduler tool unavailable pattern=%q schedule_id=%q schedule_run_id=%q", pattern, strings.TrimSpace(mode.ScheduleID), strings.TrimSpace(mode.ScheduleRunID))
}
