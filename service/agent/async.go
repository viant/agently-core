package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/logx"
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

func asyncModelManaged(rec *asynccfg.OperationRecord) bool {
	if rec == nil || rec.Terminal() {
		return false
	}
	statusTool := strings.TrimSpace(rec.StatusToolName)
	toolName := strings.TrimSpace(rec.ToolName)
	sameToolReuse := statusTool != "" && strings.EqualFold(statusTool, toolName)
	return sameToolReuse || statusTool != ""
}

func (s *Service) activeModelManagedAsyncRecords(ctx context.Context, turn *runtimerequestctx.TurnMeta) []*asynccfg.OperationRecord {
	if s == nil || s.asyncManager == nil || turn == nil {
		return nil
	}
	var result []*asynccfg.OperationRecord
	for _, rec := range s.asyncManager.OperationsForTurn(ctx, turn.ConversationID, turn.TurnID) {
		if rec == nil || !asyncModelManaged(rec) {
			continue
		}
		if !rec.WaitForResponse {
			result = append(result, rec)
		}
	}
	return result
}

func (s *Service) renderModelManagedAsyncControl(ctx context.Context, turn *runtimerequestctx.TurnMeta) string {
	records := s.activeModelManagedAsyncRecords(ctx, turn)
	if len(records) == 0 {
		return ""
	}
	return s.renderBatchedAsyncReinforcement(ctx, records)
}

// injectAsyncReinforcementForRecords emits a single batched system message
// covering all eligible changed operations. All records use the centralized
// shared template; per-operation prompt overrides are not supported.
func (s *Service) injectAsyncReinforcementForRecords(ctx context.Context, turn *runtimerequestctx.TurnMeta, records []*asynccfg.OperationRecord) {
	if s == nil || turn == nil || len(records) == 0 {
		return
	}
	var eligible []*asynccfg.OperationRecord
	for _, rec := range records {
		if rec == nil {
			continue
		}
		if !rec.WaitForResponse {
			continue
		}
		if _, ok := s.asyncManager.TryRecordReinforcement(ctx, rec.ID); !ok {
			continue
		}
		eligible = append(eligible, rec)
	}
	if len(eligible) == 0 {
		return
	}
	content := s.renderBatchedAsyncReinforcement(ctx, eligible)
	if strings.TrimSpace(content) == "" {
		return
	}
	_, _ = s.addMessage(ctx, turn, string(llm.RoleSystem), asyncMessageActor, content, nil, asyncMessageMode, "")
}

// renderBatchedAsyncReinforcement renders one turn-level reinforcement message
// for all eligible changed operations using the centralized prompt.
func (s *Service) renderBatchedAsyncReinforcement(ctx context.Context, records []*asynccfg.OperationRecord) string {
	if len(records) == 0 {
		return ""
	}
	pp := s.resolveAsyncReinforcementPrompt()
	binding := &prompt.Binding{
		Context: s.buildBatchedAsyncContext(ctx, records),
	}
	rendered, err := pp.Generate(ctx, binding)
	if err == nil && strings.TrimSpace(rendered) != "" {
		return strings.TrimSpace(rendered)
	}
	if err != nil {
		logx.Warnf("conversation", "agent.async reinforcement render failed err=%v", err)
	}
	return ""
}

// resolveAsyncReinforcementPrompt returns the workspace/defaults-configured
// prompt when set, falling back to the embedded default.
func (s *Service) resolveAsyncReinforcementPrompt() *prompt.Prompt {
	if s != nil && s.defaults != nil && s.defaults.AsyncReinforcementPrompt != nil {
		p := *s.defaults.AsyncReinforcementPrompt
		return &p
	}
	return &prompt.Prompt{
		Text:   prompts.AsyncReinforcement,
		Engine: "go",
	}
}

// buildBatchedAsyncContext builds the template context for the centralized
// reinforcement template: turn-level counts plus a minimal per-operation
// control-plane view (no raw payloads, no status tool args for runtime-polled ops).
func (s *Service) buildBatchedAsyncContext(ctx context.Context, records []*asynccfg.OperationRecord) map[string]interface{} {
	turnAsync := map[string]interface{}{
		"total": 0, "active": 0, "pending": 0,
		"completed": 0, "failed": 0, "canceled": 0,
		"allResolved": true, "allCompleted": true,
	}
	if s != nil && s.asyncManager != nil && len(records) > 0 {
		if first := records[0]; first != nil {
			for _, op := range s.asyncManager.OperationsForTurn(ctx, first.ParentConvID, first.ParentTurnID) {
				if op == nil {
					continue
				}
				if !op.WaitForResponse && !asyncModelManaged(op) {
					continue
				}
				turnAsync["total"] = turnAsync["total"].(int) + 1
				countAsyncState(turnAsync, op)
			}
		}
	}

	changedOps := make([]map[string]interface{}, 0, len(records))
	hasSameToolReuse := false
	hasExplicitStatusTool := false
	for _, rec := range records {
		if rec == nil {
			continue
		}
		statusTool := strings.TrimSpace(rec.StatusToolName)
		toolName := strings.TrimSpace(rec.ToolName)

		// sameToolReuse: status tool == run tool → model-mediated polling.
		sameToolReuse := statusTool != "" && strings.EqualFold(statusTool, toolName)
		// runtimePolled: only wait-managed distinct status tools are owned by the runtime.
		runtimePolled := rec.WaitForResponse && statusTool != "" && !sameToolReuse

		if !rec.Terminal() && sameToolReuse {
			hasSameToolReuse = true
		}
		if !rec.Terminal() && !runtimePolled && !sameToolReuse && statusTool != "" {
			hasExplicitStatusTool = true
		}

		op := map[string]interface{}{
			"id":            strings.TrimSpace(rec.ID),
			"toolName":      toolName,
			"status":        firstNonEmptyAsyncValue(strings.TrimSpace(rec.Status), strings.TrimSpace(string(rec.State))),
			"terminal":      rec.Terminal(),
			"sameToolReuse": sameToolReuse,
			"runtimePolled": runtimePolled,
		}
		if sameToolReuse {
			op["requestArgsJSON"] = mustJSONText(rec.RequestArgs)
		} else if !runtimePolled && statusTool != "" {
			op["statusToolName"] = statusTool
			op["statusToolArgsJSON"] = mustJSONText(rec.StatusArgs)
		}
		if msg := strings.TrimSpace(rec.Message); msg != "" {
			op["message"] = msg
		}
		if errMsg := strings.TrimSpace(rec.Error); errMsg != "" && rec.Terminal() {
			op["error"] = errMsg
		}
		if inst := strings.TrimSpace(rec.Instruction); inst != "" && !rec.Terminal() {
			op["instruction"] = inst
		}
		if termInst := strings.TrimSpace(rec.TerminalInstruction); termInst != "" && rec.Terminal() {
			op["terminalInstruction"] = termInst
		}
		changedOps = append(changedOps, op)
	}

	turnAsync["hasSameToolReuse"] = hasSameToolReuse
	turnAsync["hasExplicitStatusTool"] = hasExplicitStatusTool

	return map[string]interface{}{
		"turnAsync":         turnAsync,
		"changedOperations": changedOps,
	}
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

func firstNonEmptyAsyncValue(values ...string) string {
	for _, v := range values {
		if t := strings.TrimSpace(v); t != "" {
			return t
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
