package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
)

type GenerateInput struct {
	llm.ModelSelection
	SystemPrompt *binding.Prompt
	Instruction  *binding.Prompt
	Prompt       *binding.Prompt
	Binding      *binding.Binding
	Message      []llm.Message
	Instructions string

	ExpandedUserPrompt    string `yaml:"expandedUserPrompt,omitempty" json:"expandedUserPrompt,omitempty"`
	IncludeCurrentHistory bool   `yaml:"includeCurrentHistory,omitempty" json:"includeCurrentHistory,omitempty"`
	UserID                string `yaml:"userID" json:"userID"`
	AgentID               string `yaml:"agentID" json:"agentID"`
}

func (i *GenerateInput) MatchModelIfNeeded(matcher llm.Matcher) {
	if i.Model == "" && i.Binding != nil {
		if model := strings.TrimSpace(i.Binding.Model); model != "" {
			i.Model = model
		}
	}
	if i.Model != "" || i.Preferences == nil || matcher == nil {
		return
	}
	if rm, ok := matcher.(llm.ReducingMatcher); ok && (len(i.AllowedModels) > 0 || len(i.AllowedProviders) > 0) {
		allowSet := map[string]struct{}{}
		for _, m := range i.AllowedModels {
			if v := strings.TrimSpace(m); v != "" {
				allowSet[v] = struct{}{}
			}
		}
		provSet := map[string]struct{}{}
		for _, p := range i.AllowedProviders {
			if v := strings.TrimSpace(p); v != "" {
				provSet[v] = struct{}{}
			}
		}
		allow := func(id string) bool {
			id = strings.TrimSpace(strings.ToLower(id))
			if id == "" {
				return false
			}
			if len(allowSet) > 0 {
				_, ok := allowSet[id]
				return ok
			}
			if len(provSet) > 0 {
				if idx := strings.IndexByte(id, '_'); idx > 0 {
					_, ok := provSet[id[:idx]]
					return ok
				}
				return false
			}
			return true
		}
		if m := rm.BestWithFilter(i.Preferences, allow); m != "" {
			i.Model = m
			return
		}
	}
	if m := matcher.Best(i.Preferences); m != "" {
		i.Model = m
	}
}

func (i *GenerateInput) Init(ctx context.Context) error {
	if i.Instruction != nil {
		if err := i.Instruction.Init(ctx); err != nil {
			return err
		}
		expanded, err := i.Instruction.Generate(ctx, i.Binding)
		if err != nil {
			return fmt.Errorf("failed to expand instruction prompt: %w", err)
		}
		i.Instructions = strings.TrimSpace(expanded)
	}
	if i.SystemPrompt != nil {
		if err := i.SystemPrompt.Init(ctx); err != nil {
			return err
		}
		expanded, err := i.SystemPrompt.Generate(ctx, i.Binding.SystemBinding())
		if err != nil {
			return fmt.Errorf("failed to expand system prompt: %w", err)
		}
		i.Message = append(i.Message, llm.NewSystemMessage(expanded))
	}
	if i.Prompt == nil {
		i.Prompt = &binding.Prompt{}
	}
	if err := i.Prompt.Init(ctx); err != nil {
		return err
	}
	currentPrompt, err := i.Prompt.Generate(ctx, i.Binding)
	if err != nil {
		return fmt.Errorf("failed to prompt: %w", err)
	}
	i.ExpandedUserPrompt = currentPrompt

	if i.Binding != nil {
		for _, doc := range i.Binding.SystemDocuments.Items {
			i.Message = append(i.Message, llm.NewTextMessage(llm.MessageRole("system"), doc.PageContent))
		}
		for _, doc := range i.Binding.Documents.Items {
			i.Message = append(i.Message, llm.NewTextMessage(llm.MessageRole("user"), doc.PageContent))
		}
		msgs := historyLLMMessagesWithExpandedCurrentPrompt(&i.Binding.History, currentPrompt, i.Binding.Task.Attachments)
		if !i.IncludeCurrentHistory && i.Binding.History.Current != nil {
			filtered := make([]llm.Message, 0, len(msgs))
			filtered = append(filtered, msgs...)
			msgs = filtered
		}
		i.Message = append(i.Message, msgs...)
	}

	if tools := i.Binding.Tools; len(tools.Signatures) > 0 {
		for _, tool := range tools.Signatures {
			i.Options.Tools = append(i.Options.Tools, llm.Tool{Type: "function", Definition: *tool})
		}
	}

	if i.Binding.History.Current != nil {
		for _, elicitationMsg := range i.Binding.History.Current.Messages {
			if elicitationMsg == nil {
				continue
			}
			if elicitationMsg.Kind != binding.MessageKindElicitPrompt && elicitationMsg.Kind != binding.MessageKindElicitAnswer {
				continue
			}
			i.Message = append(i.Message, llm.NewTextMessage(llm.MessageRole(elicitationMsg.Role), elicitationMsg.Content))
			content := strings.TrimSpace(elicitationMsg.Content)
			keys := []string{}
			if content != "" && strings.HasPrefix(content, "{") {
				var tmp map[string]interface{}
				if err := json.Unmarshal([]byte(content), &tmp); err == nil {
					for k := range tmp {
						keys = append(keys, k)
					}
					sort.Strings(keys)
				}
			}
			_ = keys
		}
	}
	return nil
}

func appendCurrentHistoryMessages(h *binding.History, msgs ...*binding.Message) {
	if h == nil || len(msgs) == 0 {
		return
	}
	if h.Current == nil {
		h.Current = &binding.Turn{ID: h.CurrentTurnID}
	}
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if m.CreatedAt.IsZero() {
			m.CreatedAt = time.Now().UTC()
		}
		if n := len(h.Current.Messages); n > 0 {
			last := h.Current.Messages[n-1].CreatedAt
			if m.CreatedAt.Before(last) {
				m.CreatedAt = last.Add(time.Nanosecond)
			}
		}
		h.Current.Messages = append(h.Current.Messages, m)
	}
}

func historyLLMMessagesWithExpandedCurrentPrompt(h *binding.History, expandedPrompt string, attachments []*binding.Attachment) []llm.Message {
	trimmedPrompt := strings.TrimSpace(expandedPrompt)
	if h == nil {
		if trimmedPrompt == "" {
			return nil
		}
		return []llm.Message{newExpandedUserLLMMessage(trimmedPrompt, attachments)}
	}
	if trimmedPrompt == "" {
		return h.LLMMessages()
	}

	var out []llm.Message
	appendLLM := func(msg *binding.Message, omitTools bool) {
		if msg == nil {
			return
		}
		switch msg.Kind {
		case binding.MessageKindToolResult:
			if omitTools {
				return
			}
			out = append(out, binding.ToolResultLLMMessages(msg)...)
		case binding.MessageKindElicitPrompt, binding.MessageKindElicitAnswer:
			return
		default:
			out = append(out, msg.ToLLM())
		}
	}

	trimmedCurrentID := strings.TrimSpace(h.CurrentTurnID)
	omitTools := h.ToolExposure == "turn"
	replacedCurrentUser := false
	currentTurn := h.Current

	if currentTurn == nil && trimmedCurrentID != "" {
		for _, t := range h.Past {
			if t == nil {
				continue
			}
			if strings.TrimSpace(t.ID) == trimmedCurrentID {
				currentTurn = t
				break
			}
		}
	}

	if len(h.Past) > 0 || currentTurn != nil {
		for _, t := range h.Past {
			if t == nil {
				continue
			}
			if currentTurn != nil && t == currentTurn {
				continue
			}
			isCurrentTurn := trimmedCurrentID != "" && strings.TrimSpace(t.ID) == trimmedCurrentID
			omitToolsForTurn := omitTools && !isCurrentTurn
			for _, m := range t.Messages {
				appendLLM(m, omitToolsForTurn)
			}
		}
		if currentTurn != nil {
			for _, m := range currentTurn.Messages {
				if m == nil {
					continue
				}
				if !replacedCurrentUser &&
					m.Kind == binding.MessageKindChatUser &&
					strings.EqualFold(strings.TrimSpace(m.Role), string(llm.RoleUser)) {
					out = append(out, newExpandedUserLLMMessage(trimmedPrompt, attachments))
					replacedCurrentUser = true
					continue
				}
				appendLLM(m, false)
			}
		}
		if !replacedCurrentUser {
			out = append(out, newExpandedUserLLMMessage(trimmedPrompt, attachments))
		}
		return out
	}

	out = append(out, h.LLMMessages()...)
	out = append(out, newExpandedUserLLMMessage(trimmedPrompt, attachments))
	return out
}

func newExpandedUserLLMMessage(content string, attachments []*binding.Attachment) llm.Message {
	if len(attachments) == 0 {
		return llm.NewUserMessage(content)
	}
	items := make([]*llm.AttachmentItem, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment == nil || len(attachment.Data) == 0 {
			continue
		}
		items = append(items, &llm.AttachmentItem{
			Name:     attachment.Name,
			MimeType: attachment.Mime,
			Data:     attachment.Data,
			Content:  attachment.Content,
		})
	}
	if len(items) == 0 {
		return llm.NewUserMessage(content)
	}
	return llm.NewMessageWithBinaries(llm.RoleUser, items, content)
}

func (i *GenerateInput) Validate(ctx context.Context) error {
	_ = ctx
	if strings.TrimSpace(i.UserID) == "" {
		return fmt.Errorf("userId is required")
	}
	if i.Model == "" {
		return fmt.Errorf("model is required")
	}
	if len(i.Message) == 0 {
		return fmt.Errorf("content is required")
	}
	return nil
}
