package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/service/agent/prompts"
	"github.com/viant/agently-core/service/core"
)

type agentSelection struct {
	AgentID string `json:"agentId"`
	AgentId string `json:"agent_id"`
	ID      string `json:"id"`
	Agent   string `json:"agent"`
}

func (s *Service) classifyAgentIDWithLLM(ctx context.Context, conv *apiconv.Conversation, query, preferredTurnID string, candidates []*agentmdl.Agent) (string, error) {
	started := time.Now()
	query = strings.TrimSpace(query)
	candidates = filterAutoSelectableAgents(candidates)
	if query == "" || len(candidates) == 0 || s == nil || s.llm == nil || s.llm.ModelFinder() == nil {
		return "", nil
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
	infof("agent.selector config convo=%q agent_auto_model=%q default_model=%q effective_model=%q candidates=%d", strings.TrimSpace(convID(conv)), strings.TrimSpace(valueOrDefaultDefaultsModel(s)), strings.TrimSpace(valueOrDefaultModel(s)), strings.TrimSpace(modelName), len(candidates))
	if modelName == "" {
		infof("agent.selector skip convo=%q reason=%q", strings.TrimSpace(convID(conv)), "empty_model")
		return "", nil
	}

	model, err := s.llm.ModelFinder().Find(ctx, modelName)
	if err != nil || model == nil {
		infof("agent.selector skip convo=%q reason=%q model=%q err=%v", strings.TrimSpace(convID(conv)), "model_not_found", strings.TrimSpace(modelName), err)
		return "", nil
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
		return "", nil
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
	infof("agent.selector start convo=%q model=%q timeout_sec=%d candidates=%d query_len=%d", strings.TrimSpace(convID), strings.TrimSpace(modelName), timeoutSec, len(candidateLines), len(query))
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
		warnf("agent.selector error convo=%q model=%q elapsed=%s err=%v", strings.TrimSpace(convID), strings.TrimSpace(modelName), time.Since(started), err)
		return "", err
	}
	selected := parseSelectedAgentID(responseForContent(out.Response, out.Content), outputKey)
	if selected == "" {
		debugf("agent.selector done convo=%q model=%q selected=\"\" elapsed=%s", strings.TrimSpace(convID), strings.TrimSpace(modelName), time.Since(started))
		return "", nil
	}
	infof("agent.selector done convo=%q model=%q selected=%q elapsed=%s", strings.TrimSpace(convID), strings.TrimSpace(modelName), strings.TrimSpace(selected), time.Since(started))
	if strings.EqualFold(strings.TrimSpace(selected), "agent_selector") {
		return "agent_selector", nil
	}
	if canonical, ok := candidateByKey[strings.ToLower(selected)]; ok {
		return canonical, nil
	}
	// Allow agents to be referred by name when name differs from ID.
	for key, canonical := range candidateByKey {
		if strings.EqualFold(key, selected) {
			return canonical, nil
		}
	}
	return "", nil
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

func parseSelectedAgentID(resp *llm.GenerateResponse, outputKey string) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return ""
	}
	content = strings.TrimSpace(strings.TrimPrefix(content, "```json"))
	content = strings.TrimSpace(strings.TrimPrefix(content, "```"))
	content = strings.TrimSpace(strings.TrimSuffix(content, "```"))

	var sel agentSelection
	if json.Unmarshal([]byte(content), &sel) == nil {
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
	}
	// Best-effort fallback: treat first token/line as agent id.
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		content = strings.TrimSpace(content[:idx])
	}
	if idx := strings.IndexAny(content, " \t"); idx >= 0 {
		content = strings.TrimSpace(content[:idx])
	}
	return strings.Trim(content, `"'`)
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
