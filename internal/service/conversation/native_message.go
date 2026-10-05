package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	convcli "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/message/read"
	write "github.com/viant/agently-core/internal/datly/message/write"
	store "github.com/viant/agently-core/internal/store/conversation"
)

func (s *Service) messageStore() *store.MessageStore {
	return &store.MessageStore{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
}

func (s *Service) getMessageNative(ctx context.Context, id string, options ...convcli.Option) (*convcli.Message, error) {
	var selected convcli.Input
	for _, option := range options {
		if option != nil {
			option(&selected)
		}
	}
	modelCalls := selected.Has != nil && selected.Has.IncludeModelCal && selected.IncludeModelCal
	toolCalls := selected.Has != nil && selected.Has.IncludeToolCall && selected.IncludeToolCall
	row, err := s.messageStore().Get(ctx, id, modelCalls, toolCalls)
	if err != nil {
		return nil, err
	}
	return decodeNativeMessage(row)
}

func (s *Service) getMessageByElicitationNative(ctx context.Context, conversationID, elicitationID string) (*convcli.Message, error) {
	row, err := s.messageStore().ByElicitation(ctx, conversationID, elicitationID)
	if err != nil {
		return nil, err
	}
	return decodeNativeMessage(row)
}

func (s *Service) getMessageByParentElicitationNative(ctx context.Context, parentID, elicitationID string) (*convcli.Message, error) {
	row, err := s.messageStore().ByParentElicitation(ctx, parentID, elicitationID)
	if err != nil {
		return nil, err
	}
	return decodeNativeMessage(row)
}

func (s *Service) getMessageByLinkedElicitationNative(ctx context.Context, linkedID, elicitationID string) (*convcli.Message, error) {
	row, err := s.messageStore().ByLinkedElicitation(ctx, linkedID, elicitationID)
	if err != nil {
		return nil, err
	}
	return decodeNativeMessage(row)
}

func decodeNativeMessage(row *read.MessageView) (*convcli.Message, error) {
	if row == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, fmt.Errorf("encode native message: %w", err)
	}
	var result convcli.Message
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, fmt.Errorf("decode message contract: %w", err)
	}
	restoreNativePayloadBytes(&result, row)
	return &result, nil
}

func (s *Service) patchMessageNative(ctx context.Context, message *convcli.MutableMessage) error {
	if message == nil {
		return fmt.Errorf("message mutation is required")
	}
	row := &write.Message{}
	row.SetId(strings.TrimSpace(message.Id))
	if h := message.Has; h != nil {
		if h.Archived {
			row.SetArchived(message.Archived)
		}
		if h.ConversationID {
			row.SetConversationId(message.ConversationID)
		}
		if h.TurnID {
			row.SetTurnId(message.TurnID)
		}
		if h.Sequence {
			row.SetSequence(message.Sequence)
		}
		if h.CreatedAt {
			row.SetCreatedAt(message.CreatedAt)
		}
		if h.UpdatedAt {
			row.SetUpdatedAt(message.UpdatedAt)
		}
		if h.CreatedByUserID {
			row.SetCreatedByUserId(message.CreatedByUserID)
		}
		if h.Mode {
			row.SetMode(message.Mode)
		}
		if h.Role {
			row.SetRole(message.Role)
		}
		if h.Status {
			row.SetStatus(message.Status)
		}
		if h.Type {
			row.SetType(message.Type)
		}
		if h.Content {
			row.SetContent(message.Content)
		}
		if h.RawContent {
			row.SetRawContent(message.RawContent)
		}
		if h.Summary {
			row.SetSummary(message.Summary)
		}
		if h.ContextSummary {
			row.SetContextSummary(message.ContextSummary)
		}
		if h.EmbeddingIndex {
			row.SetEmbeddingIndex(message.EmbeddingIndex)
		}
		if h.Tags {
			row.SetTags(message.Tags)
		}
		if h.Interim {
			row.SetInterim(message.Interim)
		}
		if h.ElicitationID {
			row.SetElicitationId(message.ElicitationID)
		}
		if h.ParentMessageID {
			row.SetParentMessageId(message.ParentMessageID)
		}
		if h.SupersededBy {
			row.SetSupersededBy(message.SupersededBy)
		}
		if h.LinkedConversationID {
			row.SetLinkedConversationId(message.LinkedConversationID)
		}
		if h.ToolName {
			row.SetToolName(message.ToolName)
		}
		if h.Narration {
			row.SetNarration(message.Narration)
		}
		if h.Iteration {
			row.SetIteration(message.Iteration)
		}
		if h.Phase {
			row.SetPhase(message.Phase)
		}
		if h.AttachmentPayloadID {
			row.SetAttachmentPayloadId(message.AttachmentPayloadID)
		}
		if h.ElicitationPayloadID {
			row.SetElicitationPayloadId(message.ElicitationPayloadID)
		}
	}
	initial := nativePresence(row)
	output, err := s.messageStore().PatchTrustedResult(ctx, row)
	if err != nil {
		return err
	}
	return applyNativeMutation(message, output.Data, initial)
}
