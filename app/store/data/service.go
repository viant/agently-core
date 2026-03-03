package data

import (
	"context"
	"errors"
	"strings"

	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agconvlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	agconvwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	agmessage "github.com/viant/agently-core/pkg/agently/message"
	elicitationmsg "github.com/viant/agently-core/pkg/agently/message/elicitation"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
	agmessagewrite "github.com/viant/agently-core/pkg/agently/message/write"
	agmodelcallwrite "github.com/viant/agently-core/pkg/agently/modelcall/write"
	agpayload "github.com/viant/agently-core/pkg/agently/payload"
	agpayloadwrite "github.com/viant/agently-core/pkg/agently/payload/write"
	agrun "github.com/viant/agently-core/pkg/agently/run"
	agrunactive "github.com/viant/agently-core/pkg/agently/run/active"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	agrunsteps "github.com/viant/agently-core/pkg/agently/run/steps"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	agtoolcall "github.com/viant/agently-core/pkg/agently/toolcall/byOp"
	agtoolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agturnlistall "github.com/viant/agently-core/pkg/agently/turn/list"
	agturnnext "github.com/viant/agently-core/pkg/agently/turn/nextQueued"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	agturnlist "github.com/viant/agently-core/pkg/agently/turn/queuedList"
	agturnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
	hstate "github.com/viant/xdatly/handler/state"
)

var ErrPermissionDenied = errors.New("permission denied")

// Service is a thin facade over generated Datly read components.
type Service interface {
	Raw() *datly.Service

	GetConversation(ctx context.Context, id string, in *agconv.ConversationInput, opts ...Option) (*agconv.ConversationView, error)
	ListConversations(ctx context.Context, in *agconvlist.ConversationRowsInput, page *PageInput, opts ...Option) (*ConversationPage, error)
	GetMessage(ctx context.Context, id string, in *agmessage.MessageInput, opts ...Option) (*agmessage.MessageView, error)
	GetMessagesPage(ctx context.Context, in *agmessagelist.MessageRowsInput, page *PageInput, opts ...Option) (*MessagePage, error)
	GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string, opts ...Option) (*elicitationmsg.MessageView, error)

	GetRun(ctx context.Context, id string, in *agrun.RunRowsInput, opts ...Option) (*agrun.RunRowsView, error)
	GetRunStepsPage(ctx context.Context, in *agrunsteps.RunStepsInput, page *PageInput, opts ...Option) (*RunStepPage, error)
	GetActiveRun(ctx context.Context, in *agrunactive.ActiveRunsInput, opts ...Option) (*agrunactive.ActiveRunsView, error)
	ListStaleRuns(ctx context.Context, in *agrunstale.StaleRunsInput, opts ...Option) ([]*agrunstale.StaleRunsView, error)

	GetActiveTurn(ctx context.Context, in *agturnactive.ActiveTurnsInput, opts ...Option) (*agturnactive.ActiveTurnsView, error)
	GetTurnByID(ctx context.Context, in *agturnbyid.TurnLookupInput, opts ...Option) (*agturnbyid.TurnLookupView, error)
	GetTurnsPage(ctx context.Context, in *agturnlistall.TurnRowsInput, page *PageInput, opts ...Option) (*TurnPage, error)
	GetNextQueuedTurn(ctx context.Context, in *agturnnext.QueuedTurnInput, opts ...Option) (*agturnnext.QueuedTurnView, error)
	ListQueuedTurns(ctx context.Context, in *agturnlist.QueuedTurnsInput, opts ...Option) ([]*agturnlist.QueuedTurnsView, error)
	CountQueuedTurns(ctx context.Context, in *agturncount.QueuedTotalInput, opts ...Option) (int, error)

	GetToolCallByOp(ctx context.Context, opID string, in *agtoolcall.ToolCallRowsInput, opts ...Option) ([]*agtoolcall.ToolCallRowsView, error)
	ListPayloadRows(ctx context.Context, in *agpayload.PayloadRowsInput, opts ...Option) ([]*agpayload.PayloadRowsView, error)

	ListGeneratedFiles(ctx context.Context, conversationID string, opts ...Option) ([]*gfread.GeneratedFileView, error)

	PatchConversations(ctx context.Context, rows []*agconvwrite.MutableConversationView) ([]*agconvwrite.MutableConversationView, error)
	PatchMessages(ctx context.Context, rows []*agmessagewrite.MutableMessageView) ([]*agmessagewrite.MutableMessageView, error)
	PatchTurns(ctx context.Context, rows []*agturnwrite.MutableTurnView) ([]*agturnwrite.MutableTurnView, error)
	PatchModelCalls(ctx context.Context, rows []*agmodelcallwrite.MutableModelCallView) ([]*agmodelcallwrite.MutableModelCallView, error)
	PatchToolCalls(ctx context.Context, rows []*agtoolcallwrite.MutableToolCallView) ([]*agtoolcallwrite.MutableToolCallView, error)
	PatchPayloads(ctx context.Context, rows []*agpayloadwrite.MutablePayloadView) ([]*agpayloadwrite.MutablePayloadView, error)
	PatchRuns(ctx context.Context, rows []*agrunwrite.MutableRunView) ([]*agrunwrite.MutableRunView, error)

	DeleteConversations(ctx context.Context, ids ...string) error
	DeleteMessages(ctx context.Context, ids ...string) error
	DeleteTurns(ctx context.Context, ids ...string) error
	DeleteModelCalls(ctx context.Context, messageIDs ...string) error
	DeleteToolCalls(ctx context.Context, messageIDs ...string) error
	DeletePayloads(ctx context.Context, ids ...string) error
	DeleteRuns(ctx context.Context, ids ...string) error
}

type datlyService struct {
	dao *datly.Service
}

type Direction string

const (
	DirectionBefore Direction = "before"
	DirectionAfter  Direction = "after"
	DirectionLatest Direction = "latest"
)

type PageInput struct {
	Limit     int
	Cursor    string
	Direction Direction
}

type ConversationPage struct {
	Rows       []*agconvlist.ConversationRowsView
	NextCursor string
	PrevCursor string
	HasMore    bool
}

type MessagePage struct {
	Rows       []*agmessagelist.MessageRowsView
	NextCursor string
	PrevCursor string
	HasMore    bool
}

type TurnPage struct {
	Rows       []*agturnlistall.TurnRowsView
	NextCursor string
	PrevCursor string
	HasMore    bool
}

type RunStepPage struct {
	Rows       []*agrunsteps.RunStepsView
	NextCursor string
	PrevCursor string
	HasMore    bool
}

type options struct {
	selectors []*hstate.NamedQuerySelector
	principal string
	isAdmin   bool
}

// Option customizes read execution without changing generated input DTOs.
type Option func(*options)

// WithQuerySelector delegates pagination/projection/order constraints to Datly selectors.
func WithQuerySelector(selectors ...*hstate.NamedQuerySelector) Option {
	return func(o *options) {
		o.selectors = append(o.selectors, selectors...)
	}
}

// WithPrincipal enforces conversation visibility rules for reads.
func WithPrincipal(userID string) Option {
	return func(o *options) {
		o.principal = userID
	}
}

// WithAdminPrincipal bypasses visibility restrictions.
func WithAdminPrincipal(userID string) Option {
	return func(o *options) {
		o.principal = userID
		o.isAdmin = true
	}
}

func toOperateOptions(opts []Option) []datly.OperateOption {
	callOpts := collectOptions(opts)
	if len(callOpts.selectors) == 0 {
		return nil
	}
	return []datly.OperateOption{
		datly.WithSessionOptions(datly.WithQuerySelectors(callOpts.selectors...)),
	}
}

func collectOptions(opts []Option) *options {
	if len(opts) == 0 {
		return &options{}
	}
	callOpts := &options{}
	for _, opt := range opts {
		if opt != nil {
			opt(callOpts)
		}
	}
	return callOpts
}

// NewService creates a thin data service on top of a Datly DAO.
func NewService(dao *datly.Service) Service {
	return &datlyService{dao: dao}
}

func (s *datlyService) Raw() *datly.Service {
	return s.dao
}

func (s *datlyService) GetConversation(ctx context.Context, id string, in *agconv.ConversationInput, opts ...Option) (*agconv.ConversationView, error) {
	callOpts := collectOptions(opts)
	input := agconv.ConversationInput{Id: id, Has: &agconv.ConversationInputHas{Id: true}}
	if in != nil {
		input = *in
		input.Id = id
		if input.Has == nil {
			input.Has = &agconv.ConversationInputHas{}
		}
		input.Has.Id = true
	}
	out := &agconv.ConversationOutput{}
	uri := strings.ReplaceAll(agconv.ConversationPathURI, "{id}", id)
	operateOpts := append([]datly.OperateOption{datly.WithURI(uri), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	if err := authorizeConversation(out.Data[0], callOpts); err != nil {
		return nil, err
	}
	return out.Data[0], nil
}

func (s *datlyService) GetMessage(ctx context.Context, id string, in *agmessage.MessageInput, opts ...Option) (*agmessage.MessageView, error) {
	callOpts := collectOptions(opts)
	input := agmessage.MessageInput{Id: id, Has: &agmessage.MessageInputHas{Id: true}}
	if in != nil {
		input = *in
		input.Id = id
		if input.Has == nil {
			input.Has = &agmessage.MessageInputHas{}
		}
		input.Has.Id = true
	}
	out := &agmessage.MessageOutput{}
	uri := strings.ReplaceAll(agmessage.MessagePathURI, "{id}", id)
	operateOpts := append([]datly.OperateOption{datly.WithURI(uri), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	if err := s.authorizeConversationID(ctx, out.Data[0].ConversationId, callOpts, nil); err != nil {
		return nil, err
	}
	return out.Data[0], nil
}

func (s *datlyService) GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string, opts ...Option) (*elicitationmsg.MessageView, error) {
	callOpts := collectOptions(opts)
	if err := s.authorizeConversationID(ctx, conversationID, callOpts, nil); err != nil {
		return nil, err
	}
	input := elicitationmsg.MessageInput{
		ConversationId: conversationID,
		ElicitationId:  elicitationID,
		Has:            &elicitationmsg.MessageInputHas{ConversationId: true, ElicitationId: true},
	}
	out := &elicitationmsg.MessageOutput{}
	uri := strings.ReplaceAll(
		strings.ReplaceAll(elicitationmsg.MessagePathURI, "{convId}", conversationID),
		"{elicId}", elicitationID,
	)
	operateOpts := append([]datly.OperateOption{datly.WithURI(uri), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	return out.Data[0], nil
}

func (s *datlyService) GetRun(ctx context.Context, id string, in *agrun.RunRowsInput, opts ...Option) (*agrun.RunRowsView, error) {
	callOpts := collectOptions(opts)
	input := agrun.RunRowsInput{Id: id, Has: &agrun.RunRowsInputHas{Id: true}}
	if in != nil {
		input = *in
		input.Id = id
		if input.Has == nil {
			input.Has = &agrun.RunRowsInputHas{}
		}
		input.Has.Id = true
	}
	out := &agrun.RunRowsOutput{}
	uri := strings.ReplaceAll(agrun.RunRowsPathURI, "{id}", id)
	operateOpts := append([]datly.OperateOption{datly.WithURI(uri), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	if out.Data[0].ConversationId != nil {
		if err := s.authorizeConversationID(ctx, *out.Data[0].ConversationId, callOpts, nil); err != nil {
			return nil, err
		}
	}
	return out.Data[0], nil
}

func (s *datlyService) GetActiveRun(ctx context.Context, in *agrunactive.ActiveRunsInput, opts ...Option) (*agrunactive.ActiveRunsView, error) {
	callOpts := collectOptions(opts)
	input := agrunactive.ActiveRunsInput{Has: &agrunactive.ActiveRunsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agrunactive.ActiveRunsInputHas{}
		}
	}
	out := &agrunactive.ActiveRunsOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agrunactive.ActiveRunsPathURI), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	if out.Data[0].ConversationId != nil {
		if err := s.authorizeConversationID(ctx, *out.Data[0].ConversationId, callOpts, nil); err != nil {
			return nil, err
		}
	}
	return out.Data[0], nil
}

func (s *datlyService) ListStaleRuns(ctx context.Context, in *agrunstale.StaleRunsInput, opts ...Option) ([]*agrunstale.StaleRunsView, error) {
	callOpts := collectOptions(opts)
	input := agrunstale.StaleRunsInput{Has: &agrunstale.StaleRunsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agrunstale.StaleRunsInputHas{}
		}
	}
	out := &agrunstale.StaleRunsOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agrunstale.StaleRunsPathURI), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	if callOpts.principal == "" || callOpts.isAdmin {
		return out.Data, nil
	}
	cache := newAuthCache()
	filtered := make([]*agrunstale.StaleRunsView, 0, len(out.Data))
	for _, row := range out.Data {
		if row == nil || row.ConversationId == nil {
			continue
		}
		if err := s.authorizeConversationID(ctx, *row.ConversationId, callOpts, cache); err == nil {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

func (s *datlyService) GetActiveTurn(ctx context.Context, in *agturnactive.ActiveTurnsInput, opts ...Option) (*agturnactive.ActiveTurnsView, error) {
	callOpts := collectOptions(opts)
	input := agturnactive.ActiveTurnsInput{Has: &agturnactive.ActiveTurnsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agturnactive.ActiveTurnsInputHas{}
		}
	}
	out := &agturnactive.ActiveTurnsOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agturnactive.ActiveTurnsPathURI), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	if err := s.authorizeConversationID(ctx, out.Data[0].ConversationId, callOpts, nil); err != nil {
		return nil, err
	}
	return out.Data[0], nil
}

func (s *datlyService) GetTurnByID(ctx context.Context, in *agturnbyid.TurnLookupInput, opts ...Option) (*agturnbyid.TurnLookupView, error) {
	callOpts := collectOptions(opts)
	input := agturnbyid.TurnLookupInput{Has: &agturnbyid.TurnLookupInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agturnbyid.TurnLookupInputHas{}
		}
	}
	out := &agturnbyid.TurnLookupOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agturnbyid.TurnLookupPathURI), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	if err := s.authorizeConversationID(ctx, out.Data[0].ConversationId, callOpts, nil); err != nil {
		return nil, err
	}
	return out.Data[0], nil
}

func (s *datlyService) GetNextQueuedTurn(ctx context.Context, in *agturnnext.QueuedTurnInput, opts ...Option) (*agturnnext.QueuedTurnView, error) {
	callOpts := collectOptions(opts)
	if callOpts.principal != "" && !callOpts.isAdmin && (in == nil || strings.TrimSpace(in.ConversationID) == "") {
		return nil, ErrPermissionDenied
	}
	input := agturnnext.QueuedTurnInput{Has: &agturnnext.QueuedTurnInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agturnnext.QueuedTurnInputHas{}
		}
	}
	out := &agturnnext.QueuedTurnOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agturnnext.QueuedTurnPathURI), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	if err := s.authorizeConversationID(ctx, out.Data[0].ConversationId, callOpts, nil); err != nil {
		return nil, err
	}
	return out.Data[0], nil
}

func (s *datlyService) ListQueuedTurns(ctx context.Context, in *agturnlist.QueuedTurnsInput, opts ...Option) ([]*agturnlist.QueuedTurnsView, error) {
	callOpts := collectOptions(opts)
	if callOpts.principal != "" && !callOpts.isAdmin && (in == nil || strings.TrimSpace(in.ConversationID) == "") {
		return nil, ErrPermissionDenied
	}
	input := agturnlist.QueuedTurnsInput{Has: &agturnlist.QueuedTurnsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agturnlist.QueuedTurnsInputHas{}
		}
	}
	out := &agturnlist.QueuedTurnsOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agturnlist.QueuedTurnsPathURI), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (s *datlyService) CountQueuedTurns(ctx context.Context, in *agturncount.QueuedTotalInput, opts ...Option) (int, error) {
	callOpts := collectOptions(opts)
	if callOpts.principal != "" && !callOpts.isAdmin && (in == nil || strings.TrimSpace(in.ConversationID) == "") {
		return 0, ErrPermissionDenied
	}
	input := agturncount.QueuedTotalInput{Has: &agturncount.QueuedTotalInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agturncount.QueuedTotalInputHas{}
		}
	}
	out := &agturncount.QueuedTotalOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agturncount.QueuedTotalPathURI), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return 0, err
	}
	if len(out.Data) == 0 {
		return 0, nil
	}
	return out.Data[0].QueuedCount, nil
}

func (s *datlyService) GetToolCallByOp(ctx context.Context, opID string, in *agtoolcall.ToolCallRowsInput, opts ...Option) ([]*agtoolcall.ToolCallRowsView, error) {
	input := agtoolcall.ToolCallRowsInput{OpId: opID, Has: &agtoolcall.ToolCallRowsInputHas{OpId: true}}
	if in != nil {
		input = *in
		input.OpId = opID
		if input.Has == nil {
			input.Has = &agtoolcall.ToolCallRowsInputHas{}
		}
		input.Has.OpId = true
	}
	out := &agtoolcall.ToolCallRowsOutput{}
	uri := strings.ReplaceAll(agtoolcall.ToolCallRowsPathURI, "{opId}", opID)
	operateOpts := append([]datly.OperateOption{datly.WithURI(uri), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (s *datlyService) ListPayloadRows(ctx context.Context, in *agpayload.PayloadRowsInput, opts ...Option) ([]*agpayload.PayloadRowsView, error) {
	input := agpayload.PayloadRowsInput{Has: &agpayload.PayloadRowsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agpayload.PayloadRowsInputHas{}
		}
	}
	out := &agpayload.PayloadRowsOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agpayload.PayloadRowsPathURI), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (s *datlyService) PatchConversations(ctx context.Context, rows []*agconvwrite.MutableConversationView) ([]*agconvwrite.MutableConversationView, error) {
	in := &agconvwrite.Input{Conversations: rows}
	out := &agconvwrite.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agconvwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return out.Data, err
	}
	return out.Data, nil
}

func (s *datlyService) PatchMessages(ctx context.Context, rows []*agmessagewrite.MutableMessageView) ([]*agmessagewrite.MutableMessageView, error) {
	in := &agmessagewrite.Input{Messages: rows}
	out := &agmessagewrite.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agmessagewrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return out.Data, err
	}
	return out.Data, nil
}

func (s *datlyService) PatchTurns(ctx context.Context, rows []*agturnwrite.MutableTurnView) ([]*agturnwrite.MutableTurnView, error) {
	in := &agturnwrite.Input{Turns: rows}
	out := &agturnwrite.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agturnwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return out.Data, err
	}
	return out.Data, nil
}

func (s *datlyService) PatchModelCalls(ctx context.Context, rows []*agmodelcallwrite.MutableModelCallView) ([]*agmodelcallwrite.MutableModelCallView, error) {
	in := &agmodelcallwrite.Input{ModelCalls: rows}
	out := &agmodelcallwrite.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agmodelcallwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return out.Data, err
	}
	return out.Data, nil
}

func (s *datlyService) PatchToolCalls(ctx context.Context, rows []*agtoolcallwrite.MutableToolCallView) ([]*agtoolcallwrite.MutableToolCallView, error) {
	in := &agtoolcallwrite.Input{ToolCalls: rows}
	out := &agtoolcallwrite.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agtoolcallwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return out.Data, err
	}
	return out.Data, nil
}

func (s *datlyService) PatchPayloads(ctx context.Context, rows []*agpayloadwrite.MutablePayloadView) ([]*agpayloadwrite.MutablePayloadView, error) {
	in := &agpayloadwrite.Input{Payloads: rows}
	out := &agpayloadwrite.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agpayloadwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return out.Data, err
	}
	return out.Data, nil
}

func (s *datlyService) PatchRuns(ctx context.Context, rows []*agrunwrite.MutableRunView) ([]*agrunwrite.MutableRunView, error) {
	in := &agrunwrite.Input{Runs: rows}
	out := &agrunwrite.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agrunwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return out.Data, err
	}
	return out.Data, nil
}

func (s *datlyService) DeleteConversations(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	rows := make([]*agconvwrite.MutableConversationView, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		rows = append(rows, agconvwrite.NewMutableConversationView(agconvwrite.WithConversationID(id)))
	}
	in := &agconvwrite.DeleteInput{Rows: rows}
	out := &agconvwrite.DeleteOutput{}
	_, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", agconvwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
	return err
}

func (s *datlyService) DeleteMessages(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	rows := make([]*agmessagewrite.MutableMessageView, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		rows = append(rows, agmessagewrite.NewMutableMessageView(agmessagewrite.WithMessageID(id)))
	}
	in := &agmessagewrite.DeleteInput{Rows: rows}
	out := &agmessagewrite.DeleteOutput{}
	_, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", agmessagewrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
	return err
}

func (s *datlyService) DeleteTurns(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	rows := make([]*agturnwrite.MutableTurnView, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		rows = append(rows, agturnwrite.NewMutableTurnView(agturnwrite.WithTurnID(id)))
	}
	in := &agturnwrite.DeleteInput{Rows: rows}
	out := &agturnwrite.DeleteOutput{}
	_, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", agturnwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
	return err
}

func (s *datlyService) DeleteModelCalls(ctx context.Context, messageIDs ...string) error {
	if len(messageIDs) == 0 {
		return nil
	}
	rows := make([]*agmodelcallwrite.MutableModelCallView, 0, len(messageIDs))
	for _, id := range messageIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		rows = append(rows, agmodelcallwrite.NewMutableModelCallView(agmodelcallwrite.WithModelCallMessageID(id)))
	}
	in := &agmodelcallwrite.DeleteInput{Rows: rows}
	out := &agmodelcallwrite.DeleteOutput{}
	_, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", agmodelcallwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
	return err
}

func (s *datlyService) DeleteToolCalls(ctx context.Context, messageIDs ...string) error {
	if len(messageIDs) == 0 {
		return nil
	}
	rows := make([]*agtoolcallwrite.MutableToolCallView, 0, len(messageIDs))
	for _, id := range messageIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		rows = append(rows, agtoolcallwrite.NewMutableToolCallView(agtoolcallwrite.WithToolCallMessageID(id)))
	}
	in := &agtoolcallwrite.DeleteInput{Rows: rows}
	out := &agtoolcallwrite.DeleteOutput{}
	_, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", agtoolcallwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
	return err
}

func (s *datlyService) DeletePayloads(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	rows := make([]*agpayloadwrite.MutablePayloadView, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		rows = append(rows, agpayloadwrite.NewMutablePayloadView(agpayloadwrite.WithPayloadID(id)))
	}
	in := &agpayloadwrite.DeleteInput{Rows: rows}
	out := &agpayloadwrite.DeleteOutput{}
	_, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", agpayloadwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
	return err
}

func (s *datlyService) DeleteRuns(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	rows := make([]*agrunwrite.MutableRunView, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		rows = append(rows, agrunwrite.NewMutableRunView(agrunwrite.WithRunID(id)))
	}
	in := &agrunwrite.DeleteInput{Rows: rows}
	out := &agrunwrite.DeleteOutput{}
	_, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", agrunwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
	return err
}

func (s *datlyService) ListGeneratedFiles(ctx context.Context, conversationID string, opts ...Option) ([]*gfread.GeneratedFileView, error) {
	callOpts := collectOptions(opts)
	if err := s.authorizeConversationID(ctx, conversationID, callOpts, nil); err != nil {
		return nil, err
	}
	input := &gfread.Input{
		ConversationID: conversationID,
		Has:            &gfread.Has{ConversationID: true},
	}
	out := &gfread.Output{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(gfread.URI), datly.WithInput(input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func authorizeConversation(item *agconv.ConversationView, opts *options) error {
	if item == nil || opts == nil || opts.principal == "" || opts.isAdmin {
		return nil
	}
	if strings.EqualFold(item.Visibility, "public") {
		return nil
	}
	if item.CreatedByUserId != nil && *item.CreatedByUserId == opts.principal {
		return nil
	}
	return ErrPermissionDenied
}

type authCache struct {
	byConversationID map[string]error
}

func newAuthCache() *authCache {
	return &authCache{byConversationID: map[string]error{}}
}

func (s *datlyService) authorizeConversationID(ctx context.Context, conversationID string, opts *options, cache *authCache) error {
	if opts == nil || opts.principal == "" || opts.isAdmin {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ErrPermissionDenied
	}
	if cache != nil {
		if err, ok := cache.byConversationID[conversationID]; ok {
			return err
		}
	}
	conv, err := s.loadConversationForAuth(ctx, conversationID)
	if err != nil {
		if cache != nil {
			cache.byConversationID[conversationID] = err
		}
		return err
	}
	err = authorizeConversation(conv, opts)
	if cache != nil {
		cache.byConversationID[conversationID] = err
	}
	return err
}

func (s *datlyService) loadConversationForAuth(ctx context.Context, id string) (*agconv.ConversationView, error) {
	input := &agconv.ConversationInput{Id: id, Has: &agconv.ConversationInputHas{Id: true}}
	out := &agconv.ConversationOutput{}
	uri := strings.ReplaceAll(agconv.ConversationPathURI, "{id}", id)
	if _, err := s.dao.Operate(ctx, datly.WithURI(uri), datly.WithInput(input), datly.WithOutput(out)); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, ErrPermissionDenied
	}
	return out.Data[0], nil
}
