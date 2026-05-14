package conversation

import (
	"encoding/json"
	"path"
	"strings"
	"unsafe"

	"github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/binding"
)

func (t *Turn) GetMessages() Messages {
	return *(*Messages)(unsafe.Pointer(&t.Message))
}

func (t *Turn) SetMessages(msg Messages) {
	t.Message = *(*[]*conversation.MessageView)(unsafe.Pointer(&msg))
}

func (t *Turn) ToolCalls() Messages {
	filtered := t.Filter(func(v *Message) bool {
		if v != nil && len(v.ToolMessage) > 0 {
			return true
		}
		return false
	})
	return filtered
}

func (t *Transcript) History(minimal bool) []*binding.Message {
	if t == nil || len(*t) == 0 {
		return nil
	}

	transcript := *t
	if minimal {
		transcript = transcript[len(transcript)-1:]
	}

	normalized := transcript.Filter(func(v *Message) bool {
		if v == nil || v.IsArchived() || v.IsInterim() || v.Content == nil || *v.Content == "" {
			return false
		}
		if v.Mode != nil && strings.EqualFold(strings.TrimSpace(*v.Mode), "chain") {
			return false
		}
		// Only include regular chat text; exclude elicitation/status/tool/etc.
		if strings.ToLower(strings.TrimSpace(v.Type)) != "text" {
			return false
		}
		role := strings.ToLower(strings.TrimSpace(v.Role))
		return role == "user" || role == "assistant"
	})

	var result []*binding.Message
	for _, v := range normalized {

		role := v.Role
		content := ""
		if v.Content != nil {
			content = *v.Content
		}
		// Collect attachments associated to this base message (joined via parent_message_id)
		var attachments []*binding.Attachment
		if len(v.Attachment) > 0 {
			for _, av := range v.Attachment {
				if av == nil {
					continue
				}
				var data []byte
				if av.InlineBody != nil {
					data = []byte(*av.InlineBody)
				}
				name := ""
				if av.Uri != nil && *av.Uri != "" {
					name = path.Base(*av.Uri)
				}
				attachments = append(attachments, &binding.Attachment{
					Name: name,
					URI: func() string {
						if av.Uri != nil {
							return *av.Uri
						}
						return ""
					}(),
					Mime: av.MimeType,
					Data: data,
				})
			}

		}
		result = append(result, &binding.Message{Role: role, Content: content, Attachment: attachments})
	}
	return result
}

func (t *Turn) Filter(f func(v *Message) bool) Messages {
	result := make(Messages, 0)
	for _, m := range t.GetMessages() {
		if f(m) {
			result = append(result, m)
		}
	}
	return result
}

func (t *Transcript) Filter(f func(v *Message) bool) Messages {
	var result Messages
	for _, turn := range *t {
		for _, message := range turn.GetMessages() {
			if f(message) {
				result = append(result, message)
			}
		}
	}
	return result
}

// PostAnchorTextContentSet returns a set of normalized contents for user/assistant
// text messages created strictly after the provided time.
// PostAnchorTextContentSet was removed to avoid transcript calls during continuation build.

// LastAssistantMessageWithModelCall returns the last assistant text message in this transcript that has a model call.
// It scans turns from the end and messages from the end to preserve chronology.
func (t *Transcript) LastAssistantMessageWithModelCall() *Message {
	if t == nil || len(*t) == 0 {
		return nil
	}
	for ti := len(*t) - 1; ti >= 0; ti-- {
		turn := (*t)[ti]
		if turn == nil {
			continue
		}

		var last *Message
		msgs := turn.GetMessages()
		for mi := len(msgs) - 1; mi >= 0; mi-- {
			m := msgs[mi]
			if m == nil || m.ModelCall == nil {
				continue
			}
			if strings.ToLower(strings.TrimSpace(m.Role)) != "assistant" {
				continue
			}
			if m.Mode != nil {
				switch strings.ToLower(strings.TrimSpace(*m.Mode)) {
				case "summary", "router":
					continue
				}
			}
			if m.Status != nil && strings.EqualFold(strings.TrimSpace(*m.Status), "summary") {
				continue
			}
			if !assistantMessageHasContinuationSafeModelCall(m) {
				continue
			}
			last = m
			if m.ModelCall.TraceId != nil {
				return m
			}
		}
		if last != nil {
			return last
		}
	}
	return nil
}

func assistantMessageHasContinuationSafeModelCall(m *Message) bool {
	if m == nil || m.ModelCall == nil {
		return false
	}
	if strings.ToLower(strings.TrimSpace(m.Role)) != "assistant" {
		return false
	}
	if len(m.ToolMessage) > 0 {
		return true
	}
	if strings.TrimSpace(ptrString(m.Content)) != "" || strings.TrimSpace(ptrString(m.RawContent)) != "" || strings.TrimSpace(ptrString(m.Narration)) != "" {
		return true
	}
	if hasAssistantPayloadChoice(m.ModelCall.ModelCallResponsePayload) || hasAssistantPayloadChoice(m.ModelCall.ModelCallProviderResponsePayload) {
		return true
	}
	// Compatibility fallback: older transcripts may only persist the trace id
	// without a decoded provider payload. Those calls remain usable anchors.
	return m.ModelCall.ModelCallResponsePayload == nil &&
		m.ModelCall.ModelCallProviderResponsePayload == nil &&
		m.ModelCall.TraceId != nil &&
		strings.TrimSpace(*m.ModelCall.TraceId) != ""
}

func hasAssistantPayloadChoice(payload *conversation.ModelCallStreamPayloadView) bool {
	if payload == nil || payload.InlineBody == nil {
		return false
	}
	body := strings.TrimSpace(*payload.InlineBody)
	if body == "" {
		return false
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []any  `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return false
	}
	for _, choice := range decoded.Choices {
		if strings.TrimSpace(choice.Message.Content) != "" {
			return true
		}
		if len(choice.Message.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// LastAssistantMessage returns the last assistant text message in this transcript.
// It scans turns from the end and messages from the end to preserve chronology.
func (t *Transcript) LastAssistantMessage() *Message {
	if t == nil || len(*t) == 0 {
		return nil
	}
	for ti := len(*t) - 1; ti >= 0; ti-- {
		turn := (*t)[ti]
		if turn == nil {
			continue
		}

		msgs := turn.GetMessages()
		for mi := len(msgs) - 1; mi >= 0; mi-- {
			m := msgs[mi]
			if strings.ToLower(strings.TrimSpace(m.Role)) == "assistant" {
				return m
			}
		}
	}
	return nil
}

func (t *Transcript) LastElicitationMessage() *Message {
	if t == nil || len(*t) == 0 {
		return nil
	}
	for ti := len(*t) - 1; ti >= 0; ti-- {
		turn := (*t)[ti]
		if turn == nil {
			continue
		}

		msgs := turn.GetMessages()
		for mi := len(msgs) - 1; mi >= 0; mi-- {
			m := msgs[mi]
			if strings.ToLower(strings.TrimSpace(m.Role)) == "assistant" && m.ElicitationId != nil && *m.ElicitationId != "" {
				return m
			}
		}
	}
	return nil
}
