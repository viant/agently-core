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
	conversationmodel "github.com/viant/agently-core/model/conversation"
	generatedfilemodel "github.com/viant/agently-core/model/generatedfile"
	messagemodel "github.com/viant/agently-core/model/message"
	modelcallmodel "github.com/viant/agently-core/model/modelcall"
	payloadmodel "github.com/viant/agently-core/model/payload"
	runmodel "github.com/viant/agently-core/model/run"
	toolcallmodel "github.com/viant/agently-core/model/toolcall"
	turnmodel "github.com/viant/agently-core/model/turn"
	turnqueuemodel "github.com/viant/agently-core/model/turnqueue"

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
	GetConversation(ctx context.Context, id string, in *conversationmodel.ConversationInput, opts ...Option) (*conversationmodel.ConversationView, error)
	ListConversations(ctx context.Context, in *conversationmodel.ConversationRowsInput, page *PageInput, opts ...Option) (*ConversationPage, error)
	GetMessage(ctx context.Context, id string, in *messagemodel.MessageInput, opts ...Option) (*messagemodel.MessageView, error)
	GetMessagesPage(ctx context.Context, in *messagemodel.MessageRowsInput, page *PageInput, opts ...Option) (*MessagePage, error)
	GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string, opts ...Option) (*messagemodel.ElicitationMessageView, error)

	GetRun(ctx context.Context, id string, in *runmodel.RunRowsInput, opts ...Option) (*runmodel.RunRowsView, error)
	GetRunStepsPage(ctx context.Context, in *runmodel.RunStepsInput, page *PageInput, opts ...Option) (*RunStepPage, error)
	GetActiveRun(ctx context.Context, in *runmodel.ActiveRunsInput, opts ...Option) (*runmodel.ActiveRunsView, error)
	ListStaleRuns(ctx context.Context, in *runmodel.StaleRunsInput, opts ...Option) ([]*runmodel.StaleRunsView, error)

	GetActiveTurn(ctx context.Context, in *turnmodel.ActiveTurnsInput, opts ...Option) (*turnmodel.ActiveTurnsView, error)
	GetTurnByID(ctx context.Context, in *turnmodel.TurnLookupInput, opts ...Option) (*turnmodel.TurnLookupView, error)
	GetTurnsPage(ctx context.Context, in *turnmodel.TurnRowsInput, page *PageInput, opts ...Option) (*TurnPage, error)
	GetNextQueuedTurn(ctx context.Context, in *turnmodel.QueuedTurnInput, opts ...Option) (*turnmodel.QueuedTurnView, error)
	ListQueuedTurns(ctx context.Context, in *turnmodel.QueuedTurnsInput, opts ...Option) ([]*turnmodel.QueuedTurnsView, error)
	CountQueuedTurns(ctx context.Context, in *turnmodel.QueuedTotalInput, opts ...Option) (int, error)

	GetToolCallByOp(ctx context.Context, opID string, in *toolcallmodel.ToolCallByOpInput, opts ...Option) ([]*toolcallmodel.ToolCallByOpView, error)
	ListPayloadRows(ctx context.Context, in *payloadmodel.PayloadRowsInput, opts ...Option) ([]*payloadmodel.PayloadRowsView, error)

	ListGeneratedFiles(ctx context.Context, conversationID string, opts ...Option) ([]*generatedfilemodel.GeneratedFileView, error)

	PatchConversations(ctx context.Context, rows []*conversationmodel.MutableConversationView) ([]*conversationmodel.MutableConversationView, error)
	PatchMessages(ctx context.Context, rows []*messagemodel.MutableMessageView) ([]*messagemodel.MutableMessageView, error)
	PatchTurns(ctx context.Context, rows []*turnmodel.MutableTurnView) ([]*turnmodel.MutableTurnView, error)
	PatchModelCalls(ctx context.Context, rows []*modelcallmodel.MutableModelCallView) ([]*modelcallmodel.MutableModelCallView, error)
	PatchToolCalls(ctx context.Context, rows []*toolcallmodel.MutableToolCallView) ([]*toolcallmodel.MutableToolCallView, error)
	PatchPayloads(ctx context.Context, rows []*payloadmodel.MutablePayloadView) ([]*payloadmodel.MutablePayloadView, error)
	PatchRuns(ctx context.Context, rows []*runmodel.MutableRunView) ([]*runmodel.MutableRunView, error)

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

func (s *datlyService) GetConversation(ctx context.Context, id string, in *conversationmodel.ConversationInput, opts ...Option) (*conversationmodel.ConversationView, error) {
	return s.getConversationNative(ctx, id, in, collectOptions(opts))
}

func (s *datlyService) GetMessage(ctx context.Context, id string, in *messagemodel.MessageInput, opts ...Option) (*messagemodel.MessageView, error) {
	return s.getMessageNative(ctx, id, in, collectOptions(opts))
}

func (s *datlyService) GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string, opts ...Option) (*messagemodel.ElicitationMessageView, error) {
	return s.getMessageByElicitationNative(ctx, conversationID, elicitationID, collectOptions(opts))
}

func (s *datlyService) GetRun(ctx context.Context, id string, in *runmodel.RunRowsInput, opts ...Option) (*runmodel.RunRowsView, error) {
	return s.getRunNative(ctx, id, in, collectOptions(opts))
}

func (s *datlyService) GetActiveRun(ctx context.Context, in *runmodel.ActiveRunsInput, opts ...Option) (*runmodel.ActiveRunsView, error) {
	return s.getActiveRunNative(ctx, in, collectOptions(opts))
}

func (s *datlyService) ListStaleRuns(ctx context.Context, in *runmodel.StaleRunsInput, opts ...Option) ([]*runmodel.StaleRunsView, error) {
	return s.listStaleRunsNative(ctx, in, collectOptions(opts))
}

func (s *datlyService) GetActiveTurn(ctx context.Context, in *turnmodel.ActiveTurnsInput, opts ...Option) (*turnmodel.ActiveTurnsView, error) {
	rows, err := s.readTurnNative(ctx, "active", in, collectOptions(opts))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func (s *datlyService) GetTurnByID(ctx context.Context, in *turnmodel.TurnLookupInput, opts ...Option) (*turnmodel.TurnLookupView, error) {
	rows, err := s.readTurnNative(ctx, "byId", in, collectOptions(opts))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func (s *datlyService) GetNextQueuedTurn(ctx context.Context, in *turnmodel.QueuedTurnInput, opts ...Option) (*turnmodel.QueuedTurnView, error) {
	rows, err := s.readTurnNative(ctx, "nextQueued", in, collectOptions(opts))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func (s *datlyService) ListQueuedTurns(ctx context.Context, in *turnmodel.QueuedTurnsInput, opts ...Option) ([]*turnmodel.QueuedTurnsView, error) {
	rows, err := s.readTurnNative(ctx, "queued", in, collectOptions(opts))
	if err != nil {
		return nil, err
	}
	return mapDataDTOs[turnmodel.QueuedTurnsView](rows)
}

func (s *datlyService) CountQueuedTurns(ctx context.Context, in *turnmodel.QueuedTotalInput, opts ...Option) (int, error) {
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

func (s *datlyService) ListTurnQueueRows(ctx context.Context, in *turnqueuemodel.QueueRowsInput, opts ...Option) ([]*turnqueuemodel.QueueRowView, error) {
	return s.listTurnQueueNative(ctx, in, collectOptions(opts))
}

func (s *datlyService) PatchTurnQueue(ctx context.Context, in *turnqueuemodel.TurnQueue) error {
	return s.patchTurnQueueNative(ctx, in)
}

func (s *datlyService) GetToolCallByOp(ctx context.Context, opID string, in *toolcallmodel.ToolCallByOpInput, opts ...Option) ([]*toolcallmodel.ToolCallByOpView, error) {
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

func (s *datlyService) ListPayloadRows(ctx context.Context, in *payloadmodel.PayloadRowsInput, opts ...Option) ([]*payloadmodel.PayloadRowsView, error) {
	return s.listPayloadsNative(ctx, in, collectOptions(opts))
}

func (s *datlyService) PatchConversations(ctx context.Context, rows []*conversationmodel.MutableConversationView) ([]*conversationmodel.MutableConversationView, error) {
	return nativePatchData(ctx, s, "conversation", rows)
}

func (s *datlyService) PatchMessages(ctx context.Context, rows []*messagemodel.MutableMessageView) ([]*messagemodel.MutableMessageView, error) {
	return nativePatchData(ctx, s, "message", rows)
}

func (s *datlyService) PatchTurns(ctx context.Context, rows []*turnmodel.MutableTurnView) ([]*turnmodel.MutableTurnView, error) {
	return nativePatchData(ctx, s, "turn", rows)
}

func (s *datlyService) PatchModelCalls(ctx context.Context, rows []*modelcallmodel.MutableModelCallView) ([]*modelcallmodel.MutableModelCallView, error) {
	return nativePatchData(ctx, s, "modelcall", rows)
}

func (s *datlyService) PatchToolCalls(ctx context.Context, rows []*toolcallmodel.MutableToolCallView) ([]*toolcallmodel.MutableToolCallView, error) {
	return nativePatchData(ctx, s, "toolcall", rows)
}

func (s *datlyService) PatchPayloads(ctx context.Context, rows []*payloadmodel.MutablePayloadView) ([]*payloadmodel.MutablePayloadView, error) {
	return nativePatchData(ctx, s, "payload", rows)
}

func (s *datlyService) PatchRuns(ctx context.Context, rows []*runmodel.MutableRunView) ([]*runmodel.MutableRunView, error) {
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

func (s *datlyService) ListGeneratedFiles(ctx context.Context, conversationID string, opts ...Option) ([]*generatedfilemodel.GeneratedFileView, error) {
	return s.listGeneratedFilesNative(ctx, conversationID, collectOptions(opts))
}

// CloseService releases a facade-owned native runtime.
func CloseService(ctx context.Context, service Service) error {
	if owned, ok := service.(*datlyService); ok && owned.closeOwned != nil {
		return owned.closeOwned(ctx)
	}
	return nil
}
