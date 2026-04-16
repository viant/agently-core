package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	promptdef "github.com/viant/agently-core/protocol/prompt"
)

// expansionMetaPrompt is the fixed system instruction sent to the sidecar LLM.
// It is hardcoded here — agents cannot override it.
const expansionMetaPrompt = `You are a prompt refinement assistant.
You receive a set of scenario instructions and a specific user objective.
Your task is to refine the instructions so they are tightly scoped to the objective,
without adding tool names, data access, or hallucinated specifics.

Rules:
- Preserve the role structure exactly (system stays system, user stays user, assistant stays assistant).
- Do not add tool names or capability lists.
- Do not invent entities, dates, or constraints not present in the objective.
- Keep each message focused and concise.
- Return ONLY a JSON array of {"role":"...","text":"..."} objects. No prose, no markdown fences.`

// expandMessages calls a sidecar LLM to synthesize task-specific instructions
// from generic profile messages + the user objective.
//
// The expansion is skipped (original messages returned) when:
//   - s.modelFinder is nil
//   - cfg.Model is empty
//   - the LLM call fails (non-fatal: original messages are used as fallback)
//
// Output is validated: role structure must match input and total text is
// bounded by cfg.MaxTokens (estimated as characters / 4).
func (s *Service) expandMessages(ctx context.Context, msgs []promptdef.Message, objective string, cfg *promptdef.Expansion) []promptdef.Message {
	if s.modelFinder == nil || cfg == nil || strings.TrimSpace(cfg.Model) == "" || len(msgs) == 0 {
		return msgs
	}
	expanded, err := s.callExpansionSidecar(ctx, msgs, objective, cfg)
	if err != nil {
		// Non-fatal: fall back to original messages.
		return msgs
	}
	if !validExpansionOutput(expanded, msgs) {
		return msgs
	}
	return expanded
}

// callExpansionSidecar performs the actual LLM call.
func (s *Service) callExpansionSidecar(ctx context.Context, msgs []promptdef.Message, objective string, cfg *promptdef.Expansion) ([]promptdef.Message, error) {
	model, err := s.modelFinder.Find(ctx, strings.TrimSpace(cfg.Model))
	if err != nil {
		return nil, fmt.Errorf("expansion sidecar: find model %q: %w", cfg.Model, err)
	}

	// Build the request messages:
	//   1. Each profile message in its authored role.
	//   2. A final user message with the specific objective.
	var reqMsgs []llm.Message
	for _, m := range msgs {
		reqMsgs = append(reqMsgs, llm.Message{
			Role:    llm.MessageRole(strings.ToLower(strings.TrimSpace(m.Role))),
			Content: strings.TrimSpace(m.Text),
		})
	}
	reqMsgs = append(reqMsgs, llm.Message{
		Role:    llm.RoleUser,
		Content: buildExpansionUserPrompt(objective),
	})

	opts := &llm.Options{}
	if cfg.MaxTokens > 0 {
		opts.MaxTokens = cfg.MaxTokens
	}

	resp, err := model.Generate(ctx, &llm.GenerateRequest{
		Instructions: expansionMetaPrompt,
		Messages:     reqMsgs,
		Options:      opts,
	})
	if err != nil {
		return nil, fmt.Errorf("expansion sidecar: generate: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		return nil, fmt.Errorf("expansion sidecar: empty response")
	}

	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	if raw == "" {
		return nil, fmt.Errorf("expansion sidecar: empty content")
	}
	return parseExpansionOutput(raw)
}

// buildExpansionUserPrompt formats the refinement request sent as the final user message.
func buildExpansionUserPrompt(objective string) string {
	var b strings.Builder
	b.WriteString("Refine the above instructions for the following specific objective:\n\n")
	b.WriteString(strings.TrimSpace(objective))
	b.WriteString("\n\nReturn the refined messages as a JSON array of {\"role\":\"...\",\"text\":\"...\"} objects.")
	return b.String()
}

// parseExpansionOutput parses the JSON array returned by the sidecar.
// Tolerates a markdown code fence wrapper if present.
func parseExpansionOutput(raw string) ([]promptdef.Message, error) {
	raw = stripMarkdownFence(raw)
	var out []promptdef.Message
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		// Try a lenient parse: look for the first '[' and last ']'.
		if start := strings.Index(raw, "["); start >= 0 {
			if end := strings.LastIndex(raw, "]"); end > start {
				if err2 := json.Unmarshal([]byte(raw[start:end+1]), &out); err2 == nil {
					return out, nil
				}
			}
		}
		return nil, fmt.Errorf("expansion sidecar: parse output: %w", err)
	}
	return out, nil
}

// validExpansionOutput checks that the sidecar output is safe to use:
//   - same number of messages as the input
//   - same role sequence
//   - no message has empty text
//   - estimated token count within MaxTokens (characters / 4 heuristic)
func validExpansionOutput(out, original []promptdef.Message) bool {
	if len(out) != len(original) {
		return false
	}
	for i, m := range out {
		if strings.ToLower(strings.TrimSpace(m.Role)) != strings.ToLower(strings.TrimSpace(original[i].Role)) {
			return false
		}
		if strings.TrimSpace(m.Text) == "" {
			return false
		}
	}
	return true
}

// stripMarkdownFence removes ```json ... ``` or ``` ... ``` wrappers.
func stripMarkdownFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.Index(s, "\n"); nl >= 0 {
			s = s[nl+1:]
		}
		if strings.HasSuffix(s, "```") {
			s = s[:len(s)-3]
		}
		s = strings.TrimSpace(s)
	}
	return s
}
