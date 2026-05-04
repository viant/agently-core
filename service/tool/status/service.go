package status

import (
	"context"
	"fmt"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/textutil"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// Service publishes tool-run status messages into the parent conversation turn.
// It supports start/update/finalize lifecycle with minimal, consistent fields.
type Service struct {
	conv apiconv.Client
}

func New(c apiconv.Client) *Service { return &Service{conv: c} }

// Start creates an interim status message under the parent turn and returns its id.
// role defaults to "assistant"; mode defaults to "exec"; actor defaults to "tool".
func (s *Service) Start(ctx context.Context, parent runtimerequestctx.TurnMeta, toolName, role, actor, mode string) (string, error) {
	if s == nil || s.conv == nil {
		return "", fmt.Errorf("status: conversation client not configured")
	}
	logx.Infof("conversation", "status start parent_convo=%q parent_turn=%q tool=%q role=%q actor=%q mode=%q", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID), strings.TrimSpace(toolName), strings.TrimSpace(role), strings.TrimSpace(actor), strings.TrimSpace(mode))
	if strings.TrimSpace(role) == "" {
		role = "assistant"
	}
	if strings.TrimSpace(actor) == "" {
		actor = "tool"
	}
	if strings.TrimSpace(mode) == "" {
		mode = "exec"
	}
	m, err := apiconv.AddMessage(ctx, s.conv, &parent,
		apiconv.WithRole(role),
		apiconv.WithInterim(1),
		apiconv.WithContent(""),
		apiconv.WithCreatedByUserID(actor),
		apiconv.WithMode(mode),
		apiconv.WithToolName(mcpname.Display(toolName)),
	)
	if err != nil {
		logx.Errorf("conversation", "status start error parent_convo=%q tool=%q err=%v", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(toolName), err)
		return "", fmt.Errorf("status: start failed: %w", err)
	}
	logx.Infof("conversation", "status start ok parent_convo=%q tool=%q message_id=%q", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(toolName), strings.TrimSpace(m.Id))
	return m.Id, nil
}

// StartNarration creates an interim assistant message whose visible content is
// carried through the message preamble field. This is the backend-authored
// counterpart to model-authored assistant preambles and reuses the same
// assistant_preamble event contract.
func (s *Service) StartNarration(ctx context.Context, parent runtimerequestctx.TurnMeta, toolName, role, actor, mode, preamble string) (string, error) {
	if s == nil || s.conv == nil {
		return "", fmt.Errorf("status: conversation client not configured")
	}
	if existingID := strings.TrimSpace(s.findNarrationMessageID(ctx, parent)); existingID != "" {
		if err := s.UpdateNarration(ctx, parent, existingID, preamble); err != nil {
			return "", err
		}
		return existingID, nil
	}
	if strings.TrimSpace(role) == "" {
		role = "assistant"
	}
	if strings.TrimSpace(actor) == "" {
		actor = "tool"
	}
	if strings.TrimSpace(mode) == "" {
		mode = "narrator"
	}
	m, err := apiconv.AddMessage(ctx, s.conv, &parent,
		apiconv.WithRole(role),
		apiconv.WithInterim(1),
		apiconv.WithContent(strings.TrimSpace(preamble)),
		apiconv.WithNarration(strings.TrimSpace(preamble)),
		apiconv.WithCreatedByUserID(actor),
		apiconv.WithMode(mode),
		apiconv.WithToolName(mcpname.Display(toolName)),
	)
	if err != nil {
		return "", fmt.Errorf("status: start preamble failed: %w", err)
	}
	return m.Id, nil
}

func (s *Service) findNarrationMessageID(ctx context.Context, parent runtimerequestctx.TurnMeta) string {
	if s == nil || s.conv == nil || strings.TrimSpace(parent.ConversationID) == "" || strings.TrimSpace(parent.TurnID) == "" {
		return ""
	}
	conv, err := s.conv.GetConversation(ctx, parent.ConversationID, apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return ""
	}
	transcript := conv.GetTranscript()
	for i := len(transcript) - 1; i >= 0; i-- {
		turn := transcript[i]
		if turn == nil || strings.TrimSpace(turn.Id) != strings.TrimSpace(parent.TurnID) {
			continue
		}
		for j := len(turn.Message) - 1; j >= 0; j-- {
			msg := turn.Message[j]
			if msg == nil {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
				continue
			}
			if msg.Interim != 1 {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(ptrString(msg.Mode)), "narrator") {
				continue
			}
			if id := strings.TrimSpace(msg.Id); id != "" {
				return id
			}
		}
	}
	return ""
}

// Update sets interim content (e.g., progress text) on the status message.
func (s *Service) Update(ctx context.Context, parent runtimerequestctx.TurnMeta, messageID, content string) error {
	if s == nil || s.conv == nil {
		return fmt.Errorf("status: conversation client not configured")
	}
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("status: empty messageID")
	}
	logx.Infof("conversation", "status update parent_convo=%q parent_turn=%q message_id=%q content_len=%d content_head=%q content_tail=%q", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID), strings.TrimSpace(messageID), len(content), textutil.Head(content, 512), textutil.Tail(content, 512))
	mu := apiconv.NewMessage()
	mu.SetId(messageID)
	mu.SetConversationID(parent.ConversationID)
	mu.SetTurnID(parent.TurnID)
	mu.SetContent(content)
	mu.SetInterim(1)
	if err := s.conv.PatchMessage(ctx, mu); err != nil {
		logx.Errorf("conversation", "status update error parent_convo=%q message_id=%q err=%v", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(messageID), err)
		return fmt.Errorf("status: update failed: %w", err)
	}
	logx.Infof("conversation", "status update ok parent_convo=%q message_id=%q", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(messageID))
	return nil
}

// UpdateNarration refreshes an existing interim assistant message in place using
// the preamble field while preserving the same assistant message id.
func (s *Service) UpdateNarration(ctx context.Context, parent runtimerequestctx.TurnMeta, messageID, preamble string) error {
	if s == nil || s.conv == nil {
		return fmt.Errorf("status: conversation client not configured")
	}
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("status: empty messageID")
	}
	mu := apiconv.NewMessage()
	mu.SetId(messageID)
	mu.SetConversationID(parent.ConversationID)
	mu.SetTurnID(parent.TurnID)
	mu.SetNarration(strings.TrimSpace(preamble))
	mu.SetContent(strings.TrimSpace(preamble))
	mu.SetInterim(1)
	mu.SetMode("narrator")
	if err := s.conv.PatchMessage(ctx, mu); err != nil {
		return fmt.Errorf("status: preamble update failed: %w", err)
	}
	return nil
}

// Finalize clears interim, sets final status, and writes an optional preview content.
// status should be one of running|succeeded|failed|canceled|auth-required.
func (s *Service) Finalize(ctx context.Context, parent runtimerequestctx.TurnMeta, messageID, status, preview string) error {
	if s == nil || s.conv == nil {
		return fmt.Errorf("status: conversation client not configured")
	}
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("status: empty messageID")
	}
	if strings.TrimSpace(preview) == "" {
		logx.Infof("conversation", "status finalize skip-empty-preview parent_convo=%q parent_turn=%q message_id=%q status=%q", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID), strings.TrimSpace(messageID), strings.TrimSpace(status))
		return nil
	}
	logx.Infof("conversation", "status finalize parent_convo=%q parent_turn=%q message_id=%q status=%q preview_len=%d preview_head=%q preview_tail=%q", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID), strings.TrimSpace(messageID), strings.TrimSpace(status), len(preview), textutil.Head(preview, 512), textutil.Tail(preview, 512))
	mu := apiconv.NewMessage()
	mu.SetId(messageID)
	mu.SetConversationID(parent.ConversationID)
	mu.SetTurnID(parent.TurnID)
	if strings.TrimSpace(preview) != "" {
		mu.SetContent(preview)
	}
	mu.SetInterim(0)
	if strings.TrimSpace(status) != "" {
		mu.SetStatus(normalizeMessageStatus(status))
	}
	if err := s.conv.PatchMessage(ctx, mu); err != nil {
		logx.Errorf("conversation", "status finalize error parent_convo=%q message_id=%q err=%v", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(messageID), err)
		return fmt.Errorf("status: finalize failed: %w", err)
	}
	logx.Infof("conversation", "status finalize ok parent_convo=%q message_id=%q status=%q", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(messageID), strings.TrimSpace(status))
	return nil
}

// PublishFinal creates a final assistant status message in the parent turn.
// It is used for detached/background completions where no interim status row
// exists yet but the parent conversation still needs a surfaced result.
func (s *Service) PublishFinal(ctx context.Context, parent runtimerequestctx.TurnMeta, toolName, role, actor, mode, linkedConversationID, status, preview string) (string, error) {
	if s == nil || s.conv == nil {
		return "", fmt.Errorf("status: conversation client not configured")
	}
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return "", nil
	}
	if strings.TrimSpace(role) == "" {
		role = "assistant"
	}
	if strings.TrimSpace(actor) == "" {
		actor = "tool"
	}
	if strings.TrimSpace(mode) == "" {
		mode = "exec"
	}
	msg, err := apiconv.AddMessage(ctx, s.conv, &parent,
		apiconv.WithRole(role),
		apiconv.WithInterim(0),
		apiconv.WithContent(preview),
		apiconv.WithCreatedByUserID(actor),
		apiconv.WithMode(mode),
		apiconv.WithToolName(mcpname.Display(toolName)),
		apiconv.WithLinkedConversationID(strings.TrimSpace(linkedConversationID)),
		apiconv.WithStatus(normalizeMessageStatus(status)),
	)
	if err != nil {
		return "", fmt.Errorf("status: publish final failed: %w", err)
	}
	return strings.TrimSpace(msg.Id), nil
}

func normalizeMessageStatus(status string) string {
	v := strings.ToLower(strings.TrimSpace(status))
	switch v {
	case "succeeded":
		return "completed"
	case "failed":
		return "error"
	case "canceled", "cancelled":
		return "cancel"
	case "running", "processing", "pending":
		return "open"
	case "auth-required", "auth_required":
		return "open"
	default:
		return v
	}
}

func ptrString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
