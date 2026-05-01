package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/internal/logx"
	"strings"
	"time"

	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/agent/prompts"
	"github.com/viant/agently-core/service/core"
)

type agentSelection struct {
	// Unified action envelope — workspace intake schema. Action is one of
	// "route" | "answer" | "clarify". For action=route, the agent id rides
	// alongside under one of the AgentID/AgentId/ID/Agent fields. For
	// action=answer the Text field carries the capability answer; for
	// action=clarify the Question field carries the disambiguation question.
	Action   string `json:"action,omitempty"`
	Text     string `json:"text,omitempty"`
	Question string `json:"question,omitempty"`
	// Agent-id fields (used when action=route, or when the LLM emits the
	// legacy {agentId: X} schema without an action key). Multiple key names
	// are accepted for backward compatibility.
	AgentID string `json:"agentId"`
	AgentId string `json:"agent_id"`
	ID      string `json:"id"`
	Agent   string `json:"agent"`
}

// ClassifierAction enumerates the three terminal outcomes of a workspace
// intake LLM call. Exactly one of {AgentID, Answer, Question} is populated
// per result.
const (
	ClassifierActionRoute   = "route"
	ClassifierActionAnswer  = "answer"
	ClassifierActionClarify = "clarify"
)

// ClassifierResult is the structured output of the workspace intake LLM call.
// This is the single source of truth for "agentId=auto" turn outcomes.
//
// Exactly one of AgentID / Answer / Question is non-empty per Action:
//
//	Action=ClassifierActionRoute   → AgentID  is set
//	Action=ClassifierActionAnswer  → Answer   is set (capability response)
//	Action=ClassifierActionClarify → Question is set (one disambiguation question)
//
// An empty ClassifierResult (Action == "") means the LLM call produced no
// usable output; callers fall through to the deterministic continuity /
// default chain.
type ClassifierResult struct {
	Action   string
	AgentID  string
	Answer   string
	Question string
}

func (s *Service) classifyAgentIDWithLLM(ctx context.Context, conv *apiconv.Conversation, query, preferredTurnID string, candidates []*agentmdl.Agent) (*ClassifierResult, error) {
	started := time.Now()
	query = strings.TrimSpace(query)
	candidates = filterAutoSelectableAgents(candidates)
	if query == "" || len(candidates) == 0 || s == nil || s.llm == nil || s.llm.ModelFinder() == nil {
		return nil, nil
	}

	modelName := ""
	if conv != nil && conv.DefaultModel != nil {
		modelName = strings.TrimSpace(*conv.DefaultModel)
	}
	if modelName == "" && s.defaults != nil {
		modelName = strings.TrimSpace(s.defaults.AgentAutoSelection.Model)
		if modelName == "" {
			modelName = strings.TrimSpace(s.defaults.Model)
		}
	}
	logx.Infof("conversation", "agent.selector config convo=%q agent_auto_model=%q default_model=%q effective_model=%q candidates=%d", strings.TrimSpace(convID(conv)), strings.TrimSpace(valueOrDefaultDefaultsModel(s)), strings.TrimSpace(valueOrDefaultModel(s)), strings.TrimSpace(modelName), len(candidates))
	if modelName == "" {
		logx.Infof("conversation", "agent.selector skip convo=%q reason=%q", strings.TrimSpace(convID(conv)), "empty_model")
		return nil, nil
	}

	model, err := s.llm.ModelFinder().Find(ctx, modelName)
	if err != nil || model == nil {
		logx.Infof("conversation", "agent.selector skip convo=%q reason=%q model=%q err=%v", strings.TrimSpace(convID(conv)), "model_not_found", strings.TrimSpace(modelName), err)
		return nil, nil
	}

	candidateByKey := map[string]string{}
	candidateLines := make([]string, 0, len(candidates))
	for _, a := range candidates {
		if a == nil {
			continue
		}
		id := strings.TrimSpace(a.ID)
		if id == "" {
			id = strings.TrimSpace(a.Name)
		}
		if id == "" {
			continue
		}
		candidateByKey[strings.ToLower(id)] = id
		desc := strings.TrimSpace(a.Description)
		role := ""
		if a.Persona != nil {
			role = strings.TrimSpace(a.Persona.Role)
		}
		label := id
		if name := strings.TrimSpace(a.Name); name != "" && name != id {
			label = fmt.Sprintf("%s (%s)", id, name)
		}
		if role != "" {
			label = fmt.Sprintf("%s [role=%s]", label, role)
		}
		if a.Profile != nil {
			if len(a.Profile.Tags) > 0 {
				label = fmt.Sprintf("%s [tags=%s]", label, strings.Join(a.Profile.Tags, ","))
			}
			if a.Profile.Rank != 0 {
				label = fmt.Sprintf("%s [rank=%d]", label, a.Profile.Rank)
			}
		}
		if desc != "" {
			candidateLines = append(candidateLines, fmt.Sprintf("- %s: %s", label, desc))
		} else {
			candidateLines = append(candidateLines, fmt.Sprintf("- %s", label))
		}
	}
	if len(candidateLines) == 0 {
		return nil, nil
	}

	history := recentNonInterimTurnsText(conv, 3)

	outputKey := agentRouterOutputKey(s.defaults)
	systemPrompt := agentRouterSystemPrompt(s.defaults, outputKey)

	userParts := []string{}
	if strings.TrimSpace(history) != "" {
		userParts = append(userParts,
			"Recent conversation context (last 3 turns):",
			history,
			"",
		)
	}
	userParts = append(userParts,
		"User request:",
		query,
		"",
		"Available agents:",
		strings.Join(candidateLines, "\n"),
		"",
		"JSON response:",
	)
	user := strings.Join(userParts, "\n")

	convID := ""
	if conv != nil {
		convID = conv.Id
	}
	runCtx := s.ensureRunTrackedLLMContext(ctx, convID, "agent_selector", preferredTurnID)
	timeoutSec := 20
	if s.defaults != nil && s.defaults.AgentAutoSelection.TimeoutSec > 0 {
		timeoutSec = s.defaults.AgentAutoSelection.TimeoutSec
	}
	var cancel func()
	runCtx, cancel = context.WithTimeout(runCtx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	logx.Infof("conversation", "agent.selector start convo=%q model=%q timeout_sec=%d candidates=%d query_len=%d", strings.TrimSpace(convID), strings.TrimSpace(modelName), timeoutSec, len(candidateLines), len(query))
	in := &core.GenerateInput{
		UserID: "system",
		ModelSelection: llm.ModelSelection{
			Model: modelName,
			Options: &llm.Options{
				// Note: provider adapters may treat 0 as "unset"; use a tiny value to force near-deterministic routing.
				Temperature:      0.0000001,
				MaxTokens:        64,
				JSONMode:         true,
				ResponseMIMEType: "application/json",
				ToolChoice:       llm.NewNoneToolChoice(),
				Mode:             "router",
			},
		},
		Message: []llm.Message{
			llm.NewSystemMessage(systemPrompt),
			llm.NewUserMessage(user),
		},
	}
	out := &core.GenerateOutput{}
	err = s.llm.Generate(runCtx, in, out)
	if err != nil {
		logx.Warnf("conversation", "agent.selector error convo=%q model=%q elapsed=%s err=%v", strings.TrimSpace(convID), strings.TrimSpace(modelName), time.Since(started), err)
		s.publishIntakeWorkspaceFailed(ctx, convID, "llm_error", "", modelName, err.Error())
		return nil, err
	}
	parsed := parseClassifierResult(responseForContent(out.Response, out.Content), outputKey)
	if parsed == nil {
		logx.Infof("conversation", "agent.selector done convo=%q model=%q selected=\"\" elapsed=%s", strings.TrimSpace(convID), strings.TrimSpace(modelName), time.Since(started))
		s.publishIntakeWorkspaceFailed(ctx, convID, "parse_error", "", modelName, "")
		return nil, nil
	}
	durationMs := time.Since(started).Milliseconds()

	// Action=answer / action=clarify: pass through as-is. The runtime
	// (resolveAgentIDForConversation + downstream) writes the text as the
	// turn's assistant message — no agent runs.
	if parsed.Action == ClassifierActionAnswer {
		logx.Infof("conversation", "agent.selector done convo=%q model=%q action=%q elapsed=%s answer_len=%d", strings.TrimSpace(convID), strings.TrimSpace(modelName), parsed.Action, time.Since(started), len(parsed.Answer))
		s.publishIntakeWorkspaceCompleted(ctx, convID, parsed, modelName, durationMs)
		return parsed, nil
	}
	if parsed.Action == ClassifierActionClarify {
		logx.Infof("conversation", "agent.selector done convo=%q model=%q action=%q elapsed=%s question_len=%d", strings.TrimSpace(convID), strings.TrimSpace(modelName), parsed.Action, time.Since(started), len(parsed.Question))
		s.publishIntakeWorkspaceCompleted(ctx, convID, parsed, modelName, durationMs)
		return parsed, nil
	}

	// Action=route (or legacy schema with bare agentId): canonicalize the id
	// against the authorized candidate set. Drop unauthorized selections.
	selected := strings.TrimSpace(parsed.AgentID)
	if selected == "" {
		logx.Infof("conversation", "agent.selector done convo=%q model=%q selected=\"\" elapsed=%s", strings.TrimSpace(convID), strings.TrimSpace(modelName), time.Since(started))
		s.publishIntakeWorkspaceFailed(ctx, convID, "empty_agent_id", "", modelName, "")
		return nil, nil
	}
	logx.Infof("conversation", "agent.selector done convo=%q model=%q selected=%q elapsed=%s", strings.TrimSpace(convID), strings.TrimSpace(modelName), selected, time.Since(started))
	canonicalID := ""
	if canonical, ok := candidateByKey[strings.ToLower(selected)]; ok {
		canonicalID = canonical
	} else {
		// Allow agents to be referred by name when name differs from ID.
		for key, canonical := range candidateByKey {
			if strings.EqualFold(key, selected) {
				canonicalID = canonical
				break
			}
		}
	}
	if canonicalID == "" {
		// LLM picked an agent not in the authorized set — drop and let the
		// caller fall through to the deterministic continuity / default chain.
		s.publishIntakeWorkspaceFailed(ctx, convID, "agent_unauthorized", "", modelName, "")
		return nil, nil
	}
	result := &ClassifierResult{Action: ClassifierActionRoute, AgentID: canonicalID}
	s.publishIntakeWorkspaceCompleted(ctx, convID, result, modelName, durationMs)
	return result, nil
}

// publishIntakeWorkspaceCompleted emits an intake.workspace.completed
// streaming event after a successful workspace-intake LLM call. Wraps the
// existing streaming.Publisher infrastructure — same pattern as bootstrap
// tool events — so subscribers receive a uniform shape regardless of
// whether the action was route, answer, or clarify.
func (s *Service) publishIntakeWorkspaceCompleted(ctx context.Context, conversationID string, result *ClassifierResult, modelName string, durationMs int64) {
	if s == nil || s.streamPub == nil || result == nil {
		return
	}
	patch := map[string]interface{}{
		"action":     result.Action,
		"durationMs": durationMs,
		"model":      strings.TrimSpace(modelName),
		"source":     "workspace",
	}
	switch result.Action {
	case ClassifierActionRoute:
		patch["selectedAgentId"] = strings.TrimSpace(result.AgentID)
	case ClassifierActionAnswer:
		patch["answerLen"] = len(result.Answer)
	case ClassifierActionClarify:
		patch["questionLen"] = len(result.Question)
	}
	turnID := ""
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = strings.TrimSpace(tm.TurnID)
	}
	convID := strings.TrimSpace(conversationID)
	event := &streaming.Event{
		Type:           streaming.EventTypeIntakeWorkspaceCompleted,
		StreamID:       convID,
		ConversationID: convID,
		TurnID:         turnID,
		Phase:          "intake",
		Mode:           "router",
		Patch:          patch,
		CreatedAt:      time.Now(),
	}
	event.NormalizeIdentity(convID, turnID)
	if err := s.streamPub.Publish(ctx, event); err != nil {
		logx.Warnf("conversation", "intake.workspace.completed publish err=%v convo=%q turn=%q", err, convID, turnID)
	}
}

// publishIntakeWorkspaceFailed emits intake.workspace.failed for any case
// where the workspace-intake LLM call did not produce a usable result —
// LLM error, timeout, parse failure, unauthorized agent selection, or empty
// agent id from the model.
func (s *Service) publishIntakeWorkspaceFailed(ctx context.Context, conversationID, reason, fallbackAgentID, modelName, errMessage string) {
	if s == nil || s.streamPub == nil {
		return
	}
	patch := map[string]interface{}{
		"reason": strings.TrimSpace(reason),
		"model":  strings.TrimSpace(modelName),
	}
	if fb := strings.TrimSpace(fallbackAgentID); fb != "" {
		patch["fallbackAgentId"] = fb
	}
	if msg := strings.TrimSpace(errMessage); msg != "" {
		patch["errMessage"] = msg
	}
	turnID := ""
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = strings.TrimSpace(tm.TurnID)
	}
	convID := strings.TrimSpace(conversationID)
	event := &streaming.Event{
		Type:           streaming.EventTypeIntakeWorkspaceFailed,
		StreamID:       convID,
		ConversationID: convID,
		TurnID:         turnID,
		Phase:          "intake",
		Mode:           "router",
		Patch:          patch,
		CreatedAt:      time.Now(),
	}
	event.NormalizeIdentity(convID, turnID)
	if err := s.streamPub.Publish(ctx, event); err != nil {
		logx.Warnf("conversation", "intake.workspace.failed publish err=%v convo=%q turn=%q", err, convID, turnID)
	}
}

func responseForContent(resp *llm.GenerateResponse, content string) *llm.GenerateResponse {
	if resp != nil {
		return resp
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Content: content}}}}
}

// parseClassifierResult extracts the unified action envelope from the LLM
// response. Recognizes both the new schema:
//
//	{"action":"route","agentId":"X"}
//	{"action":"answer","text":"..."}
//	{"action":"clarify","question":"..."}
//
// and the legacy bare-agent-id schema:
//
//	{"agentId":"X"}
//
// Returns nil when content is empty / unparseable AND no fallback id can be
// extracted. Returns a ClassifierResult with Action=ClassifierActionRoute and
// non-empty AgentID for legacy outputs (so callers see a single normalized
// shape regardless of which schema the LLM emitted).
func parseClassifierResult(resp *llm.GenerateResponse, outputKey string) *ClassifierResult {
	if resp == nil || len(resp.Choices) == 0 {
		return nil
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return nil
	}
	content = strings.TrimSpace(strings.TrimPrefix(content, "```json"))
	content = strings.TrimSpace(strings.TrimPrefix(content, "```"))
	content = strings.TrimSpace(strings.TrimSuffix(content, "```"))

	var sel agentSelection
	if err := json.Unmarshal([]byte(content), &sel); err == nil {
		// Unified action envelope takes precedence.
		switch strings.ToLower(strings.TrimSpace(sel.Action)) {
		case ClassifierActionAnswer:
			if text := strings.TrimSpace(sel.Text); text != "" {
				return &ClassifierResult{Action: ClassifierActionAnswer, Answer: text}
			}
			return nil
		case ClassifierActionClarify:
			if q := strings.TrimSpace(sel.Question); q != "" {
				return &ClassifierResult{Action: ClassifierActionClarify, Question: q}
			}
			return nil
		case ClassifierActionRoute, "":
			// Empty action = legacy {"agentId":"X"} schema. Route action
			// with explicit "route" string also lands here.
			if id := pickAgentIDField(sel, outputKey); id != "" {
				return &ClassifierResult{Action: ClassifierActionRoute, AgentID: id}
			}
			return nil
		}
		// Unknown action value — defensive recovery: if a usable agentId is
		// also present, treat as a route. Otherwise nil. We do NOT fall
		// through to the non-JSON token fallback because the input parsed
		// as JSON; treating its raw bytes as an agent id would be nonsense.
		if id := pickAgentIDField(sel, outputKey); id != "" {
			return &ClassifierResult{Action: ClassifierActionRoute, AgentID: id}
		}
		return nil
	}
	// Non-JSON content: best-effort fallback — treat the first token/line as
	// agent id (legacy "raw model output" handling).
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		content = strings.TrimSpace(content[:idx])
	}
	if idx := strings.IndexAny(content, " \t"); idx >= 0 {
		content = strings.TrimSpace(content[:idx])
	}
	if id := strings.Trim(content, `"'`); id != "" {
		return &ClassifierResult{Action: ClassifierActionRoute, AgentID: id}
	}
	return nil
}

// pickAgentIDField selects the agent-id field according to the configured
// output key, falling back to any of the known synonyms (id, agent_id,
// agent). Returns the trimmed value or "" when none populated.
func pickAgentIDField(sel agentSelection, outputKey string) string {
	if key := strings.TrimSpace(outputKey); key != "" {
		switch strings.ToLower(key) {
		case "agentid":
			if v := strings.TrimSpace(sel.AgentID); v != "" {
				return v
			}
		case "agent_id":
			if v := strings.TrimSpace(sel.AgentId); v != "" {
				return v
			}
		}
	}
	if v := strings.TrimSpace(sel.AgentID); v != "" {
		return v
	}
	if v := strings.TrimSpace(sel.AgentId); v != "" {
		return v
	}
	if v := strings.TrimSpace(sel.ID); v != "" {
		return v
	}
	if v := strings.TrimSpace(sel.Agent); v != "" {
		return v
	}
	return ""
}

func agentRouterOutputKey(defaults *config.Defaults) string {
	if defaults == nil {
		return "agentId"
	}
	if v := strings.TrimSpace(defaults.AgentAutoSelection.OutputKey); v != "" {
		return v
	}
	return "agentId"
}

func agentRouterSystemPrompt(defaults *config.Defaults, outputKey string) string {
	if defaults != nil {
		if v := strings.TrimSpace(defaults.AgentAutoSelection.Prompt); v != "" {
			return v
		}
	}
	return prompts.RouterPrompt(outputKey)
}

func recentNonInterimTurnsText(conv *apiconv.Conversation, lastN int) string {
	if conv == nil || lastN <= 0 || len(conv.Transcript) == 0 {
		return ""
	}
	turns := conv.Transcript
	if len(turns) > lastN {
		turns = turns[len(turns)-lastN:]
	}
	lines := make([]string, 0, lastN*2)
	for _, t := range turns {
		if t == nil || len(t.Message) == 0 {
			continue
		}
		for _, m := range t.Message {
			if m == nil {
				continue
			}
			if m.Interim != 0 {
				continue
			}
			role := strings.ToLower(strings.TrimSpace(m.Role))
			if role != "user" && role != "assistant" {
				continue
			}
			typ := strings.ToLower(strings.TrimSpace(m.Type))
			if typ != "" && typ != "text" {
				continue
			}
			if m.Mode != nil && strings.EqualFold(strings.TrimSpace(*m.Mode), "summary") {
				continue
			}
			content := ""
			if m.Content != nil {
				content = strings.TrimSpace(*m.Content)
			}
			if content == "" && m.RawContent != nil {
				content = strings.TrimSpace(*m.RawContent)
			}
			if content == "" {
				continue
			}
			if idx := strings.IndexByte(content, '\n'); idx >= 0 {
				content = strings.TrimSpace(content[:idx])
			}
			const max = 160
			if len(content) > max {
				content = strings.TrimSpace(content[:max]) + "…"
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", role, content))
		}
	}
	return strings.Join(lines, "\n")
}

func convID(conv *apiconv.Conversation) string {
	if conv == nil {
		return ""
	}
	return strings.TrimSpace(conv.Id)
}

func valueOrDefaultDefaultsModel(s *Service) string {
	if s == nil || s.defaults == nil {
		return ""
	}
	return strings.TrimSpace(s.defaults.AgentAutoSelection.Model)
}

func valueOrDefaultModel(s *Service) string {
	if s == nil || s.defaults == nil {
		return ""
	}
	return strings.TrimSpace(s.defaults.Model)
}
