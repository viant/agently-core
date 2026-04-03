package sdk

import (
	"strings"
	"time"

	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

func normalizedCreatedAt(value time.Time) time.Time {
	if value.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return value.UTC()
}

func lessTimeAndID(leftAt time.Time, leftID string, rightAt time.Time, rightID string) bool {
	leftCreatedAt := normalizedCreatedAt(leftAt)
	rightCreatedAt := normalizedCreatedAt(rightAt)
	if !leftCreatedAt.Equal(rightCreatedAt) {
		return leftCreatedAt.Before(rightCreatedAt)
	}
	return strings.TrimSpace(leftID) < strings.TrimSpace(rightID)
}

func toolMessageSequence(message *agconv.ToolMessageView) int {
	if message == nil {
		return 0
	}
	if message.Sequence != nil {
		return *message.Sequence
	}
	if message.ToolCall != nil && message.ToolCall.MessageSequence != nil {
		return *message.ToolCall.MessageSequence
	}
	if message.Iteration != nil {
		return *message.Iteration
	}
	return 0
}

func lessToolMessage(left, right *agconv.ToolMessageView) bool {
	leftSeq := toolMessageSequence(left)
	rightSeq := toolMessageSequence(right)
	if leftSeq != rightSeq {
		return leftSeq < rightSeq
	}
	if left == nil || right == nil {
		return left != nil
	}
	return lessTimeAndID(left.CreatedAt, left.Id, right.CreatedAt, right.Id)
}

func lessPendingElicitation(left, right *PendingElicitation) bool {
	if left == nil || right == nil {
		return left != nil
	}
	if !normalizedCreatedAt(left.CreatedAt).Equal(normalizedCreatedAt(right.CreatedAt)) {
		return normalizedCreatedAt(left.CreatedAt).Before(normalizedCreatedAt(right.CreatedAt))
	}
	return strings.TrimSpace(left.ElicitationID) < strings.TrimSpace(right.ElicitationID)
}
