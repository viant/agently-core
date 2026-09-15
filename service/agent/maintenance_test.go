package agent

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agconvlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	agrunactive "github.com/viant/agently-core/pkg/agently/run/active"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnnext "github.com/viant/agently-core/pkg/agently/turn/nextQueued"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
)

type testCancelRegistry struct {
	cancelTurnCalls         []string
	cancelConversationCalls []string
}

func (t *testCancelRegistry) Register(string, string, context.CancelFunc) {}
func (t *testCancelRegistry) Complete(string, string, context.CancelFunc) {}

func (t *testCancelRegistry) CancelTurn(turnID string) bool {
	t.cancelTurnCalls = append(t.cancelTurnCalls, turnID)
	return true
}

func (t *testCancelRegistry) CancelConversation(conversationID string) bool {
	t.cancelConversationCalls = append(t.cancelConversationCalls, conversationID)
	return true
}

func TestServiceTerminateCancelsConversation(t *testing.T) {
	reg := &testCancelRegistry{}
	svc := &Service{cancelReg: reg}

	if err := svc.Terminate(context.Background(), "conv-1"); err != nil {
		t.Fatalf("Terminate() unexpected error: %v", err)
	}

	if len(reg.cancelConversationCalls) != 1 || reg.cancelConversationCalls[0] != "conv-1" {
		t.Fatalf("expected CancelConversation to be called with conv-1, got %#v", reg.cancelConversationCalls)
	}
	if len(reg.cancelTurnCalls) != 0 {
		t.Fatalf("expected CancelTurn to remain unused, got %#v", reg.cancelTurnCalls)
	}
}

type terminateConversationClient struct {
	apiconv.Client
	conversation        *apiconv.Conversation
	conversationPatches []*apiconv.MutableConversation
	turnPatches         []*apiconv.MutableTurn
}

func (m *terminateConversationClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return m.conversation, nil
}

func (m *terminateConversationClient) PatchConversations(_ context.Context, patch *apiconv.MutableConversation) error {
	m.conversationPatches = append(m.conversationPatches, patch)
	return nil
}

func (m *terminateConversationClient) PatchTurn(_ context.Context, patch *apiconv.MutableTurn) error {
	m.turnPatches = append(m.turnPatches, patch)
	return nil
}

func TestServiceTerminatePersistsNonTerminalTurnsAsCanceled(t *testing.T) {
	client := &terminateConversationClient{conversation: (*apiconv.Conversation)(&agconv.ConversationView{
		Id: "conv-1",
		Transcript: []*agconv.TranscriptView{
			{Id: "running", ConversationId: "conv-1", Status: "running"},
			{Id: "queued", ConversationId: "conv-1", Status: "queued"},
			{Id: "done", ConversationId: "conv-1", Status: "succeeded"},
			{Id: "failed", ConversationId: "conv-1", Status: "failed"},
		},
	})}
	svc := &Service{conversation: client}

	if err := svc.Terminate(context.Background(), "conv-1"); err != nil {
		t.Fatalf("Terminate() unexpected error: %v", err)
	}
	if len(client.turnPatches) != 2 {
		t.Fatalf("expected running and queued turns to be patched, got %d", len(client.turnPatches))
	}
	for _, patch := range client.turnPatches {
		if patch.Status != "canceled" || patch.StatusReason == nil || *patch.StatusReason != "conversation_terminated" {
			t.Fatalf("unexpected turn patch: %#v", patch)
		}
	}
}

type maintenanceDataService struct {
	data.Service

	mu                   sync.Mutex
	rows                 []*agconvlist.ConversationRowsView
	listCalls            int
	listLimit            int
	activeRunErrors      map[string]error
	activeRunSequences   map[string][]*agrunactive.ActiveRunsView
	activeRunCalls       map[string]int
	activeTurns          map[string]*agturnactive.ActiveTurnsView
	queuedCounts         map[string]int
	conversations        map[string]*agconv.ConversationView
	patchedConversations []*convw.Conversation
	queueDrainCalled     chan string
}

func (s *maintenanceDataService) ListConversations(_ context.Context, _ *agconvlist.ConversationRowsInput, page *data.PageInput, _ ...data.Option) (*data.ConversationPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	if page != nil {
		s.listLimit = page.Limit
	}
	return &data.ConversationPage{Rows: s.rows}, nil
}

func (s *maintenanceDataService) GetActiveRun(_ context.Context, input *agrunactive.ActiveRunsInput, _ ...data.Option) (*agrunactive.ActiveRunsView, error) {
	conversationID := strings.TrimSpace(input.ConversationId)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.activeRunErrors[conversationID]; err != nil {
		return nil, err
	}
	call := s.activeRunCalls[conversationID]
	s.activeRunCalls[conversationID] = call + 1
	sequence := s.activeRunSequences[conversationID]
	if call >= len(sequence) {
		return nil, nil
	}
	return sequence[call], nil
}

func (s *maintenanceDataService) GetActiveTurn(_ context.Context, input *agturnactive.ActiveTurnsInput, _ ...data.Option) (*agturnactive.ActiveTurnsView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeTurns[strings.TrimSpace(input.ConversationID)], nil
}

func (s *maintenanceDataService) CountQueuedTurns(_ context.Context, input *agturncount.QueuedTotalInput, _ ...data.Option) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queuedCounts[strings.TrimSpace(input.ConversationID)], nil
}

func (s *maintenanceDataService) GetConversation(_ context.Context, id string, _ *agconv.ConversationInput, _ ...data.Option) (*agconv.ConversationView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conversations[strings.TrimSpace(id)], nil
}

func (s *maintenanceDataService) PatchConversations(_ context.Context, rows []*convw.Conversation) ([]*convw.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patchedConversations = append(s.patchedConversations, rows...)
	return rows, nil
}

func (s *maintenanceDataService) GetNextQueuedTurn(_ context.Context, input *agturnnext.QueuedTurnInput, _ ...data.Option) (*agturnnext.QueuedTurnView, error) {
	if s.queueDrainCalled != nil {
		select {
		case s.queueDrainCalled <- strings.TrimSpace(input.ConversationID):
		default:
		}
	}
	return nil, nil
}

type maintenanceConversationClient struct {
	apiconv.Client

	mu                   sync.Mutex
	patchedTurns         []*apiconv.MutableTurn
	patchedConversations []*apiconv.MutableConversation
}

func (c *maintenanceConversationClient) PatchTurn(_ context.Context, turn *apiconv.MutableTurn) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.patchedTurns = append(c.patchedTurns, turn)
	return nil
}

func (c *maintenanceConversationClient) PatchConversations(_ context.Context, conversation *apiconv.MutableConversation) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.patchedConversations = append(c.patchedConversations, conversation)
	return nil
}

func newMaintenanceDataService(rows ...*agconvlist.ConversationRowsView) *maintenanceDataService {
	return &maintenanceDataService{
		rows:               rows,
		activeRunErrors:    map[string]error{},
		activeRunSequences: map[string][]*agrunactive.ActiveRunsView{},
		activeRunCalls:     map[string]int{},
		activeTurns:        map[string]*agturnactive.ActiveTurnsView{},
		queuedCounts:       map[string]int{},
		conversations:      map[string]*agconv.ConversationView{},
	}
}

func captureMaintenanceLogs(buffer *bytes.Buffer) func() {
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(buffer)
	log.SetFlags(0)
	log.SetPrefix("")
	return func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	}
}

func TestReconcileRunningConversationStatuses_ContinuesAfterConversationErrorAndCapsLimit(t *testing.T) {
	store := newMaintenanceDataService(
		&agconvlist.ConversationRowsView{Id: "conversation-error"},
		&agconvlist.ConversationRowsView{Id: "conversation-ok"},
	)
	store.activeRunErrors["conversation-error"] = errors.New("active run unavailable")
	store.conversations["conversation-ok"] = &agconv.ConversationView{Id: "conversation-ok", Status: strptr("succeeded")}
	conversationClient := &maintenanceConversationClient{}
	service := &Service{dataService: store, conversation: conversationClient}
	var logs bytes.Buffer
	restoreLogs := captureMaintenanceLogs(&logs)
	err := service.ReconcileRunningConversationStatuses(context.Background(), maxReconcileLimit+100)
	restoreLogs()

	if err == nil || !strings.Contains(err.Error(), "conversation-error") {
		t.Fatalf("ReconcileRunningConversationStatuses() error=%v, want aggregated conversation error", err)
	}
	if store.listCalls != 1 || store.listLimit != maxReconcileLimit {
		t.Fatalf("list calls=%d limit=%d, want one call with limit %d", store.listCalls, store.listLimit, maxReconcileLimit)
	}
	if len(conversationClient.patchedConversations) != 1 || conversationClient.patchedConversations[0].Id != "conversation-ok" {
		t.Fatalf("later conversation was not reconciled: %+v", conversationClient.patchedConversations)
	}
	gotLogs := logs.String()
	if !strings.Contains(gotLogs, "conversation_id=conversation-error") ||
		!strings.Contains(gotLogs, "found=2 processed=1 repaired=1 skipped=0 failed=1") {
		t.Fatalf("unexpected reconcile logs: %s", gotLogs)
	}
}

func TestReconcileRunningConversationStatuses_UsesGracePeriodForOrphanTurns(t *testing.T) {
	store := newMaintenanceDataService(
		&agconvlist.ConversationRowsView{Id: "conversation-fresh"},
		&agconvlist.ConversationRowsView{Id: "conversation-old"},
	)
	now := time.Now()
	store.activeTurns["conversation-fresh"] = &agturnactive.ActiveTurnsView{
		Id:             "turn-fresh",
		ConversationId: "conversation-fresh",
		CreatedAt:      now,
		Status:         "running",
	}
	store.activeTurns["conversation-old"] = &agturnactive.ActiveTurnsView{
		Id:             "turn-old",
		ConversationId: "conversation-old",
		CreatedAt:      now.Add(-orphanActiveTurnGracePeriod - time.Second),
		Status:         "running",
	}
	store.queueDrainCalled = make(chan string, 1)
	conversationClient := &maintenanceConversationClient{}
	service := &Service{dataService: store, conversation: conversationClient}
	var logs bytes.Buffer
	restoreLogs := captureMaintenanceLogs(&logs)
	err := service.ReconcileRunningConversationStatuses(context.Background(), maxReconcileLimit)
	restoreLogs()

	if err != nil {
		t.Fatalf("ReconcileRunningConversationStatuses() error: %v", err)
	}
	if len(conversationClient.patchedTurns) != 1 || conversationClient.patchedTurns[0].Id != "turn-old" {
		t.Fatalf("unexpected orphan turn patches: %+v", conversationClient.patchedTurns)
	}
	if len(conversationClient.patchedConversations) != 1 || conversationClient.patchedConversations[0].Id != "conversation-old" {
		t.Fatalf("unexpected conversation patches: %+v", conversationClient.patchedConversations)
	}
	select {
	case conversationID := <-store.queueDrainCalled:
		if conversationID != "conversation-old" {
			t.Fatalf("queue drain conversation=%q, want conversation-old", conversationID)
		}
	case <-time.After(time.Second):
		t.Fatalf("queue drain was not triggered")
	}
	if !strings.Contains(logs.String(), "found=2 processed=2 repaired=1 skipped=1 failed=0") {
		t.Fatalf("unexpected reconcile logs: %s", logs.String())
	}
}

func TestReconcileRunningConversationStatuses_RechecksActiveRunBeforeMutation(t *testing.T) {
	store := newMaintenanceDataService(&agconvlist.ConversationRowsView{Id: "conversation-race"})
	store.activeTurns["conversation-race"] = &agturnactive.ActiveTurnsView{
		Id:             "turn-race",
		ConversationId: "conversation-race",
		CreatedAt:      time.Now().Add(-orphanActiveTurnGracePeriod - time.Second),
		Status:         "running",
	}
	store.activeRunSequences["conversation-race"] = []*agrunactive.ActiveRunsView{
		nil,
		{Id: "new-active-run"},
	}
	conversationClient := &maintenanceConversationClient{}
	service := &Service{dataService: store, conversation: conversationClient}

	if err := service.ReconcileRunningConversationStatuses(context.Background(), maxReconcileLimit); err != nil {
		t.Fatalf("ReconcileRunningConversationStatuses() error: %v", err)
	}
	if len(conversationClient.patchedTurns) != 0 || len(conversationClient.patchedConversations) != 0 {
		t.Fatalf("reconcile mutated conversation after active run appeared")
	}
}
