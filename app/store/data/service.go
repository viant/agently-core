package data

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/sqlitewrite"
	agentrun "github.com/viant/agently-core/internal/store/agentrun"
	convstore "github.com/viant/agently-core/internal/store/conversation"
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
	turnqueueread "github.com/viant/agently-core/pkg/agently/turnqueue/read"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	dexec "github.com/viant/datly/exec"
)

var ErrPermissionDenied = errors.New("permission denied")
var ErrConversationNotFound = errors.New("conversation not found")
var ErrConversationActive = errors.New("conversation is still in progress")
var ErrConversationNonTerminal = errors.New("conversation_graph_non_terminal: every non-empty conversation in the graph must be terminal")
var ErrConversationGraphReferenced = errors.New("conversation_graph_referenced: the conversation graph is referenced from outside the graph")
var ErrConversationGraphTooLarge = errors.New("conversation_graph_too_large: the conversation graph exceeds the deletion limit")
var ErrConversationScheduleReferenced = errors.New("conversation_schedule_referenced: a user schedule references the conversation graph")
var ErrInvalidConversationMaintenanceRequest = errors.New("invalid conversation maintenance request")
var ErrScheduledRunNotFound = errors.New("scheduled run not found")
var ErrMaintenanceLeaseLost = errors.New("maintenance lease is no longer owned by this worker")
var ErrInvalidMaintenanceLease = errors.New("invalid maintenance lease request")

// Service is a thin facade over generated Datly read components.
type Service interface {
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
	DeleteConversationTree(ctx context.Context, ids ...string) error
	ListConversationMaintenanceCandidates(ctx context.Context, request ConversationMaintenanceCandidateRequest) ([]ConversationMaintenanceCandidate, error)
	MaintainConversationTree(ctx context.Context, request ConversationMaintenanceRequest) (*ConversationMaintenanceResult, error)
	ListScheduledRunMaintenanceCandidates(ctx context.Context, request ScheduledRunMaintenanceCandidateRequest) ([]ScheduledRunMaintenanceCandidate, error)
	MaintainScheduledRun(ctx context.Context, request ScheduledRunMaintenanceRequest) (*ScheduledRunMaintenanceResult, error)
	ListOrphanMaintenanceCandidates(ctx context.Context, request OrphanMaintenanceCandidateRequest) ([]OrphanMaintenanceCandidate, error)
	MaintainOrphanCandidate(ctx context.Context, request OrphanMaintenanceRequest) (*OrphanMaintenanceResult, error)
	ListTechnicalMaintenanceCandidates(ctx context.Context, request TechnicalMaintenanceCandidateRequest) ([]TechnicalMaintenanceCandidate, error)
	MaintainTechnicalCandidate(ctx context.Context, request TechnicalMaintenanceRequest) (*TechnicalMaintenanceResult, error)
	AcquireMaintenanceLease(ctx context.Context, request MaintenanceLeaseAcquireRequest) (*MaintenanceLeaseAcquireResult, error)
	RenewMaintenanceLease(ctx context.Context, lease MaintenanceLease, ttl time.Duration) (*MaintenanceLeaseRenewResult, error)
	ReleaseMaintenanceLease(ctx context.Context, lease MaintenanceLease) (bool, error)
	DeleteExpiredMaintenanceLeases(ctx context.Context, lease MaintenanceLease) (int64, error)
	DeleteScheduledRun(ctx context.Context, id string) error
	DeleteScheduleCascade(ctx context.Context, id string) error
	DeleteMessages(ctx context.Context, ids ...string) error
	DeleteTurns(ctx context.Context, ids ...string) error
	DeleteModelCalls(ctx context.Context, messageIDs ...string) error
	DeleteToolCalls(ctx context.Context, messageIDs ...string) error
	DeletePayloads(ctx context.Context, ids ...string) error
	DeleteRuns(ctx context.Context, ids ...string) error
}

type datlyService struct {
	native     dexec.ComponentInvoker
	writeGate  string
	closeOwned func(context.Context) error
}

// ServiceOption binds application-owned execution policy to a data facade.
type ServiceOption func(*datlyService)

// WithWriteGate coordinates custom invokers that share a SQLite connection.
func WithWriteGate(key string) ServiceOption {
	return func(s *datlyService) { s.writeGate = strings.TrimSpace(key) }
}

type connectionMetadata interface {
	ConfiguredDriver(context.Context, string) (string, error)
	ConnectionIdentity(context.Context, string) (string, error)
}

// NewService binds the facade to the shared stock component runtime.
func NewService(invoker dexec.ComponentInvoker, opts ...ServiceOption) Service {
	service := &datlyService{}
	if invoker != nil {
		value := reflect.ValueOf(invoker)
		if value.Kind() != reflect.Pointer || !value.IsNil() {
			service.native = invoker
		}
	}
	if metadata, ok := service.native.(connectionMetadata); ok {
		ctx := context.Background()
		driver, err := metadata.ConfiguredDriver(ctx, "agently")
		if err == nil {
			identity, err := metadata.ConnectionIdentity(ctx, "agently")
			if err == nil {
				service.writeGate = sqlitewrite.KeyForConnector(driver, identity, "agently")
			}
		}
	}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	return service
}

func (s *datlyService) GetConversation(ctx context.Context, id string, in *agconv.ConversationInput, opts ...Option) (*agconv.ConversationView, error) {
	return s.getConversationNative(ctx, id, in, collectOptions(opts))
}

func (s *datlyService) GetMessage(ctx context.Context, id string, in *agmessage.MessageInput, opts ...Option) (*agmessage.MessageView, error) {
	return s.getMessageNative(ctx, id, in, collectOptions(opts))
}

func (s *datlyService) GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string, opts ...Option) (*elicitationmsg.MessageView, error) {
	return s.getMessageByElicitationNative(ctx, conversationID, elicitationID, collectOptions(opts))
}

func (s *datlyService) GetRun(ctx context.Context, id string, in *agrun.RunRowsInput, opts ...Option) (*agrun.RunRowsView, error) {
	return s.getRunNative(ctx, id, in, collectOptions(opts))
}

func (s *datlyService) GetActiveRun(ctx context.Context, in *agrunactive.ActiveRunsInput, opts ...Option) (*agrunactive.ActiveRunsView, error) {
	return s.getActiveRunNative(ctx, in, collectOptions(opts))
}

func (s *datlyService) ListStaleRuns(ctx context.Context, in *agrunstale.StaleRunsInput, opts ...Option) ([]*agrunstale.StaleRunsView, error) {
	return s.listStaleRunsNative(ctx, in, collectOptions(opts))
}

func (s *datlyService) GetActiveTurn(ctx context.Context, in *agturnactive.ActiveTurnsInput, opts ...Option) (*agturnactive.ActiveTurnsView, error) {
	rows, err := s.readTurnNative(ctx, "active", in, collectOptions(opts))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return mapDataDTO[agturnactive.ActiveTurnsView](rows[0])
}

func (s *datlyService) GetTurnByID(ctx context.Context, in *agturnbyid.TurnLookupInput, opts ...Option) (*agturnbyid.TurnLookupView, error) {
	rows, err := s.readTurnNative(ctx, "byId", in, collectOptions(opts))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return mapDataDTO[agturnbyid.TurnLookupView](rows[0])
}

func (s *datlyService) GetNextQueuedTurn(ctx context.Context, in *agturnnext.QueuedTurnInput, opts ...Option) (*agturnnext.QueuedTurnView, error) {
	rows, err := s.readTurnNative(ctx, "nextQueued", in, collectOptions(opts))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return mapDataDTO[agturnnext.QueuedTurnView](rows[0])
}

func (s *datlyService) ListQueuedTurns(ctx context.Context, in *agturnlist.QueuedTurnsInput, opts ...Option) ([]*agturnlist.QueuedTurnsView, error) {
	rows, err := s.readTurnNative(ctx, "queued", in, collectOptions(opts))
	if err != nil {
		return nil, err
	}
	return mapDataDTOs[agturnlist.QueuedTurnsView](rows)
}

func (s *datlyService) CountQueuedTurns(ctx context.Context, in *agturncount.QueuedTotalInput, opts ...Option) (int, error) {
	id := ""
	if in != nil {
		id = in.ConversationID
	}
	present := in != nil && in.Has != nil && in.Has.ConversationID
	return s.countTurnNative(ctx, id, "QueuedCount", present, collectOptions(opts))
}

// CountControllerTurns returns the number of controller-owned (autonomous)
// turns created for a conversation. It backs the AutonomousTurnsUsed guard.
func (s *datlyService) CountControllerTurns(ctx context.Context, conversationID string, opts ...Option) (int, error) {
	return s.countTurnNative(ctx, conversationID, "ControllerCount", true, collectOptions(opts))
}

// CountPendingApprovals returns the number of pending tool approvals for a
// conversation. It backs the PendingApproval guard.
func (s *datlyService) CountPendingApprovals(ctx context.Context, conversationID string, opts ...Option) (int, error) {
	return s.countPendingNative(ctx, conversationID, false, collectOptions(opts))
}

// CountPendingElicitations returns the number of unresolved elicitation
// requests for a conversation. It backs the PendingElicitation guard.
func (s *datlyService) CountPendingElicitations(ctx context.Context, conversationID string, opts ...Option) (int, error) {
	return s.countPendingNative(ctx, conversationID, true, collectOptions(opts))
}

func (s *datlyService) ListTurnQueueRows(ctx context.Context, in *turnqueueread.QueueRowsInput, opts ...Option) ([]*turnqueueread.QueueRowView, error) {
	return s.listTurnQueueNative(ctx, in, collectOptions(opts))
}

func (s *datlyService) PatchTurnQueue(ctx context.Context, in *turnqueuewrite.TurnQueue) error {
	return s.patchTurnQueueNative(ctx, in)
}

func (s *datlyService) GetToolCallByOp(ctx context.Context, opID string, in *agtoolcall.ToolCallRowsInput, opts ...Option) ([]*agtoolcall.ToolCallRowsView, error) {
	return s.getToolCallByOpNative(ctx, opID, in, collectOptions(opts))
}

func (s *datlyService) GetToolMessageIDsByTurn(ctx context.Context, conversationID, turnID string) (map[string]string, error) {
	rows, err := s.toolCallsByTurnNative(ctx, conversationID, turnID)
	if err != nil {
		return nil, err
	}

	type selectedMessage struct {
		id      string
		attempt int
	}
	selected := make(map[string]selectedMessage, len(rows))
	for _, row := range rows {
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
	return s.listPayloadsNative(ctx, in, collectOptions(opts))
}

func (s *datlyService) PatchConversations(ctx context.Context, rows []*agconvwrite.MutableConversationView) ([]*agconvwrite.MutableConversationView, error) {
	return nativePatchData(ctx, s, "conversation", rows)
}

func (s *datlyService) PatchMessages(ctx context.Context, rows []*agmessagewrite.MutableMessageView) ([]*agmessagewrite.MutableMessageView, error) {
	return nativePatchData(ctx, s, "message", rows)
}

func (s *datlyService) PatchTurns(ctx context.Context, rows []*agturnwrite.MutableTurnView) ([]*agturnwrite.MutableTurnView, error) {
	return nativePatchData(ctx, s, "turn", rows)
}

func (s *datlyService) PatchModelCalls(ctx context.Context, rows []*agmodelcallwrite.MutableModelCallView) ([]*agmodelcallwrite.MutableModelCallView, error) {
	return nativePatchData(ctx, s, "modelcall", rows)
}

func (s *datlyService) PatchToolCalls(ctx context.Context, rows []*agtoolcallwrite.MutableToolCallView) ([]*agtoolcallwrite.MutableToolCallView, error) {
	return nativePatchData(ctx, s, "toolcall", rows)
}

func (s *datlyService) PatchPayloads(ctx context.Context, rows []*agpayloadwrite.MutablePayloadView) ([]*agpayloadwrite.MutablePayloadView, error) {
	return nativePatchData(ctx, s, "payload", rows)
}

func (s *datlyService) PatchRuns(ctx context.Context, rows []*agrunwrite.MutableRunView) ([]*agrunwrite.MutableRunView, error) {
	return s.patchRunsNative(ctx, rows)
}

func (s *datlyService) DeleteConversations(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return (&convstore.Store{Invoker: s.native}).DeleteTrusted(ctx, ids...)
}

func (s *datlyService) DeleteMessages(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return (&convstore.MessageStore{Invoker: s.native}).DeleteTrusted(ctx, ids...)
}

func (s *datlyService) DeleteTurns(ctx context.Context, ids ...string) error {
	return s.deleteDataNative(ctx, "turn", ids...)
}

func (s *datlyService) DeleteModelCalls(ctx context.Context, messageIDs ...string) error {
	return s.deleteDataNative(ctx, "modelcall", messageIDs...)
}

func (s *datlyService) DeleteToolCalls(ctx context.Context, messageIDs ...string) error {
	return s.deleteDataNative(ctx, "toolcall", messageIDs...)
}

func (s *datlyService) DeletePayloads(ctx context.Context, ids ...string) error {
	return s.deleteDataNative(ctx, "payload", ids...)
}

func (s *datlyService) DeleteRuns(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return (&agentrun.Store{Invoker: s.native}).DeleteTrusted(ctx, ids...)
}

func (s *datlyService) ListGeneratedFiles(ctx context.Context, conversationID string, opts ...Option) ([]*gfread.GeneratedFileView, error) {
	return s.listGeneratedFilesNative(ctx, conversationID, collectOptions(opts))
}

// CloseService releases a facade-owned native runtime.
func CloseService(ctx context.Context, service Service) error {
	if owned, ok := service.(*datlyService); ok && owned.closeOwned != nil {
		return owned.closeOwned(ctx)
	}
	return nil
}
