package conversation

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// OnRelation normalizes transcript messages and computes the turn stage.
func (t *TranscriptView) OnRelation(ctx context.Context) {
	_ = ctx
	if len(t.Message) > 0 {
		t.normalizeMessages()
	}
	t.Stage = computeTurnStage(t)
}

func (t *TranscriptView) normalizeMessages() {
	sort.SliceStable(t.Message, func(i, j int) bool {
		mi, mj := t.Message[i], t.Message[j]
		if mi == nil || mj == nil {
			return mj == nil && mi != nil
		}
		if mi.CreatedAt.Equal(mj.CreatedAt) {
			miTool := isToolMessage(mi)
			mjTool := isToolMessage(mj)
			if miTool != mjTool {
				return miTool
			}
			if mi.Sequence != nil && mj.Sequence != nil {
				return *mi.Sequence < *mj.Sequence
			}
			return mi.Id < mj.Id
		}
		return mi.CreatedAt.Before(mj.CreatedAt)
	})

	minTime := t.Message[0].CreatedAt
	maxTime := t.Message[len(t.Message)-1].CreatedAt
	t.ElapsedInSec = int(maxTime.Sub(minTime).Seconds())

	for _, m := range t.Message {
		if m == nil {
			continue
		}
		if len(m.Elicitation) == 0 {
			m.Elicitation = buildElicitationMap(m)
		}
		if m.ModelCall != nil {
			m.Status = &m.ModelCall.Status
		}
		if status := latestToolStatusPtr(m); status != nil {
			m.Status = status
		}
		if m.LinkedConversation != nil {
			m.Status = m.LinkedConversation.Status
		}
	}
}

func buildElicitationMap(m *MessageView) map[string]interface{} {
	if m == nil || m.ElicitationId == nil || strings.TrimSpace(*m.ElicitationId) == "" {
		return nil
	}
	elicitationID := strings.TrimSpace(*m.ElicitationId)
	if m.UserElicitationData != nil && m.UserElicitationData.InlineBody != nil {
		inline := strings.TrimSpace(*m.UserElicitationData.InlineBody)
		if inline != "" {
			if out := parseElicitationMap(inline, elicitationID, valueOrEmpty(m.Content)); len(out) > 0 {
				return out
			}
		}
	}
	return parseElicitationMap(valueOrEmpty(m.Content), elicitationID, valueOrEmpty(m.Content))
}

func parseElicitationMap(raw, elicitationID, fallbackMessage string) map[string]interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out) == 0 {
		return nil
	}
	out["elicitationId"] = elicitationID
	if msg := strings.TrimSpace(fallbackMessage); msg != "" {
		if _, ok := out["message"]; !ok {
			out["message"] = msg
		}
	}
	return out
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func computeTurnStage(t *TranscriptView) string {
	if t == nil {
		return StageWaiting
	}
	if strings.EqualFold(strings.TrimSpace(t.Status), "canceled") {
		return StageCanceled
	}
	if strings.EqualFold(strings.TrimSpace(t.Status), "failed") || strings.EqualFold(strings.TrimSpace(t.Status), "error") {
		return StageError
	}
	if len(t.Message) == 0 {
		return StageWaiting
	}

	lastRole := ""
	lastAssistantElic := false
	lastAssistantElicStopped := false
	lastToolRunning := false
	lastToolFailed := false
	lastModelRunning := false
	lastModelFailed := false
	lastAssistantCanceled := false

	for i := len(t.Message) - 1; i >= 0; i-- {
		m := t.Message[i]
		if m == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(m.Role), "assistant") && m.Status != nil && strings.EqualFold(strings.TrimSpace(*m.Status), "canceled") {
			lastAssistantCanceled = true
			break
		}

		if m.ModelCall != nil {
			mstatus := strings.ToLower(strings.TrimSpace(m.ModelCall.Status))
			if mstatus == "failed" {
				lastModelFailed = true
				break
			}
		}

		if m.Interim != 0 {
			continue
		}

		r := strings.ToLower(strings.TrimSpace(m.Role))
		if lastRole == "" {
			lastRole = r
		}

		if status, completed := latestToolStatus(m); status != "" {
			if status == "running" || !completed {
				lastToolRunning = true
			}
			if status == "failed" {
				lastToolFailed = true
			}
		}
		if m.ModelCall != nil {
			mstatus := strings.ToLower(strings.TrimSpace(m.ModelCall.Status))
			if mstatus == "running" || m.ModelCall.CompletedAt == nil {
				lastModelRunning = true
			}
		}
		if r == "assistant" && m.ElicitationId != nil && strings.TrimSpace(*m.ElicitationId) != "" {
			msgStatus := ""
			if m.Status != nil {
				msgStatus = strings.ToLower(strings.TrimSpace(*m.Status))
			}
			if msgStatus == "" || msgStatus == "pending" || msgStatus == "open" {
				lastAssistantElic = true
			}
			if msgStatus == "rejected" || msgStatus == "cancel" || msgStatus == "failed" {
				lastAssistantElicStopped = true
			}
		}
		break
	}

	if lastModelFailed {
		return StageError
	}
	if lastAssistantElicStopped {
		return StageError
	}

	switch {
	case lastAssistantCanceled:
		return StageCanceled
	case lastToolRunning:
		return StageExecuting
	case lastAssistantElic:
		return StageEliciting
	case lastModelRunning:
		return StageThinking
	case lastRole == "user":
		return StageThinking
	case lastToolFailed:
		return StageError
	default:
		return StageDone
	}
}

func isToolMessage(m *MessageView) bool {
	if m == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(m.Type), "tool_op") || strings.EqualFold(strings.TrimSpace(m.Role), "tool") {
		return true
	}
	return len(m.ToolMessage) > 0
}

func latestToolStatusPtr(m *MessageView) *string {
	if m == nil {
		return nil
	}
	status, _ := latestToolStatus(m)
	if status == "" {
		return nil
	}
	out := status
	return &out
}

func latestToolStatus(m *MessageView) (string, bool) {
	if m == nil {
		return "", false
	}

	var latest *ToolMessageView
	for _, tm := range m.ToolMessage {
		if tm == nil || tm.ToolCall == nil {
			continue
		}
		if latest == nil || tm.CreatedAt.After(latest.CreatedAt) {
			latest = tm
		}
	}
	if latest != nil && latest.ToolCall != nil {
		status := strings.ToLower(strings.TrimSpace(latest.ToolCall.Status))
		return status, latest.ToolCall.CompletedAt != nil
	}

	if isToolMessage(m) && m.Status != nil {
		status := strings.ToLower(strings.TrimSpace(*m.Status))
		return status, status != "running"
	}
	return "", false
}
