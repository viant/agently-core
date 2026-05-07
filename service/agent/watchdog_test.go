package agent

import (
	"context"
	"github.com/viant/agently-core/app/store/data"
	token "github.com/viant/agently-core/internal/auth/token"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agmessagewrite "github.com/viant/agently-core/pkg/agently/message/write"
	agmodelcallwrite "github.com/viant/agently-core/pkg/agently/modelcall/write"
	agrunactive "github.com/viant/agently-core/pkg/agently/run/active"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	agrunsteps "github.com/viant/agently-core/pkg/agently/run/steps"
	agtoolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
	agturnlistall "github.com/viant/agently-core/pkg/agently/turn/list"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	turnPages        []*data.TurnPage

	patchedModelCalls []*agmodelcallwrite.MutableModelCallView
	patchedToolCalls  []*agtoolcallwrite.MutableToolCallView
	patchedMessages   []*agmessagewrite.MutableMessageView
	turnPageCalls     int
}

func (s *cleanupCaptureDataService) GetRunStepsPage(_ context.Context, _ *agrunsteps.RunStepsInput, _ *data.PageInput, _ ...data.Option) (*data.RunStepPage, error) {
	return &data.RunStepPage{Rows: s.runStepRows}, nil
}

func (s *cleanupCaptureDataService) GetConversation(_ context.Context, _ string, _ *agconv.ConversationInput, _ ...data.Option) (*agconv.ConversationView, error) {
	return s.conversationView, nil
}

func (s *cleanupCaptureDataService) GetTurnsPage(_ context.Context, _ *agturnlistall.TurnRowsInput, _ *data.PageInput, _ ...data.Option) (*data.TurnPage, error) {
	if s.turnPageCalls >= len(s.turnPages) {
		return &data.TurnPage{}, nil
	}
	page := s.turnPages[s.turnPageCalls]
	s.turnPageCalls++
	return page, nil
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

func TestRepairTerminalTurnArtifacts_CleansRecentTerminalTurnResidue(t *testing.T) {
	running := "running"
	store := &cleanupCaptureDataService{
		turnPages: []*data.TurnPage{{
			Rows: []*agturnlistall.TurnRowsView{{
				Id:             "turn-1",
				ConversationId: "conv-1",
				Status:         "failed",
				ErrorMessage:   strptr("stale turn superseded by resumed run run-2"),
				RunId:          strptr("run-1"),
			}},
		}},
		runStepRows: []*agrunsteps.RunStepsView{
			{StepType: "tool_call", MessageId: "tool-running", Status: "running"},
		},
		conversationView: cleanupConversationView("conv-1", "turn-1",
			cleanupToolMessage("conv-1", "turn-1", "tool-running", &running, "running"),
		),
	}
	w := NewWatchdog(store, nil)

	err := w.repairTerminalTurnArtifacts(context.Background())
	if err != nil {
		t.Fatalf("repairTerminalTurnArtifacts() error: %v", err)
	}
	if len(store.patchedToolCalls) != 1 || store.patchedToolCalls[0].MessageID != "tool-running" || store.patchedToolCalls[0].Status != "failed" {
		t.Fatalf("unexpected tool call cleanup: %+v", store.patchedToolCalls)
	}
	if len(store.patchedMessages) != 1 || store.patchedMessages[0].Id != "tool-running" || store.patchedMessages[0].Status == nil || *store.patchedMessages[0].Status != "failed" {
		t.Fatalf("unexpected message cleanup: %+v", store.patchedMessages)
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
