package sdk

import (
	"strings"
	"time"

	"github.com/viant/agently-core/runtime/streaming"
)

func terminalStatusForEventType(t streaming.EventType) TurnStatus {
	switch t {
	case streaming.EventTypeTurnFailed:
		return TurnStatusFailed
	case streaming.EventTypeTurnCanceled:
		return TurnStatusCanceled
	default:
		return TurnStatusCompleted
	}
}

func turnStatusFromString(status string, fallback TurnStatus) TurnStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued":
		return TurnStatusQueued
	case "running":
		return TurnStatusRunning
	case "waiting_for_user":
		return TurnStatusWaitingForUser
	case "completed", "succeeded", "success", "done":
		return TurnStatusCompleted
	case "failed", "error":
		return TurnStatusFailed
	case "canceled", "cancelled", "terminated":
		return TurnStatusCanceled
	default:
		if fallback != "" {
			return fallback
		}
		return TurnStatusCompleted
	}
}

func elicitationStatusForString(status string, fallback ElicitationStatus) ElicitationStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending":
		return ElicitationStatusPending
	case "accepted":
		return ElicitationStatusAccepted
	case "declined", "rejected":
		return ElicitationStatusDeclined
	case "canceled", "cancelled":
		return ElicitationStatusCanceled
	default:
		if fallback != "" {
			return fallback
		}
		return ElicitationStatusDeclined
	}
}

func stepStatusFromString(status, fallback string) string {
	return firstNonEmptyString(status, fallback)
}

func modelStepStatusForEvent(event *streaming.Event, existingStatus, fallbackStatus string) string {
	if event == nil {
		return firstNonEmptyString(fallbackStatus, existingStatus, string(TurnStatusRunning))
	}
	if status := strings.TrimSpace(event.Status); status != "" {
		return status
	}
	if event.Type == streaming.EventTypeTextDelta {
		return "streaming"
	}
	return firstNonEmptyString(fallbackStatus, existingStatus, string(TurnStatusRunning))
}

func completedAtForEvent(event *streaming.Event) *time.Time {
	if event == nil {
		return nil
	}
	if event.CompletedAt != nil {
		return event.CompletedAt
	}
	t := event.CreatedAt
	return &t
}

func elicitationStatusForEventStatus(status string) ElicitationStatus {
	return elicitationStatusForString(status, ElicitationStatusDeclined)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
