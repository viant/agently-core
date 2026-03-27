package memory

import (
	"sort"
	"strings"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

func buildInput(id string, options []convcli.Option) agconv.ConversationInput {
	in := agconv.ConversationInput{Id: id, Has: &agconv.ConversationInputHas{Id: true}}
	for _, opt := range options {
		if opt != nil {
			opt((*convcli.Input)(&in))
		}
	}
	return in
}

func cloneConversationView(src *agconv.ConversationView) *agconv.ConversationView {
	if src == nil {
		return nil
	}
	out := *src
	if src.Transcript != nil {
		out.Transcript = make([]*agconv.TranscriptView, 0, len(src.Transcript))
		for _, t := range src.Transcript {
			if t == nil {
				continue
			}
			tt := *t
			if t.Message != nil {
				tt.Message = make([]*agconv.MessageView, 0, len(t.Message))
				for _, m := range t.Message {
					tt.Message = append(tt.Message, copyMessage(m))
				}
			}
			out.Transcript = append(out.Transcript, &tt)
		}
	}
	return &out
}

func copyMessage(m *agconv.MessageView) *agconv.MessageView {
	if m == nil {
		return nil
	}
	cp := *m
	if m.Attachment != nil {
		cp.Attachment = make([]*agconv.AttachmentView, len(m.Attachment))
		copy(cp.Attachment, m.Attachment)
	}
	if m.ModelCall != nil {
		tmp := *m.ModelCall
		cp.ModelCall = &tmp
	}
	if m.ToolMessage != nil {
		cp.ToolMessage = make([]*agconv.ToolMessageView, 0, len(m.ToolMessage))
		for _, tm := range m.ToolMessage {
			if tm == nil {
				continue
			}
			tmCopy := *tm
			if tm.ToolCall != nil {
				tc := *tm.ToolCall
				tmCopy.ToolCall = &tc
			}
			cp.ToolMessage = append(cp.ToolMessage, &tmCopy)
		}
	}
	return &cp
}

func copyPayload(p *convcli.Payload) *convcli.Payload {
	if p == nil {
		return nil
	}
	cp := *p
	if p.InlineBody != nil {
		b := make([]byte, len(*p.InlineBody))
		copy(b, *p.InlineBody)
		cp.InlineBody = &b
	}
	return &cp
}

func findOrCreateTurn(conv *agconv.ConversationView, turnID string) *agconv.TranscriptView {
	if conv.Transcript == nil {
		conv.Transcript = []*agconv.TranscriptView{}
	}
	for _, t := range conv.Transcript {
		if t != nil && t.Id == turnID {
			return t
		}
	}
	t := &agconv.TranscriptView{Id: turnID, ConversationId: conv.Id, Status: "active", CreatedAt: time.Now()}
	conv.Transcript = append(conv.Transcript, t)
	sort.SliceStable(conv.Transcript, func(i, j int) bool { return conv.Transcript[i].CreatedAt.Before(conv.Transcript[j].CreatedAt) })
	return t
}

func messageInTurn(t *agconv.TranscriptView, id string) bool {
	for _, m := range t.Message {
		if m != nil && m.Id == id {
			return true
		}
	}
	return false
}

func toClientConversation(v *agconv.ConversationView) *convcli.Conversation {
	if v == nil {
		return nil
	}
	c := convcli.Conversation(*v)
	return &c
}

func toClientMessage(v *agconv.MessageView) *convcli.Message {
	if v == nil {
		return nil
	}
	m := convcli.Message(*v)
	return &m
}

func applySinceFilter(conv *agconv.ConversationView, in *agconv.ConversationInput) {
	if conv == nil || in == nil || in.Has == nil || !in.Has.Since || strings.TrimSpace(in.Since) == "" || conv.Transcript == nil {
		return
	}
	turnID := in.Since
	var sinceTime *time.Time
	for _, t := range conv.Transcript {
		if t != nil && t.Id == turnID {
			ts := t.CreatedAt
			sinceTime = &ts
			break
		}
	}
	if sinceTime == nil {
		return
	}
	filtered := make([]*agconv.TranscriptView, 0, len(conv.Transcript))
	for _, t := range conv.Transcript {
		if t != nil && (t.CreatedAt.Equal(*sinceTime) || t.CreatedAt.After(*sinceTime)) {
			filtered = append(filtered, t)
		}
	}
	conv.Transcript = filtered
}

func applyIncludeFlags(conv *agconv.ConversationView, in *agconv.ConversationInput) {
	if conv == nil || conv.Transcript == nil {
		return
	}
	includeModel := in != nil && in.Has != nil && in.Has.IncludeModelCal && in.IncludeModelCal
	includeTool := in != nil && in.Has != nil && in.Has.IncludeToolCall && in.IncludeToolCall
	if includeModel && includeTool {
		return
	}
	for _, t := range conv.Transcript {
		for _, m := range t.Message {
			if !includeModel {
				m.ModelCall = nil
			}
			if !includeTool {
				m.ToolMessage = nil
			}
		}
	}
}

func defaultTurnID(convID string) string { return convID + ":turn" }
