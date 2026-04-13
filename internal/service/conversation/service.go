package conversation

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/debugtrace"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	convdel "github.com/viant/agently-core/pkg/agently/conversation/delete"
	convlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	generatedfileread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	generatedfilewrite "github.com/viant/agently-core/pkg/agently/generatedfile/write"
	msgdel "github.com/viant/agently-core/pkg/agently/message/delete"
	messagelist "github.com/viant/agently-core/pkg/agently/message/list"
	messageread "github.com/viant/agently-core/pkg/agently/message/read"
	msgwrite "github.com/viant/agently-core/pkg/agently/message/write"
	modelcallwrite "github.com/viant/agently-core/pkg/agently/modelcall/write"
	payloadread "github.com/viant/agently-core/pkg/agently/payload/read"
	payloadwrite "github.com/viant/agently-core/pkg/agently/payload/write"
	queueRead "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queueWrite "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	toolread "github.com/viant/agently-core/pkg/agently/toolcall/read"
	toolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
	turnread "github.com/viant/agently-core/pkg/agently/turn/read"
	turnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
)

type Service struct {
	dao       *datly.Service
	streamPub streaming.Publisher
}

func (s *Service) SetStreamPublisher(p streaming.Publisher) {
	if s == nil {
		return
	}
	s.streamPub = p
}

// New constructs a conversation Service using the provided datly service
// and registers the rich conversation components.
func New(ctx context.Context, dao *datly.Service) (*Service, error) {
	if dao == nil {
		return nil, errors.New("conversation service requires a non-nil datly.Service")
	}
	srv := &Service{dao: dao}
	err := srv.init(ctx, dao)
	if err != nil {
		return nil, err
	}
	return srv, nil
}

func (s *Service) init(ctx context.Context, dao *datly.Service) error {
	if dao == nil {
		return errors.New("datly service was nil")
	}
	key := reflect.ValueOf(dao).Pointer()
	if _, loaded := componentsByDAO.LoadOrStore(key, struct{}{}); loaded {
		return nil
	}
	if err := agconv.DefineConversationComponent(ctx, dao); err != nil {
		return err
	}
	if err := convlist.DefineConversationRowsComponent(ctx, dao); err != nil {
		return err
	}
	if err := messageread.DefineMessageComponent(ctx, dao); err != nil {
		return err
	}
	if err := messageread.DefineMessageByElicitationComponent(ctx, dao); err != nil {
		return err
	}
	if err := messagelist.DefineMessageRowsComponent(ctx, dao); err != nil {
		return err
	}
	if err := payloadread.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if err := generatedfileread.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := convw.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := msgwrite.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := modelcallwrite.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := toolcallwrite.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := toolread.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := queueWrite.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if err := queueRead.DefineQueueRowsComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := turnwrite.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if err := turnread.DefineNextQueuedComponent(ctx, dao); err != nil {
		return err
	}
	if err := turnread.DefineActiveTurnComponent(ctx, dao); err != nil {
		return err
	}
	if err := turnread.DefineTurnByIDComponent(ctx, dao); err != nil {
		return err
	}
	if err := turnread.DefineQueuedCountComponent(ctx, dao); err != nil {
		return err
	}
	if err := turnread.DefineQueuedListComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := payloadwrite.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := generatedfilewrite.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := convdel.DefineComponent(ctx, dao); err != nil {
		return err
	}
	if _, err := msgdel.DefineComponent(ctx, dao); err != nil {
		return err
	}
	return nil
}

var componentsByDAO sync.Map

func (s *Service) PatchConversations(ctx context.Context, conversations *convcli.MutableConversation) error {
	if conversations != nil {
		debugf("PatchConversations start id=%q status=%q visibility=%q", strings.TrimSpace(conversations.Id), strings.TrimSpace(valueOrEmptyStr(conversations.Status)), strings.TrimSpace(valueOrEmptyStr(conversations.Visibility)))
	} else {
		debugf("PatchConversations start id=\"\" status=\"\" visibility=\"\" (nil input)")
	}
	conv := []*convw.Conversation{(*convw.Conversation)(conversations)}
	input := &convw.Input{Conversations: conv}
	out := &convw.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, convw.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	if err != nil {
		errorf("PatchConversations error id=%q err=%v", strings.TrimSpace(conversations.Id), err)
		return err
	}
	if len(out.Violations) > 0 {
		warnf("PatchConversations violation id=%q msg=%q", strings.TrimSpace(conversations.Id), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	debugf("PatchConversations ok id=%q", strings.TrimSpace(conversations.Id))
	return nil
}

// GetConversations implements conversation.API using the generated component and returns SDK Conversation.
func (s *Service) GetConversations(ctx context.Context, input *convcli.Input) ([]*convcli.Conversation, error) {
	listInput := &convlist.ConversationRowsInput{
		Has: &convlist.ConversationRowsInputHas{},
	}
	if input != nil && input.Has != nil {
		if input.Has.AgentId {
			listInput.AgentId = input.AgentId
			listInput.Has.AgentId = true
		}
		if input.Has.ParentId {
			listInput.ParentId = input.ParentId
			listInput.Has.ParentId = true
		}
		if input.Has.ParentTurnId {
			listInput.ParentTurnId = input.ParentTurnId
			listInput.Has.ParentTurnId = true
		}
		if input.Has.ExcludeChildren {
			listInput.ExcludeChildren = input.ExcludeChildren
			listInput.Has.ExcludeChildren = true
		}
		if input.Has.ExcludeScheduled {
			listInput.ExcludeScheduled = input.ExcludeScheduled
			listInput.Has.ExcludeScheduled = true
		}
		if input.Has.ScheduleId {
			listInput.ScheduleId = input.ScheduleId
			listInput.Has.ScheduleId = true
		}
		if input.Has.ScheduleRunId {
			listInput.ScheduleRunId = input.ScheduleRunId
			listInput.Has.ScheduleRunId = true
		}
		if input.Has.Query {
			listInput.Query = input.Query
			listInput.Has.Query = true
		}
		if input.Has.StatusFilter {
			listInput.StatusFilter = input.StatusFilter
			listInput.Has.StatusFilter = true
		}
	}
	if !listInput.Has.ParentId && !listInput.Has.ParentTurnId && !listInput.Has.ExcludeChildren {
		listInput.ExcludeChildren = true
		listInput.Has.ExcludeChildren = true
	}
	if strings.TrimSpace(authctx.EffectiveUserID(ctx)) == "" {
		listInput.DefaultPredicate = "1"
		listInput.Has.DefaultPredicate = true
	}
	out := &convlist.ConversationRowsOutput{}
	if _, err := s.dao.Operate(ctx,
		datly.WithOutput(out),
		datly.WithURI(convlist.ConversationRowsPathURI),
		datly.WithInput(listInput),
	); err != nil {
		return nil, err
	}
	result := make([]*convcli.Conversation, 0, len(out.Data))
	for _, row := range out.Data {
		if row == nil {
			continue
		}
		conv := convcli.Conversation{
			LastTurnId:               row.LastTurnId,
			Stage:                    row.Stage,
			Id:                       row.Id,
			Summary:                  row.Summary,
			LastActivity:             row.LastActivity,
			UsageInputTokens:         row.UsageInputTokens,
			UsageOutputTokens:        row.UsageOutputTokens,
			UsageEmbeddingTokens:     row.UsageEmbeddingTokens,
			CreatedAt:                row.CreatedAt,
			UpdatedAt:                row.UpdatedAt,
			CreatedByUserId:          row.CreatedByUserId,
			AgentId:                  row.AgentId,
			DefaultModelProvider:     row.DefaultModelProvider,
			DefaultModel:             row.DefaultModel,
			DefaultModelParams:       row.DefaultModelParams,
			Title:                    row.Title,
			ConversationParentId:     row.ConversationParentId,
			ConversationParentTurnId: row.ConversationParentTurnId,
			Metadata:                 row.Metadata,
			Visibility:               row.Visibility,
			Shareable:                row.Shareable,
			Status:                   row.Status,
			Scheduled:                row.Scheduled,
			ScheduleId:               row.ScheduleId,
			ScheduleRunId:            row.ScheduleRunId,
			ScheduleKind:             row.ScheduleKind,
			ScheduleTimezone:         row.ScheduleTimezone,
			ScheduleCronExpr:         row.ScheduleCronExpr,
			ExternalTaskRef:          row.ExternalTaskRef,
		}
		result = append(result, &conv)
	}
	return result, nil
}

// GetConversation implements conversation.API using the generated component and returns SDK Conversation.
func (s *Service) GetConversation(ctx context.Context, id string, options ...convcli.Option) (*convcli.Conversation, error) {
	// Build SDK input via options
	inSDK := convcli.Input{Id: id, Has: &agconv.ConversationInputHas{Id: true}}
	for _, opt := range options {
		if opt != nil {
			opt(&inSDK)
		}
	}
	// Map SDK input to generated input
	in := agconv.ConversationInput(inSDK)

	out := &agconv.ConversationOutput{}
	uri := strings.ReplaceAll(agconv.ConversationPathURI, "{id}", id)
	if _, err := s.dao.Operate(ctx, datly.WithOutput(out), datly.WithURI(uri), datly.WithInput(&in)); err != nil {
		return nil, err
	}

	if len(out.Data) == 0 {
		// Some include flags (tool/model call expansion) can over-constrain the
		// generated SQL shape and return no rows even when the base conversation exists.
		// Fallback to a base fetch to preserve API semantics for callers that need
		// existence checks first and can fetch rich transcript separately.
		if inSDK.IncludeToolCall || inSDK.IncludeModelCal || inSDK.IncludeTranscript {
			baseIn := agconv.ConversationInput{Id: id, Has: &agconv.ConversationInputHas{Id: true}}
			baseOut := &agconv.ConversationOutput{}
			if _, err := s.dao.Operate(ctx, datly.WithOutput(baseOut), datly.WithURI(uri), datly.WithInput(&baseIn)); err != nil {
				return nil, err
			}
			if len(baseOut.Data) == 0 {
				// No conversation found; mirror API behavior by returning nil without logging.
				return nil, nil
			}
			conv := convcli.Conversation(*baseOut.Data[0])
			return &conv, nil
		}
		// No conversation found; mirror API behavior by returning nil without logging.
		return nil, nil
	}
	// Cast generated to SDK type
	conv := convcli.Conversation(*out.Data[0])
	return &conv, nil
}

func (s *Service) GetPayload(ctx context.Context, id string) (*convcli.Payload, error) {
	if s == nil || s.dao == nil {
		return nil, errors.New("conversation service not configured: dao is nil")
	}
	in := payloadread.Input{Id: id, Has: &payloadread.Has{Id: true}}
	out := &payloadread.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithOutput(out), datly.WithURI(payloadread.PayloadURI), datly.WithInput(&in)); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	res := convcli.Payload(*out.Data[0])
	return &res, nil
}

func (s *Service) PatchPayload(ctx context.Context, payload *convcli.MutablePayload) error {
	if s == nil || s.dao == nil {
		return errors.New("conversation service not configured: dao is nil")
	}
	if payload == nil {
		return errors.New("invalid payload: nil")
	}
	logPayloadDebug := payload.SizeBytes%512 == 0
	if logPayloadDebug {
		debugf("PatchPayload start id=%q kind=%q mime=%q size_bytes=%d", strings.TrimSpace(payload.Id), strings.TrimSpace(payload.Kind), strings.TrimSpace(payload.MimeType), payload.SizeBytes)
	}
	// MutablePayload is an alias of pkg/agently/payload.Payload
	pw := (*payloadwrite.Payload)(payload)
	input := &payloadwrite.Input{Payloads: []*payloadwrite.Payload{pw}}
	out := &payloadwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, payloadwrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	if err != nil {
		errorf("PatchPayload error id=%q err=%v", strings.TrimSpace(payload.Id), err)
		return err
	}
	if len(out.Violations) > 0 {
		warnf("PatchPayload violation id=%q msg=%q", strings.TrimSpace(payload.Id), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	if logPayloadDebug {
		debugf("PatchPayload ok id=%q", strings.TrimSpace(payload.Id))
	}
	return nil
}

func (s *Service) GetGeneratedFiles(ctx context.Context, input *generatedfileread.Input) ([]*generatedfileread.GeneratedFileView, error) {
	if s == nil || s.dao == nil {
		return nil, errors.New("conversation service not configured: dao is nil")
	}
	in := generatedfileread.Input{}
	if input != nil {
		in = *input
	}
	if in.Has == nil {
		in.Has = &generatedfileread.Has{}
	}
	out := &generatedfileread.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithOutput(out), datly.WithURI(generatedfileread.URI), datly.WithInput(&in)); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (s *Service) PatchGeneratedFile(ctx context.Context, generatedFile *generatedfilewrite.GeneratedFile) error {
	if s == nil || s.dao == nil {
		return errors.New("conversation service not configured: dao is nil")
	}
	if generatedFile == nil {
		return errors.New("invalid generated file: nil")
	}
	debugf("PatchGeneratedFile start id=%q provider=%q mode=%q status=%q", strings.TrimSpace(generatedFile.ID), strings.TrimSpace(generatedFile.Provider), strings.TrimSpace(generatedFile.Mode), strings.TrimSpace(generatedFile.Status))
	input := &generatedfilewrite.Input{GeneratedFiles: []*generatedfilewrite.GeneratedFile{generatedFile}}
	out := &generatedfilewrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, generatedfilewrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	if err != nil {
		errorf("PatchGeneratedFile error id=%q err=%v", strings.TrimSpace(generatedFile.ID), err)
		return err
	}
	if len(out.Violations) > 0 {
		warnf("PatchGeneratedFile violation id=%q msg=%q", strings.TrimSpace(generatedFile.ID), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	debugf("PatchGeneratedFile ok id=%q", strings.TrimSpace(generatedFile.ID))
	return nil
}

func (s *Service) GetMessage(ctx context.Context, id string, options ...convcli.Option) (*convcli.Message, error) {
	if s == nil || s.dao == nil {
		return nil, errors.New("conversation service not configured: dao is nil")
	}
	// Map conversation-style options to message read input flags
	var convIn convcli.Input
	for _, opt := range options {
		if opt == nil {
			continue
		}
		opt(&convIn)
	}
	in := messageread.MessageInput{Id: id, Has: &messageread.MessageInputHas{Id: true}}
	if convIn.Has != nil {
		if convIn.Has.IncludeToolCall && convIn.IncludeToolCall {
			in.IncludeToolCall = true
			in.Has.IncludeToolCall = true
		}
		if convIn.Has.IncludeModelCal && convIn.IncludeModelCal {
			in.IncludeModelCal = true
			in.Has.IncludeModelCal = true
		}
	}

	uri := strings.ReplaceAll(messageread.MessagePathURI, "{id}", id)
	out := &messageread.MessageOutput{}
	if _, err := s.dao.Operate(ctx, datly.WithOutput(out), datly.WithURI(uri), datly.WithInput(&in)); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	row := out.Data[0]
	res := convcli.Message{
		Id:                   row.Id,
		ConversationId:       row.ConversationId,
		TurnId:               row.TurnId,
		Archived:             row.Archived,
		Sequence:             row.Sequence,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
		CreatedByUserId:      row.CreatedByUserId,
		Status:               row.Status,
		Mode:                 row.Mode,
		Role:                 row.Role,
		Type:                 row.Type,
		Content:              row.Content,
		RawContent:           row.RawContent,
		Summary:              row.Summary,
		ContextSummary:       row.ContextSummary,
		Tags:                 row.Tags,
		Interim:              row.Interim,
		ElicitationId:        row.ElicitationId,
		ParentMessageId:      row.ParentMessageId,
		SupersededBy:         row.SupersededBy,
		LinkedConversationId: row.LinkedConversationId,
		AttachmentPayloadId:  row.AttachmentPayloadId,
		ElicitationPayloadId: row.ElicitationPayloadId,
		ToolName:             row.ToolName,
		EmbeddingIndex:       row.EmbeddingIndex,
		Preamble:             row.Preamble,
		Iteration:            row.Iteration,
		Phase:                row.Phase,
	}
	return &res, nil
}

func (s *Service) GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string) (*convcli.Message, error) {
	if s == nil || s.dao == nil {
		return nil, errors.New("conversation service not configured: dao is nil")
	}
	in := messageread.MessageByElicitationInput{ConversationId: conversationID, ElicitationId: elicitationID}
	out := &messageread.MessageByElicitationOutput{}
	uri := messageread.MessageByElicitationPathURI
	if _, err := s.dao.Operate(ctx, datly.WithOutput(out), datly.WithURI(uri), datly.WithInput(&in)); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	row := out.Data[0]
	res := convcli.Message{
		Id:                   row.Id,
		ConversationId:       row.ConversationId,
		TurnId:               row.TurnId,
		Archived:             row.Archived,
		Sequence:             row.Sequence,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
		CreatedByUserId:      row.CreatedByUserId,
		Status:               row.Status,
		Mode:                 row.Mode,
		Role:                 row.Role,
		Type:                 row.Type,
		Content:              row.Content,
		RawContent:           row.RawContent,
		Summary:              row.Summary,
		ContextSummary:       row.ContextSummary,
		Tags:                 row.Tags,
		Interim:              row.Interim,
		ElicitationId:        row.ElicitationId,
		ParentMessageId:      row.ParentMessageId,
		SupersededBy:         row.SupersededBy,
		LinkedConversationId: row.LinkedConversationId,
		AttachmentPayloadId:  row.AttachmentPayloadId,
		ElicitationPayloadId: row.ElicitationPayloadId,
		ToolName:             row.ToolName,
		EmbeddingIndex:       row.EmbeddingIndex,
		Preamble:             row.Preamble,
		Iteration:            row.Iteration,
		Phase:                row.Phase,
	}
	return &res, nil
}

func (s *Service) PatchMessage(ctx context.Context, message *convcli.MutableMessage) error {
	if message != nil {
		debugf("PatchMessage start id=%q convo=%q turn=%v role=%q type=%q status=%q", message.Id, message.ConversationID, valueOrEmpty(message.TurnID), strings.TrimSpace(message.Role), strings.TrimSpace(message.Type), strings.TrimSpace(valueOrEmptyStr(message.Status)))
	} else {
		debugf("PatchMessage start id=\"\" convo=\"\" turn=\"\" role=\"\" type=\"\" status=\"\" (nil input)")
	}
	mm := (*msgwrite.Message)(message)
	input := &msgwrite.Input{Messages: []*msgwrite.Message{mm}}
	out := &msgwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, msgwrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	if err != nil {
		// Augment DB/validation error with key message fields to aid diagnosis
		errorf("PatchMessage error id=%q convo=%q err=%v", message.Id, message.ConversationID, err)
		return fmt.Errorf(
			"patch message failed (id=%s convo=%s turn=%v role=%s type=%s status=%q): %w",
			message.Id,
			message.ConversationID,
			valueOrEmpty(message.TurnID),
			strings.TrimSpace(message.Role),
			strings.TrimSpace(message.Type),
			strings.TrimSpace(valueOrEmptyStr(message.Status)),
			err,
		)
	}
	if len(out.Violations) > 0 {
		warnf("PatchMessage violation id=%q convo=%q msg=%q", message.Id, message.ConversationID, out.Violations[0].Message)
		return fmt.Errorf(
			"patch message violation (id=%s convo=%s turn=%v role=%s type=%s status=%q): %s",
			message.Id,
			message.ConversationID,
			valueOrEmpty(message.TurnID),
			strings.TrimSpace(message.Role),
			strings.TrimSpace(message.Type),
			strings.TrimSpace(valueOrEmptyStr(message.Status)),
			out.Violations[0].Message,
		)
	}
	s.publishMessagePatchEvent(ctx, message)
	debugf("PatchMessage ok id=%q convo=%q", message.Id, message.ConversationID)
	return nil
}

func (s *Service) publishMessagePatchEvent(ctx context.Context, message *convcli.MutableMessage) {
	if s == nil || s.streamPub == nil || message == nil {
		return
	}
	conversationID := strings.TrimSpace(message.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	}
	if conversationID == "" {
		return
	}
	if shouldSuppressMessagePatchEvent(ctx, message) {
		s.emitCanonicalAssistantEvents(ctx, message, conversationID)
		return
	}
	patch := messagePatchPayload(message)
	if len(patch) == 0 {
		return
	}
	event := &streaming.Event{
		ID:             strings.TrimSpace(message.Id),
		StreamID:       conversationID,
		ConversationID: conversationID,
		MessageID:      strings.TrimSpace(message.Id),
		Mode:           strings.TrimSpace(valueOrEmptyStr(message.Mode)),
		Type:           streaming.EventTypeControl,
		Op:             "message_patch",
		Patch:          patch,
		CreatedAt:      patchEventCreatedAt(message),
	}
	s.emitTimelineEvent(ctx, event, "PatchMessage publish event")
	s.emitCanonicalAssistantEvents(ctx, message, conversationID)
}

func shouldSuppressMessagePatchEvent(ctx context.Context, message *convcli.MutableMessage) bool {
	if message == nil {
		return false
	}
	if isToolStatusMessage(message) {
		return true
	}
	if isToolMessage(message) {
		return true
	}
	toolMessageID := strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx))
	return toolMessageID != "" && toolMessageID == strings.TrimSpace(message.Id)
}

func isToolMessage(message *convcli.MutableMessage) bool {
	if message == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(message.Role), "tool") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(message.Type), "tool_op")
}

func isToolStatusMessage(message *convcli.MutableMessage) bool {
	if message == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
		return false
	}
	if message.CreatedByUserID == nil || !strings.EqualFold(strings.TrimSpace(*message.CreatedByUserID), "tool") {
		return false
	}
	if message.Mode == nil || !strings.EqualFold(strings.TrimSpace(*message.Mode), "exec") {
		return false
	}
	return message.ToolName != nil && strings.TrimSpace(*message.ToolName) != ""
}

func messagePatchPayload(message *convcli.MutableMessage) map[string]interface{} {
	if message == nil || message.Has == nil {
		return nil
	}
	out := map[string]interface{}{}
	if message.Has.LinkedConversationID && message.LinkedConversationID != nil {
		out["linkedConversationId"] = strings.TrimSpace(*message.LinkedConversationID)
	}
	if message.Has.ParentMessageID && message.ParentMessageID != nil {
		out["parentMessageId"] = strings.TrimSpace(*message.ParentMessageID)
	}
	if message.Has.TurnID && message.TurnID != nil {
		out["turnId"] = strings.TrimSpace(*message.TurnID)
	}
	if message.Has.Status && message.Status != nil {
		out["status"] = strings.TrimSpace(*message.Status)
	}
	if message.Has.ToolName && message.ToolName != nil {
		out["toolName"] = mcpname.Display(strings.TrimSpace(*message.ToolName))
	}
	if message.Has.Interim && message.Interim != nil {
		out["interim"] = *message.Interim
	}
	if message.Has.Preamble && message.Preamble != nil {
		out["preamble"] = strings.TrimSpace(*message.Preamble)
	}
	if message.Has.Content && message.Content != nil {
		out["content"] = *message.Content
	}
	if message.Has.RawContent && message.RawContent != nil && strings.TrimSpace(*message.RawContent) != "" {
		out["rawContent"] = *message.RawContent
	}
	if message.Has.Role && strings.TrimSpace(message.Role) != "" {
		out["role"] = strings.TrimSpace(message.Role)
	}
	if message.Has.Mode && strings.TrimSpace(valueOrEmptyStr(message.Mode)) != "" {
		out["mode"] = strings.TrimSpace(valueOrEmptyStr(message.Mode))
	}
	if message.Has.Type && strings.TrimSpace(message.Type) != "" {
		out["messageType"] = strings.TrimSpace(message.Type)
	}
	if message.Has.CreatedAt && message.CreatedAt != nil && !message.CreatedAt.IsZero() {
		out["createdAt"] = message.CreatedAt.Format(time.RFC3339Nano)
	}
	if message.Has.Sequence && message.Sequence != nil {
		out["sequence"] = *message.Sequence
	}
	if message.Has.Iteration && message.Iteration != nil {
		out["iteration"] = *message.Iteration
	}
	return out
}

func patchEventCreatedAt(message *convcli.MutableMessage) time.Time {
	if message != nil && message.Has != nil && message.Has.CreatedAt && message.CreatedAt != nil && !message.CreatedAt.IsZero() {
		return *message.CreatedAt
	}
	return time.Now()
}

func turnEventCreatedAt(turn *convcli.MutableTurn) time.Time {
	if turn != nil && turn.Has != nil && turn.Has.CreatedAt && turn.CreatedAt != nil && !turn.CreatedAt.IsZero() {
		return *turn.CreatedAt
	}
	return time.Now()
}

func valueOrZeroInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func applyIterationPage(event *streaming.Event, iteration *int) {
	if event == nil || iteration == nil || *iteration <= 0 {
		return
	}
	event.Iteration = *iteration
	event.PageIndex = *iteration
	event.PageCount = *iteration
	event.LatestPage = true
}

func timelineDebugFields(event *streaming.Event) map[string]any {
	if event == nil {
		return nil
	}
	fields := map[string]any{
		"type":                      string(event.Type),
		"op":                        strings.TrimSpace(event.Op),
		"streamID":                  strings.TrimSpace(event.StreamID),
		"conversationID":            strings.TrimSpace(event.ConversationID),
		"turnID":                    strings.TrimSpace(event.TurnID),
		"messageID":                 strings.TrimSpace(event.MessageID),
		"eventSeq":                  event.EventSeq,
		"agentIDUsed":               strings.TrimSpace(event.AgentIDUsed),
		"agentName":                 strings.TrimSpace(event.AgentName),
		"assistantID":               strings.TrimSpace(event.AssistantMessageID),
		"parentID":                  strings.TrimSpace(event.ParentMessageID),
		"userMessageID":             strings.TrimSpace(event.UserMessageID),
		"modelCallID":               strings.TrimSpace(event.ModelCallID),
		"requestID":                 strings.TrimSpace(event.RequestID),
		"responseID":                strings.TrimSpace(event.ResponseID),
		"toolCallID":                strings.TrimSpace(event.ToolCallID),
		"toolMessageID":             strings.TrimSpace(event.ToolMessageID),
		"requestPayloadID":          strings.TrimSpace(event.RequestPayloadID),
		"responsePayloadID":         strings.TrimSpace(event.ResponsePayloadID),
		"providerRequestPayloadID":  strings.TrimSpace(event.ProviderRequestPayloadID),
		"providerResponsePayloadID": strings.TrimSpace(event.ProviderResponsePayloadID),
		"streamPayloadID":           strings.TrimSpace(event.StreamPayloadID),
		"linkedConversationID":      strings.TrimSpace(event.LinkedConversationID),
		"mode":                      strings.TrimSpace(event.Mode),
		"status":                    strings.TrimSpace(event.Status),
		"iteration":                 event.Iteration,
		"pageIndex":                 event.PageIndex,
		"pageCount":                 event.PageCount,
		"latestPage":                event.LatestPage,
		"finalResponse":             event.FinalResponse,
		"toolName":                  strings.TrimSpace(event.ToolName),
		"provider":                  strings.TrimSpace(event.Provider),
		"modelName":                 strings.TrimSpace(event.ModelName),
		"feedID":                    strings.TrimSpace(event.FeedID),
		"createdAt":                 event.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if event.StartedAt != nil && !event.StartedAt.IsZero() {
		fields["startedAt"] = event.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if event.CompletedAt != nil && !event.CompletedAt.IsZero() {
		fields["completedAt"] = event.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(event.Content) != "" {
		fields["contentPreview"] = event.Content
	}
	if strings.TrimSpace(event.Preamble) != "" {
		fields["preamblePreview"] = event.Preamble
	}
	if event.Model != nil {
		fields["model"] = map[string]any{
			"provider": strings.TrimSpace(event.Model.Provider),
			"model":    strings.TrimSpace(event.Model.Model),
			"kind":     strings.TrimSpace(event.Model.Kind),
		}
	}
	if len(event.ToolCallsPlanned) > 0 {
		fields["toolCallsPlanned"] = event.ToolCallsPlanned
	}
	return fields
}

func (s *Service) emitTimelineEvent(ctx context.Context, event *streaming.Event, action string) {
	if s == nil || s.streamPub == nil || event == nil {
		return
	}
	fallbackConversationID := ""
	fallbackTurnID := ""
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		fallbackConversationID = strings.TrimSpace(turn.ConversationID)
		fallbackTurnID = strings.TrimSpace(turn.TurnID)
	}
	event.NormalizeIdentity(fallbackConversationID, fallbackTurnID)
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	startedAt := ""
	if event.StartedAt != nil && !event.StartedAt.IsZero() {
		startedAt = event.StartedAt.Format(time.RFC3339Nano)
	}
	completedAt := ""
	if event.CompletedAt != nil && !event.CompletedAt.IsZero() {
		completedAt = event.CompletedAt.Format(time.RFC3339Nano)
	}
	log.Printf("[emitTimelineEvent] %s type=%q op=%q stream_id=%q convo=%q turn=%q msg=%q seq=%d mode=%q agent=%q agent_name=%q user_msg=%q assistant_msg=%q parent_msg=%q model_call=%q tool_call=%q tool_msg=%q tool=%q status=%q final=%v iter=%d page=%d/%d latest=%v linked=%q feed=%q created_at=%q started_at=%q completed_at=%q sent_at=%q req=%q resp=%q preq=%q presp=%q stream=%q id=%q",
		action,
		string(event.Type),
		event.Op,
		event.StreamID,
		event.ConversationID,
		event.TurnID,
		event.MessageID,
		event.EventSeq,
		event.Mode,
		event.AgentIDUsed,
		event.AgentName,
		event.UserMessageID,
		event.AssistantMessageID,
		event.ParentMessageID,
		event.ModelCallID,
		event.ToolCallID,
		event.ToolMessageID,
		event.ToolName,
		event.Status,
		event.FinalResponse,
		event.Iteration,
		event.PageIndex,
		event.PageCount,
		event.LatestPage,
		event.LinkedConversationID,
		event.FeedID,
		event.CreatedAt.Format(time.RFC3339Nano),
		startedAt,
		completedAt,
		time.Now().Format(time.RFC3339Nano),
		event.RequestPayloadID,
		event.ResponsePayloadID,
		event.ProviderRequestPayloadID,
		event.ProviderResponsePayloadID,
		event.StreamPayloadID,
		event.ID,
	)
	if err := s.streamPub.Publish(ctx, event); err != nil {
		warnf("%s error type=%q id=%q convo=%q err=%v", action, strings.TrimSpace(string(event.Type)), strings.TrimSpace(event.ID), strings.TrimSpace(event.ConversationID), err)
		return
	}
	if debugtrace.Enabled() {
		debugtrace.Write("conversation", "timeline", timelineDebugFields(event))
	}
}

func toolCallEvent(ctx context.Context, toolCall *convcli.MutableToolCall) *streaming.Event {
	if toolCall == nil {
		return nil
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	}
	if conversationID == "" {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(toolCall.Status))
	if status == "" {
		return nil
	}
	eventType := streaming.EventTypeToolCallStarted
	if status != "running" && status != "thinking" && status != "waiting_for_user" {
		eventType = streaming.EventTypeToolCallCompleted
	}
	event := &streaming.Event{
		ID:                 strings.TrimSpace(toolCall.MessageID),
		StreamID:           conversationID,
		ConversationID:     conversationID,
		MessageID:          strings.TrimSpace(toolCall.MessageID),
		Mode:               strings.TrimSpace(runtimerequestctx.RequestModeFromContext(ctx)),
		Type:               eventType,
		TurnID:             resolveTurnID(ctx, valueOrEmptyStr(toolCall.TurnID)),
		AssistantMessageID: strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx)),
		ParentMessageID:    strings.TrimSpace(turn.ParentMessageID),
		ToolCallID:         strings.TrimSpace(toolCall.OpID),
		ToolMessageID:      strings.TrimSpace(toolCall.MessageID),
		RequestID:          strings.TrimSpace(valueOrEmptyStr(toolCall.TraceID)),
		ResponseID:         strings.TrimSpace(valueOrEmptyStr(toolCall.TraceID)),
		RequestPayloadID:   strings.TrimSpace(valueOrEmptyStr(toolCall.RequestPayloadID)),
		ResponsePayloadID:  strings.TrimSpace(valueOrEmptyStr(toolCall.ResponsePayloadID)),
		ToolName:           mcpname.Display(strings.TrimSpace(toolCall.ToolName)),
		Status:             strings.TrimSpace(toolCall.Status),
		CreatedAt:          time.Now(),
	}
	if event.AssistantMessageID == "" {
		event.AssistantMessageID = strings.TrimSpace(turn.ParentMessageID)
	}
	if toolCall.Has != nil {
		if toolCall.Has.StartedAt && toolCall.StartedAt != nil && !toolCall.StartedAt.IsZero() {
			event.StartedAt = toolCall.StartedAt
			event.CreatedAt = *toolCall.StartedAt
		}
		if toolCall.Has.CompletedAt && toolCall.CompletedAt != nil && !toolCall.CompletedAt.IsZero() {
			event.CompletedAt = toolCall.CompletedAt
			if eventType == streaming.EventTypeToolCallCompleted {
				event.CreatedAt = *toolCall.CompletedAt
			}
		}
		applyIterationPage(event, toolCall.Iteration)
	}
	return event
}

// valueOrEmpty renders pointer values without exposing nil dereference in logs.
func valueOrEmpty[T any](p *T) interface{} {
	if p == nil {
		return ""
	}
	return *p
}

func valueOrEmptyStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Service) PatchModelCall(ctx context.Context, modelCall *convcli.MutableModelCall) error {
	if s == nil || s.dao == nil {
		return errors.New("conversation service not configured: dao is nil")
	}
	if modelCall == nil {
		return errors.New("invalid modelCall: nil")
	}
	debugf("PatchModelCall start message_id=%q turn_id=%q provider=%q model=%q status=%q", strings.TrimSpace(modelCall.MessageID), strings.TrimSpace(valueOrEmptyStr(modelCall.TurnID)), strings.TrimSpace(modelCall.Provider), strings.TrimSpace(modelCall.Model), strings.TrimSpace(modelCall.Status))
	mc := (*modelcallwrite.ModelCall)(modelCall)
	input := &modelcallwrite.Input{ModelCalls: []*modelcallwrite.ModelCall{mc}}
	out := &modelcallwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, modelcallwrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)

	if err != nil {
		errorf("PatchModelCall error message_id=%q err=%v", strings.TrimSpace(modelCall.MessageID), err)
		return err
	}
	if len(out.Violations) > 0 {
		warnf("PatchModelCall violation message_id=%q msg=%q", strings.TrimSpace(modelCall.MessageID), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	s.emitCanonicalModelEvent(ctx, modelCall)
	debugf("PatchModelCall ok message_id=%q status=%q", strings.TrimSpace(modelCall.MessageID), strings.TrimSpace(modelCall.Status))
	return nil
}

func (s *Service) PatchToolCall(ctx context.Context, toolCall *convcli.MutableToolCall) error {
	if s == nil || s.dao == nil {
		return errors.New("conversation service not configured: dao is nil")
	}
	if toolCall == nil {
		return errors.New("invalid toolCall: nil")
	}
	debugf("PatchToolCall start message_id=%q op_id=%q tool=%q status=%q", strings.TrimSpace(toolCall.MessageID), strings.TrimSpace(toolCall.OpID), strings.TrimSpace(toolCall.ToolName), strings.TrimSpace(toolCall.Status))
	tc := (*toolcallwrite.ToolCall)(toolCall)
	input := &toolcallwrite.Input{ToolCalls: []*toolcallwrite.ToolCall{tc}}
	out := &toolcallwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, toolcallwrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	if err != nil {
		errorf("PatchToolCall error message_id=%q err=%v", strings.TrimSpace(toolCall.MessageID), err)
		return err
	}
	if len(out.Violations) > 0 {
		warnf("PatchToolCall violation message_id=%q msg=%q", strings.TrimSpace(toolCall.MessageID), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	if event := toolCallEvent(ctx, toolCall); event != nil {
		s.emitTimelineEvent(ctx, event, "PatchToolCall publish timeline event")
	}
	debugf("PatchToolCall ok message_id=%q status=%q", strings.TrimSpace(toolCall.MessageID), strings.TrimSpace(toolCall.Status))
	return nil
}

func (s *Service) PatchToolApprovalQueue(ctx context.Context, queue *queueWrite.ToolApprovalQueue) error {
	if s == nil || s.dao == nil {
		return errors.New("conversation service not configured: dao is nil")
	}
	if queue == nil {
		return errors.New("invalid tool approval queue: nil")
	}
	input := &queueWrite.Input{Queues: []*queueWrite.ToolApprovalQueue{queue}}
	out := &queueWrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, queueWrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	if err != nil {
		return err
	}
	if len(out.Violations) > 0 {
		return errors.New(out.Violations[0].Message)
	}
	return nil
}

func (s *Service) ListToolApprovalQueues(ctx context.Context, in *queueRead.QueueRowsInput) ([]*queueRead.QueueRowView, error) {
	if s == nil || s.dao == nil {
		return nil, errors.New("conversation service not configured: dao is nil")
	}
	if in == nil {
		in = &queueRead.QueueRowsInput{}
	}
	out := &queueRead.QueueRowsOutput{}
	if _, err := s.dao.Operate(ctx, datly.WithOutput(out), datly.WithURI(queueRead.QueueRowsPathURI), datly.WithInput(in)); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ToolCallTraceByOp returns the persisted trace_id (LLM response.id anchor) for a tool call op_id
// scoped to a conversation. It returns an empty string when not found.
func (s *Service) ToolCallTraceByOp(ctx context.Context, conversationID, opID string) (string, error) {
	if s == nil || s.dao == nil {
		return "", errors.New("conversation service not configured: dao is nil")
	}
	in := &toolread.ByOpInput{ConversationId: strings.TrimSpace(conversationID), OpId: strings.TrimSpace(opID), Has: &toolread.ByOpInputHas{ConversationId: true, OpId: true}}
	out := &toolread.ByOpOutput{}
	if _, err := s.dao.Operate(ctx, datly.WithOutput(out), datly.WithURI(toolread.PathURI), datly.WithInput(in)); err != nil {
		return "", err
	}
	if len(out.Data) == 0 {
		return "", nil
	}
	if out.Data[0] == nil || out.Data[0].TraceId == nil {
		return "", nil
	}
	return strings.TrimSpace(*out.Data[0].TraceId), nil
}

func (s *Service) PatchTurn(ctx context.Context, turn *convcli.MutableTurn) error {
	if s == nil || s.dao == nil {
		return errors.New("conversation service not configured: dao is nil")
	}
	if turn == nil {
		return errors.New("invalid turn: nil")
	}
	debugf("PatchTurn start id=%q convo=%q status=%q queue_seq=%v", strings.TrimSpace(turn.Id), strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.Status), valueOrEmpty(turn.QueueSeq))
	tr := (*turnwrite.Turn)(turn)
	input := &turnwrite.Input{Turns: []*turnwrite.Turn{tr}}
	out := &turnwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, turnwrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	if err != nil {
		errorf("PatchTurn error id=%q err=%v", strings.TrimSpace(turn.Id), err)
		return err
	}
	if len(out.Violations) > 0 {
		warnf("PatchTurn violation id=%q msg=%q", strings.TrimSpace(turn.Id), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	s.publishTurnEvent(ctx, turn)
	debugf("PatchTurn ok id=%q status=%q", strings.TrimSpace(turn.Id), strings.TrimSpace(turn.Status))
	return nil
}

func (s *Service) publishTurnEvent(ctx context.Context, turn *convcli.MutableTurn) {
	if s == nil || s.streamPub == nil || turn == nil || turn.Has == nil {
		return
	}
	status := strings.ToLower(strings.TrimSpace(turn.Status))
	if !turn.Has.Status || status == "" {
		return
	}
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	}
	if conversationID == "" {
		return
	}
	createdAt := turnEventCreatedAt(turn)
	userMessageID := strings.TrimSpace(valueOrEmptyStr(turn.StartedByMessageID))
	if userMessageID == "" {
		if turnMeta, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
			userMessageID = strings.TrimSpace(turnMeta.ParentMessageID)
		}
	}
	if userMessageID == "" && status == "running" {
		userMessageID = strings.TrimSpace(turn.Id)
	}
	if status == "running" {
		patch := map[string]interface{}{
			"turnId":         strings.TrimSpace(turn.Id),
			"conversationId": conversationID,
			"status":         "running",
		}
		if userMessageID != "" {
			patch["userMessageId"] = userMessageID
		}
		agentIDUsed := strings.TrimSpace(valueOrEmptyStr(turn.AgentIDUsed))
		if agentIDUsed != "" {
			patch["agentIdUsed"] = agentIDUsed
		}
		if turn.Has.CreatedAt && turn.CreatedAt != nil && !turn.CreatedAt.IsZero() {
			patch["createdAt"] = turn.CreatedAt.Format(time.RFC3339Nano)
		}
		if turn.Has.RunID && turn.RunID != nil {
			patch["runId"] = strings.TrimSpace(*turn.RunID)
		}
		s.emitTimelineEvent(ctx, &streaming.Event{
			ID:             strings.TrimSpace(turn.Id),
			StreamID:       conversationID,
			ConversationID: conversationID,
			MessageID:      userMessageID,
			Type:           streaming.EventTypeControl,
			Op:             "turn_started",
			Patch:          patch,
			CreatedAt:      createdAt,
		}, "PatchTurn publish turn_started control")
		s.emitTimelineEvent(ctx, &streaming.Event{
			ID:             strings.TrimSpace(turn.Id),
			StreamID:       conversationID,
			ConversationID: conversationID,
			Type:           streaming.EventTypeTurnStarted,
			TurnID:         strings.TrimSpace(turn.Id),
			MessageID:      userMessageID,
			UserMessageID:  userMessageID,
			AgentIDUsed:    agentIDUsed,
			Status:         "running",
			CreatedAt:      createdAt,
		}, "PatchTurn publish turn_started")
		return
	}
	if status == "completed" || status == "succeeded" || status == "failed" || status == "canceled" || status == "cancelled" {
		eventType := streaming.EventTypeTurnCompleted
		switch status {
		case "failed":
			eventType = streaming.EventTypeTurnFailed
		case "canceled", "cancelled":
			eventType = streaming.EventTypeTurnCanceled
		}
		s.emitTimelineEvent(ctx, &streaming.Event{
			ID:             strings.TrimSpace(turn.Id),
			StreamID:       conversationID,
			ConversationID: conversationID,
			Type:           eventType,
			TurnID:         strings.TrimSpace(turn.Id),
			MessageID:      userMessageID,
			UserMessageID:  userMessageID,
			Status:         status,
			Error:          strings.TrimSpace(valueOrEmptyStr(turn.ErrorMessage)),
			CreatedAt:      createdAt,
		}, "PatchTurn publish "+string(eventType))
	}
}

// emitCanonicalAssistantEvents emits assistant_preamble or assistant_final events
// based on message patch content. These are canonical events from the render pipeline
// that provide explicit semantics (not overloaded like llm_response).
func (s *Service) emitCanonicalAssistantEvents(ctx context.Context, message *convcli.MutableMessage, conversationID string) {
	if s == nil || s.streamPub == nil || message == nil || message.Has == nil {
		return
	}
	role := strings.ToLower(strings.TrimSpace(message.Role))
	if role != "assistant" {
		return
	}
	preamble := ""
	if message.Has.Preamble && message.Preamble != nil {
		preamble = strings.TrimSpace(*message.Preamble)
	}
	content := ""
	if message.Has.Content && message.Content != nil {
		content = strings.TrimSpace(*message.Content)
	}
	isFinal := false
	if message.Has.Interim && message.Interim != nil {
		isFinal = *message.Interim == 0 && content != ""
	}

	turnID := ""
	if message.Has.TurnID && message.TurnID != nil {
		turnID = strings.TrimSpace(*message.TurnID)
	}
	if turnID == "" {
		if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
			turnID = strings.TrimSpace(turn.TurnID)
		}
	}

	// Emit preamble event when we have preamble text and the message is interim
	if preamble != "" && !isFinal {
		debugf("emitCanonicalAssistantEvents preamble convo=%q turn=%q msg=%q preamble_len=%d", conversationID, turnID, strings.TrimSpace(message.Id), len(preamble))
		event := &streaming.Event{
			ID:                 strings.TrimSpace(message.Id),
			StreamID:           conversationID,
			ConversationID:     conversationID,
			MessageID:          strings.TrimSpace(message.Id),
			Mode:               strings.TrimSpace(valueOrEmptyStr(message.Mode)),
			Type:               streaming.EventTypeAssistantPreamble,
			TurnID:             turnID,
			AssistantMessageID: strings.TrimSpace(message.Id),
			Content:            preamble,
			CreatedAt:          patchEventCreatedAt(message),
		}
		applyIterationPage(event, message.Iteration)
		s.emitTimelineEvent(ctx, event, "PatchMessage publish assistant_preamble")
	}

	// Emit final event when the message has non-empty content and is not interim
	if isFinal && content != "" {
		event := &streaming.Event{
			ID:                 strings.TrimSpace(message.Id),
			StreamID:           conversationID,
			ConversationID:     conversationID,
			MessageID:          strings.TrimSpace(message.Id),
			Mode:               strings.TrimSpace(valueOrEmptyStr(message.Mode)),
			Type:               streaming.EventTypeAssistantFinal,
			TurnID:             turnID,
			AssistantMessageID: strings.TrimSpace(message.Id),
			Content:            content,
			FinalResponse:      true,
			CreatedAt:          patchEventCreatedAt(message),
		}
		applyIterationPage(event, message.Iteration)
		s.emitTimelineEvent(ctx, event, "PatchMessage publish assistant_final")
	}
}

// emitCanonicalModelEvent emits a model_started or model_completed event
// alongside the legacy llm_request_started / llm_response events.
func (s *Service) emitCanonicalModelEvent(ctx context.Context, modelCall *convcli.MutableModelCall) {
	if s == nil || s.streamPub == nil || modelCall == nil {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	}
	if conversationID == "" {
		log.Printf("[emitCanonicalModelEvent] SKIP no conversationID msg=%q status=%q", modelCall.MessageID, modelCall.Status)
		return
	}
	status := strings.ToLower(strings.TrimSpace(modelCall.Status))
	mode := strings.TrimSpace(runtimerequestctx.RequestModeFromContext(ctx))
	log.Printf("[emitCanonicalModelEvent] convo=%q turn=%q msg=%q status=%q", conversationID, strings.TrimSpace(valueOrEmptyStr(modelCall.TurnID)), modelCall.MessageID, status)
	if status == "thinking" || status == "streaming" || status == "running" {
		event := &streaming.Event{
			ID:                 strings.TrimSpace(modelCall.MessageID),
			StreamID:           conversationID,
			ConversationID:     conversationID,
			MessageID:          strings.TrimSpace(modelCall.MessageID),
			Mode:               mode,
			Type:               streaming.EventTypeModelStarted,
			TurnID:             resolveTurnID(ctx, valueOrEmptyStr(modelCall.TurnID)),
			AssistantMessageID: strings.TrimSpace(modelCall.MessageID),
			ParentMessageID:    strings.TrimSpace(turn.ParentMessageID),
			ModelCallID:        strings.TrimSpace(modelCall.MessageID),
			Provider:           strings.TrimSpace(modelCall.Provider),
			ModelName:          strings.TrimSpace(modelCall.Model),
			Status:             strings.TrimSpace(modelCall.Status),
			CreatedAt:          time.Now(),
		}
		if modelCall.Model != "" || modelCall.Provider != "" {
			event.Model = &streaming.EventModel{
				Provider: strings.TrimSpace(modelCall.Provider),
				Model:    strings.TrimSpace(modelCall.Model),
				Kind:     strings.TrimSpace(modelCall.ModelKind),
			}
		}
		if modelCall.Has != nil && modelCall.Has.StartedAt && modelCall.StartedAt != nil && !modelCall.StartedAt.IsZero() {
			event.CreatedAt = *modelCall.StartedAt
			event.StartedAt = modelCall.StartedAt
		}
		if modelCall.Has != nil && modelCall.Has.RequestPayloadID && modelCall.RequestPayloadID != nil {
			event.RequestPayloadID = strings.TrimSpace(*modelCall.RequestPayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.ProviderRequestPayloadID && modelCall.ProviderRequestPayloadID != nil {
			event.ProviderRequestPayloadID = strings.TrimSpace(*modelCall.ProviderRequestPayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.StreamPayloadID && modelCall.StreamPayloadID != nil {
			event.StreamPayloadID = strings.TrimSpace(*modelCall.StreamPayloadID)
		}
		applyIterationPage(event, modelCall.Iteration)
		s.emitTimelineEvent(ctx, event, "PatchModelCall publish model_started")
	} else if status == "completed" || status == "succeeded" || status == "failed" {
		now := time.Now()
		event := &streaming.Event{
			ID:                 strings.TrimSpace(modelCall.MessageID),
			StreamID:           conversationID,
			ConversationID:     conversationID,
			MessageID:          strings.TrimSpace(modelCall.MessageID),
			Mode:               mode,
			Type:               streaming.EventTypeModelCompleted,
			TurnID:             resolveTurnID(ctx, valueOrEmptyStr(modelCall.TurnID)),
			AssistantMessageID: strings.TrimSpace(modelCall.MessageID),
			ParentMessageID:    strings.TrimSpace(turn.ParentMessageID),
			ModelCallID:        strings.TrimSpace(modelCall.MessageID),
			Provider:           strings.TrimSpace(modelCall.Provider),
			ModelName:          strings.TrimSpace(modelCall.Model),
			Status:             strings.TrimSpace(modelCall.Status),
			CreatedAt:          now,
			CompletedAt:        &now,
		}
		if modelCall.Model != "" || modelCall.Provider != "" {
			event.Model = &streaming.EventModel{
				Provider: strings.TrimSpace(modelCall.Provider),
				Model:    strings.TrimSpace(modelCall.Model),
				Kind:     strings.TrimSpace(modelCall.ModelKind),
			}
		}
		if modelCall.Has != nil && modelCall.Has.StartedAt && modelCall.StartedAt != nil && !modelCall.StartedAt.IsZero() {
			event.StartedAt = modelCall.StartedAt
		}
		if modelCall.Has != nil && modelCall.Has.RequestPayloadID && modelCall.RequestPayloadID != nil {
			event.RequestPayloadID = strings.TrimSpace(*modelCall.RequestPayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.ResponsePayloadID && modelCall.ResponsePayloadID != nil {
			event.ResponsePayloadID = strings.TrimSpace(*modelCall.ResponsePayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.ProviderRequestPayloadID && modelCall.ProviderRequestPayloadID != nil {
			event.ProviderRequestPayloadID = strings.TrimSpace(*modelCall.ProviderRequestPayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.ProviderResponsePayloadID && modelCall.ProviderResponsePayloadID != nil {
			event.ProviderResponsePayloadID = strings.TrimSpace(*modelCall.ProviderResponsePayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.StreamPayloadID && modelCall.StreamPayloadID != nil {
			event.StreamPayloadID = strings.TrimSpace(*modelCall.StreamPayloadID)
		}
		// Include LLM response data (content, preamble, finalResponse) when
		// available via context — makes model_completed self-sufficient.
		if meta, ok := runtimerequestctx.ModelCompletionMetaFromContext(ctx); ok {
			event.Content = meta.Content
			event.Preamble = meta.Preamble
			event.FinalResponse = meta.FinalResponse
		}
		applyIterationPage(event, modelCall.Iteration)
		s.emitTimelineEvent(ctx, event, "PatchModelCall publish model_completed")
	}
}

func resolveTurnID(ctx context.Context, explicit string) string {
	if turnID := strings.TrimSpace(explicit); turnID != "" {
		return turnID
	}
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		return strings.TrimSpace(turn.TurnID)
	}
	return ""
}

// DeleteConversation removes a conversation by id. Dependent rows are removed via DB FKs (ON DELETE CASCADE).
func (s *Service) DeleteConversation(ctx context.Context, id string) error {
	if s == nil || s.dao == nil {
		return errors.New("conversation service not configured: dao is nil")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("conversation id is required")
	}
	in := &convdel.Input{Ids: []string{id}}
	out := &convdel.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodDelete, convdel.PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	)
	if err != nil {
		return err
	}
	if len(out.Violations) > 0 {
		return errors.New(out.Violations[0].Message)
	}
	return nil
}

// DeleteMessage removes a single message from a conversation using the dedicated DELETE component.
func (s *Service) DeleteMessage(ctx context.Context, conversationID, messageID string) error {
	if s == nil || s.dao == nil {
		return errors.New("conversation service not configured: dao is nil")
	}
	if strings.TrimSpace(messageID) == "" {
		return errors.New("message id is required")
	}

	// Optional safety check: if conversationID provided, verify the message belongs to it.
	if strings.TrimSpace(conversationID) != "" {
		if got, _ := s.GetMessage(ctx, messageID); got != nil && strings.TrimSpace(got.ConversationId) != "" {
			if !strings.EqualFold(strings.TrimSpace(got.ConversationId), strings.TrimSpace(conversationID)) {
				return errors.New("message does not belong to the specified conversation")
			}
		}
	}

	in := &msgdel.Input{Ids: []string{strings.TrimSpace(messageID)}}
	out := &msgdel.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodDelete, msgdel.PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	)
	if err != nil {
		return err
	}
	if len(out.Violations) > 0 {
		return errors.New(out.Violations[0].Message)
	}
	return nil
}
