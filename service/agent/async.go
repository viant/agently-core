package agent

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/internal/logx"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/prompt"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/agent/prompts"
)

const (
	asyncMessageActor = "async"
	asyncMessageMode  = "async_wait"
)

func (s *Service) injectAsyncReinforcement(ctx context.Context, turn *runtimerequestctx.TurnMeta) {
	if s == nil || s.asyncManager == nil || turn == nil {
		return
	}
	changed := s.asyncManager.ConsumeChanged(turn.ConversationID, turn.TurnID)
	if len(changed) == 0 {
		changed = s.asyncManager.ActiveWaitOps(ctx, turn.ConversationID, turn.TurnID)
	}
	s.injectAsyncReinforcementForRecords(ctx, turn, changed)
}

func (s *Service) injectAsyncReinforcementForRecords(ctx context.Context, turn *runtimerequestctx.TurnMeta, records []*asynccfg.OperationRecord) {
	if s == nil || turn == nil || len(records) == 0 {
		return
	}
	for _, rec := range records {
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
		rec.Reinforcement = &asynccfg.PromptConfig{
			Text:   text,
			Engine: "go",
		}
	}

	p := rec.Reinforcement
	if p == nil {
		p = &asynccfg.PromptConfig{
			Text:   prompts.AsyncReinforcement,
			Engine: "go",
		}
	}
	turnAsync, requestGroup := s.buildAsyncPromptContext(ctx, rec)
	binding := &prompt.Binding{
		Context: map[string]interface{}{
			"operation": map[string]interface{}{
				"id":                  strings.TrimSpace(rec.ID),
				"toolName":            strings.TrimSpace(rec.ToolName),
				"statusToolName":      strings.TrimSpace(rec.StatusToolName),
				"statusToolArgs":      rec.StatusArgs,
				"statusToolArgsJSON":  mustJSONText(rec.StatusArgs),
				"cancelToolName":      strings.TrimSpace(rec.CancelToolName),
				"status":              firstNonEmptyAsyncValue(strings.TrimSpace(rec.Status), strings.TrimSpace(string(rec.State))),
				"state":               strings.TrimSpace(string(rec.State)),
				"message":             strings.TrimSpace(rec.Message),
				"error":               strings.TrimSpace(rec.Error),
				"waitForResponse":     rec.WaitForResponse,
				"terminal":            rec.Terminal(),
				"timeoutMs":           rec.TimeoutMs,
				"pollIntervalMs":      rec.PollIntervalMs,
				"percent":             rec.Percent,
				"response":            rec.KeyData,
				"responseJSON":        rawJSONText(rec.KeyData),
				"requestArgs":         rec.RequestArgs,
				"requestArgsJSON":     mustJSONText(rec.RequestArgs),
				"turnPending":         turnAsync["pending"],
				"turnAllResolved":     turnAsync["allResolved"],
				"turnAllCompleted":    turnAsync["allCompleted"],
				"pendingRequestsJSON": mustJSONText(turnAsync["pendingRequests"]),
				"turnpending":         turnAsync["pending"],
				"turnallresolved":     turnAsync["allResolved"],
				"turnallcompleted":    turnAsync["allCompleted"],
				"pendingrequestsjson": mustJSONText(turnAsync["pendingRequests"]),
			},
			"tool": map[string]interface{}{
				"name":        strings.TrimSpace(rec.ToolName),
				"displayName": strings.TrimSpace(rec.ToolName),
			},
			"async": map[string]interface{}{
				"timeoutMs":      rec.TimeoutMs,
				"pollIntervalMs": rec.PollIntervalMs,
			},
			"turnAsync":    turnAsync,
			"requestGroup": requestGroup,
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
		logx.Warnf("conversation", "agent.async reinforcement prompt render failed op_id=%q tool=%q err=%v", strings.TrimSpace(rec.ID), strings.TrimSpace(rec.ToolName), err)
	} else {
		logx.Warnf("conversation", "agent.async reinforcement prompt rendered empty op_id=%q tool=%q", strings.TrimSpace(rec.ID), strings.TrimSpace(rec.ToolName))
	}
	return ""
}

func (s *Service) buildAsyncPromptContext(ctx context.Context, rec *asynccfg.OperationRecord) (map[string]interface{}, map[string]interface{}) {
	turnAsync := map[string]interface{}{
		"total":        0,
		"active":       0,
		"pending":      0,
		"completed":    0,
		"failed":       0,
		"canceled":     0,
		"allResolved":  true,
		"allCompleted": true,
	}
	requestGroup := map[string]interface{}{
		"toolName":        strings.TrimSpace(rec.ToolName),
		"requestArgs":     rec.RequestArgs,
		"requestArgsJSON": mustJSONText(rec.RequestArgs),
		"total":           0,
		"active":          0,
		"pending":         0,
		"completed":       0,
		"failed":          0,
		"canceled":        0,
		"allResolved":     true,
		"allCompleted":    true,
	}
	if s == nil || s.asyncManager == nil || rec == nil {
		return turnAsync, requestGroup
	}
	ops := s.asyncManager.OperationsForTurn(ctx, rec.ParentConvID, rec.ParentTurnID)
	turnPendingRequests := make([]map[string]interface{}, 0)
	groupPendingRequests := make([]map[string]interface{}, 0)
	toolName := strings.TrimSpace(rec.ToolName)
	requestDigest := strings.TrimSpace(rec.RequestArgsDigest)
	for _, item := range ops {
		if item == nil || !item.WaitForResponse {
			continue
		}
		turnAsync["total"] = turnAsync["total"].(int) + 1
		countAsyncState(turnAsync, item)
		if matchesRequestGroup(item, toolName, requestDigest) {
			requestGroup["total"] = requestGroup["total"].(int) + 1
			countAsyncState(requestGroup, item)
		}
		if !item.Terminal() {
			entry := map[string]interface{}{
				"id":              strings.TrimSpace(item.ID),
				"toolName":        strings.TrimSpace(item.ToolName),
				"status":          strings.TrimSpace(item.Status),
				"state":           strings.TrimSpace(string(item.State)),
				"requestArgs":     item.RequestArgs,
				"requestArgsJSON": mustJSONText(item.RequestArgs),
			}
			turnPendingRequests = append(turnPendingRequests, entry)
			if matchesRequestGroup(item, toolName, requestDigest) {
				groupPendingRequests = append(groupPendingRequests, entry)
			}
		}
	}
	turnAsync["pendingRequests"] = turnPendingRequests
	requestGroup["pendingRequests"] = groupPendingRequests
	return turnAsync, requestGroup
}

func countAsyncState(target map[string]interface{}, rec *asynccfg.OperationRecord) {
	if target == nil || rec == nil {
		return
	}
	if rec.Terminal() {
		switch rec.State {
		case asynccfg.StateCompleted:
			target["completed"] = target["completed"].(int) + 1
		case asynccfg.StateFailed:
			target["failed"] = target["failed"].(int) + 1
			target["allCompleted"] = false
		case asynccfg.StateCanceled:
			target["canceled"] = target["canceled"].(int) + 1
			target["allCompleted"] = false
		default:
			target["allCompleted"] = false
		}
		return
	}
	target["active"] = target["active"].(int) + 1
	target["pending"] = target["pending"].(int) + 1
	target["allResolved"] = false
	target["allCompleted"] = false
}

func matchesRequestGroup(rec *asynccfg.OperationRecord, toolName, requestDigest string) bool {
	if rec == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(rec.ToolName), toolName) {
		return false
	}
	if requestDigest == "" {
		return strings.TrimSpace(rec.RequestArgsDigest) == ""
	}
	return strings.TrimSpace(rec.RequestArgsDigest) == requestDigest
}

func firstNonEmptyAsyncValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func mustJSONText(value interface{}) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func rawJSONText(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}
	return string(value)
}
