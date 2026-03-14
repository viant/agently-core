package sdk

import (
	"sort"
	"strings"
	"time"

	convstore "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

func wrapTranscriptTurns(turns convstore.Transcript, selector *QuerySelector) []*TranscriptTurn {
	if len(turns) == 0 {
		return nil
	}
	out := make([]*TranscriptTurn, 0, len(turns))
	for _, turn := range turns {
		if turn == nil {
			continue
		}
		groups := buildExecutionGroups(turn)
		total := len(groups)
		offset := 0
		limit := total
		if selector != nil {
			if selector.Offset > 0 {
				offset = selector.Offset
			}
			if selector.Limit > 0 {
				limit = selector.Limit
			}
			if offset > total {
				offset = total
			}
			end := total
			if selector.Limit > 0 {
				end = offset + selector.Limit
				if end > total {
					end = total
				}
			}
			groups = groups[offset:end]
		}
		out = append(out, &TranscriptTurn{
			Turn:                  turn,
			ExecutionGroups:       groups,
			ExecutionGroupsTotal:  total,
			ExecutionGroupsOffset: offset,
			ExecutionGroupsLimit:  limit,
		})
	}
	return out
}

func buildExecutionGroups(turn *convstore.Turn) []*ExecutionGroup {
	if turn == nil || len(turn.Message) == 0 {
		return nil
	}
	groups := make([]*ExecutionGroup, 0, len(turn.Message))
	for _, message := range turn.Message {
		if message == nil || message.ModelCall == nil {
			continue
		}
		group := &ExecutionGroup{
			ParentMessageID: message.Id,
			ModelMessageID:  message.Id,
			Sequence:        len(groups) + 1,
			Iteration:       message.Iteration,
			Preamble:        executionPreamble(message),
			Content:         strings.TrimSpace(valueOrEmpty(message.Content)),
			FinalResponse:   isFinalExecutionMessage(message),
			Status:          strings.TrimSpace(valueOrEmpty(message.Status)),
			ModelCall:       message.ModelCall,
		}
		group.ToolMessages, group.ToolCalls = collectToolChildren(message)
		if group.Status == "" && group.ModelCall != nil {
			group.Status = strings.TrimSpace(group.ModelCall.Status)
		}
		groups = append(groups, group)
	}
	return groups
}

func executionPreamble(message *agconv.MessageView) string {
	if message == nil {
		return ""
	}
	if preamble := strings.TrimSpace(stringValue(message.Preamble)); preamble != "" {
		return preamble
	}
	if message.Interim == 1 {
		return strings.TrimSpace(stringValue(message.Content))
	}
	return ""
}

func isFinalExecutionMessage(message *agconv.MessageView) bool {
	if message == nil {
		return false
	}
	if strings.ToLower(strings.TrimSpace(message.Role)) != "assistant" {
		return false
	}
	if message.Interim != 0 {
		return false
	}
	return strings.TrimSpace(stringValue(message.Content)) != ""
}

func collectToolChildren(message *agconv.MessageView) ([]*agconv.ToolMessageView, []*agconv.ToolCallView) {
	if message == nil || len(message.ToolMessage) == 0 {
		return nil, nil
	}
	toolMessages := make([]*agconv.ToolMessageView, 0, len(message.ToolMessage))
	for _, toolMessage := range message.ToolMessage {
		if toolMessage == nil {
			continue
		}
		toolMessages = append(toolMessages, toolMessage)
	}
	sort.SliceStable(toolMessages, func(i, j int) bool {
		left, right := toolMessages[i], toolMessages[j]
		leftSeq := sequenceValue(left)
		rightSeq := sequenceValue(right)
		if leftSeq != rightSeq {
			return leftSeq < rightSeq
		}
		leftAt := createdAtValue(left.CreatedAt)
		rightAt := createdAtValue(right.CreatedAt)
		if !leftAt.Equal(rightAt) {
			return leftAt.Before(rightAt)
		}
		return left.Id < right.Id
	})
	toolCalls := make([]*agconv.ToolCallView, 0, len(toolMessages))
	for _, toolMessage := range toolMessages {
		if toolMessage.ToolCall != nil {
			toolCalls = append(toolCalls, toolMessage.ToolCall)
		}
	}
	return toolMessages, toolCalls
}

func sequenceValue(message *agconv.ToolMessageView) int {
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

func createdAtValue(value time.Time) time.Time {
	if value.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return value.UTC()
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
