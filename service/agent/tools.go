package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	toolmatcher "github.com/viant/agently-core/internal/tool/matcher"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	skillproto "github.com/viant/agently-core/protocol/skill"
	toolctx "github.com/viant/agently-core/protocol/tool"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolasyncconfig "github.com/viant/agently-core/protocol/tool/asyncconfig"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	runtimediscovery "github.com/viant/agently-core/runtime/discovery"
	agenttool "github.com/viant/agently-core/service/agent/tool"
)

// Small utilities for tool name resolution and filtering.

type resolvedToolSurface struct {
	Definitions  []llm.ToolDefinition
	ApprovalByID map[string]*llm.ApprovalConfig
	AsyncByID    map[string]*llm.Tool
	Complete     bool
}

// resolveTools resolves tools using the following precedence:
//   - If input.ToolsAllowed is provided and non-empty, resolve exactly those tools by name
//     and do not gate by agent patterns (explicit allow-list).
//   - Otherwise, resolve tools from agent patterns.
func (s *Service) resolveTools(ctx context.Context, qi *QueryInput) ([]llm.Tool, error) {
	if qi != nil && len(qi.ToolBundles) > 0 {
		ctx = runtimediscovery.MergeMode(ctx, runtimediscovery.Mode{ToolSurface: true, Required: true})
	}
	// Clear any previous registry warnings before this resolution cycle.
	if w, ok := s.registry.(interface{ ClearWarnings() }); ok {
		w.ClearWarnings()
	}
	// Prefer explicit allow-list when provided and non-empty.
	// When explicit tool bundles are also present, resolve the allowed tools
	// through the structured bundle path so bundle-owned approval/async metadata
	// is preserved on the final tool surface.
	if len(qi.ToolsAllowed) > 0 && len(qi.ToolBundles) == 0 {
		var out []llm.Tool
		var missing []string
		for _, n := range qi.ToolsAllowed {
			name := strings.TrimSpace(n)
			if name == "" {
				continue
			}
			if def, ok := s.getDefinition(ctx, name); ok && def != nil {
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

	if len(control.Tools) == 0 && len(control.Bundles) == 0 {
		return nil, nil
	}
	defs, err := s.resolveStructuredToolDefinitions(ctx, control)
	if err != nil {
		return nil, err
	}
	if unresolved, validationErr := s.unresolvedTurnBundles(ctx, qi.ToolBundles, defs); validationErr != nil {
		return nil, validationErr
	} else if len(unresolved) > 0 {
		return nil, fmt.Errorf("requested tool bundles resolved zero tool definitions: %s", strings.Join(unresolved, ", "))
	}
	if len(defs) == 0 {
		required := requiredResolvedToolBundlesFromContext(ctx)
		// Turn-level bundles (explicit, intake, or classifier-selected) are part
		// of the trusted route. Continuing without them lets the model answer without the
		// authoritative MCP capability, so fail closed with a connectivity /
		// discovery error instead.
		if len(required) == 0 && qi != nil && len(qi.ToolBundles) > 0 {
			required = append([]string(nil), qi.ToolBundles...)
		}
		if len(required) > 0 {
			return nil, fmt.Errorf("requested tool bundles resolved zero tool definitions: %s", strings.Join(required, ", "))
		}
	}
	out := defsToTools(defs)
	out = s.appendRegistryWarnings(ctx, out)
	return out, nil
}

// unresolvedTurnBundles validates only the turn-level bundles against the
// already-resolved surface. It performs no MCP discovery and therefore adds no
// network latency; it prevents unrelated skill tools from masking a missing
// authoritative bundle.
func (s *Service) unresolvedTurnBundles(ctx context.Context, bundleIDs []string, defs []llm.ToolDefinition) ([]string, error) {
	if len(bundleIDs) == 0 {
		return nil, nil
	}
	bundles, err := s.loadBundles(ctx)
	if err != nil {
		return nil, err
	}
	var unresolved []string
	for _, rawID := range bundleIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		bundle := bundles[strings.ToLower(id)]
		if bundle == nil {
			unresolved = append(unresolved, id)
			continue
		}
		resolved := toolbundle.ResolveDefinitionsWithOptions(bundle, func(pattern string) []*llm.ToolDefinition {
			var matches []*llm.ToolDefinition
			for i := range defs {
				if toolmatcher.Match(pattern, defs[i].Name) {
					matches = append(matches, &defs[i])
				}
			}
			return matches
		})
		if len(resolved.Definitions) == 0 {
			unresolved = append(unresolved, id)
		}
	}
	return unresolved, nil
}

func (s *Service) resolveToolControl(ctx context.Context, qi *QueryInput) (agenttool.Selection, error) {
	if qi == nil {
		return agenttool.Selection{}, nil
	}
	hasVisibleSkills := s.hasVisibleSkills(qi.Agent)
	agentSelection := agenttool.FromAgentTool(agentmdlTool(qi.Agent))
	if hasVisibleSkills {
		agentSelection.Tools = append(agentSelection.Tools, skillproto.ListToolName, skillproto.ActivateToolName)
		agentSelection = agenttool.Normalize(agentSelection)
	}
	selections := agenttool.Selections{
		Agent: agentSelection,
		Runtime: agenttool.Selection{
			Bundles: append([]string(nil), qi.ToolBundles...),
			Tools:   append([]string(nil), qi.ToolsAllowed...),
		},
	}
	if qi.toolBundlesAutoSelected {
		selections.Agent.Bundles = nil
	}
	profileDef, err := s.selectedPromptProfile(ctx, qi)
	if err != nil {
		return agenttool.Selection{}, err
	}
	selections.Profile = agenttool.FromPromptProfile(profileDef)
	effective := agenttool.BuildEffective(selections)
	if !hasVisibleSkills {
		effective.Final = withoutSkillControlSelection(effective.Final)
	}
	return effective.Final, nil
}

func (s *Service) hasVisibleSkills(agent *agentmdl.Agent) bool {
	if s == nil || s.skillSvc == nil {
		return false
	}
	return len(s.skillSvc.VisibleSkillsByName(agent, configuredAgentSkills(agent))) > 0
}

func withoutSkillControlSelection(in agenttool.Selection) agenttool.Selection {
	var bundles []string
	for _, bundle := range in.Bundles {
		if strings.EqualFold(strings.TrimSpace(bundle), "llm/skills") {
			continue
		}
		bundles = append(bundles, bundle)
	}
	var tools []string
	for _, tool := range in.Tools {
		name := strings.TrimSpace(tool)
		if strings.EqualFold(name, skillproto.ListToolName) || strings.EqualFold(name, skillproto.ActivateToolName) {
			continue
		}
		tools = append(tools, tool)
	}
	return agenttool.Normalize(agenttool.Selection{Bundles: bundles, Tools: tools})
}

func agentmdlTool(agent *agentmdl.Agent) agentmdl.Tool {
	if agent == nil {
		return agentmdl.Tool{}
	}
	return agent.Tool
}

func configuredAgentSkills(agent *agentmdl.Agent) []string {
	if agent == nil {
		return nil
	}
	return append([]string(nil), agent.Skills...)
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

func (s *Service) resolveStructuredToolDefinitions(ctx context.Context, control agenttool.Selection) ([]llm.ToolDefinition, error) {
	key := toolSelectionCacheKey(control)
	if key != "" {
		if cached, ok := s.toolSurfaceCache.Load(key); ok {
			if entry, ok := cached.(*resolvedToolSurface); ok && entry != nil {
				s.applyResolvedToolSurfaceMetadata(ctx, entry)
				return cloneToolDefinitions(entry.Definitions), nil
			}
		}
	}

	entry := &resolvedToolSurface{Complete: true}
	if len(control.Bundles) > 0 {
		res, err := s.resolveBundleResult(ctx, control.Bundles)
		if err != nil {
			return nil, err
		}
		entry.Definitions = append(entry.Definitions, res.Definitions...)
		entry.ApprovalByID = res.ApprovalByID
		entry.AsyncByID = res.AsyncByID
		entry.Complete = res.Complete
	}
	var err error
	entry.Definitions, err = s.appendToolSelections(ctx, entry.Definitions, control.Tools)
	if err != nil {
		return nil, err
	}
	entry.Definitions = dedupeDefinitions(entry.Definitions)
	entry.Definitions = augmentPromptApprovalReviewDefinitions(entry.Definitions, entry.ApprovalByID)
	// External MCP definitions can become available after delegated OAuth or a
	// lazy discovery reconnect. Caching an empty surface makes that transient
	// state permanent for the process, so only cache successful resolutions.
	if key != "" && len(entry.Definitions) > 0 && entry.Complete {
		s.toolSurfaceCache.Store(key, entry)
	}
	s.applyResolvedToolSurfaceMetadata(ctx, entry)
	return cloneToolDefinitions(entry.Definitions), nil
}

func (s *Service) promptApprovalConfigsForSelection(ctx context.Context, control agenttool.Selection) (map[string]*llm.ApprovalConfig, error) {
	if s == nil {
		return nil, nil
	}
	if len(control.Bundles) == 0 {
		return nil, nil
	}
	res, err := s.resolveBundleResult(ctx, control.Bundles)
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.ApprovalByID) == 0 {
		return nil, nil
	}
	out := map[string]*llm.ApprovalConfig{}
	for key, cfg := range res.ApprovalByID {
		if cfg == nil || (!cfg.IsPrompt() && !cfg.IsQueue()) || cfg.Review == nil || len(cfg.Review.RequestedSchema) == 0 {
			continue
		}
		out[key] = cfg
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (s *Service) reapplyPromptApprovalReviewToolSurface(ctx context.Context, input *QueryInput, b *binding.Binding) error {
	if s == nil || input == nil || b == nil || len(b.Tools.Signatures) == 0 {
		return nil
	}
	control, err := s.resolveToolControl(ctx, input)
	if err != nil {
		return err
	}
	approvalByID, err := s.promptApprovalConfigsForSelection(ctx, control)
	if err != nil {
		return err
	}
	if len(approvalByID) == 0 {
		return nil
	}
	defs := make([]llm.ToolDefinition, 0, len(b.Tools.Signatures))
	for _, sig := range b.Tools.Signatures {
		if sig == nil {
			continue
		}
		defs = append(defs, cloneToolDefinition(*sig))
	}
	defs = augmentPromptApprovalReviewDefinitions(defs, approvalByID)
	updated := make([]*llm.ToolDefinition, 0, len(defs))
	for i := range defs {
		def := defs[i]
		def.Normalize()
		updated = append(updated, &def)
	}
	b.Tools.Signatures = dedupeToolDefinitions(updated)
	return nil
}

func (s *Service) resolveBundleDefinitions(ctx context.Context, bundleIDs []string) ([]llm.ToolDefinition, error) {
	res, err := s.resolveBundleResult(ctx, bundleIDs)
	if err != nil {
		return nil, err
	}
	s.applyResolvedToolSurfaceMetadata(ctx, res)
	return res.Definitions, nil
}

func (s *Service) resolveBundleResult(ctx context.Context, bundleIDs []string) (*resolvedToolSurface, error) {
	if s == nil || s.registry == nil {
		return &resolvedToolSurface{}, nil
	}
	bundles, err := s.loadBundles(ctx)
	if err != nil {
		return nil, err
	}
	var derived map[string]*toolbundle.Bundle
	if len(bundles) == 0 {
		derived = indexBundlesByID(toolbundle.DeriveBundles(s.definitions(ctx)))
		bundles = derived
	}
	entry := &resolvedToolSurface{
		ApprovalByID: map[string]*llm.ApprovalConfig{},
		AsyncByID:    map[string]*llm.Tool{},
		Complete:     true,
	}
	for _, id := range bundleIDs {
		key := strings.ToLower(strings.TrimSpace(id))
		b := bundles[key]
		if b == nil && len(bundles) > 0 {
			// When workspace bundles exist but don't include the requested id,
			// fall back to derived bundles from tool registry.
			if derived == nil {
				derived = indexBundlesByID(toolbundle.DeriveBundles(s.definitions(ctx)))
			}
			b = derived[key]
		}
		if b == nil {
			direct := s.resolveDirectBundleDefinitions(ctx, id)
			entry.Definitions = append(entry.Definitions, direct...)
			if len(direct) == 0 {
				entry.Complete = false
			}
			appendWarning(ctx, fmt.Sprintf("unknown tool bundle: %s", id))
			continue
		}
		res := toolbundle.ResolveDefinitionsWithOptions(b, func(pattern string) []*llm.ToolDefinition {
			definitions, matchErr := s.matchDefinitionsResult(ctx, pattern)
			if err == nil && matchErr != nil {
				err = matchErr
			}
			return definitions
		})
		if err != nil {
			return nil, err
		}
		if len(res.Definitions) == 0 {
			entry.Complete = false
		}
		for name, cfg := range res.ApprovalByID {
			entry.ApprovalByID[name] = cfg
		}
		for name, asyncRule := range res.AsyncByID {
			entry.AsyncByID[name] = asyncRule
		}
		entry.Definitions = append(entry.Definitions, res.Definitions...)
	}
	entry.Definitions = dedupeDefinitions(entry.Definitions)
	return entry, nil
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

func (s *Service) applyResolvedToolSurfaceMetadata(ctx context.Context, entry *resolvedToolSurface) {
	if entry == nil {
		return
	}
	for _, d := range entry.Definitions {
		key := strings.ToLower(mcpname.Canonical(strings.TrimSpace(d.Name)))
		cfg := entry.ApprovalByID[key]
		if cfg != nil && (cfg.IsQueue() || cfg.IsPrompt()) {
			toolapprovalqueue.MarkTool(ctx, d.Name, cfg)
		}
		if asyncRule := entry.AsyncByID[key]; asyncRule != nil && asyncRule.Async != nil {
			toolasyncconfig.MarkTool(ctx, d.Name, asyncRule.Async)
		}
	}
}

func toolSelectionCacheKey(control agenttool.Selection) string {
	if len(control.Bundles) == 0 && len(control.Tools) == 0 {
		return ""
	}
	return strings.Join(control.Bundles, "\x1f") + "\x1e" + strings.Join(control.Tools, "\x1f")
}

func cloneToolDefinitions(in []llm.ToolDefinition) []llm.ToolDefinition {
	if len(in) == 0 {
		return nil
	}
	out := make([]llm.ToolDefinition, len(in))
	for i, def := range in {
		out[i] = cloneToolDefinition(def)
	}
	return out
}

func cloneToolDefinition(in llm.ToolDefinition) llm.ToolDefinition {
	out := in
	out.Parameters = cloneInterfaceMap(in.Parameters)
	out.OutputSchema = cloneInterfaceMap(in.OutputSchema)
	if len(in.Required) > 0 {
		out.Required = append([]string(nil), in.Required...)
	}
	return out
}

func cloneInterfaceMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return nil
	}
	raw, err := json.Marshal(src)
	if err != nil {
		out := make(map[string]interface{}, len(src))
		for key, value := range src {
			out[key] = value
		}
		return out
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

func augmentPromptApprovalReviewDefinitions(defs []llm.ToolDefinition, approvalByID map[string]*llm.ApprovalConfig) []llm.ToolDefinition {
	if len(defs) == 0 || len(approvalByID) == 0 {
		return defs
	}
	out := make([]llm.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		cfg := approvalByID[strings.ToLower(mcpname.Canonical(strings.TrimSpace(def.Name)))]
		if cfg == nil || (!cfg.IsPrompt() && !cfg.IsQueue()) || cfg.Review == nil || len(cfg.Review.RequestedSchema) == 0 {
			out = append(out, def)
			continue
		}
		out = append(out, augmentPromptApprovalReviewDefinition(def, cfg.Review.RequestedSchema))
	}
	return out
}

func augmentPromptApprovalReviewDefinition(def llm.ToolDefinition, reviewSchema map[string]interface{}) llm.ToolDefinition {
	out := cloneToolDefinition(def)
	if out.Parameters == nil {
		out.Parameters = map[string]interface{}{}
	}
	if _, ok := out.Parameters["type"]; !ok {
		out.Parameters["type"] = "object"
	}
	props, _ := out.Parameters["properties"].(map[string]interface{})
	if props == nil {
		props = map[string]interface{}{}
		out.Parameters["properties"] = props
	}
	reviewProps, _ := reviewSchema["properties"].(map[string]interface{})
	for key, value := range reviewProps {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, exists := props[key]; exists {
			continue
		}
		props[key] = value
	}
	existingRequired := map[string]struct{}{}
	required := make([]interface{}, 0)
	switch actual := out.Parameters["required"].(type) {
	case []interface{}:
		for _, item := range actual {
			name := strings.TrimSpace(fmt.Sprintf("%v", item))
			if name == "" {
				continue
			}
			existingRequired[name] = struct{}{}
			required = append(required, name)
		}
	case []string:
		for _, item := range actual {
			name := strings.TrimSpace(item)
			if name == "" {
				continue
			}
			existingRequired[name] = struct{}{}
			required = append(required, name)
		}
	}
	switch actual := reviewSchema["required"].(type) {
	case []interface{}:
		for _, item := range actual {
			name := strings.TrimSpace(fmt.Sprintf("%v", item))
			if name == "" {
				continue
			}
			if _, ok := existingRequired[name]; ok {
				continue
			}
			existingRequired[name] = struct{}{}
			required = append(required, name)
		}
	}
	if len(required) > 0 {
		out.Parameters["required"] = required
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
	definitions, _ := s.matchDefinitionsResult(ctx, pattern)
	return definitions
}

func (s *Service) matchDefinitionsResult(ctx context.Context, pattern string) ([]*llm.ToolDefinition, error) {
	ctx = toolSurfaceDiscoveryContext(ctx)
	if cm, ok := s.registry.(toolctx.ContextMatcherWithError); ok {
		return cm.MatchDefinitionWithContextResult(ctx, pattern)
	}
	if cm, ok := s.registry.(toolctx.ContextMatcher); ok {
		return cm.MatchDefinitionWithContext(ctx, pattern), nil
	}
	return s.registry.MatchDefinition(pattern), nil
}

func (s *Service) definitions(ctx context.Context) []llm.ToolDefinition {
	ctx = toolSurfaceDiscoveryContext(ctx)
	if lister, ok := s.registry.(toolctx.ContextDefinitionLister); ok {
		return lister.DefinitionsWithContext(ctx)
	}
	return s.registry.Definitions()
}

func (s *Service) getDefinition(ctx context.Context, name string) (*llm.ToolDefinition, bool) {
	ctx = toolSurfaceDiscoveryContext(ctx)
	if getter, ok := s.registry.(toolctx.ContextDefinitionGetter); ok {
		return getter.GetDefinitionWithContext(ctx, name)
	}
	return s.registry.GetDefinition(name)
}

func toolSurfaceDiscoveryContext(ctx context.Context) context.Context {
	return runtimediscovery.MergeMode(ctx, runtimediscovery.Mode{ToolSurface: true})
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
