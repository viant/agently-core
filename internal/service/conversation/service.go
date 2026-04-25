package conversation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
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
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
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
		logx.Infof("conversation", "PatchConversations start id=%q status=%q visibility=%q", strings.TrimSpace(conversations.Id), strings.TrimSpace(valueOrEmptyStr(conversations.Status)), strings.TrimSpace(valueOrEmptyStr(conversations.Visibility)))
	} else {
		logx.Infof("conversation", "PatchConversations start id=\"\" status=\"\" visibility=\"\" (nil input)")
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
		logx.Errorf("conversation", "PatchConversations error id=%q err=%v", strings.TrimSpace(conversations.Id), err)
		return err
	}
	if len(out.Violations) > 0 {
		logx.Warnf("conversation", "PatchConversations violation id=%q msg=%q", strings.TrimSpace(conversations.Id), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	logx.Infof("conversation", "PatchConversations ok id=%q", strings.TrimSpace(conversations.Id))
	s.publishConversationMetaEvent(ctx, conversations)
	s.publishConversationUsageEvent(ctx, conversations)
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
			pruneBlankAssistantPlaceholders(baseOut.Data[0].Transcript)
			baseOut.Data[0].OnRelation(ctx)
			logx.Infof("conversation", "GetConversation base after OnRelation id=%q stage=%q status=%q last_turn_id=%q transcript_len=%d latest_turn_status=%q latest_turn_stage=%q",
				strings.TrimSpace(baseOut.Data[0].Id),
				strings.TrimSpace(baseOut.Data[0].Stage),
				strings.TrimSpace(valueOrEmptyStr(baseOut.Data[0].Status)),
				strings.TrimSpace(valueOrEmptyStr(baseOut.Data[0].LastTurnId)),
				len(baseOut.Data[0].Transcript),
				latestTranscriptStatus(baseOut.Data[0].Transcript),
				latestTranscriptStage(baseOut.Data[0].Transcript),
			)
			conv := convcli.Conversation(*baseOut.Data[0])
			return &conv, nil
		}
		// No conversation found; mirror API behavior by returning nil without logging.
		return nil, nil
	}
	// Cast generated to SDK type
	pruneBlankAssistantPlaceholders(out.Data[0].Transcript)
	out.Data[0].OnRelation(ctx)
	logx.Infof("conversation", "GetConversation after OnRelation id=%q stage=%q status=%q last_turn_id=%q transcript_len=%d latest_turn_status=%q latest_turn_stage=%q",
		strings.TrimSpace(out.Data[0].Id),
		strings.TrimSpace(out.Data[0].Stage),
		strings.TrimSpace(valueOrEmptyStr(out.Data[0].Status)),
		strings.TrimSpace(valueOrEmptyStr(out.Data[0].LastTurnId)),
		len(out.Data[0].Transcript),
		latestTranscriptStatus(out.Data[0].Transcript),
		latestTranscriptStage(out.Data[0].Transcript),
	)
	conv := convcli.Conversation(*out.Data[0])
	return &conv, nil
}

func latestTranscriptStatus(transcript []*agconv.TranscriptView) string {
	for i := len(transcript) - 1; i >= 0; i-- {
		if transcript[i] == nil {
			continue
		}
		if status := strings.TrimSpace(transcript[i].Status); status != "" {
			return status
		}
	}
	return ""
}

func latestTranscriptStage(transcript []*agconv.TranscriptView) string {
	for i := len(transcript) - 1; i >= 0; i-- {
		if transcript[i] == nil {
			continue
		}
		if stage := strings.TrimSpace(transcript[i].Stage); stage != "" {
			return stage
		}
	}
	return ""
}

func pruneBlankAssistantPlaceholders(turns []*agconv.TranscriptView) {
	for _, turn := range turns {
		if turn == nil || len(turn.Message) == 0 {
			continue
		}
		filtered := turn.Message[:0]
		for _, msg := range turn.Message {
			if shouldDropBlankAssistantPlaceholder(msg) {
				continue
			}
			filtered = append(filtered, msg)
		}
		turn.Message = filtered
	}
}

func shouldDropBlankAssistantPlaceholder(msg *agconv.MessageView) bool {
	if msg == nil {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
		return false
	}
	if msg.Interim != 1 {
		return false
	}
	if valueOrEmptyStr(msg.Content) != "" || valueOrEmptyStr(msg.RawContent) != "" || valueOrEmptyStr(msg.Narration) != "" {
		return false
	}
	if msg.ElicitationId != nil && strings.TrimSpace(*msg.ElicitationId) != "" {
		return false
	}
	return !(msg.ModelCall != nil || len(msg.ToolMessage) > 0)
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
		logx.Infof("conversation", "PatchPayload start id=%q kind=%q mime=%q size_bytes=%d", strings.TrimSpace(payload.Id), strings.TrimSpace(payload.Kind), strings.TrimSpace(payload.MimeType), payload.SizeBytes)
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
		logx.Errorf("conversation", "PatchPayload error id=%q err=%v", strings.TrimSpace(payload.Id), err)
		return err
	}
	if len(out.Violations) > 0 {
		logx.Warnf("conversation", "PatchPayload violation id=%q msg=%q", strings.TrimSpace(payload.Id), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	if logPayloadDebug {
		logx.Infof("conversation", "PatchPayload ok id=%q", strings.TrimSpace(payload.Id))
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
	logx.Infof("conversation", "PatchGeneratedFile start id=%q provider=%q mode=%q status=%q", strings.TrimSpace(generatedFile.ID), strings.TrimSpace(generatedFile.Provider), strings.TrimSpace(generatedFile.Mode), strings.TrimSpace(generatedFile.Status))
	input := &generatedfilewrite.Input{GeneratedFiles: []*generatedfilewrite.GeneratedFile{generatedFile}}
	out := &generatedfilewrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, generatedfilewrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	if err != nil {
		logx.Errorf("conversation", "PatchGeneratedFile error id=%q err=%v", strings.TrimSpace(generatedFile.ID), err)
		return err
	}
	if len(out.Violations) > 0 {
		logx.Warnf("conversation", "PatchGeneratedFile violation id=%q msg=%q", strings.TrimSpace(generatedFile.ID), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	logx.Infof("conversation", "PatchGeneratedFile ok id=%q", strings.TrimSpace(generatedFile.ID))
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
		Narration:            row.Narration,
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
		Narration:            row.Narration,
		Iteration:            row.Iteration,
		Phase:                row.Phase,
	}
	return &res, nil
}

func (s *Service) PatchMessage(ctx context.Context, message *convcli.MutableMessage) error {
	if message != nil {
		logx.Infof("conversation", "PatchMessage start id=%q convo=%q turn=%v role=%q type=%q status=%q", message.Id, message.ConversationID, valueOrEmpty(message.TurnID), strings.TrimSpace(message.Role), strings.TrimSpace(message.Type), strings.TrimSpace(valueOrEmptyStr(message.Status)))
	} else {
		logx.Infof("conversation", "PatchMessage start id=\"\" convo=\"\" turn=\"\" role=\"\" type=\"\" status=\"\" (nil input)")
	}
	if merged, err := s.mergeMessagePatchWithExisting(ctx, message); err != nil {
		return err
	} else if merged != nil {
		message = merged
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
		logx.Errorf("conversation", "PatchMessage error id=%q convo=%q err=%v", message.Id, message.ConversationID, err)
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
		logx.Warnf("conversation", "PatchMessage violation id=%q convo=%q msg=%q", message.Id, message.ConversationID, out.Violations[0].Message)
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
	logx.Infof("conversation", "PatchMessage ok id=%q convo=%q", message.Id, message.ConversationID)
	return nil
}

func (s *Service) mergeMessagePatchWithExisting(ctx context.Context, patch *convcli.MutableMessage) (*convcli.MutableMessage, error) {
	if s == nil || patch == nil || patch.Has == nil || !patch.Has.Id {
		return patch, nil
	}
	existing, err := s.GetMessage(ctx, strings.TrimSpace(patch.Id))
	if err != nil || existing == nil {
		return patch, err
	}
	if !patch.Has.ConversationID && strings.TrimSpace(existing.ConversationId) != "" {
		patch.SetConversationID(existing.ConversationId)
	}
	if !patch.Has.TurnID && existing.TurnId != nil && strings.TrimSpace(*existing.TurnId) != "" {
		patch.SetTurnID(strings.TrimSpace(*existing.TurnId))
	}
	if !patch.Has.ParentMessageID && existing.ParentMessageId != nil && strings.TrimSpace(*existing.ParentMessageId) != "" {
		patch.SetParentMessageID(strings.TrimSpace(*existing.ParentMessageId))
	}
	if !patch.Has.Role && strings.TrimSpace(existing.Role) != "" {
		patch.SetRole(existing.Role)
	}
	if !patch.Has.Type && strings.TrimSpace(existing.Type) != "" {
		patch.SetType(existing.Type)
	}
	if !patch.Has.Mode && existing.Mode != nil && strings.TrimSpace(*existing.Mode) != "" {
		patch.SetMode(strings.TrimSpace(*existing.Mode))
	}
	if !patch.Has.Iteration && existing.Iteration != nil && *existing.Iteration > 0 {
		patch.SetIteration(*existing.Iteration)
	}
	if !patch.Has.Phase && existing.Phase != nil && strings.TrimSpace(*existing.Phase) != "" {
		patch.SetPhase(strings.TrimSpace(*existing.Phase))
	}
	return patch, nil
}

// publishMessagePatchEvent forwards a message write to the streaming
// bus as SEMANTIC events. Never emits raw DB column diffs.
//
// There are EXACTLY TWO semantic emissions from this path:
//
//  1. `narration` — when the message carries interim narration text
//     (commentary during tool execution / pre-tool-call framing).
//     Carries the text in `event.Narration`. Interim — not a real
//     turn message.
//
//  2. `assistant` (wire name) — when this write is an explicit ADD
//     (`runtimerequestctx.MessageAddEvent` flag on ctx), fires to
//     signal "a new standalone message exists in the turn". Reducers
//     upsert into `turn.messages` / `turn.users` by messageId, creating
//     the bubble. Patches to EXISTING message rows do NOT emit here —
//     content for page-owned messages is already live on the client
//     via `text_delta` stream accumulation into `page.content`; a
//     redundant patch event would cause double-rendering.
//
// **There is no "final message" event.** Historically we had
// per-message final markers; these kept
// leaking the end-of-turn signal into individual messages and caused
// repeated regressions ("which message is the final?", "why are there
// two final messages?", "why doesn't this message render?"). The
// end-of-turn signal lives exclusively on `EventTypeTurnCompleted` /
// `EventTypeTurnFailed` / `EventTypeTurnCanceled` — fired ONCE per
// turn. A turn can have any number of assistant messages; none of them
// is "the final", they're just messages.
//
// Active-turn invariant: all client state flows from these semantic
// events. Past turns refresh via `applyTranscript`, which populates
// the same fields from the canonical snapshot.
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
	s.emitCanonicalAssistantEvents(ctx, message, conversationID)
	// Emit `assistant` event ONLY for explicit adds (AddMessage code
	// path sets the MessageAddEvent ctx flag). Patches to existing
	// rows don't fire — the UI already has the content live from
	// text_delta for page-owned messages, and re-emitting would
	// double-render. message/add tool, user-submit, and similar
	// "new message row" call sites DO set the flag and therefore
	// produce a bubble.
	if runtimerequestctx.MessageAddEventFromContext(ctx) {
		s.emitMessageAppendedEvent(ctx, message, conversationID)
	}
}

// emitMessageAppendedEvent publishes a `message_appended` event for a
// user or assistant message row. Idempotent by messageId: the first
// emission creates the client-side bubble, subsequent emissions update
// its fields. Works for ADD (new row) and PATCH (content/status
// update) alike — clients upsert by messageId.
//
// Interim messages (interim=1) are NOT emitted here — they're carried
// as `narration` events instead (interim commentary, not a real turn
// message). This function emits ONLY real messages that belong in
// `turn.messages` / `turn.users`.
func (s *Service) emitMessageAppendedEvent(ctx context.Context, message *convcli.MutableMessage, conversationID string) {
	if s == nil || s.streamPub == nil || message == nil || message.Has == nil {
		return
	}
	role := strings.ToLower(strings.TrimSpace(message.Role))
	if role != "user" && role != "assistant" {
		return
	}
	// Skip interim messages — narration path handles those.
	if message.Has.Interim && message.Interim != nil && *message.Interim == 1 {
		return
	}
	content := ""
	if message.Has.Content && message.Content != nil {
		content = strings.TrimSpace(*message.Content)
	}
	// Require content — an empty add/patch carries no bubble-worthy
	// information; skip it to keep the stream clean.
	if content == "" {
		return
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
	event := &streaming.Event{
		ID:             strings.TrimSpace(message.Id),
		StreamID:       conversationID,
		ConversationID: conversationID,
		TurnID:         turnID,
		MessageID:      strings.TrimSpace(message.Id),
		Type:           streaming.EventTypeAssistant,
		Mode:           firstNonEmpty(strings.TrimSpace(valueOrEmptyStr(message.Mode)), requestModeForEvent(ctx)),
		Content:        content,
		CreatedAt:      patchEventCreatedAt(message),
	}
	// Carry role, sequence, status via the Patch map so the reducer
	// receives them without widening the Event struct with fields only
	// meaningful for this event type.
	patch := map[string]interface{}{"role": role}
	if message.Has.Sequence && message.Sequence != nil {
		patch["sequence"] = *message.Sequence
	}
	if message.Has.Status && message.Status != nil {
		patch["status"] = strings.TrimSpace(*message.Status)
	}
	event.Patch = patch
	applyIterationPage(event, message.Iteration)
	s.emitTimelineEvent(ctx, event, "PatchMessage publish message_appended")
}

// publishConversationMetaEvent emits a conversation_meta_updated event when
// conversation-level metadata (title, agentId, summary, …) changes.
// Only fields that were explicitly set in the patch are included in the payload.
func (s *Service) publishConversationMetaEvent(ctx context.Context, conv *convcli.MutableConversation) {
	if s == nil || s.streamPub == nil || conv == nil || conv.Has == nil {
		return
	}
	convID := strings.TrimSpace(conv.Id)
	if convID == "" {
		return
	}
	patch := conversationMetaPatch(conv)
	if len(patch) == 0 {
		return
	}
	event := &streaming.Event{
		StreamID:       convID,
		ConversationID: convID,
		Type:           streaming.EventTypeConversationMetaUpdated,
		Patch:          patch,
	}
	s.emitTimelineEvent(ctx, event, "PatchConversations publish meta event")
}

// conversationMetaPatch builds the patch payload from the fields that were
// explicitly set on the MutableConversation.
func conversationMetaPatch(conv *convcli.MutableConversation) map[string]interface{} {
	if conv == nil || conv.Has == nil {
		return nil
	}
	out := map[string]interface{}{}
	if conv.Has.Title && conv.Title != nil {
		out["title"] = strings.TrimSpace(*conv.Title)
	}
	if conv.Has.AgentId && conv.AgentId != nil {
		out["agentId"] = strings.TrimSpace(*conv.AgentId)
	}
	if conv.Has.Summary && conv.Summary != nil {
		out["summary"] = strings.TrimSpace(*conv.Summary)
	}
	if conv.Has.Status && conv.Status != nil {
		out["status"] = strings.TrimSpace(*conv.Status)
	}
	return out
}

func (s *Service) publishConversationUsageEvent(ctx context.Context, conv *convcli.MutableConversation) {
	if s == nil || s.streamPub == nil || conv == nil || conv.Has == nil {
		return
	}
	convID := strings.TrimSpace(conv.Id)
	if convID == "" {
		return
	}
	hasUsage := conv.Has.UsageInputTokens || conv.Has.UsageOutputTokens || conv.Has.UsageEmbeddingTokens
	if !hasUsage {
		return
	}
	ev := &streaming.Event{
		StreamID:             convID,
		ConversationID:       convID,
		Type:                 streaming.EventTypeUsage,
		UsageInputTokens:     intValuePtr(conv.UsageInputTokens),
		UsageOutputTokens:    intValuePtr(conv.UsageOutputTokens),
		UsageEmbeddingTokens: intValuePtr(conv.UsageEmbeddingTokens),
	}
	ev.UsageTotalTokens = ev.UsageInputTokens + ev.UsageOutputTokens + ev.UsageEmbeddingTokens
	s.emitTimelineEvent(ctx, ev, "PatchConversations publish usage event")
}

func intValuePtr(v *int) int {
	if v == nil {
		return 0
	}
	return *v
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
	if message.Has.Phase && message.Phase != nil {
		out["phase"] = strings.TrimSpace(*message.Phase)
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
	if message.Has.Narration && message.Narration != nil {
		out["narration"] = strings.TrimSpace(*message.Narration)
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

func modelEventPhase(mode string, iteration *int) string {
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	switch normalizedMode {
	case "router":
		return "intake"
	case "summary":
		return "summary"
	}
	return ""
}

func requestModeForEvent(ctx context.Context) string {
	mode := strings.TrimSpace(runtimerequestctx.RequestModeFromContext(ctx))
	if mode != "" {
		return mode
	}
	if toolexec.IsChainMode(ctx) {
		return "chain"
	}
	return "task"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
		"phase":                     strings.TrimSpace(event.Phase),
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
	if strings.TrimSpace(event.Narration) != "" {
		fields["narrationPreview"] = event.Narration
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
	logx.DebugCtxf(ctx, "conversation", "[emitTimelineEvent] %s type=%q op=%q stream_id=%q convo=%q turn=%q msg=%q seq=%d mode=%q agent=%q agent_name=%q user_msg=%q assistant_msg=%q parent_msg=%q model_call=%q tool_call=%q tool_msg=%q tool=%q status=%q final=%v iter=%d page=%d/%d latest=%v linked=%q feed=%q created_at=%q started_at=%q completed_at=%q sent_at=%q req=%q resp=%q preq=%q presp=%q stream=%q id=%q",
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
		logx.Warnf("conversation", "%s error type=%q id=%q convo=%q err=%v", action, strings.TrimSpace(string(event.Type)), strings.TrimSpace(event.ID), strings.TrimSpace(event.ConversationID), err)
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
		Mode:               requestModeForEvent(ctx),
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
		Phase:              modelEventPhase(requestModeForEvent(ctx), toolCall.Iteration),
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
	logx.Infof("conversation", "PatchModelCall start message_id=%q turn_id=%q provider=%q model=%q status=%q", strings.TrimSpace(modelCall.MessageID), strings.TrimSpace(valueOrEmptyStr(modelCall.TurnID)), strings.TrimSpace(modelCall.Provider), strings.TrimSpace(modelCall.Model), strings.TrimSpace(modelCall.Status))
	mc := (*modelcallwrite.ModelCall)(modelCall)
	input := &modelcallwrite.Input{ModelCalls: []*modelcallwrite.ModelCall{mc}}
	out := &modelcallwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, modelcallwrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)

	if err != nil {
		logx.Errorf("conversation", "PatchModelCall error message_id=%q err=%v", strings.TrimSpace(modelCall.MessageID), err)
		return err
	}
	if len(out.Violations) > 0 {
		logx.Warnf("conversation", "PatchModelCall violation message_id=%q msg=%q", strings.TrimSpace(modelCall.MessageID), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	s.emitCanonicalModelEvent(ctx, modelCall)
	logx.Infof("conversation", "PatchModelCall ok message_id=%q status=%q", strings.TrimSpace(modelCall.MessageID), strings.TrimSpace(modelCall.Status))
	return nil
}

func (s *Service) PatchToolCall(ctx context.Context, toolCall *convcli.MutableToolCall) error {
	if s == nil || s.dao == nil {
		return errors.New("conversation service not configured: dao is nil")
	}
	if toolCall == nil {
		return errors.New("invalid toolCall: nil")
	}
	logx.Infof("conversation", "PatchToolCall start message_id=%q op_id=%q tool=%q status=%q", strings.TrimSpace(toolCall.MessageID), strings.TrimSpace(toolCall.OpID), strings.TrimSpace(toolCall.ToolName), strings.TrimSpace(toolCall.Status))
	tc := (*toolcallwrite.ToolCall)(toolCall)
	input := &toolcallwrite.Input{ToolCalls: []*toolcallwrite.ToolCall{tc}}
	out := &toolcallwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, toolcallwrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	if err != nil {
		logx.Errorf("conversation", "PatchToolCall error message_id=%q err=%v", strings.TrimSpace(toolCall.MessageID), err)
		return err
	}
	if len(out.Violations) > 0 {
		logx.Warnf("conversation", "PatchToolCall violation message_id=%q msg=%q", strings.TrimSpace(toolCall.MessageID), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	if event := toolCallEvent(ctx, toolCall); event != nil {
		s.emitTimelineEvent(ctx, event, "PatchToolCall publish timeline event")
	}
	logx.Infof("conversation", "PatchToolCall ok message_id=%q status=%q", strings.TrimSpace(toolCall.MessageID), strings.TrimSpace(toolCall.Status))
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
	logx.Infof("conversation", "PatchTurn start id=%q convo=%q status=%q queue_seq=%v", strings.TrimSpace(turn.Id), strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.Status), valueOrEmpty(turn.QueueSeq))
	tr := (*turnwrite.Turn)(turn)
	input := &turnwrite.Input{Turns: []*turnwrite.Turn{tr}}
	out := &turnwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, turnwrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	if err != nil {
		logx.Errorf("conversation", "PatchTurn error id=%q err=%v", strings.TrimSpace(turn.Id), err)
		return err
	}
	if len(out.Violations) > 0 {
		logx.Warnf("conversation", "PatchTurn violation id=%q msg=%q", strings.TrimSpace(turn.Id), out.Violations[0].Message)
		return errors.New(out.Violations[0].Message)
	}
	s.publishTurnEvent(ctx, turn)
	logx.Infof("conversation", "PatchTurn ok id=%q status=%q", strings.TrimSpace(turn.Id), strings.TrimSpace(turn.Status))
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

// emitCanonicalAssistantEvents emits ONLY the `narration` event for
// interim assistant messages that carry narration text. It never emits
// a final-message event — the "final assistant message" concept has
// been removed (see publishMessagePatchEvent doc for rationale). Real
// messages (interim=0 with content) flow through emitMessageAppendedEvent.
func (s *Service) emitCanonicalAssistantEvents(ctx context.Context, message *convcli.MutableMessage, conversationID string) {
	if s == nil || s.streamPub == nil || message == nil || message.Has == nil {
		return
	}
	role := strings.ToLower(strings.TrimSpace(message.Role))
	if role != "assistant" {
		return
	}
	preamble := ""
	if message.Has.Narration && message.Narration != nil {
		preamble = strings.TrimSpace(*message.Narration)
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

	// Emit narration event when we have narration text and the message is interim.
	// The text is carried in the dedicated `Narration` field (NOT `Content`) so
	// the TS reducer's `onAssistantNarration` handler — which reads
	// `event.narration` — sees it. Previously this was stuffed into
	// `Content`, leaving `event.narration` empty on the wire and causing the
	// client to silently drop the narration.
	if preamble != "" && !isFinal {
		logx.Infof("conversation", "emitCanonicalAssistantEvents narration convo=%q turn=%q msg=%q narration_len=%d", conversationID, turnID, strings.TrimSpace(message.Id), len(preamble))
		executionRole := strings.ToLower(strings.TrimSpace(valueOrEmptyStr(message.Mode)))
		if executionRole != "narrator" {
			executionRole = ""
		}
		// Narration source: runtime narrator when message.Mode == narrator,
		// otherwise the model itself wrote this text (pre-tool-call framing
		// or aggregated reasoning from the current iteration).
		narrationSource := "model"
		if executionRole == "narrator" {
			narrationSource = "narrator"
		}
		// For assistant-content events, `MessageID` is the canonical
		// assistant-bubble id. `AssistantMessageID` is intentionally not
		// populated — it's redundant on these events (the two coincide)
		// and its duplicate emission was the reason clients had to probe
		// both fields. The field remains on tool events where it
		// meaningfully differs from MessageID (parent bubble vs tool row).
		event := &streaming.Event{
			ID:              strings.TrimSpace(message.Id),
			StreamID:        conversationID,
			ConversationID:  conversationID,
			MessageID:       strings.TrimSpace(message.Id),
			Mode:            firstNonEmpty(strings.TrimSpace(valueOrEmptyStr(message.Mode)), requestModeForEvent(ctx)),
			Type:            streaming.EventTypeNarration,
			TurnID:          turnID,
			ExecutionRole:   executionRole,
			Narration:       preamble,
			NarrationSource: narrationSource,
			CreatedAt:       patchEventCreatedAt(message),
		}
		// Carry sequence + status on the narration event so the bubble
		// the client creates from narration can be ordered correctly
		// against sibling messages BEFORE the real `assistant` event
		// arrives for the same messageId. Sequence is DB-assigned and
		// stable from the moment the interim row is first persisted;
		// it will match whatever the later `assistant` event emits.
		narrationPatch := map[string]interface{}{}
		if message.Has != nil {
			if message.Has.Sequence && message.Sequence != nil {
				narrationPatch["sequence"] = *message.Sequence
			}
			if message.Has.Status && message.Status != nil {
				narrationPatch["status"] = strings.TrimSpace(*message.Status)
			}
		}
		if len(narrationPatch) > 0 {
			event.Patch = narrationPatch
		}
		applyIterationPage(event, message.Iteration)
		s.emitTimelineEvent(ctx, event, "PatchMessage publish narration")
	}
	// No final-message emission here. Non-interim assistant
	// messages flow through emitMessageAppendedEvent as
	// `message_appended`. End-of-turn is signaled separately by
	// `EventTypeTurnCompleted` / `EventTypeTurnFailed` /
	// `EventTypeTurnCanceled`.
	_ = content
	_ = isFinal
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
		logx.DebugCtxf(ctx, "conversation", "[emitCanonicalModelEvent] SKIP no conversationID msg=%q status=%q", modelCall.MessageID, modelCall.Status)
		return
	}
	status := strings.ToLower(strings.TrimSpace(modelCall.Status))
	mode := requestModeForEvent(ctx)
	modelCallID := strings.TrimSpace(valueOrEmptyStr(modelCall.TraceID))
	logx.DebugCtxf(ctx, "conversation", "[emitCanonicalModelEvent] convo=%q turn=%q msg=%q model_call=%q status=%q", conversationID, strings.TrimSpace(valueOrEmptyStr(modelCall.TurnID)), modelCall.MessageID, modelCallID, status)
	if status == "thinking" || status == "streaming" || status == "running" {
		event := &streaming.Event{
			ID:              strings.TrimSpace(modelCall.MessageID),
			StreamID:        conversationID,
			ConversationID:  conversationID,
			MessageID:       strings.TrimSpace(modelCall.MessageID),
			Mode:            mode,
			Type:            streaming.EventTypeModelStarted,
			TurnID:          resolveTurnID(ctx, valueOrEmptyStr(modelCall.TurnID)),
			ParentMessageID: strings.TrimSpace(turn.ParentMessageID),
			ModelCallID:     modelCallID,
			Provider:        strings.TrimSpace(modelCall.Provider),
			ModelName:       strings.TrimSpace(modelCall.Model),
			Status:          strings.TrimSpace(modelCall.Status),
			Phase:           modelEventPhase(mode, modelCall.Iteration),
			CreatedAt:       time.Now(),
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
		logx.Infof("conversation", "stream.model_started msg=%q turn=%q iteration=%d request_payload=%q provider_request_payload=%q stream_payload=%q provider=%q model=%q",
			strings.TrimSpace(modelCall.MessageID),
			strings.TrimSpace(event.TurnID),
			modelCall.Iteration,
			strings.TrimSpace(event.RequestPayloadID),
			strings.TrimSpace(event.ProviderRequestPayloadID),
			strings.TrimSpace(event.StreamPayloadID),
			strings.TrimSpace(event.Provider),
			strings.TrimSpace(event.ModelName),
		)
		applyIterationPage(event, modelCall.Iteration)
		s.emitTimelineEvent(ctx, event, "PatchModelCall publish model_started")
	} else if status == "completed" || status == "succeeded" || status == "failed" {
		now := time.Now()
		event := &streaming.Event{
			ID:              strings.TrimSpace(modelCall.MessageID),
			StreamID:        conversationID,
			ConversationID:  conversationID,
			MessageID:       strings.TrimSpace(modelCall.MessageID),
			Mode:            mode,
			Type:            streaming.EventTypeModelCompleted,
			TurnID:          resolveTurnID(ctx, valueOrEmptyStr(modelCall.TurnID)),
			ParentMessageID: strings.TrimSpace(turn.ParentMessageID),
			ModelCallID:     modelCallID,
			Provider:        strings.TrimSpace(modelCall.Provider),
			ModelName:       strings.TrimSpace(modelCall.Model),
			Status:          strings.TrimSpace(modelCall.Status),
			Phase:           modelEventPhase(mode, modelCall.Iteration),
			CreatedAt:       now,
			CompletedAt:     &now,
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
		logx.Infof("conversation", "stream.model_completed msg=%q turn=%q iteration=%d request_payload=%q response_payload=%q provider_request_payload=%q provider_response_payload=%q stream_payload=%q provider=%q model=%q status=%q",
			strings.TrimSpace(modelCall.MessageID),
			strings.TrimSpace(event.TurnID),
			modelCall.Iteration,
			strings.TrimSpace(event.RequestPayloadID),
			strings.TrimSpace(event.ResponsePayloadID),
			strings.TrimSpace(event.ProviderRequestPayloadID),
			strings.TrimSpace(event.ProviderResponsePayloadID),
			strings.TrimSpace(event.StreamPayloadID),
			strings.TrimSpace(event.Provider),
			strings.TrimSpace(event.ModelName),
			strings.TrimSpace(event.Status),
		)
		// Include LLM response data (content, preamble, finalResponse) when
		// available via context — makes model_completed self-sufficient.
		if meta, ok := runtimerequestctx.ModelCompletionMetaFromContext(ctx); ok {
			event.Content = meta.Content
			event.Narration = meta.Narration
			// Narration carried on a model_completed event is always the
			// model's own authoring (reasoning-delta aggregate or
			// pre-tool-call framing). Tag it so clients don't conflate
			// with runtime-narrator updates.
			if strings.TrimSpace(meta.Narration) != "" {
				event.NarrationSource = "model"
			}
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
