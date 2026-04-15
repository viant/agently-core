package conversation

import (
	"context"
	"sort"
	"strings"
)

// OnRelation sorts turns and computes the effective conversation stage.
func (c *ConversationView) OnRelation(ctx context.Context) {
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
