package linking

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/shared"
)

// Service encapsulates helpers to create child conversations linked to a parent
// turn and to add parent-side link messages. It centralizes conversation
// linkage so both internal and external agent runs can rely on consistent
// behavior.
type Service struct {
	conv      apiconv.Client
	streamPub streaming.Publisher
}

// New returns a new linking Service.
func New(c apiconv.Client) *Service { return &Service{conv: c} }

// SetStreamPublisher wires a streaming publisher so the service can emit
// canonical linked-conversation events to the SSE bus.
func (s *Service) SetStreamPublisher(p streaming.Publisher) {
	if s == nil {
		return
	}
	s.streamPub = p
}

func (s *Service) emitLinkedConversationAttached(ctx context.Context, parent runtimerequestctx.TurnMeta, childConversationID, toolCallID, childAgentID, childTitle string) {
	if s == nil || s.streamPub == nil {
		return
	}
	debugf("emitLinkedConversationAttached parent_convo=%q parent_turn=%q child_convo=%q tool_call=%q", parent.ConversationID, parent.TurnID, childConversationID, toolCallID)
	toolMessageID := strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx))
	modelMessageID := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	messageID := toolMessageID
	if messageID == "" {
		messageID = modelMessageID
	}
	if messageID == "" {
		messageID = strings.TrimSpace(parent.ParentMessageID)
	}
	event := &streaming.Event{
		StreamID:                  strings.TrimSpace(parent.ConversationID),
		ConversationID:            strings.TrimSpace(parent.ConversationID),
		TurnID:                    strings.TrimSpace(parent.TurnID),
		MessageID:                 messageID,
		Type:                      streaming.EventTypeLinkedConversationAttached,
		LinkedConversationID:      strings.TrimSpace(childConversationID),
		LinkedConversationAgentID: strings.TrimSpace(childAgentID),
		LinkedConversationTitle:   strings.TrimSpace(childTitle),
		ToolCallID:                strings.TrimSpace(toolCallID),
		ToolMessageID:             toolMessageID,
		AssistantMessageID:        modelMessageID,
		CreatedAt:                 time.Now(),
	}
	event.NormalizeIdentity(strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID))
	if err := s.streamPub.Publish(ctx, event); err != nil {
		warnf("linked_conversation_attached publish error parent_convo=%q child_convo=%q err=%v", parent.ConversationID, childConversationID, err)
	}
	debugf("emitLinkedConversationAttached ok parent_convo=%q child_convo=%q", parent.ConversationID, childConversationID)
}

func (s *Service) EmitLinkedConversationAttached(ctx context.Context, parent runtimerequestctx.TurnMeta, childConversationID, toolCallID, childAgentID, childTitle string) {
	s.emitLinkedConversationAttached(ctx, parent, childConversationID, toolCallID, childAgentID, childTitle)
}

// CreateLinkedConversation creates a new conversation linked to the provided
// parent turn (by conversation/turn id). When cloneTranscript is true and a
// transcript is provided, it clones the last transcript into the new
// conversation for context.
func (s *Service) CreateLinkedConversation(ctx context.Context, parent runtimerequestctx.TurnMeta, cloneTranscript bool, transcript apiconv.Transcript) (string, error) {
	childID := uuid.New().String()
	debugf("CreateLinkedConversation parent_convo=%q parent_turn=%q child_convo=%q streamPub_nil=%v", parent.ConversationID, parent.TurnID, childID, s.streamPub == nil)
	debugf("CreateLinkedConversation start parent_convo=%q parent_turn=%q child_convo=%q clone=%v transcript_len=%d", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID), strings.TrimSpace(childID), cloneTranscript, len(transcript))
	// Create child conversation and set parent ids
	w := convw.Conversation{Has: &convw.ConversationHas{}}
	w.SetId(childID)
	w.SetVisibility(convw.VisibilityPublic)
	if uid := strings.TrimSpace(authctx.EffectiveUserID(ctx)); uid != "" {
		w.SetCreatedByUserID(uid)
	}
	if strings.TrimSpace(parent.ConversationID) != "" {
		w.SetConversationParentId(parent.ConversationID)
	}
	if strings.TrimSpace(parent.TurnID) != "" {
		w.SetConversationParentTurnId(parent.TurnID)
	}
	if err := s.conv.PatchConversations(ctx, (*apiconv.MutableConversation)(&w)); err != nil {
		errorf("CreateLinkedConversation patch error parent_convo=%q parent_turn=%q child_convo=%q err=%v", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID), strings.TrimSpace(childID), err)
		return "", fmt.Errorf("linking: create conversation failed: %w", err)
	}
	if cloneTranscript && transcript != nil {
		// Clone messages (excluding chain-mode supervised follow-ups) as a single synthetic turn
		if err := s.cloneMessages(ctx, transcript, childID); err != nil {
			errorf("CreateLinkedConversation clone error child_convo=%q err=%v", strings.TrimSpace(childID), err)
			return "", err
		}
	}
	debugf("CreateLinkedConversation ok parent_convo=%q parent_turn=%q child_convo=%q", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID), strings.TrimSpace(childID))
	// ToolMessageID is used as the toolCallId in the SSE event — the UI matches
	// against both toolCallId and toolMessageId on the step, so this works.
	return childID, nil
}

// AddLinkMessage adds an interim message to the parent turn with a linked
// conversation id so UIs and tooling can navigate to the child.
func (s *Service) AddLinkMessage(ctx context.Context, parent runtimerequestctx.TurnMeta, childConversationID, role, actor, mode string, content string) error {
	if s == nil || s.conv == nil {
		return fmt.Errorf("linking: conversation client not configured")
	}
	debugf("AddLinkMessage start parent_convo=%q parent_turn=%q child_convo=%q role=%q actor=%q mode=%q content_len=%d content_head=%q content_tail=%q", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID), strings.TrimSpace(childConversationID), strings.TrimSpace(role), strings.TrimSpace(actor), strings.TrimSpace(mode), len(content), headString(content, 512), tailString(content, 512))
	if strings.TrimSpace(role) == "" {
		role = "assistant"
	}
	if strings.TrimSpace(actor) == "" {
		actor = "link"
	}
	if strings.TrimSpace(mode) == "" {
		mode = "link"
	}
	_, err := apiconv.AddMessage(ctx, s.conv, &parent,
		apiconv.WithId(uuid.New().String()),
		apiconv.WithRole(role),
		apiconv.WithInterim(1),
		apiconv.WithContent(content),
		apiconv.WithCreatedByUserID(actor),
		apiconv.WithMode(mode),
		apiconv.WithLinkedConversationID(childConversationID),
	)
	if err != nil {
		errorf("AddLinkMessage error parent_convo=%q child_convo=%q err=%v", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(childConversationID), err)
		return fmt.Errorf("linking: add link message failed: %w", err)
	}
	debugf("AddLinkMessage ok parent_convo=%q child_convo=%q", strings.TrimSpace(parent.ConversationID), strings.TrimSpace(childConversationID))
	s.emitLinkedConversationAttached(ctx, parent, childConversationID, "", "", "")
	return nil
}

// cloneMessages clones the last transcript into a new conversation under a
// synthetic turn, excluding messages with mode == "chain" (supervised follow-ups).
func (s *Service) cloneMessages(ctx context.Context, transcript apiconv.Transcript, conversationID string) error {
	if len(transcript) == 0 {
		return nil
	}
	debugf("cloneMessages start convo=%q transcript_len=%d", strings.TrimSpace(conversationID), len(transcript))
	turnID := uuid.New().String()
	turn := runtimerequestctx.TurnMeta{ParentMessageID: turnID, TurnID: turnID, ConversationID: conversationID}
	mt := apiconv.NewTurn()
	mt.SetId(turn.TurnID)
	mt.SetConversationID(turn.ConversationID)
	mt.SetStatus("running")
	if err := s.conv.PatchTurn(ctx, mt); err != nil {
		errorf("cloneMessages patch turn error convo=%q turn=%q err=%v", strings.TrimSpace(conversationID), strings.TrimSpace(turnID), err)
		return fmt.Errorf("linking: start synthetic turn failed: %w", err)
	}
	last := transcript[0]
	msgs := last.GetMessages()
	debugf("cloneMessages source messages convo=%q count=%d", strings.TrimSpace(conversationID), len(msgs))
	cloned := 0
	for _, m := range msgs {
		if m.Mode != nil && *m.Mode == "chain" {
			continue
		}
		mut := m.NewMutable()
		mut.SetId(uuid.New().String())
		mut.SetTurnID(turn.TurnID)
		mut.SetConversationID(turn.ConversationID)
		mut.SetParentMessageID(turn.ParentMessageID)
		if mut.Status != nil && strings.TrimSpace(*mut.Status) != "" {
			mut.SetStatus(shared.NormalizeMessageStatus(*mut.Status))
		}
		if err := s.conv.PatchMessage(ctx, mut); err != nil {
			errorf("cloneMessages patch message error convo=%q turn=%q msg=%q err=%v", strings.TrimSpace(conversationID), strings.TrimSpace(turnID), strings.TrimSpace(mut.Id), err)
			return fmt.Errorf(
				"linking: clone message failed (id=%s convo=%s turn=%s role=%s type=%s status=%q): %w",
				mut.Id,
				turn.ConversationID,
				turn.TurnID,
				strings.TrimSpace(mut.Role),
				strings.TrimSpace(mut.Type),
				strings.TrimSpace(func() string {
					if mut.Status == nil {
						return ""
					}
					return *mut.Status
				}()),
				err,
			)
		}
		cloned++
	}
	debugf("cloneMessages ok convo=%q turn=%q cloned=%d", strings.TrimSpace(conversationID), strings.TrimSpace(turnID), cloned)
	return nil
}
