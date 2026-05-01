package agent

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
)

type agentCatalog interface {
	All() []*agentmdl.Agent
}

func isAutoAgentRef(agentRef string) bool {
	switch strings.ToLower(strings.TrimSpace(agentRef)) {
	case "", "agent_id", "auto":
		return true
	}
	return false
}

func lastTurnAgentIDUsed(conv *apiconv.Conversation) string {
	if conv == nil || len(conv.Transcript) == 0 {
		return ""
	}
	for i := len(conv.Transcript) - 1; i >= 0; i-- {
		t := conv.Transcript[i]
		if t == nil || t.AgentIdUsed == nil {
			continue
		}
		id := strings.TrimSpace(*t.AgentIdUsed)
		if id == "" || isAutoAgentRef(id) {
			continue
		}
		return id
	}
	return ""
}

func lastUserQueryText(conv *apiconv.Conversation) string {
	if conv == nil || len(conv.Transcript) == 0 {
		return ""
	}
	for ti := len(conv.Transcript) - 1; ti >= 0; ti-- {
		t := conv.Transcript[ti]
		if t == nil || len(t.Message) == 0 {
			continue
		}
		for mi := len(t.Message) - 1; mi >= 0; mi-- {
			m := t.Message[mi]
			if m == nil || !strings.EqualFold(strings.TrimSpace(m.Role), "user") || !strings.EqualFold(strings.TrimSpace(m.Type), "text") {
				continue
			}
			if m.Content == nil {
				continue
			}
			if s := strings.TrimSpace(*m.Content); s != "" {
				return s
			}
		}
	}
	return ""
}

func tokenizeText(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r))
	})
	if len(parts) == 0 {
		return nil
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func filterAutoSelectableAgents(agents []*agentmdl.Agent) []*agentmdl.Agent {
	if len(agents) == 0 {
		return nil
	}
	out := make([]*agentmdl.Agent, 0, len(agents))
	for _, a := range agents {
		if a == nil {
			continue
		}
		if a.Internal {
			continue
		}
		id := strings.TrimSpace(a.ID)
		if id == "" {
			id = strings.TrimSpace(a.Name)
		}
		if id == "" {
			continue
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func autoSelectAgentID(query string, candidates []*agentmdl.Agent) string {
	candidates = filterAutoSelectableAgents(candidates)
	if len(candidates) == 0 {
		return ""
	}
	queryTokens := tokenizeText(query)
	if len(queryTokens) == 0 {
		return ""
	}
	stopWords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "but": {}, "by": {},
		"for": {}, "from": {}, "how": {}, "i": {}, "in": {}, "is": {}, "it": {}, "me": {}, "my": {},
		"of": {}, "on": {}, "or": {}, "please": {}, "the": {}, "to": {}, "we": {}, "with": {}, "you": {},
	}

	bestID := ""
	bestScore := 0
	for _, a := range candidates {
		if a == nil {
			continue
		}
		parts := []string{
			strings.TrimSpace(a.ID),
			strings.TrimSpace(a.Name),
			strings.TrimSpace(a.Description),
		}
		if a.Profile != nil {
			parts = append(parts,
				strings.TrimSpace(a.Profile.Name),
				strings.TrimSpace(a.Profile.Description),
				strings.Join(a.Profile.Tags, " "),
				strings.Join(a.Profile.Responsibilities, " "),
				strings.Join(a.Profile.InScope, " "),
			)
		}
		agentText := strings.Join(parts, " ")
		agentTokens := tokenizeText(agentText)
		if len(agentTokens) == 0 {
			continue
		}
		tokenSet := map[string]struct{}{}
		for _, t := range agentTokens {
			tokenSet[t] = struct{}{}
		}
		score := 0
		for _, qt := range queryTokens {
			if _, skip := stopWords[qt]; skip {
				continue
			}
			if len(qt) < 3 {
				continue
			}
			if _, ok := tokenSet[qt]; ok {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestID = strings.TrimSpace(a.ID)
			if bestID == "" {
				bestID = strings.TrimSpace(a.Name)
			}
		}
	}
	if bestScore == 0 {
		return ""
	}
	return bestID
}

// tryReuseFromPriorTurn implements workspace-intake cross-turn reuse: when a
// follow-up turn's query is topically close to the prior turn (Jaccard
// divergence below threshold) AND the prior agent is in the current
// authorized candidate set AND the prior agent isn't the synthetic
// capability responder, reuse it without an LLM router call.
//
// The threshold defaults to 0.65 — same value the agent-intake sidecar
// uses (see shouldRunIntake) — so reuse heuristics are consistent across
// the two intake layers. A future workspace-config knob can override.
//
// Returns nil when reuse cannot fire; caller falls through to the
// LLM router.
func (s *Service) tryReuseFromPriorTurn(ctx context.Context, conv *apiconv.Conversation, currentQuery string, candidates []*agentmdl.Agent) *routingDecision {
	if s == nil || conv == nil {
		return nil
	}
	priorAgent := strings.TrimSpace(lastTurnAgentIDUsed(conv))
	if priorAgent == "" {
		return nil
	}
	// Capability/clarify turns ran agent_selector — never reuse those; the
	// next turn must reclassify fresh per intake-impt.md §6.
	if isCapabilityAgentID(priorAgent) {
		return nil
	}
	// Prior agent must still be in the authorized candidate set.
	authorized := false
	for _, c := range candidates {
		if c == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(c.ID), priorAgent) {
			authorized = true
			break
		}
	}
	if !authorized {
		return nil
	}
	// Topic-shift gate.
	current := strings.TrimSpace(currentQuery)
	previous := s.previousUserMessage(ctx, conv.Id)
	if current == "" || previous == "" {
		return nil
	}
	const reuseThreshold = 0.65
	divergence := 1.0 - jaccardWordSimilarity(previous, current)
	if divergence >= reuseThreshold {
		return nil
	}
	return &routingDecision{
		AgentID:       priorAgent,
		AutoSelected:  true,
		RoutingReason: "reused",
	}
}

// Capability-question detection used to live here as a hardcoded marker list
// (`isCapabilityDiscoveryQuery`). It has been removed: the workspace intake
// LLM router is now the sole decider of whether a turn is a routing case or
// a capability-answer case. No heuristic patterns. No zero-LLM shortcuts.
//
// The router's system prompt instructs the LLM to output one of:
//
//	{"action":"route","agentId":"..."}     — pick an authorized agent
//	{"action":"answer","text":"..."}       — answer a capability question directly
//	{"action":"clarify","question":"..."}  — ask the user to disambiguate
//
// Workspaces customize behavior by overriding `agentAutoSelection.prompt`
// in workspace defaults; no code-level marker tables remain.

// tryResolveCapabilityAgent removed: capability-question routing is now
// produced by the workspace-intake LLM router via classifyAgentIDWithLLM,
// which returns ClassifierActionAnswer / ClassifierActionClarify directly.
// The downstream code path still routes those actions to the existing
// `agent_selector` agent for response composition.

// routingDecision carries the full outcome of resolveAgentIDForConversation.
// AgentID, AutoSelected, RoutingReason mirror the legacy 4-tuple return; the
// new Preset field carries action=answer / action=clarify text from the
// workspace-intake classifier so the runtime can publish it as the
// assistant message without a second LLM call. Preset is nil for normal
// route turns.
type routingDecision struct {
	AgentID       string
	AutoSelected  bool
	RoutingReason string
	Preset        *ClassifierResult // non-nil only for action=answer or action=clarify
}

func (s *Service) resolveAgentIDForConversation(ctx context.Context, conv *apiconv.Conversation, requestedAgent string, query string, preferredTurnID string) (string, bool, string, error) {
	dec, err := s.resolveTurnRouting(ctx, conv, requestedAgent, query, preferredTurnID)
	if err != nil || dec == nil {
		return "", false, "", err
	}
	return dec.AgentID, dec.AutoSelected, dec.RoutingReason, nil
}

// resolveTurnRouting is the full workspace-intake routing entry point. It
// returns a routingDecision that may contain a preset assistant-message
// payload when the classifier elected to answer the user directly
// (capability question) or to ask a clarification (ambiguous request).
//
// The legacy resolveAgentIDForConversation wrapper above keeps existing
// callers working without code change; new code paths that need access to
// the preset use this function directly.
func (s *Service) resolveTurnRouting(ctx context.Context, conv *apiconv.Conversation, requestedAgent string, query string, preferredTurnID string) (*routingDecision, error) {
	providedQuery := strings.TrimSpace(query)
	if strings.TrimSpace(query) == "" {
		query = lastUserQueryText(conv)
	}

	defaultAgent := ""
	if s != nil && s.defaults != nil {
		defaultAgent = strings.TrimSpace(s.defaults.Agent)
	}

	explicitAgent := strings.TrimSpace(requestedAgent)
	autoRequested := false
	if explicitAgent != "" {
		if !isAutoAgentRef(explicitAgent) {
			return &routingDecision{AgentID: explicitAgent, RoutingReason: "explicit"}, nil
		}
		autoRequested = true
	}
	if conv != nil && conv.AgentId != nil {
		conversationAgent := strings.TrimSpace(*conv.AgentId)
		if explicitAgent == "" {
			if conversationAgent != "" && !isAutoAgentRef(conversationAgent) {
				return &routingDecision{AgentID: conversationAgent, RoutingReason: "explicit"}, nil
			}
			autoRequested = isAutoAgentRef(conversationAgent)
		}
	} else if explicitAgent == "" {
		autoRequested = autoRequested || isAutoAgentRef(defaultAgent)
	}

	// When auto is not requested, preserve continuity by using the last agent
	// that executed in this conversation, before falling back to workspace
	// defaults.
	if !autoRequested {
		if id := lastTurnAgentIDUsed(conv); id != "" {
			return &routingDecision{AgentID: id, RoutingReason: "continuity"}, nil
		}
		if defaultAgent != "" && !isAutoAgentRef(defaultAgent) {
			return &routingDecision{AgentID: defaultAgent, RoutingReason: "default"}, nil
		}
		return nil, fmt.Errorf("agent is required")
	}

	var candidates []*agentmdl.Agent
	if s != nil {
		if items, err := s.listPublishedAgents(ctx); err == nil {
			candidates = items
		}
	}
	if len(candidates) == 0 && s != nil && s.agentFinder != nil {
		if c, ok := s.agentFinder.(agentCatalog); ok {
			candidates = c.All()
		}
	}

	// Cross-turn reuse: when the user is on a follow-up turn whose query is
	// topically close to the previous turn AND the prior agent is still in
	// the authorized candidate set, reuse it without paying for the LLM
	// router. This is the workspace-intake equivalent of agent intake's
	// reuse rule (intake-impt.md §8.2). Capability turns (prior agent =
	// agent_selector) are never reused — every capability question
	// re-classifies fresh, matching the §6 contract.
	if providedQuery != "" {
		if reused := s.tryReuseFromPriorTurn(ctx, conv, query, candidates); reused != nil {
			return reused, nil
		}
	}

	// Workspace intake LLM call. Single source of truth for "agentId=auto"
	// turn outcomes. Only run when the caller provided a query for this turn
	// (avoids extra LLM calls during internal operations such as summarization,
	// where the routing should rely on continuity / last-used agent).
	//
	// The classifier produces a ClassifierResult with one of three actions:
	//   route   — pick an authorized agent; downstream agent.Query() runs
	//   answer  — workspace-capability response; runtime persists the text
	//             as the assistant message (no second LLM call)
	//   clarify — disambiguation question; same persistence path as answer
	if providedQuery != "" {
		if result, err := s.classifyAgentIDWithLLM(ctx, conv, query, preferredTurnID, candidates); err != nil {
			return nil, err
		} else if result != nil {
			switch result.Action {
			case ClassifierActionRoute:
				if id := strings.TrimSpace(result.AgentID); id != "" {
					return &routingDecision{
						AgentID:       id,
						AutoSelected:  true,
						RoutingReason: "llm_router",
					}, nil
				}
			case ClassifierActionAnswer, ClassifierActionClarify:
				// Route to agent_selector for the response *agent identity*
				// (the conversation message is attributed to it), but carry
				// the classifier's already-produced text on Preset so the
				// downstream short-circuit publishes it directly without a
				// second LLM call. ensureAgent stashes the preset under
				// QueryInput.Context for the publishing layer.
				return &routingDecision{
					AgentID:       "agent_selector",
					AutoSelected:  true,
					RoutingReason: "llm_router_" + result.Action,
					Preset:        result,
				}, nil
			}
		}
	}
	if selected := autoSelectAgentID(query, candidates); selected != "" {
		return &routingDecision{
			AgentID:       selected,
			AutoSelected:  true,
			RoutingReason: "token_match",
		}, nil
	}
	// If routing cannot decide, keep continuity as a safe fallback.
	if id := lastTurnAgentIDUsed(conv); id != "" {
		return &routingDecision{AgentID: id, RoutingReason: "continuity"}, nil
	}
	if defaultAgent != "" && !isAutoAgentRef(defaultAgent) {
		return &routingDecision{AgentID: defaultAgent, RoutingReason: "default"}, nil
	}
	return &routingDecision{AutoSelected: true}, fmt.Errorf("agent is required")
}
