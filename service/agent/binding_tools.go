package agent

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/viant/afs/url"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	"github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/prompt"
	padapter "github.com/viant/agently-core/protocol/prompt/adapter"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/workspace/repository/toolplaybook"
)

func (s *Service) appendToolPlaybooks(ctx context.Context, defs []*llm.ToolDefinition, docs *prompt.Documents) error {
	if docs == nil {
		return nil
	}
	_, services := collectToolPresence(defs)
	if !services["webdriver"] {
		return nil
	}
	repo := toolplaybook.New(s.fs)
	content, uri, err := repo.Load(ctx, "webdriver.md")
	if err != nil {
		return err
	}
	if strings.TrimSpace(content) == "" || strings.TrimSpace(uri) == "" {
		return nil
	}
	if hasDocumentURI(docs.Items, uri) {
		return nil
	}
	doc := &prompt.Document{
		Title:       "tools/instructions/webdriver",
		PageContent: strings.TrimSpace(content),
		SourceURI:   uri,
		Score:       1.0,
		MimeType:    "text/markdown",
		Metadata:    map[string]string{"kind": "tool_playbook", "tool": "webdriver"},
	}
	docs.Items = append(docs.Items, doc)
	return nil
}

func hasDocumentURI(items []*prompt.Document, uri string) bool {
	u := strings.TrimSpace(uri)
	if u == "" || len(items) == 0 {
		return false
	}
	for _, d := range items {
		if d == nil {
			continue
		}
		if strings.TrimSpace(d.SourceURI) == u {
			return true
		}
	}
	return false
}

func applyToolContext(ctx map[string]interface{}, defs []*llm.ToolDefinition) {
	if ctx == nil {
		return
	}
	toolsCtx := ensureToolsContextMap(ctx)
	presentSet, serviceSet := collectToolPresence(defs)
	present := make(map[string]interface{}, len(presentSet))
	services := make(map[string]interface{}, len(serviceSet))
	for k, v := range presentSet {
		if v {
			present[k] = true
		}
	}
	for k, v := range serviceSet {
		if v {
			services[k] = true
		}
	}

	toolsCtx["present"] = present
	toolsCtx["services"] = services
	toolsCtx["hasWebdriver"] = serviceSet["webdriver"]
	toolsCtx["hasResources"] = serviceSet["resources"]
}

func collectToolPresence(defs []*llm.ToolDefinition) (map[string]bool, map[string]bool) {
	present := map[string]bool{}
	services := map[string]bool{}
	for _, d := range defs {
		if d == nil {
			continue
		}
		raw := strings.TrimSpace(d.Name)
		if raw == "" {
			continue
		}
		name := mcpname.Canonical(raw)
		if strings.TrimSpace(name) == "" {
			name = raw
		}
		present[name] = true
		svc := mcpname.Name(name).Service()
		if strings.TrimSpace(svc) != "" {
			services[svc] = true
		}
	}
	return present, services
}

func filterDelegationDiscoveryTools(defs []*llm.ToolDefinition, docs *prompt.Documents) []*llm.ToolDefinition {
	if len(defs) == 0 || docs == nil || !hasDocumentURI(docs.Items, "internal://llm/agents/list") {
		return defs
	}
	filtered := make([]*llm.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		name := mcpname.Canonical(strings.TrimSpace(def.Name))
		if name == "llm_agents-list" {
			continue
		}
		filtered = append(filtered, def)
	}
	return filtered
}

func dedupeToolDefinitions(defs []*llm.ToolDefinition) []*llm.ToolDefinition {
	if len(defs) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	filtered := make([]*llm.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(mcpname.Canonical(def.Name)))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, def)
	}
	return filtered
}

func ensureToolsContextMap(ctx map[string]interface{}) map[string]interface{} {
	if ctx == nil {
		return map[string]interface{}{}
	}
	if v, ok := ctx["tools"]; ok && v != nil {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
		// Preserve existing "tools" key when not an object.
		if v2, ok := ctx["agentlyTools"]; ok && v2 != nil {
			if m, ok := v2.(map[string]interface{}); ok {
				return m
			}
		}
		m := map[string]interface{}{}
		ctx["agentlyTools"] = m
		return m
	}
	m := map[string]interface{}{}
	ctx["tools"] = m
	return m
}

func (s *Service) handleOverflow(ctx context.Context, input *QueryInput, current *apiconv.Turn, b *prompt.Binding) {
	// Detect token-limit recovery by scanning current turn for an assistant error message
	tokenLimit := false
	if current != nil && len(current.Message) > 0 {
		for _, m := range current.Message {
			if m == nil || m.Content == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(m.Role), "assistant") && m.Status != nil && strings.EqualFold(strings.TrimSpace(*m.Status), "error") {
				{
					msg := strings.ToLower(*m.Content)
					if core.ContainsContextLimitError(msg) {
						tokenLimit = true
						break
					}
				}
			}
		}
	}
	// Drive from flags or token-limit hint
	if !b.Flags.HasMessageOverflow && !tokenLimit {
		return
	}

	// removed debug print: hasOverflow and existing signatures

	// Build a canonical set of already exposed tools to avoid duplicates
	have := map[string]bool{}
	for _, e := range b.Tools.Signatures {
		if e == nil {
			continue
		}
		have[mcpname.Canonical(e.Name)] = true
	}

	// Query only message tools from the registry (avoid full scan)
	// Using a service-only pattern matches any method under that service.
	pattern := "message" // service-only pattern (match any method)
	defs := s.registry.MatchDefinition(pattern)

	for _, d := range defs {
		if d == nil {
			continue
		}
		name := mcpname.Canonical(d.Name)
		// Only expose show/summarize/match on overflow; gate remove for token-limit flow
		// Derive method from tool name. Names can be in forms like:
		//   message:show  (service:method)
		//   message-show  (canonicalized with dash)
		// Fallback to full name when no separator present.
		method := ""
		if i := strings.LastIndexAny(name, ":-"); i != -1 && i+1 < len(name) {
			method = name[i+1:]
		}
		if method == "" {
			method = name
		}
		allowed := false
		if tokenLimit {
			if method == "remove" || method == "summarize" {
				allowed = true
			}
		} else if b.Flags.HasMessageOverflow {
			// For normal overflow, always expose show; gate summarize on
			// configured summaryThresholdBytes and recorded MaxOverflowBytes.
			if method == "show" || method == "match" {
				allowed = true
			} else if method == "summarize" {
				threshold := 0
				if s.defaults != nil {
					threshold = s.defaults.PreviewSettings.SummaryThresholdBytes
				}
				// When threshold <= 0, fallback to previous behavior and
				// allow summarize for any overflow. Otherwise, require at
				// least one overflowed message to exceed the threshold.
				if threshold <= 0 || b.Flags.MaxOverflowBytes > threshold {
					allowed = true
				}
			}
		}

		if !allowed {
			continue
		}
		if have[name] {
			continue
		}
		dd := *d
		// Canonicalize name to service_path-method for consistency (e.g., message-match)
		dd.Name = mcpname.Canonical(dd.Name)
		b.Tools.Signatures = append(b.Tools.Signatures, &dd)
		have[name] = true
	}
	// removed debug print: final signatures count

	// Optionally append a system guide document when configured in defaults
	s.appendCallToolResultGuide(ctx, b)
}

func (s *Service) appendCallToolResultGuide(ctx context.Context, b *prompt.Binding) {
	if s.defaults != nil && strings.TrimSpace(s.defaults.PreviewSettings.SystemGuidePath) != "" {
		guide := strings.TrimSpace(s.defaults.PreviewSettings.SystemGuidePath)
		uri := guide
		if url.Scheme(uri, "") == "" {
			uri = "file://" + guide
		}
		if data, err := s.fs.DownloadWithURL(ctx, uri); err == nil && len(data) > 0 {
			title := filepath.Base(guide)
			if strings.TrimSpace(title) == "" {
				title = "Tool Result Guide"
			}
			doc := &prompt.Document{Title: title, PageContent: string(data), SourceURI: uri, MimeType: "text/markdown"}
			b.SystemDocuments.Items = append(b.SystemDocuments.Items, doc)
		}
	}
}

// ensureInternalToolsIfNeeded appends message tools that are used during
// continuation-by-response-id flows so that the model can reference them when
// continuing a prior response. Tool are appended only when the selected model
// supports continuation. Duplicates are avoided by canonical name.
func (s *Service) ensureInternalToolsIfNeeded(ctx context.Context, input *QueryInput, b *prompt.Binding) {
	if s == nil || s.registry == nil || b == nil {
		return
	}
	if input != nil {
		if (input.Agent != nil && isCapabilityAgentID(strings.TrimSpace(input.Agent.ID))) || isCapabilityAgentID(strings.TrimSpace(input.AgentID)) {
			return
		}
	}
	modelName := strings.TrimSpace(b.Model)
	if modelName == "" {
		return
	}

	// Decide based on the same continuation semantics as the core service.
	finder := s.llm.ModelFinder()
	if finder == nil {
		return
	}
	model, err := finder.Find(ctx, modelName)
	if err != nil || model == nil {
		return
	}
	if !core.IsContextContinuationEnabled(model) {
		return
	}

	// Build set of existing tool names to avoid duplicates
	have := map[string]bool{}
	for _, sig := range b.Tools.Signatures {
		if sig == nil {
			continue
		}
		have[mcpname.Canonical(sig.Name)] = true
	}

	// Collect message tool definitions and append a consistent subset used in overflow handling
	// We include: show, summarize, match, remove (the union of tools referenced in handleOverflow).
	defs := s.registry.MatchDefinition("message")
	wanted := map[string]bool{"show": true, "summarize": true, "match": true, "remove": true}
	for _, d := range defs {
		if d == nil {
			continue
		}
		name := mcpname.Canonical(d.Name)
		// Derive method suffix
		method := name
		if i := strings.LastIndexAny(name, ":-"); i != -1 && i+1 < len(name) {
			method = name[i+1:]
		}
		if !wanted[method] {
			continue
		}
		if have[name] {
			continue
		}
		dd := *d
		dd.Name = name
		b.Tools.Signatures = append(b.Tools.Signatures, &dd)
		have[name] = true
	}
}

// ToolExecutionsResult holds the combined result of building tool call
// executions from transcript history.
type ToolExecutionsResult struct {
	Calls            []*llm.ToolCall
	Overflow         bool
	MaxOverflowBytes int
}

func (s *Service) buildToolExecutions(ctx context.Context, input *QueryInput, conv *apiconv.Conversation, exposure agent.ToolCallExposure) (*ToolExecutionsResult, error) {
	turnMeta, ok := memory.TurnMetaFromContext(ctx)
	if !ok || strings.TrimSpace(turnMeta.TurnID) == "" {
		return &ToolExecutionsResult{}, nil
	}
	transcript := conv.GetTranscript()
	// Determine whether continuation preview format is enabled for the selected model.
	allowContinuation := s.allowContinuationPreview(ctx, input)
	totalTurns := len(transcript)
	overflowFound := false
	maxOverflowBytes := 0
	buildFromTurn := func(turnIdx int, t *apiconv.Turn, applyAging bool) []*llm.ToolCall {
		var out []*llm.ToolCall
		if t == nil {
			return out
		}

		toolCalls := t.ToolCalls()
		if len(toolCalls) > s.defaults.ToolCallMaxResults && s.defaults.ToolCallMaxResults > 0 {
			toolCalls = toolCalls[len(toolCalls)-s.defaults.ToolCallMaxResults:]
		}
		limit := s.turnPreviewLimit(turnIdx, totalTurns, applyAging)
		for _, m := range toolCalls {
			tcView := messageToolCall(m)
			if tcView == nil {
				continue
			}
			args := m.ToolCallArguments()
			// Prepare result content for LLM: derive preview from message content with per-turn limit
			result := ""
			if body := s.messageToolResultBody(ctx, m); body != "" && limit > 0 {
				preview, overflow := buildOverflowPreview(body, limit, m.Id, allowContinuation)
				if overflow {
					overflowFound = true
					if size := len(body); size > maxOverflowBytes {
						maxOverflowBytes = size
					}
				}
				result = preview
			}

			// Canonicalize tool name so it matches declared tool signatures for providers.
			tc := llm.NewToolCall(tcView.OpId, mcpname.Canonical(tcView.ToolName), args, result)
			out = append(out, &tc)
		}
		return out
	}

	switch strings.ToLower(string(exposure)) {
	case "conversation":
		var out []*llm.ToolCall
		for idx, t := range transcript {
			out = append(out, buildFromTurn(idx, t, true)...)
		}
		return &ToolExecutionsResult{Calls: out, Overflow: overflowFound, MaxOverflowBytes: maxOverflowBytes}, nil
	case "turn", "":
		// Find current turn only
		var aTurn *apiconv.Turn
		var turnIdx int
		for idx, t := range transcript {
			if t != nil && t.Id == turnMeta.TurnID {
				aTurn = t
				turnIdx = idx
				break
			}
		}
		if aTurn == nil {
			return &ToolExecutionsResult{}, nil
		}
		// For turn exposure, do not apply aging; always use Limit.
		execs := buildFromTurn(turnIdx, aTurn, false)
		return &ToolExecutionsResult{Calls: execs, Overflow: overflowFound, MaxOverflowBytes: maxOverflowBytes}, nil
	default:
		// Unrecognised/semantic: do not include tool calls for now
		return &ToolExecutionsResult{}, nil
	}
}

func (s *Service) buildToolSignatures(ctx context.Context, input *QueryInput) ([]*llm.ToolDefinition, error) {
	if s.registry == nil || input == nil || input.Agent == nil {
		return nil, nil
	}
	tools, err := s.resolveTools(ctx, input)
	if err != nil {
		return nil, err
	}
	out := padapter.ToToolDefinitions(tools)
	out = dedupeToolDefinitions(out)
	if DebugEnabled() {
		names := make([]string, 0, len(out))
		for _, item := range out {
			if item == nil {
				continue
			}
			if name := strings.TrimSpace(item.Name); name != "" {
				names = append(names, name)
			}
		}
		debugf("agent.buildToolSignatures agent_id=%q tool_names=%q", strings.TrimSpace(input.Agent.ID), strings.Join(names, ","))
	}
	return out, nil
}
