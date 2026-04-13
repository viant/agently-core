package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/prompt"
)

type GenerateInput struct {
	llm.ModelSelection
	SystemPrompt *prompt.Prompt
	Instruction  *prompt.Prompt
	Prompt       *prompt.Prompt
	Binding      *prompt.Binding
	Message      []llm.Message
	Instructions string

	ExpandedUserPrompt         string `yaml:"expandedUserPrompt,omitempty" json:"expandedUserPrompt,omitempty"`
	UserPromptAlreadyInHistory bool   `yaml:"userPromptAlreadyInHistory,omitempty" json:"userPromptAlreadyInHistory,omitempty"`
	IncludeCurrentHistory      bool   `yaml:"includeCurrentHistory,omitempty" json:"includeCurrentHistory,omitempty"`
	UserID                     string `yaml:"userID" json:"userID"`
	AgentID                    string `yaml:"agentID" json:"agentID"`
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
		i.Prompt = &prompt.Prompt{}
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
		if !i.UserPromptAlreadyInHistory {
			shouldAppend := true
			trimmed := strings.TrimSpace(currentPrompt)
			if trimmed != "" {
				h := &i.Binding.History
				turns := make([]*prompt.Turn, 0, len(h.Past)+1)
				turns = append(turns, h.Past...)
				if h.Current != nil {
					turns = append(turns, h.Current)
				}
				for ti := len(turns) - 1; ti >= 0 && shouldAppend; ti-- {
					turn := turns[ti]
					if turn == nil || len(turn.Messages) == 0 {
						continue
					}
					for mi := len(turn.Messages) - 1; mi >= 0; mi-- {
						m := turn.Messages[mi]
						if m == nil {
							continue
						}
						if !strings.EqualFold(strings.TrimSpace(m.Role), "user") {
							continue
						}
						if strings.TrimSpace(m.Content) == trimmed {
							shouldAppend = false
							break
						}
					}
				}
			}
			if shouldAppend {
				msg := &prompt.Message{
					Kind:    prompt.MessageKindChatUser,
					Role:    string(llm.RoleUser),
					Content: currentPrompt,
				}
				if len(i.Binding.Task.Attachments) > 0 {
					sortAttachments(i.Binding.Task.Attachments)
					msg.Attachment = i.Binding.Task.Attachments
				}
				appendCurrentHistoryMessages(&i.Binding.History, msg)
			}
		}
		for _, doc := range i.Binding.SystemDocuments.Items {
			i.Message = append(i.Message, llm.NewTextMessage(llm.MessageRole("system"), doc.PageContent))
		}
		for _, doc := range i.Binding.Documents.Items {
			i.Message = append(i.Message, llm.NewTextMessage(llm.MessageRole("user"), doc.PageContent))
		}
		msgs := i.Binding.History.LLMMessages()
		if !i.IncludeCurrentHistory && i.Binding.History.Current != nil {
			filtered := make([]llm.Message, 0, len(msgs))
			filtered = append(filtered, msgs...)
			msgs = filtered
		}
		i.Message = append(i.Message, msgs...)
		i.ensureExpandedUserPromptPresent()
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
			if elicitationMsg.Kind != prompt.MessageKindElicitPrompt && elicitationMsg.Kind != prompt.MessageKindElicitAnswer {
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

func (i *GenerateInput) ensureExpandedUserPromptPresent() {
	trimmed := strings.TrimSpace(i.ExpandedUserPrompt)
	if trimmed == "" {
		return
	}
	for idx := len(i.Message) - 1; idx >= 0; idx-- {
		msg := i.Message[idx]
		if msg.Role != llm.RoleUser {
			continue
		}
		if strings.TrimSpace(msg.Content) == trimmed {
			return
		}
	}
	msg := llm.NewUserMessage(i.ExpandedUserPrompt)
	if i.Binding != nil && len(i.Binding.Task.Attachments) > 0 {
		attachments := make([]*llm.AttachmentItem, 0, len(i.Binding.Task.Attachments))
		for _, attachment := range i.Binding.Task.Attachments {
			if attachment == nil || len(attachment.Data) == 0 {
				continue
			}
			attachments = append(attachments, &llm.AttachmentItem{
				Name:     attachment.Name,
				MimeType: attachment.Mime,
				Data:     attachment.Data,
				Content:  attachment.Content,
			})
		}
		if len(attachments) > 0 {
			msg = llm.NewMessageWithBinaries(llm.RoleUser, attachments, i.ExpandedUserPrompt)
		}
	}
	i.Message = append(i.Message, msg)
}

func sortAttachments(attachments []*prompt.Attachment) {
	sort.Slice(attachments, func(i, j int) bool {
		if attachments[i] == nil || attachments[j] == nil {
			return false
		}
		return strings.Compare(attachments[i].URI, attachments[j].URI) < 0
	})
}

func appendCurrentHistoryMessages(h *prompt.History, msgs ...*prompt.Message) {
	if h == nil || len(msgs) == 0 {
		return
	}
	if h.Current == nil {
		h.Current = &prompt.Turn{ID: h.CurrentTurnID}
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
