package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	toolctx "github.com/viant/agently-core/protocol/tool"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/runtime/memory"
)

// Small utilities for tool name resolution and filtering.

type toolSelection struct {
	name string
}

// toolSelections extracts tool selection names from the agent item configuration.
func toolSelections(qi *QueryInput) []toolSelection {
	var out []toolSelection
	if qi == nil || qi.Agent == nil {
		return out
	}
	for _, aTool := range qi.Agent.Tool.Items {
		if aTool == nil {
			continue
		}
		name := aTool.Name
		if name == "" {
			name = aTool.Definition.Name
		}
		if name == "" {
			continue
		}
		out = append(out, toolSelection{name: name})
	}
	return out
}

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
				out = append(out, llm.Tool{Type: "function", Definition: *def})
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

	// Bundle selection: runtime override, then agent config.
	bundleIDs := selectedBundleIDs(qi)

	if len(bundleIDs) > 0 {
		defs, err := s.resolveBundleDefinitions(ctx, bundleIDs)
		if err != nil {
			return nil, err
		}
		// Allow agent tool items to further extend bundle selection when present.
		extra := toolSelections(qi)
		if len(extra) > 0 {
			for _, sel := range extra {
				matched := s.matchDefinitions(ctx, sel.name)
				if strictDiscoveryMode(ctx) && len(matched) == 0 {
					return nil, strictToolDiscoveryError(ctx, sel.name)
				}
				for _, def := range matched {
					if def == nil {
						continue
					}
					defs = append(defs, *def)
				}
			}
		}
		defs = dedupeDefinitions(defs)
		tools := make([]llm.Tool, 0, len(defs))
		for i := range defs {
			tools = append(tools, llm.Tool{Type: "function", Definition: defs[i]})
		}
		tools = s.appendRegistryWarnings(ctx, tools)
		return tools, nil
	}

	// Fall back to agent items when no bundles are configured.
	selections := toolSelections(qi)
	if len(selections) == 0 {
		return nil, nil
	}
	var out []llm.Tool
	for _, sel := range selections {
		matched := s.matchDefinitions(ctx, sel.name)
		if strictDiscoveryMode(ctx) && len(matched) == 0 {
			return nil, strictToolDiscoveryError(ctx, sel.name)
		}
		for _, def := range matched {
			out = append(out, llm.Tool{Type: "function", Definition: *def})
		}
	}
	// Append any registry warnings raised during matching.
	out = s.appendRegistryWarnings(ctx, out)
	return out, nil
}

func selectedBundleIDs(qi *QueryInput) []string {
	if qi == nil {
		return nil
	}
	// runtime override
	if len(qi.ToolBundles) > 0 {
		return normalizeStringList(qi.ToolBundles)
	}
	if qi.Agent == nil {
		return nil
	}
	return normalizeStringList(qi.Agent.Tool.Bundles)
}

func normalizeStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range in {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, id)
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
			appendWarning(ctx, fmt.Sprintf("unknown tool bundle: %s", id))
			continue
		}
		res := toolbundle.ResolveDefinitionsWithOptions(b, func(pattern string) []*llm.ToolDefinition {
			return s.matchDefinitions(ctx, pattern)
		})
		for _, d := range res.Definitions {
			key := strings.ToLower(mcpname.Canonical(strings.TrimSpace(d.Name)))
			cfg := res.ApprovalByID[key]
			if cfg.IsQueue() {
				toolctx.MarkApprovalQueueTool(ctx, d.Name, cfg)
			}
		}
		defs = append(defs, res.Definitions...)
	}
	return dedupeDefinitions(defs), nil
}

func (s *Service) loadBundles(ctx context.Context) (map[string]*toolbundle.Bundle, error) {
	if s.toolBundles == nil {
		return nil, nil
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
	mode, ok := memory.DiscoveryModeFromContext(ctx)
	return ok && mode.Scheduler && mode.Strict
}

func strictToolDiscoveryError(ctx context.Context, pattern string) error {
	mode, _ := memory.DiscoveryModeFromContext(ctx)
	pattern = strings.TrimSpace(pattern)
	return fmt.Errorf("strict tool discovery: required scheduler tool unavailable pattern=%q schedule_id=%q schedule_run_id=%q", pattern, strings.TrimSpace(mode.ScheduleID), strings.TrimSpace(mode.ScheduleRunID))
}
