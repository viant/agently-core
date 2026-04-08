package agent

import (
	"context"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/service/agent/prompts"
)

const (
	asyncMessageActor = "async"
	asyncMessageMode  = "async_wait"
)

func (s *Service) injectAsyncReinforcement(ctx context.Context, turn *memory.TurnMeta) {
	if s == nil || s.asyncManager == nil || turn == nil {
		return
	}
	changed := s.asyncManager.ConsumeChanged(turn.ConversationID, turn.TurnID)
	for _, rec := range changed {
		if rec == nil {
			continue
		}
		if _, ok := s.asyncManager.TryRecordReinforcement(ctx, rec.ID); !ok {
			continue
		}
		content := s.renderAsyncReinforcement(ctx, rec)
		if strings.TrimSpace(content) == "" {
			continue
		}
		_, _ = s.addMessage(ctx, turn, string(llm.RoleSystem), asyncMessageActor, content, nil, asyncMessageMode, "")
	}
}

func (s *Service) renderAsyncReinforcement(ctx context.Context, rec *asynccfg.OperationRecord) string {
	if rec == nil {
		return ""
	}
	if text := strings.TrimSpace(rec.ReinforcementPrompt); text != "" {
		return text
	}

	p := rec.Reinforcement
	if p == nil {
		p = &asynccfg.PromptConfig{
			Text:   prompts.AsyncReinforcement,
			Engine: "go",
		}
	}
	binding := &prompt.Binding{
		Context: map[string]interface{}{
			"operation": map[string]interface{}{
				"id":              strings.TrimSpace(rec.ID),
				"toolName":        strings.TrimSpace(rec.ToolName),
				"status":          firstNonEmptyAsyncValue(strings.TrimSpace(rec.Status), strings.TrimSpace(string(rec.State))),
				"state":           strings.TrimSpace(string(rec.State)),
				"message":         strings.TrimSpace(rec.Message),
				"error":           strings.TrimSpace(rec.Error),
				"waitForResponse": rec.WaitForResponse,
				"terminal":        rec.Terminal(),
				"timeoutMs":       rec.TimeoutMs,
				"pollIntervalMs":  rec.PollIntervalMs,
				"percent":         rec.Percent,
				"response":        rec.KeyData,
			},
			"tool": map[string]interface{}{
				"name":        strings.TrimSpace(rec.ToolName),
				"displayName": strings.TrimSpace(rec.ToolName),
			},
			"async": map[string]interface{}{
				"timeoutMs":      rec.TimeoutMs,
				"pollIntervalMs": rec.PollIntervalMs,
			},
		},
	}
	pp := &prompt.Prompt{
		Text:   p.Text,
		URI:    p.URI,
		Engine: p.Engine,
	}
	rendered, err := pp.Generate(ctx, binding)
	if err == nil && strings.TrimSpace(rendered) != "" {
		return strings.TrimSpace(rendered)
	}
	if err != nil {
		warnf("agent.async reinforcement prompt render failed op_id=%q tool=%q err=%v", strings.TrimSpace(rec.ID), strings.TrimSpace(rec.ToolName), err)
	} else {
		warnf("agent.async reinforcement prompt rendered empty op_id=%q tool=%q", strings.TrimSpace(rec.ID), strings.TrimSpace(rec.ToolName))
	}
	return ""
}

func firstNonEmptyAsyncValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
