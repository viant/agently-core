package read

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	StageWaiting   = "waiting"
	StageThinking  = "thinking"
	StageExecuting = "executing"
	StageEliciting = "elicitation"
	StageCanceled  = "canceled"
	StageDone      = "done"
	StageError     = "error"

	StatusSucceeded      = "succeeded"
	StatusRunning        = "running"
	StatusWaitingForUser = "waiting_for_user"
	StatusFailed         = "failed"
	StatusCanceled       = "canceled"
)

// OnRelation sorts turns and computes the effective conversation stage.
func (c *ConversationView) OnRelation(ctx context.Context) {
	if c.ListMode {
		return
	}
	for _, t := range c.Transcript {
		if t == nil {
			continue
		}
		t.OnRelation(ctx)
	}
	sort.SliceStable(c.Transcript, func(i, j int) bool {
		mi, mj := c.Transcript[i], c.Transcript[j]
		if mi == nil || mj == nil {
			return mj == nil && mi != nil
		}
		if mi.CreatedAt.Equal(mj.CreatedAt) {
			return mi.Id < mj.Id
		}
		return mi.CreatedAt.Before(mj.CreatedAt)
	})

	c.Stage = computeStage(c)
	if c.Status != nil && isTerminalStatus(strings.TrimSpace(*c.Status)) {
		return
	}
	if status := normalizeConversationStatusFromStage(c.Stage, c.Transcript); status != "" {
		c.Status = &status
	}
}

// Read mode is trusted host metadata. A get must still identify one row even
// though the canonical contract also accepts ID-free list invocations.
func (input *ConversationInput) Init(context.Context) error {
	if !input.ListMode && !input.GraphMode && (input.Has == nil || !input.Has.Id || strings.TrimSpace(input.Id) == "") {
		return fmt.Errorf("conversation ID is required for get")
	}
	return nil
}

func normalizeConversationStatusFromStage(stage string, transcript []*TranscriptView) string {
	if len(transcript) > 0 {
		for i := len(transcript) - 1; i >= 0; i-- {
			t := transcript[i]
			if t == nil {
				continue
			}
			if v := strings.TrimSpace(t.Status); v != "" {
				switch strings.ToLower(v) {
				case "completed", "success", "done":
					return StatusSucceeded
				default:
					return v
				}
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case StageDone:
		return StatusSucceeded
	case StageExecuting:
		return StatusRunning
	case StageEliciting:
		return StatusWaitingForUser
	case StageError:
		return StatusFailed
	case StageCanceled:
		return StatusCanceled
	default:
		return ""
	}
}

func computeStage(c *ConversationView) string {
	if c == nil || len(c.Transcript) == 0 {
		return StageWaiting
	}

	latestStatus := latestTurnStatus(c.Transcript)
	convStatus := ""
	if c.Status != nil {
		convStatus = strings.TrimSpace(*c.Status)
	}
	if stage, ok := preferredExplicitConversationStage(convStatus, latestStatus); ok {
		return stage
	}

	lastRole := ""
	lastAssistantElic := false
	lastAssistantElicStopped := false
	lastPendingElic := false
	lastToolRunning := false
	lastToolFailed := false
	lastModelRunning := false
	lastModelFailed := false
	lastAssistantCanceled := false

	compacting := c.Status != nil && *c.Status == "compacting"

	for ti := len(c.Transcript) - 1; ti >= 0; ti-- {
		t := c.Transcript[ti]
		if t == nil {
			continue
		}
		if stage, ok := stageFromExplicitStatus(strings.TrimSpace(t.Status)); ok {
			return stage
		}
		if len(t.Message) == 0 {
			continue
		}
		for mi := len(t.Message) - 1; mi >= 0; mi-- {
			m := t.Message[mi]
			if m == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(m.Role), "assistant") && m.Status != nil && strings.EqualFold(strings.TrimSpace(*m.Status), "canceled") {
				lastAssistantCanceled = true
				goto DONE
			}

			if m.ModelCall != nil {
				mstatus := strings.ToLower(strings.TrimSpace(m.ModelCall.Status))
				if mstatus == "failed" {
					lastModelFailed = true
					goto DONE
				}
			}

			if m.Interim != 0 && !compacting {
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
			if m.ElicitationId != nil && strings.TrimSpace(*m.ElicitationId) != "" {
				msgStatus := ""
				if m.Status != nil {
					msgStatus = strings.ToLower(strings.TrimSpace(*m.Status))
				}
				if msgStatus == "" || msgStatus == "pending" || msgStatus == "open" {
					lastPendingElic = true
					if r == "assistant" {
						lastAssistantElic = true
					}
				}
				if msgStatus == "rejected" || msgStatus == "cancel" || msgStatus == "failed" {
					lastAssistantElicStopped = true
				}
			}
			goto DONE
		}
	}

DONE:
	if lastModelFailed {
		return StageError
	}
	if lastAssistantElicStopped {
		return StageError
	}

	switch {
	case lastAssistantCanceled:
		return StageCanceled
	case lastPendingElic:
		return StageEliciting
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

func latestTurnStatus(transcript []*TranscriptView) string {
	for i := len(transcript) - 1; i >= 0; i-- {
		t := transcript[i]
		if t == nil {
			continue
		}
		if status := strings.TrimSpace(t.Status); status != "" {
			return status
		}
	}
	return ""
}

func preferredExplicitConversationStage(conversationStatus, latestTurnStatus string) (string, bool) {
	convStatus := strings.ToLower(strings.TrimSpace(conversationStatus))
	turnStatus := strings.ToLower(strings.TrimSpace(latestTurnStatus))

	convStage, convOK := stageFromExplicitStatus(convStatus)
	turnStage, turnOK := stageFromExplicitStatus(turnStatus)

	convTerminal := isTerminalStatus(convStatus)
	turnTerminal := isTerminalStatus(turnStatus)

	switch {
	case convTerminal && turnTerminal:
		return convStage, convOK
	case convTerminal:
		return convStage, convOK
	case turnTerminal:
		return turnStage, turnOK
	case turnOK:
		return turnStage, true
	case convOK:
		return convStage, true
	default:
		return "", false
	}
}

func isTerminalStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "completed", "complete", "success", "done", "ok", "failed", "error", "canceled", "cancelled":
		return true
	default:
		return false
	}
}

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
		attachElicitationStatus(m)
		if isElicitationMessage(m) {
			continue
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

func isElicitationMessage(m *MessageView) bool {
	return m != nil && m.ElicitationId != nil && strings.TrimSpace(*m.ElicitationId) != ""
}

func attachElicitationStatus(m *MessageView) {
	if m == nil || len(m.Elicitation) == 0 || m.Status == nil {
		return
	}
	status := strings.TrimSpace(*m.Status)
	if status == "" {
		return
	}
	m.Elicitation["status"] = status
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
	if stage, ok := stageFromExplicitStatus(strings.TrimSpace(t.Status)); ok {
		return stage
	}
	if len(t.Message) == 0 {
		return StageWaiting
	}

	lastRole := ""
	lastAssistantElic := false
	lastAssistantElicStopped := false
	lastPendingElic := false
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
		if m.ElicitationId != nil && strings.TrimSpace(*m.ElicitationId) != "" {
			msgStatus := ""
			if m.Status != nil {
				msgStatus = strings.ToLower(strings.TrimSpace(*m.Status))
			}
			if msgStatus == "" || msgStatus == "pending" || msgStatus == "open" {
				lastPendingElic = true
				if r == "assistant" {
					lastAssistantElic = true
				}
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
	case lastPendingElic:
		return StageEliciting
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

func stageFromExplicitStatus(status string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "completed", "complete", "success", "done", "ok":
		return StageDone, true
	case "failed", "error":
		return StageError, true
	case "canceled", "cancelled":
		return StageCanceled, true
	case "waiting_for_user", "pending", "open":
		return StageEliciting, true
	case "running", "thinking", "processing", "streaming", "in_progress":
		return StageThinking, true
	}
	return "", false
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
