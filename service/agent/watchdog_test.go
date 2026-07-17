package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/data"
	token "github.com/viant/agently-core/internal/auth/token"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agmessagewrite "github.com/viant/agently-core/pkg/agently/message/write"
	agmodelcallwrite "github.com/viant/agently-core/pkg/agently/modelcall/write"
	agrunactive "github.com/viant/agently-core/pkg/agently/run/active"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	agrunsteps "github.com/viant/agently-core/pkg/agently/run/steps"
	agtoolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
)

func strptr(v string) *string { return &v }

func TestShouldSkipStaleRun(t *testing.T) {
	cases := []struct {
		name string
		run  *agrunstale.StaleRunsView
		want bool
	}{
		{name: "nil", run: nil, want: true},
		{name: "scheduled", run: &agrunstale.StaleRunsView{ConversationKind: "scheduled"}, want: true},
		{name: "resumed interactive run", run: &agrunstale.StaleRunsView{ConversationKind: "interactive", ResumedFromRunId: strptr("old-run")}, want: false},
		{name: "interactive root", run: &agrunstale.StaleRunsView{ConversationKind: "interactive"}, want: false},
	}
	for _, tc := range cases {
		if got := shouldSkipStaleRun(tc.run); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestResolveResumeUserID(t *testing.T) {
	effective := "persisted-user"
	tests := []struct {
		name string
		run  *agrunstale.StaleRunsView
		sd   *token.SecurityData
		want string
	}{
		{
			name: "prefers restored security subject",
			run:  &agrunstale.StaleRunsView{EffectiveUserId: &effective},
			sd:   &token.SecurityData{Subject: "restored-user"},
			want: "restored-user",
		},
		{
			name: "falls back to persisted effective user",
			run:  &agrunstale.StaleRunsView{EffectiveUserId: &effective},
			sd:   nil,
			want: "persisted-user",
		},
		{
			name: "trims persisted effective user",
			run:  &agrunstale.StaleRunsView{EffectiveUserId: strptr("  persisted-user  ")},
			sd:   &token.SecurityData{},
			want: "persisted-user",
		},
		{
			name: "empty when neither source exists",
			run:  &agrunstale.StaleRunsView{},
			sd:   nil,
			want: "",
		},
	}
	for _, tc := range tests {
		if got := resolveResumeUserID(tc.run, tc.sd); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestActiveRunSupersedesStale(t *testing.T) {
	cases := []struct {
		name   string
		stale  string
		active *agrunactive.ActiveRunsView
		want   bool
	}{
		{name: "nil active", stale: "run-1", active: nil, want: false},
		{name: "empty active id", stale: "run-1", active: &agrunactive.ActiveRunsView{}, want: false},
		{name: "same run", stale: "run-1", active: &agrunactive.ActiveRunsView{Id: "run-1"}, want: false},
		{name: "different active run", stale: "run-1", active: &agrunactive.ActiveRunsView{Id: "run-2"}, want: true},
		{name: "trimmed ids", stale: " run-1 ", active: &agrunactive.ActiveRunsView{Id: " run-2 "}, want: true},
	}
	for _, tc := range cases {
		if got := activeRunSupersedesStale(tc.stale, tc.active); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestWatchdogSweepRuns_StuckRunDoesNotBlockFollowingRuns(t *testing.T) {
	var (
		mu      sync.Mutex
		handled []string
	)
	w := NewWatchdog(nil, nil, WithWatchdogHandleTimeout(25*time.Millisecond))
	w.handleFn = func(ctx context.Context, run *agrunstale.StaleRunsView) error {
		switch run.Id {
		case "run-1":
			<-ctx.Done()
			return ctx.Err()
		default:
			mu.Lock()
			handled = append(handled, run.Id)
			mu.Unlock()
			return nil
		}
	}
	start := time.Now()
	w.sweepRuns(context.Background(), []*agrunstale.StaleRunsView{
		{Id: "run-1", ConversationKind: "interactive"},
		{Id: "run-2", ConversationKind: "interactive"},
	})
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("expected sweep to continue past timed-out run, took %s", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(handled) != 1 || handled[0] != "run-2" {
		t.Fatalf("expected second run to be handled after first timed out, got %v", handled)
	}
}

func TestWatchdogSweepRuns_HandlesStaleRunsConcurrently(t *testing.T) {
	var current int32
	var maxCurrent int32
	w := NewWatchdog(nil, nil, WithWatchdogHandleTimeout(40*time.Millisecond))
	w.handleFn = func(ctx context.Context, run *agrunstale.StaleRunsView) error {
		n := atomic.AddInt32(&current, 1)
		defer atomic.AddInt32(&current, -1)
		for {
			max := atomic.LoadInt32(&maxCurrent)
			if n <= max || atomic.CompareAndSwapInt32(&maxCurrent, max, n) {
				break
			}
		}
		if run.Id == "fast" {
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}
	start := time.Now()
	w.sweepRuns(context.Background(), []*agrunstale.StaleRunsView{
		{Id: "slow-1", ConversationKind: "interactive"},
		{Id: "slow-2", ConversationKind: "interactive"},
		{Id: "slow-3", ConversationKind: "interactive"},
		{Id: "fast", ConversationKind: "interactive"},
	})
	elapsed := time.Since(start)
	if atomic.LoadInt32(&maxCurrent) < 2 {
		t.Fatalf("expected concurrent stale handling, max concurrency=%d", atomic.LoadInt32(&maxCurrent))
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("expected bounded-concurrent sweep, took %s", elapsed)
	}
}

func TestDetachResumeContext_IgnoresParentCancel(t *testing.T) {
	type ctxKey string
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey("k"), "v"))
	child := detachResumeContext(parent)
	cancel()
	if err := child.Err(); err != nil {
		t.Fatalf("expected detached resume context to survive parent cancel, got %v", err)
	}
	if got := child.Value(ctxKey("k")); got != "v" {
		t.Fatalf("expected detached resume context to preserve values, got %v", got)
	}
}

func TestAcquireRecoverySlot_RespectsContext(t *testing.T) {
	w := NewWatchdog(nil, nil)
	sem := w.ensureRecoverySem()
	for i := 0; i < defaultRecoveryConcurrency; i++ {
		sem <- struct{}{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := w.acquireRecoverySlot(ctx)
	if err == nil {
		t.Fatalf("expected acquireRecoverySlot to fail when all slots are occupied")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("acquireRecoverySlot waited too long: %s", elapsed)
	}
}

type cleanupCaptureDataService struct {
	data.Service
	runStepRows      []*agrunsteps.RunStepsView
	conversationView *agconv.ConversationView

	patchedModelCalls []*agmodelcallwrite.MutableModelCallView
	patchedToolCalls  []*agtoolcallwrite.MutableToolCallView
	patchedMessages   []*agmessagewrite.MutableMessageView
}

func (s *cleanupCaptureDataService) GetRunStepsPage(_ context.Context, _ *agrunsteps.RunStepsInput, _ *data.PageInput, _ ...data.Option) (*data.RunStepPage, error) {
	return &data.RunStepPage{Rows: s.runStepRows}, nil
}

func (s *cleanupCaptureDataService) GetConversation(_ context.Context, _ string, _ *agconv.ConversationInput, _ ...data.Option) (*agconv.ConversationView, error) {
	return s.conversationView, nil
}

func (s *cleanupCaptureDataService) PatchModelCalls(_ context.Context, rows []*agmodelcallwrite.MutableModelCallView) ([]*agmodelcallwrite.MutableModelCallView, error) {
	s.patchedModelCalls = append(s.patchedModelCalls, rows...)
	return rows, nil
}

func (s *cleanupCaptureDataService) PatchToolCalls(_ context.Context, rows []*agtoolcallwrite.MutableToolCallView) ([]*agtoolcallwrite.MutableToolCallView, error) {
	s.patchedToolCalls = append(s.patchedToolCalls, rows...)
	return rows, nil
}

func (s *cleanupCaptureDataService) PatchMessages(_ context.Context, rows []*agmessagewrite.MutableMessageView) ([]*agmessagewrite.MutableMessageView, error) {
	s.patchedMessages = append(s.patchedMessages, rows...)
	return rows, nil
}

func TestFailSupersededRunArtifacts_TerminalizesRunningStepsAndToolMessages(t *testing.T) {
	running := "running"
	completed := "completed"
	store := &cleanupCaptureDataService{
		runStepRows: []*agrunsteps.RunStepsView{
			{StepType: "model_call", MessageId: "model-running", Status: "thinking"},
			{StepType: "model_call", MessageId: "model-done", Status: "completed"},
			{StepType: "tool_call", MessageId: "tool-running", Status: "running"},
			{StepType: "tool_call", MessageId: "tool-done", Status: "failed"},
		},
		conversationView: cleanupConversationView("conv-1", "turn-1",
			cleanupToolMessage("conv-1", "turn-1", "tool-running", &running, "running"),
			cleanupToolMessage("conv-1", "turn-1", "tool-open", nil, "running"),
			cleanupToolMessage("conv-1", "turn-1", "tool-failed", strptr("failed"), "running"),
			cleanupToolMessage("conv-1", "turn-1", "tool-done", &completed, "completed"),
		),
	}
	w := NewWatchdog(store, nil)

	err := w.failSupersededRunArtifacts(context.Background(), "conv-1", "turn-1", "run-1", "stale turn superseded by resumed run run-2")
	if err != nil {
		t.Fatalf("failSupersededRunArtifacts() error: %v", err)
	}

	if len(store.patchedModelCalls) != 1 || store.patchedModelCalls[0].MessageID != "model-running" || store.patchedModelCalls[0].Status != "failed" {
		t.Fatalf("unexpected model call patches: %+v", store.patchedModelCalls)
	}
	if store.patchedModelCalls[0].CompletedAt == nil {
		t.Fatalf("expected completed_at on patched model call")
	}

	if len(store.patchedToolCalls) != 3 {
		t.Fatalf("unexpected tool call patches: %+v", store.patchedToolCalls)
	}
	gotToolCalls := map[string]string{}
	for _, row := range store.patchedToolCalls {
		if row == nil {
			continue
		}
		gotToolCalls[row.MessageID] = row.Status
		if row.CompletedAt == nil {
			t.Fatalf("expected completed_at on patched tool call: %+v", row)
		}
	}
	if gotToolCalls["tool-running"] != "failed" || gotToolCalls["tool-open"] != "failed" || gotToolCalls["tool-failed"] != "failed" {
		t.Fatalf("unexpected tool call statuses: %+v", gotToolCalls)
	}

	if len(store.patchedMessages) != 2 {
		t.Fatalf("expected two nonterminal tool message patches, got %+v", store.patchedMessages)
	}
	got := map[string]string{}
	for _, row := range store.patchedMessages {
		if row != nil && row.Status != nil {
			got[row.Id] = *row.Status
		}
	}
	if got["tool-running"] != "failed" || got["tool-open"] != "failed" {
		t.Fatalf("unexpected message patches: %+v", got)
	}
	if _, ok := got["tool-done"]; ok {
		t.Fatalf("did not expect completed tool message to be patched: %+v", got)
	}
}

func cleanupConversationView(conversationID, turnID string, messages ...*agconv.MessageView) *agconv.ConversationView {
	return &agconv.ConversationView{
		Id: conversationID,
		Transcript: []*agconv.TranscriptView{{
			Id:             turnID,
			ConversationId: conversationID,
			Message:        messages,
		}},
	}
}

func cleanupToolMessage(conversationID, turnID, messageID string, messageStatus *string, toolStatus string) *agconv.MessageView {
	return &agconv.MessageView{
		Id:             messageID,
		ConversationId: conversationID,
		TurnId:         strptr(turnID),
		Role:           "tool",
		Type:           "tool_op",
		Status:         messageStatus,
		MessageToolCall: &agconv.MessageToolCallView{
			MessageId: messageID,
			TurnId:    strptr(turnID),
			OpId:      messageID,
			Status:    toolStatus,
		},
	}
}

type startupCleanupStore struct {
	data.Service

	mu            sync.Mutex
	events        []string
	snapshotRows  []data.TerminalArtifactCandidate
	snapshotErr   error
	snapshotCalls int
	cleanupCalls  [][]data.TerminalArtifactCandidate
	cleanupFn     func([]data.TerminalArtifactCandidate, int) ([]data.TerminalArtifactDisposition, error)
}

func (s *startupCleanupStore) SnapshotTerminalArtifactCandidates(_ context.Context, _ time.Time, _ int) ([]data.TerminalArtifactCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "snapshot")
	s.snapshotCalls++
	return append([]data.TerminalArtifactCandidate(nil), s.snapshotRows...), s.snapshotErr
}

func (s *startupCleanupStore) CleanupTerminalArtifactCandidates(_ context.Context, candidates []data.TerminalArtifactCandidate, _ time.Time) ([]data.TerminalArtifactDisposition, error) {
	s.mu.Lock()
	call := len(s.cleanupCalls)
	batch := append([]data.TerminalArtifactCandidate(nil), candidates...)
	s.cleanupCalls = append(s.cleanupCalls, batch)
	s.events = append(s.events, "cleanup")
	fn := s.cleanupFn
	s.mu.Unlock()
	if fn != nil {
		return fn(batch, call)
	}
	result := make([]data.TerminalArtifactDisposition, len(batch))
	for i := range result {
		result[i] = data.TerminalArtifactRepaired
	}
	return result, nil
}

func (s *startupCleanupStore) ListStaleRuns(_ context.Context, _ *agrunstale.StaleRunsInput, _ ...data.Option) ([]*agrunstale.StaleRunsView, error) {
	s.mu.Lock()
	s.events = append(s.events, "sweep")
	s.mu.Unlock()
	return nil, nil
}

func (s *startupCleanupStore) state() ([]string, int, [][]data.TerminalArtifactCandidate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := append([]string(nil), s.events...)
	calls := make([][]data.TerminalArtifactCandidate, len(s.cleanupCalls))
	for i := range s.cleanupCalls {
		calls[i] = append([]data.TerminalArtifactCandidate(nil), s.cleanupCalls[i]...)
	}
	return events, s.snapshotCalls, calls
}

func cleanupCandidate(id string, kind data.TerminalArtifactKind) data.TerminalArtifactCandidate {
	return data.TerminalArtifactCandidate{
		Kind:           kind,
		ID:             id,
		ConversationID: "conversation",
		TurnID:         "turn",
		Linkage:        data.TerminalArtifactDirectTurn,
		ExpectedLink:   "turn",
		TerminalStatus: "failed",
		Reason:         "turn failed",
	}
}

func TestWatchdogStart_OrdersSnapshotSweepThenDrainWithoutSharedReadsOrPatches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &startupCleanupStore{snapshotRows: []data.TerminalArtifactCandidate{cleanupCandidate("initial", data.TerminalArtifactModelCall)}}
	store.cleanupFn = func(batch []data.TerminalArtifactCandidate, _ int) ([]data.TerminalArtifactDisposition, error) {
		cancel()
		return []data.TerminalArtifactDisposition{data.TerminalArtifactRepaired}, nil
	}
	w := NewWatchdog(store, nil, WithWatchdogInterval(time.Hour))
	w.Start(ctx)

	events, snapshots, cleanupCalls := store.state()
	if got, want := strings.Join(events, ","), "snapshot,sweep,cleanup"; got != want {
		t.Fatalf("startup order = %q, want %q", got, want)
	}
	if snapshots != 1 || len(cleanupCalls) != 1 {
		t.Fatalf("snapshot calls=%d cleanup calls=%d", snapshots, len(cleanupCalls))
	}
	// The embedded shared Service is nil. Reaching GetTurnsPage,
	// GetRunStepsPage, GetConversation, or a shared Patch method would panic.
	w.runTerminalArtifactCleanup(context.Background())
	_, _, cleanupCalls = store.state()
	if len(cleanupCalls) != 1 {
		t.Fatalf("cleanup store called after remaining reached zero: %d calls", len(cleanupCalls))
	}
}

func TestWatchdogStart_FreezesSnapshotAndRetriesOnlyRemainingCandidates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &startupCleanupStore{snapshotRows: []data.TerminalArtifactCandidate{cleanupCandidate("initial", data.TerminalArtifactToolCall)}}
	store.cleanupFn = func(batch []data.TerminalArtifactCandidate, call int) ([]data.TerminalArtifactDisposition, error) {
		if len(batch) != 1 || batch[0].ID != "initial" {
			t.Fatalf("cleanup call %d received non-frozen candidates: %+v", call, batch)
		}
		if call == 0 {
			store.mu.Lock()
			store.snapshotRows = append(store.snapshotRows, cleanupCandidate("later", data.TerminalArtifactToolCall))
			store.mu.Unlock()
			return []data.TerminalArtifactDisposition{data.TerminalArtifactUnresolved}, nil
		}
		cancel()
		return []data.TerminalArtifactDisposition{data.TerminalArtifactRepaired}, nil
	}
	w := NewWatchdog(store, nil, WithWatchdogInterval(time.Millisecond))
	w.Start(ctx)

	events, snapshots, cleanupCalls := store.state()
	if snapshots != 1 {
		t.Fatalf("snapshot calls=%d, want 1; events=%v", snapshots, events)
	}
	if len(cleanupCalls) != 2 {
		t.Fatalf("cleanup calls=%d, want startup plus one retry", len(cleanupCalls))
	}
	if got := strings.Join(events, ","); got != "snapshot,sweep,cleanup,sweep,cleanup" {
		t.Fatalf("retry order = %q", got)
	}
}

func TestWatchdogStart_SnapshotFailureStillSweepsAndRecordsStartupAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &startupCleanupStore{snapshotErr: errors.New("snapshot failed")}
	var logs bytes.Buffer
	restoreLogs := captureWatchdogLogs(&logs)
	w := NewWatchdog(store, nil, WithWatchdogInterval(time.Hour))
	w.Start(ctx)
	restoreLogs()

	events, snapshots, cleanupCalls := store.state()
	if got := strings.Join(events, ","); got != "snapshot,sweep" {
		t.Fatalf("snapshot failure startup order=%q", got)
	}
	if snapshots != 1 || len(cleanupCalls) != 0 {
		t.Fatalf("snapshot failure calls: snapshots=%d cleanup=%d", snapshots, len(cleanupCalls))
	}
	got := logs.String()
	if !strings.Contains(got, "snapshot found=0 model_calls=0 tool_calls=0 messages=0 complete=false") ||
		!strings.Contains(got, "attempt found=0 cleaned=0 already_resolved=0 no_longer_eligible=0 retryable=0 remaining=0") ||
		!strings.Contains(got, "complete=false batches=0 limit_reached=false") {
		t.Fatalf("snapshot failure logs: %s", got)
	}
}

func TestTerminalArtifactCleanup_BatchesAndLimitsEachCycle(t *testing.T) {
	store := &startupCleanupStore{}
	w := NewWatchdog(store, nil)
	w.cleanupStore = store
	w.cleanupSnapshotComplete = true
	for i := 0; i < terminalArtifactCleanupCycleMax+1; i++ {
		w.cleanupRows = append(w.cleanupRows, cleanupCandidate(fmt.Sprintf("candidate-%04d", i), data.TerminalArtifactModelCall))
	}

	var logs bytes.Buffer
	restoreLogs := captureWatchdogLogs(&logs)
	defer restoreLogs()
	w.runTerminalArtifactCleanup(context.Background())

	_, _, calls := store.state()
	if len(calls) != 4 {
		t.Fatalf("cleanup batches=%d, want 4", len(calls))
	}
	for i, call := range calls {
		if len(call) != terminalArtifactCleanupBatchSize {
			t.Fatalf("batch %d size=%d, want %d", i, len(call), terminalArtifactCleanupBatchSize)
		}
	}
	if remaining := w.terminalArtifactCleanupRemaining(); remaining != 1 {
		t.Fatalf("remaining=%d, want 1", remaining)
	}
	if !strings.Contains(logs.String(), "found=1000 cleaned=1000 already_resolved=0 no_longer_eligible=0 retryable=0 remaining=1") ||
		!strings.Contains(logs.String(), "complete=false batches=4 limit_reached=true") {
		t.Fatalf("unexpected limit attempt log: %s", logs.String())
	}

	w.runTerminalArtifactCleanup(context.Background())
	_, _, calls = store.state()
	if len(calls) != 5 || len(calls[4]) != 1 || w.terminalArtifactCleanupRemaining() != 0 {
		t.Fatalf("unexpected final retry: calls=%d final_size=%d remaining=%d", len(calls), len(calls[4]), w.terminalArtifactCleanupRemaining())
	}
	if !strings.Contains(logs.String(), "found=1 cleaned=1 already_resolved=0 no_longer_eligible=0 retryable=0 remaining=0") ||
		!strings.Contains(logs.String(), "complete=true batches=1 limit_reached=false") {
		t.Fatalf("unexpected completed attempt log: %s", logs.String())
	}
}

func TestTerminalArtifactCleanup_LogsExactCountsAndOneErrorPhaseWithoutSensitiveValues(t *testing.T) {
	store := &startupCleanupStore{}
	w := NewWatchdog(store, nil)
	w.cleanupStore = store
	w.cleanupSnapshotComplete = true
	w.cleanupRows = []data.TerminalArtifactCandidate{
		cleanupCandidate("secret-repaired", data.TerminalArtifactModelCall),
		cleanupCandidate("secret-resolved", data.TerminalArtifactToolCall),
		cleanupCandidate("secret-ineligible", data.TerminalArtifactMessage),
		cleanupCandidate("secret-failed", data.TerminalArtifactToolCall),
	}
	store.cleanupFn = func(_ []data.TerminalArtifactCandidate, _ int) ([]data.TerminalArtifactDisposition, error) {
		return []data.TerminalArtifactDisposition{
			data.TerminalArtifactRepaired,
			data.TerminalArtifactAlreadyResolved,
			data.TerminalArtifactNoLongerEligible,
			data.TerminalArtifactUnresolved,
		}, nil
	}

	var logs bytes.Buffer
	restoreLogs := captureWatchdogLogs(&logs)
	w.runTerminalArtifactCleanup(context.Background())
	restoreLogs()
	got := logs.String()
	if !strings.Contains(got, "found=4 cleaned=1 already_resolved=1 no_longer_eligible=1 retryable=1 remaining=1") ||
		!strings.Contains(got, "complete=false batches=1 limit_reached=false") {
		t.Fatalf("unexpected disposition counts: %s", got)
	}
	if strings.Contains(got, "secret-") || strings.Contains(got, "turn failed") || strings.Contains(strings.ToUpper(got), "UPDATE ") ||
		strings.Contains(got, "attempted=") || strings.Contains(got, "repaired=") || strings.Contains(got, "failed=") {
		t.Fatalf("log exposed candidate or SQL: %s", got)
	}

	store.cleanupFn = func(_ []data.TerminalArtifactCandidate, _ int) ([]data.TerminalArtifactDisposition, error) {
		return nil, errors.New("database unavailable")
	}
	logs.Reset()
	restoreLogs = captureWatchdogLogs(&logs)
	w.runTerminalArtifactCleanup(context.Background())
	restoreLogs()
	got = logs.String()
	if strings.Count(got, "error phase=") != 1 {
		t.Fatalf("error phase log count=%d: %s", strings.Count(got, "error phase="), got)
	}
	if !strings.Contains(got, "found=1 cleaned=0 already_resolved=0 no_longer_eligible=0 retryable=1 remaining=1") ||
		!strings.Contains(got, "complete=false batches=1 limit_reached=false") {
		t.Fatalf("unexpected failed batch counts: %s", got)
	}
}

func TestTerminalArtifactSnapshot_LogsTotalsAndDiscardsIncompleteCapture(t *testing.T) {
	store := &startupCleanupStore{snapshotRows: []data.TerminalArtifactCandidate{
		cleanupCandidate("m", data.TerminalArtifactModelCall),
		cleanupCandidate("t", data.TerminalArtifactToolCall),
		cleanupCandidate("msg", data.TerminalArtifactMessage),
	}}
	var logs bytes.Buffer
	restoreLogs := captureWatchdogLogs(&logs)
	w := NewWatchdog(store, nil)
	w.captureTerminalArtifactCleanupOnce(context.Background())
	restoreLogs()
	if got := logs.String(); !strings.Contains(got, "snapshot found=3 model_calls=1 tool_calls=1 messages=1 complete=true") {
		t.Fatalf("unexpected complete snapshot log: %s", got)
	}

	failedStore := &startupCleanupStore{snapshotRows: store.snapshotRows, snapshotErr: errors.New("scan incomplete")}
	logs.Reset()
	restoreLogs = captureWatchdogLogs(&logs)
	failedWatchdog := NewWatchdog(failedStore, nil)
	failedWatchdog.captureTerminalArtifactCleanupOnce(context.Background())
	failedWatchdog.captureTerminalArtifactCleanupOnce(context.Background())
	restoreLogs()
	got := logs.String()
	if !strings.Contains(got, "snapshot found=0 model_calls=0 tool_calls=0 messages=0 complete=false") || strings.Count(got, "error phase=snapshot") != 1 {
		t.Fatalf("unexpected incomplete snapshot logs: %s", got)
	}
	if failedWatchdog.terminalArtifactCleanupRemaining() != 0 {
		t.Fatalf("incomplete snapshot retained candidates")
	}
	_, snapshots, cleanupCalls := failedStore.state()
	if snapshots != 1 || len(cleanupCalls) != 0 {
		t.Fatalf("incomplete snapshot retried: snapshots=%d cleanup=%d", snapshots, len(cleanupCalls))
	}
}

func captureWatchdogLogs(buffer *bytes.Buffer) func() {
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
