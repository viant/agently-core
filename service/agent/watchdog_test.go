package agent

import (
	"context"
	token "github.com/viant/agently-core/internal/auth/token"
	agrunactive "github.com/viant/agently-core/pkg/agently/run/active"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	"sync"
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
		{name: "resumed child", run: &agrunstale.StaleRunsView{ConversationKind: "interactive", ResumedFromRunId: strptr("old-run")}, want: true},
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
