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
}

func computeStage(c *ConversationView) string {
	if c == nil || len(c.Transcript) == 0 {
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

	compacting := c.Status != nil && *c.Status == "compacting"
	if c.Status != nil && strings.EqualFold(strings.TrimSpace(*c.Status), "canceled") {
		return StageCanceled
	}

	for ti := len(c.Transcript) - 1; ti >= 0; ti-- {
		t := c.Transcript[ti]
		if t == nil || len(t.Message) == 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(t.Status), "canceled") {
			return StageCanceled
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
