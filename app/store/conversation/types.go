package conversation

import (
	conversationmodel "github.com/viant/agently-core/model/conversation"
	generatedfilemodel "github.com/viant/agently-core/model/generatedfile"
	messagemodel "github.com/viant/agently-core/model/message"
	modelcallmodel "github.com/viant/agently-core/model/modelcall"
	payloadmodel "github.com/viant/agently-core/model/payload"
	toolcallmodel "github.com/viant/agently-core/model/toolcall"
	turnmodel "github.com/viant/agently-core/model/turn"
	"strings"
)

type (
	Input                = conversationmodel.ConversationInput
	MutableConversation  = conversationmodel.Conversation
	MutableMessage       = messagemodel.Message
	MutableModelCall     = modelcallmodel.ModelCall
	MutableToolCall      = toolcallmodel.ToolCall
	MutablePayload       = payloadmodel.Payload
	MutableTurn          = turnmodel.Turn
	Payload              = payloadmodel.PayloadView
	GeneratedFile        = generatedfilemodel.GeneratedFileView
	MutableGeneratedFile = generatedfilemodel.GeneratedFile
	ToolCallView         = conversationmodel.ToolCallView
	ResponsePayloadView  = conversationmodel.ModelCallStreamPayloadView
)

type (
	Conversation conversationmodel.ConversationView
	Message      conversationmodel.MessageView
	Turn         conversationmodel.TranscriptView
	Transcript   []*Turn
)

func (c *Conversation) HasConversationParent() bool {
	if c.ConversationParentId == nil || *c.ConversationParentId == "" {
		return false
	}
	return true
}

// UniqueToolNames returns a de-duplicated list of tool names (service/method)
// observed across all messages in the transcript, preserving encounter order.
func (t Transcript) UniqueToolNames() []string {
	if len(t) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, turn := range t {
		if turn == nil || len(turn.Message) == 0 {
			continue
		}
		for _, m := range turn.Message {
			if m == nil {
				continue
			}
			name := ""
			for _, tm := range m.ToolMessage {
				if tm != nil && tm.ToolCall != nil {
					name = strings.TrimSpace(tm.ToolCall.ToolName)
					break
				}
			}
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

func (t Transcript) Last() Transcript {
	if len(t) == 0 {
		return nil
	}
	return t[len(t)-1:]
}

func (m *Message) NewMutable() *MutableMessage {
	// Allocate a fresh mutable message with Has initialized
	out := NewMessage()

	// Required identifiers and always-present fields
	out.SetId(m.Id)
	out.SetConversationID(m.ConversationId)
	out.SetRole(m.Role)
	out.SetType(m.Type)
	out.SetCreatedAt(m.CreatedAt)

	// Optional linkage and ordering
	if m.TurnId != nil {
		out.SetTurnID(*m.TurnId)
	}
	if m.Sequence != nil {
		out.SetSequence(*m.Sequence)
	}
	if m.Archived != nil {
		out.SetArchived(*m.Archived)
	}

	// Timestamps and attribution
	if m.UpdatedAt != nil {
		out.SetUpdatedAt(*m.UpdatedAt)
	}
	if m.CreatedByUserId != nil {
		out.SetCreatedByUserID(*m.CreatedByUserId)
	}

	// Message semantics/content
	if m.Mode != nil {
		out.SetMode(*m.Mode)
	}
	if m.Status != nil {
		out.SetStatus(*m.Status)
	}
	if m.Content != nil {
		out.SetContent(*m.Content)
	}
	if m.RawContent != nil {
		out.SetRawContent(*m.RawContent)
	}
	out.SetInterim(m.Interim)

	// Optional summaries/tags and relationships
	if m.ContextSummary != nil {
		out.ContextSummary = m.ContextSummary
		if out.Has != nil {
			out.Has.ContextSummary = true
		}
	}
	if m.Tags != nil {
		out.Tags = m.Tags
		if out.Has != nil {
			out.Has.Tags = true
		}
	}
	if m.ElicitationId != nil {
		out.SetElicitationID(*m.ElicitationId)
	}
	if m.ParentMessageId != nil {
		out.SetParentMessageID(*m.ParentMessageId)
	}
	if m.SupersededBy != nil {
		out.SupersededBy = m.SupersededBy
		if out.Has != nil {
			out.Has.SupersededBy = true
		}
	}
	if m.LinkedConversationId != nil {
		out.SetLinkedConversationID(*m.LinkedConversationId)
	}
	if m.AttachmentPayloadId != nil {
		out.SetAttachmentPayloadID(*m.AttachmentPayloadId)
	}
	if m.ElicitationPayloadId != nil {
		out.SetElicitationPayloadID(*m.ElicitationPayloadId)
	}
	if m.ToolName != nil {
		out.SetToolName(*m.ToolName)
	}

	return out
}
