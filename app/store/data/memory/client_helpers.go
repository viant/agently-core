package memory

import (
	"sort"
	"strings"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
)

func buildInput(id string, options []convcli.Option) conversationmodel.ConversationInput {
	in := conversationmodel.ConversationInput{Id: id, Has: &conversationmodel.ConversationInputHas{Id: true}}
	for _, opt := range options {
		if opt != nil {
			opt((*convcli.Input)(&in))
		}
	}
	return in
}

func cloneConversationView(src *conversationmodel.ConversationView) *conversationmodel.ConversationView {
	if src == nil {
		return nil
	}
	out := *src
	if src.Transcript != nil {
		out.Transcript = make([]*conversationmodel.TranscriptView, 0, len(src.Transcript))
		for _, t := range src.Transcript {
			if t == nil {
				continue
			}
			tt := *t
			if t.Message != nil {
				tt.Message = make([]*conversationmodel.MessageView, 0, len(t.Message))
				for _, m := range t.Message {
					tt.Message = append(tt.Message, copyMessage(m))
				}
			}
			out.Transcript = append(out.Transcript, &tt)
		}
	}
	return &out
}

func copyMessage(m *conversationmodel.MessageView) *conversationmodel.MessageView {
	if m == nil {
		return nil
	}
	cp := *m
	if m.Attachment != nil {
		cp.Attachment = make([]*conversationmodel.AttachmentView, len(m.Attachment))
		copy(cp.Attachment, m.Attachment)
	}
	if m.ModelCall != nil {
		tmp := *m.ModelCall
		cp.ModelCall = &tmp
	}
	if m.ToolMessage != nil {
		cp.ToolMessage = make([]*conversationmodel.ToolMessageView, 0, len(m.ToolMessage))
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

func findOrCreateTurn(conv *conversationmodel.ConversationView, turnID string) *conversationmodel.TranscriptView {
	if conv.Transcript == nil {
		conv.Transcript = []*conversationmodel.TranscriptView{}
	}
	for _, t := range conv.Transcript {
		if t != nil && t.Id == turnID {
			return t
		}
	}
	t := &conversationmodel.TranscriptView{Id: turnID, ConversationId: conv.Id, Status: "active", CreatedAt: time.Now()}
	conv.Transcript = append(conv.Transcript, t)
	sort.SliceStable(conv.Transcript, func(i, j int) bool { return conv.Transcript[i].CreatedAt.Before(conv.Transcript[j].CreatedAt) })
	return t
}

func messageInTurn(t *conversationmodel.TranscriptView, id string) bool {
	for _, m := range t.Message {
		if m != nil && m.Id == id {
			return true
		}
	}
	return false
}

func toClientConversation(v *conversationmodel.ConversationView) *convcli.Conversation {
	if v == nil {
		return nil
	}
	c := convcli.Conversation(*v)
	return &c
}

func toClientMessage(v *conversationmodel.MessageView) *convcli.Message {
	if v == nil {
		return nil
	}
	m := convcli.Message(*v)
	return &m
}

func applySinceFilter(conv *conversationmodel.ConversationView, in *conversationmodel.ConversationInput) {
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
	filtered := make([]*conversationmodel.TranscriptView, 0, len(conv.Transcript))
	for _, t := range conv.Transcript {
		if t != nil && (t.CreatedAt.Equal(*sinceTime) || t.CreatedAt.After(*sinceTime)) {
			filtered = append(filtered, t)
		}
	}
	conv.Transcript = filtered
}

func applyIncludeFlags(conv *conversationmodel.ConversationView, in *conversationmodel.ConversationInput) {
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
