package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/memory"
)

func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var nerr net.Error
	if errors.As(err, &nerr) {
		if nerr.Timeout() {
			return true
		}
		type temporary interface{ Temporary() bool }
		if t, ok := any(nerr).(temporary); ok && t.Temporary() {
			return true
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "dial tcp"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "tls handshake"),
		strings.Contains(msg, "temporary network error"),
		strings.Contains(msg, "server closed idle connection"),
		strings.Contains(msg, "status 500"),
		strings.Contains(msg, "internal server error"),
		strings.Contains(msg, "type=server_error"),
		strings.Contains(msg, "status 502"),
		strings.Contains(msg, "502 bad gateway"),
		strings.Contains(msg, "bad gateway"),
		strings.Contains(msg, "status 503"),
		strings.Contains(msg, "service unavailable"),
		strings.Contains(msg, "status 504"),
		strings.Contains(msg, "gateway timeout"):
		return true
	}
	return false
}

func (s *Service) tryGenerateContinuationByAnchor(ctx context.Context, model llm.Model, request *llm.GenerateRequest) (*llm.GenerateResponse, bool, error) {
	if !IsContextContinuationEnabled(model) {
		return nil, false, nil
	}
	turn, ok := memory.TurnMetaFromContext(ctx)
	if !ok {
		return nil, false, nil
	}
	traces := s.resolveTraces(ctx, turn.ConversationID)
	groups, order, latest := groupMessagesByAnchor(request.Messages, traces)
	if len(groups) == 0 {
		return nil, false, nil
	}
	if latest == "" && len(order) == 1 {
		latest = order[0]
	}
	if latest != "" {
		if msgs, ok := groups[latest]; ok {
			groups = map[string][]llm.Message{latest: msgs}
			order = []string{latest}
		}
	}
	var lastResp *llm.GenerateResponse
	for _, anchor := range order {
		msgs := groups[anchor]
		sub := &llm.GenerateRequest{}
		if request.Options != nil {
			opts := *request.Options
			sub.Options = &opts
		}
		sub.Messages = make([]llm.Message, len(msgs))
		copy(sub.Messages, msgs)
		sub.PreviousResponseID = anchor
		resp, gerr := model.Generate(ctx, sub)
		if gerr != nil {
			if isContextLimitError(gerr) {
				return nil, true, ContinuationContextLimitError{Err: gerr}
			}
			return nil, true, fmt.Errorf("continuation subcall failed: %w", gerr)
		}
		lastResp = resp
	}
	return lastResp, true, nil
}

func groupMessagesByAnchor(messages []llm.Message, traces apiconv.IndexedMessages) (map[string][]llm.Message, []string, string) {
	groups := map[string][]llm.Message{}
	anchorTimes := map[string]time.Time{}
	firstSeen := map[string]int{}
	seenOrder := 0
	var latestAnchor string
	var latestTime time.Time
	getAnchor := func(callID string) string {
		if callID == "" {
			return ""
		}
		if traceMsg, ok := traces[callID]; ok && traceMsg != nil {
			for _, tm := range traceMsg.ToolMessage {
				if tm != nil && tm.ToolCall != nil && tm.ToolCall.TraceId != nil {
					return strings.TrimSpace(*tm.ToolCall.TraceId)
				}
			}
		}
		return ""
	}
	appendMsg := func(anchor string, msg llm.Message) {
		if anchor == "" {
			return
		}
		if _, ok := groups[anchor]; !ok {
			firstSeen[anchor] = seenOrder
			seenOrder++
		}
		groups[anchor] = append(groups[anchor], msg)
		if traceMsg, ok := traces[anchor]; ok && traceMsg != nil {
			if traceMsg.ModelCall != nil {
				if latestTime.IsZero() || traceMsg.CreatedAt.After(latestTime) {
					latestTime = traceMsg.CreatedAt
					latestAnchor = anchor
				}
			}
			if traceMsg.CreatedAt.After(anchorTimes[anchor]) || anchorTimes[anchor].IsZero() {
				anchorTimes[anchor] = traceMsg.CreatedAt
			}
		}
	}
	for _, msg := range messages {
		if len(msg.ToolCalls) > 0 {
			byAnchor := map[string][]llm.ToolCall{}
			for _, call := range msg.ToolCalls {
				anchor := getAnchor(strings.TrimSpace(call.ID))
				if anchor == "" {
					continue
				}
				byAnchor[anchor] = append(byAnchor[anchor], call)
			}
			for anchor, calls := range byAnchor {
				copyMsg := msg
				copyMsg.ToolCalls = make([]llm.ToolCall, len(calls))
				copy(copyMsg.ToolCalls, calls)
				appendMsg(anchor, copyMsg)
			}
			continue
		}
		if id := strings.TrimSpace(msg.ToolCallId); id != "" {
			anchor := getAnchor(id)
			if anchor == "" {
				continue
			}
			appendMsg(anchor, msg)
		}
	}
	order := make([]string, 0, len(groups))
	for anchor := range groups {
		order = append(order, anchor)
	}
	sort.Slice(order, func(i, j int) bool {
		iAnchor := order[i]
		jAnchor := order[j]
		ti := anchorTimes[iAnchor]
		tj := anchorTimes[jAnchor]
		switch {
		case !ti.IsZero() && !tj.IsZero():
			if ti.Equal(tj) {
				return firstSeen[iAnchor] < firstSeen[jAnchor]
			}
			return ti.Before(tj)
		case !ti.IsZero():
			return true
		case !tj.IsZero():
			return false
		default:
			return firstSeen[iAnchor] < firstSeen[jAnchor]
		}
	})
	return groups, order, latestAnchor
}

func (s *Service) enforceAttachmentPolicy(ctx context.Context, input *GenerateInput, model llm.Model) error {
	if input == nil || len(input.Message) == 0 {
		return nil
	}
	isMM := input.Binding != nil && input.Binding.Flags.IsMultimodal
	convID := ""
	if tm, ok := memory.TurnMetaFromContext(ctx); ok {
		convID = tm.ConversationID
	}
	var limit int64 = s.ProviderAttachmentLimit(model)
	used := int64(0)
	if convID != "" && s.attachUsage != nil {
		used = s.attachUsage[convID]
	}
	var keptBytes int64
	filtered := make([]llm.Message, 0, len(input.Message))
	for _, m := range input.Message {
		if len(m.Items) == 0 {
			filtered = append(filtered, m)
			continue
		}
		newItems := make([]llm.ContentItem, 0, len(m.Items))
		for _, it := range m.Items {
			if it.Type != llm.ContentTypeBinary {
				newItems = append(newItems, it)
				continue
			}
			if !isMM {
				continue
			}
			rawSize := int64(0)
			if it.Source == llm.SourceBase64 && it.Data != "" {
				if dec, err := base64.StdEncoding.DecodeString(it.Data); err == nil {
					rawSize = int64(len(dec))
				}
			}
			if limit > 0 {
				remain := limit - used - keptBytes
				if remain <= 0 || (rawSize > 0 && rawSize > remain) {
					continue
				}
			}
			newItems = append(newItems, it)
			keptBytes += rawSize
		}
		if len(newItems) > 0 || strings.TrimSpace(m.Content) != "" {
			m.Items = newItems
			filtered = append(filtered, m)
		}
	}
	if convID != "" && s.attachUsage != nil && keptBytes > 0 {
		s.attachUsage[convID] = used + keptBytes
	}
	input.Message = filtered
	if !isMM {
		fmt.Println("[warning] attachments ignored: selected model is not multimodal")
	} else if limit > 0 && keptBytes < 0 {
		fmt.Println("[warning] attachment limit reached; some files were skipped")
	}
	return nil
}
