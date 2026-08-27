package data

import (
	"context"
	"errors"
	"strings"

	"github.com/viant/agently-core/internal/sqlitewrite"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agconvlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	agconvwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	aggoal "github.com/viant/agently-core/pkg/agently/goal"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
	agmessage "github.com/viant/agently-core/pkg/agently/message"
	elicitationmsg "github.com/viant/agently-core/pkg/agently/message/elicitation"
	agelicitationcount "github.com/viant/agently-core/pkg/agently/message/elicitationCount"
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
	agapprovalcount "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/pendingCount"
	agtoolcall "github.com/viant/agently-core/pkg/agently/toolcall/byOp"
	agtoolcallbyturn "github.com/viant/agently-core/pkg/agently/toolcall/byTurn"
	agtoolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agturnctrlcount "github.com/viant/agently-core/pkg/agently/turn/controllerCount"
	agturnlistall "github.com/viant/agently-core/pkg/agently/turn/list"
	agturnnext "github.com/viant/agently-core/pkg/agently/turn/nextQueued"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	agturnlist "github.com/viant/agently-core/pkg/agently/turn/queuedList"
	agturnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	turnqueueread "github.com/viant/agently-core/pkg/agently/turnqueue/read"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
)

var ErrPermissionDenied = errors.New("permission denied")
var ErrConversationNotFound = errors.New("conversation not found")
var ErrConversationActive = errors.New("conversation is still in progress")
var ErrConversationNonTerminal = errors.New("conversation_graph_non_terminal: every non-empty conversation in the graph must be terminal")
var ErrConversationGraphReferenced = errors.New("conversation_graph_referenced: the conversation graph is referenced from outside the graph")
var ErrConversationGraphTooLarge = errors.New("conversation_graph_too_large: the conversation graph exceeds the deletion limit")
var ErrConversationScheduleReferenced = errors.New("conversation_schedule_referenced: a user schedule references the conversation graph")

// Service is a thin facade over generated Datly read components.
type Service interface {
	Raw() *datly.Service

	GetConversation(ctx context.Context, id string, in *agconv.ConversationInput, opts ...Option) (*agconv.ConversationView, error)
	GetGoal(ctx context.Context, conversationID string, in *aggoal.GoalInput, opts ...Option) (*aggoal.GoalView, error)
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
	PatchGoals(ctx context.Context, rows []*aggoalwrite.MutableGoalView) ([]*aggoalwrite.MutableGoalView, error)
	PatchMessages(ctx context.Context, rows []*agmessagewrite.MutableMessageView) ([]*agmessagewrite.MutableMessageView, error)
	PatchTurns(ctx context.Context, rows []*agturnwrite.MutableTurnView) ([]*agturnwrite.MutableTurnView, error)
	PatchModelCalls(ctx context.Context, rows []*agmodelcallwrite.MutableModelCallView) ([]*agmodelcallwrite.MutableModelCallView, error)
	PatchToolCalls(ctx context.Context, rows []*agtoolcallwrite.MutableToolCallView) ([]*agtoolcallwrite.MutableToolCallView, error)
	PatchPayloads(ctx context.Context, rows []*agpayloadwrite.MutablePayloadView) ([]*agpayloadwrite.MutablePayloadView, error)
	PatchRuns(ctx context.Context, rows []*agrunwrite.MutableRunView) ([]*agrunwrite.MutableRunView, error)

	DeleteConversations(ctx context.Context, ids ...string) error
	DeleteGoals(ctx context.Context, ids ...string) error
	DeleteConversationTree(ctx context.Context, ids ...string) error
	DeleteScheduleCascade(ctx context.Context, id string) error
	DeleteMessages(ctx context.Context, ids ...string) error
	DeleteTurns(ctx context.Context, ids ...string) error
	DeleteModelCalls(ctx context.Context, messageIDs ...string) error
	DeleteToolCalls(ctx context.Context, messageIDs ...string) error
	DeletePayloads(ctx context.Context, ids ...string) error
	DeleteRuns(ctx context.Context, ids ...string) error
}

type datlyService struct {
	dao       *datly.Service
	writeGate string
}

// NewService creates a thin data service on top of a Datly DAO.
func NewService(dao *datly.Service) Service {
	return &datlyService{dao: dao, writeGate: sqlitewrite.Key(dao, "agently")}
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
	out.Data[0].OnRelation(ctx)
	if err := authorizeConversation(out.Data[0], callOpts); err != nil {
		return nil, err
	}
	return out.Data[0], nil
}

func (s *datlyService) GetGoal(ctx context.Context, conversationID string, in *aggoal.GoalInput, opts ...Option) (*aggoal.GoalView, error) {
	input := aggoal.GoalInput{
		ConversationID: conversationID,
		Has:            &aggoal.GoalInputHas{ConversationID: true},
	}
	if in != nil {
		input = *in
		input.ConversationID = conversationID
		if input.Has == nil {
			input.Has = &aggoal.GoalInputHas{}
		}
		input.Has.ConversationID = true
	}
	out := &aggoal.GoalOutput{}
	uri := strings.ReplaceAll(aggoal.GoalPathURI, "{conversationId}", conversationID)
	operateOpts := append([]datly.OperateOption{datly.WithURI(uri), datly.WithInput(&input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
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

// CountControllerTurns returns the number of controller-owned (autonomous)
// turns created for a conversation. It backs the AutonomousTurnsUsed guard.
func (s *datlyService) CountControllerTurns(ctx context.Context, conversationID string, opts ...Option) (int, error) {
	callOpts := collectOptions(opts)
	if callOpts.principal != "" && !callOpts.isAdmin && strings.TrimSpace(conversationID) == "" {
		return 0, ErrPermissionDenied
	}
	input := &agturnctrlcount.ControllerTotalInput{
		ConversationID: conversationID,
		Has:            &agturnctrlcount.ControllerTotalInputHas{ConversationID: true},
	}
	out := &agturnctrlcount.ControllerTotalOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agturnctrlcount.ControllerTotalPathURI), datly.WithInput(input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return 0, err
	}
	if len(out.Data) == 0 {
		return 0, nil
	}
	return out.Data[0].ControllerCount, nil
}

// CountPendingApprovals returns the number of pending tool approvals for a
// conversation. It backs the PendingApproval guard.
func (s *datlyService) CountPendingApprovals(ctx context.Context, conversationID string, opts ...Option) (int, error) {
	callOpts := collectOptions(opts)
	if callOpts.principal != "" && !callOpts.isAdmin && strings.TrimSpace(conversationID) == "" {
		return 0, ErrPermissionDenied
	}
	input := &agapprovalcount.PendingTotalInput{
		ConversationID: conversationID,
		Has:            &agapprovalcount.PendingTotalInputHas{ConversationID: true},
	}
	out := &agapprovalcount.PendingTotalOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agapprovalcount.PendingTotalPathURI), datly.WithInput(input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return 0, err
	}
	if len(out.Data) == 0 {
		return 0, nil
	}
	return out.Data[0].PendingCount, nil
}

// CountPendingElicitations returns the number of unresolved elicitation
// requests for a conversation. It backs the PendingElicitation guard.
func (s *datlyService) CountPendingElicitations(ctx context.Context, conversationID string, opts ...Option) (int, error) {
	callOpts := collectOptions(opts)
	if callOpts.principal != "" && !callOpts.isAdmin && strings.TrimSpace(conversationID) == "" {
		return 0, ErrPermissionDenied
	}
	input := &agelicitationcount.ElicitationPendingInput{
		ConversationID: conversationID,
		Has:            &agelicitationcount.ElicitationPendingInputHas{ConversationID: true},
	}
	out := &agelicitationcount.ElicitationPendingOutput{}
	operateOpts := append([]datly.OperateOption{datly.WithURI(agelicitationcount.ElicitationPendingPathURI), datly.WithInput(input), datly.WithOutput(out)}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return 0, err
	}
	if len(out.Data) == 0 {
		return 0, nil
	}
	return out.Data[0].PendingCount, nil
}

func (s *datlyService) ListTurnQueueRows(ctx context.Context, in *turnqueueread.QueueRowsInput, opts ...Option) ([]*turnqueueread.QueueRowView, error) {
	if in == nil {
		in = &turnqueueread.QueueRowsInput{}
	}
	out := &turnqueueread.QueueRowsOutput{}
	operateOpts := append([]datly.OperateOption{
		datly.WithURI(turnqueueread.QueueRowsPathURI),
		datly.WithInput(in),
		datly.WithOutput(out),
	}, toOperateOptions(opts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (s *datlyService) PatchTurnQueue(ctx context.Context, in *turnqueuewrite.TurnQueue) error {
	if in == nil {
		return errors.New("turn queue input is required")
	}
	input := &turnqueuewrite.Input{Queues: []*turnqueuewrite.TurnQueue{in}}
	out := &turnqueuewrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("PATCH", turnqueuewrite.PathURI)),
		datly.WithInput(input),
		datly.WithOutput(out),
	)
	return err
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

func (s *datlyService) GetToolMessageIDsByTurn(ctx context.Context, conversationID, turnID string) (map[string]string, error) {
	input := &agtoolcallbyturn.ToolCallRowsInput{
		ConversationId: strings.TrimSpace(conversationID),
		TurnId:         strings.TrimSpace(turnID),
		Has: &agtoolcallbyturn.ToolCallRowsInputHas{
			ConversationId: true,
			TurnId:         true,
		},
	}
	out := &agtoolcallbyturn.ToolCallRowsOutput{}
	if _, err := s.dao.Operate(ctx,
		datly.WithURI(agtoolcallbyturn.ToolCallRowsPathURI),
		datly.WithInput(input),
		datly.WithOutput(out),
	); err != nil {
		return nil, err
	}
	type selectedMessage struct {
		id      string
		attempt int
	}
	selected := make(map[string]selectedMessage, len(out.Data))
	for _, row := range out.Data {
		if row == nil {
			continue
		}
		opID := strings.TrimSpace(row.OpId)
		messageID := strings.TrimSpace(row.MessageId)
		if opID == "" || messageID == "" {
			continue
		}
		current, ok := selected[opID]
		if ok && (current.attempt > row.Attempt || current.attempt == row.Attempt && current.id >= messageID) {
			continue
		}
		selected[opID] = selectedMessage{id: messageID, attempt: row.Attempt}
	}
	result := make(map[string]string, len(selected))
	for opID, candidate := range selected {
		result[opID] = candidate.id
	}
	return result, nil
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

func (s *datlyService) PatchGoals(ctx context.Context, rows []*aggoalwrite.MutableGoalView) ([]*aggoalwrite.MutableGoalView, error) {
	in := &aggoalwrite.Input{Goals: rows}
	out := &aggoalwrite.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", aggoalwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return out.Data, err
	}
	return out.Data, nil
}

func (s *datlyService) PatchMessages(ctx context.Context, rows []*agmessagewrite.MutableMessageView) ([]*agmessagewrite.MutableMessageView, error) {
	payloadDiagPatchMessages("before", rows, nil)
	in := &agmessagewrite.Input{Messages: rows}
	out := &agmessagewrite.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agmessagewrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		payloadDiagPatchMessages("error", rows, err)
		return out.Data, err
	}
	payloadDiagPatchMessages("after", out.Data, nil)
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
	return sqlitewrite.Do(ctx, s.writeGate, func() ([]*agmodelcallwrite.MutableModelCallView, error) {
		payloadDiagPatchModelCalls("before", rows, nil)
		in := &agmodelcallwrite.Input{ModelCalls: rows}
		out := &agmodelcallwrite.Output{}
		if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agmodelcallwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
			payloadDiagPatchModelCalls("error", rows, err)
			return out.Data, err
		}
		payloadDiagPatchModelCalls("after", out.Data, nil)
		return out.Data, nil
	})
}

func (s *datlyService) PatchToolCalls(ctx context.Context, rows []*agtoolcallwrite.MutableToolCallView) ([]*agtoolcallwrite.MutableToolCallView, error) {
	return sqlitewrite.Do(ctx, s.writeGate, func() ([]*agtoolcallwrite.MutableToolCallView, error) {
		payloadDiagPatchToolCalls("before", rows, nil)
		for _, row := range rows {
			if row == nil || row.Has == nil || !row.Has.ErrorMessage || row.ErrorMessage == nil {
				continue
			}
			sanitized := agtoolcallwrite.SanitizeErrorMessage(*row.ErrorMessage)
			row.ErrorMessage = &sanitized
		}
		in := &agtoolcallwrite.Input{ToolCalls: rows}
		out := &agtoolcallwrite.Output{}
		if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agtoolcallwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
			payloadDiagPatchToolCalls("error", rows, err)
			return out.Data, err
		}
		payloadDiagPatchToolCalls("after", out.Data, nil)
		return out.Data, nil
	})
}

func (s *datlyService) PatchPayloads(ctx context.Context, rows []*agpayloadwrite.MutablePayloadView) ([]*agpayloadwrite.MutablePayloadView, error) {
	return sqlitewrite.Do(ctx, s.writeGate, func() ([]*agpayloadwrite.MutablePayloadView, error) {
		payloadDiagPatchPayloads("before", rows, nil)
		in := &agpayloadwrite.Input{Payloads: rows}
		out := &agpayloadwrite.Output{}
		if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agpayloadwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
			payloadDiagPatchPayloads("error", rows, err)
			return out.Data, err
		}
		payloadDiagPatchPayloads("after", out.Data, nil)
		return out.Data, nil
	})
}

func (s *datlyService) PatchRuns(ctx context.Context, rows []*agrunwrite.MutableRunView) ([]*agrunwrite.MutableRunView, error) {
	return sqlitewrite.Do(ctx, s.writeGate, func() ([]*agrunwrite.MutableRunView, error) {
		in := &agrunwrite.Input{Runs: rows}
		out := &agrunwrite.Output{}
		if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", agrunwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out)); err != nil {
			return out.Data, err
		}
		return out.Data, nil
	})
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

func (s *datlyService) DeleteGoals(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	rows := make([]*aggoalwrite.MutableGoalView, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		rows = append(rows, aggoalwrite.NewMutableGoalView(aggoalwrite.WithGoalID(id)))
	}
	in := &aggoalwrite.DeleteInput{Rows: rows}
	out := &aggoalwrite.DeleteOutput{}
	_, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", aggoalwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
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
