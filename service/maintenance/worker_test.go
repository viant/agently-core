package maintenance

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	coredata "github.com/viant/agently-core/app/store/data"
)

func TestConversationCleanupCandidateDurationRequiresDebug(t *testing.T) {
	for _, debug := range []bool{false, true} {
		var logs bytes.Buffer
		writer, flags := log.Writer(), log.Flags()
		log.SetOutput(&logs)
		log.SetFlags(0)
		policy := eligibleConversationCleanupPolicy("test", "success", "skip", "failed")
		policy.failures = map[string]error{"failed": errors.New("test failure")}
		policy.outcomes["skip"] = conversationCleanupOutcome{Reason: "recent_activity"}
		result, err := runConversationCleanupPolicy(context.Background(), policy, 3, debug)
		log.SetOutput(writer)
		log.SetFlags(flags)
		if err != nil || result.Failed != 1 || result.Skipped != 1 || result.Eligible != 1 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if debug {
			if strings.Count(logs.String(), "duration=") != 3 {
				t.Fatalf("missing candidate timings: %s", logs.String())
			}
			for _, id := range []string{"success", "skip", "failed"} {
				if !strings.Contains(logs.String(), "root="+id) {
					t.Fatalf("missing candidate %s: %s", id, logs.String())
				}
			}
		} else if logs.Len() != 0 {
			t.Fatalf("debug disabled: %s", logs.String())
		}
	}
}

func newTestConversationCleanupWorker(options Options, policies ...conversationCleanupPolicy) *Worker {
	return newConversationCleanupWorker(&recordingConversationCleanupData{}, options, policies...)
}

func TestConversationCleanupWorkerDisabledDoesNotCallPolicy(t *testing.T) {
	policy := &recordingConversationCleanupPolicy{}
	worker := newTestConversationCleanupWorker(Options{
		Enabled:   false,
		BatchSize: 10,
	}, policy)

	result, err := worker.RunPass(context.Background())
	if err != nil {
		t.Fatalf("runPass() error: %v", err)
	}
	if !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("result = %#v", result)
	}
	if policy.selectCalls != 0 || len(policy.processed) != 0 {
		t.Fatalf("disabled worker called policy: selects=%d processed=%v", policy.selectCalls, policy.processed)
	}
}

func TestConversationCleanupWorkerPaginatesAndContinuesAfterCandidateFailure(t *testing.T) {
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	policy := &recordingConversationCleanupPolicy{
		pages: [][]conversationCleanupCandidate{
			{{RootID: "a", ActivityAt: base}},
			{{RootID: "b", ActivityAt: base.Add(time.Minute)}},
			{{RootID: "c", ActivityAt: base.Add(2 * time.Minute)}},
			{{RootID: "d", ActivityAt: base.Add(3 * time.Minute)}},
		},
		failures: map[string]error{"a": errors.New("broken graph")},
		outcomes: map[string]conversationCleanupOutcome{
			"b": {Reason: "recent_activity"},
			"c": {Eligible: true, Reason: "eligible"},
			"d": {Eligible: true, Reason: "eligible"},
		},
	}
	worker := newTestConversationCleanupWorker(Options{
		Enabled:   true,
		BatchSize: 1,
		Timeout:   time.Second,
	}, policy)

	result, err := worker.RunPass(context.Background())
	if err != nil {
		t.Fatalf("runPass() error: %v", err)
	}
	if result.Scanned != 3 || result.Eligible != 1 || result.Deleted != 0 || result.Skipped != 1 || result.Failed != 1 {
		t.Fatalf("result = %#v", result)
	}
	if want := map[string]int{"error": 1, "recent_activity": 1, "eligible": 1}; !reflect.DeepEqual(result.Reasons, want) {
		t.Fatalf("reasons = %#v, want %#v", result.Reasons, want)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(policy.processed, want) {
		t.Fatalf("processed = %v, want %v", policy.processed, want)
	}
	if len(policy.cursors) != 3 || policy.cursors[1].RootID != "a" || policy.cursors[2].RootID != "b" {
		t.Fatalf("cursors = %#v", policy.cursors)
	}
}

func TestFormatConversationCleanupReasonsIsStable(t *testing.T) {
	got := formatConversationCleanupReasons(map[string]int{"recent_activity": 3, "eligible": 2})
	if got != "eligible:2,recent_activity:3" {
		t.Fatalf("formatConversationCleanupReasons() = %q", got)
	}
}

func TestConversationCleanupWorkerTimeoutBoundsPass(t *testing.T) {
	policy := &blockingConversationCleanupPolicy{}
	worker := newTestConversationCleanupWorker(Options{
		Enabled:   true,
		BatchSize: 1,
		Timeout:   30 * time.Millisecond,
	}, policy)

	started := time.Now()
	_, err := worker.RunPass(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout was not applied, elapsed=%s", elapsed)
	}
}

func TestConversationCleanupWorkerUsesIndependentPolicyTimeouts(t *testing.T) {
	blocking := &blockingConversationCleanupPolicy{}
	next := &recordingConversationCleanupPolicy{
		name:  "next",
		pages: [][]conversationCleanupCandidate{{{RootID: "eligible", ActivityAt: time.Now()}}},
		outcomes: map[string]conversationCleanupOutcome{
			"eligible": {Eligible: true, Reason: "eligible"},
		},
	}
	worker := newTestConversationCleanupWorker(Options{
		Enabled:   true,
		BatchSize: 1,
		Timeout:   30 * time.Millisecond,
	}, blocking, next)

	result, err := worker.RunPass(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected policy timeout, got %v", err)
	}
	if result.Eligible != 1 || !reflect.DeepEqual(next.processed, []string{"eligible"}) {
		t.Fatalf("next policy did not run after timeout: result=%#v processed=%v", result, next.processed)
	}
	if len(result.PolicyResults) != 2 || result.PolicyResults[0].Name != "blocking" || result.PolicyResults[1].Name != "next" {
		t.Fatalf("policy results = %#v", result.PolicyResults)
	}
	if !errors.Is(result.PolicyResults[0].Err, context.DeadlineExceeded) || result.PolicyResults[1].Err != nil {
		t.Fatalf("policy errors = %#v", result.PolicyResults)
	}
}

func TestConversationCleanupWorkerContinuesAfterPolicyError(t *testing.T) {
	broken := &failingConversationCleanupPolicy{name: "broken", err: errors.New("select failed")}
	next := &recordingConversationCleanupPolicy{
		name:  "next",
		pages: [][]conversationCleanupCandidate{{{RootID: "eligible", ActivityAt: time.Now()}}},
		outcomes: map[string]conversationCleanupOutcome{
			"eligible": {Eligible: true, Reason: "eligible"},
		},
	}
	worker := newTestConversationCleanupWorker(Options{
		Enabled:   true,
		BatchSize: 1,
		Timeout:   time.Second,
	}, broken, next)

	result, err := worker.RunPass(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cleanup policy broken: select failed") {
		t.Fatalf("runPass() error = %v", err)
	}
	if result.Eligible != 1 || !reflect.DeepEqual(next.processed, []string{"eligible"}) {
		t.Fatalf("next policy did not run after error: result=%#v processed=%v", result, next.processed)
	}
}

func TestConversationCleanupWorkerAppliesBatchPerPolicy(t *testing.T) {
	first := eligibleConversationCleanupPolicy("first", "first-1", "first-2")
	second := eligibleConversationCleanupPolicy("second", "second-1", "second-2")
	worker := newTestConversationCleanupWorker(Options{
		Enabled:   true,
		BatchSize: 1,
		Timeout:   time.Second,
	}, first, second)

	result, err := worker.RunPass(context.Background())
	if err != nil {
		t.Fatalf("runPass() error: %v", err)
	}
	if result.Eligible != 2 || !reflect.DeepEqual(first.processed, []string{"first-1"}) || !reflect.DeepEqual(second.processed, []string{"second-1"}) {
		t.Fatalf("batch was not applied per policy: result=%#v first=%v second=%v", result, first.processed, second.processed)
	}
}

func TestConversationCleanupWorkerStopsWhenParentContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	policy := eligibleConversationCleanupPolicy("unused", "candidate")
	worker := newTestConversationCleanupWorker(Options{
		Enabled:   true,
		BatchSize: 1,
		Timeout:   time.Second,
	}, policy)

	result, err := worker.RunPass(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runPass() error = %v", err)
	}
	if len(result.PolicyResults) != 0 || len(policy.processed) != 0 {
		t.Fatalf("canceled pass ran policies: result=%#v processed=%v", result, policy.processed)
	}
}

func TestConversationCleanupWorkerSkipsOverlappingLocalPass(t *testing.T) {
	policy := newHeldConversationCleanupPolicy()
	worker := newTestConversationCleanupWorker(Options{
		Enabled:   true,
		BatchSize: 1,
		Timeout:   time.Second,
	}, policy)

	firstDone := make(chan error, 1)
	go func() {
		_, err := worker.RunPass(context.Background())
		firstDone <- err
	}()
	select {
	case <-policy.entered:
	case <-time.After(time.Second):
		t.Fatal("first pass did not enter policy")
	}

	result, err := worker.RunPass(context.Background())
	if err != nil {
		t.Fatalf("overlapping runPass() error: %v", err)
	}
	if !result.OverlapSkipped {
		t.Fatalf("overlapping result = %#v", result)
	}
	close(policy.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first runPass() error: %v", err)
	}
}

func TestConversationCleanupWorkerSkipsPassHeldByAnotherWorker(t *testing.T) {
	data := &recordingConversationCleanupData{}
	held := newHeldConversationCleanupPolicy()
	first := newConversationCleanupWorker(data, Options{
		Enabled: true, BatchSize: 1, Timeout: time.Second,
	}, held)
	secondPolicy := eligibleConversationCleanupPolicy("second", "candidate")
	second := newConversationCleanupWorker(data, Options{
		Enabled: true, BatchSize: 1, Timeout: time.Second,
	}, secondPolicy)

	firstDone := make(chan error, 1)
	go func() {
		_, err := first.RunPass(context.Background())
		firstDone <- err
	}()
	select {
	case <-held.entered:
	case <-time.After(time.Second):
		t.Fatal("first worker did not enter policy")
	}

	result, err := second.RunPass(context.Background())
	if err != nil {
		t.Fatalf("second runPass() error: %v", err)
	}
	if !result.LeaseSkipped || result.OverlapSkipped {
		t.Fatalf("second result = %#v", result)
	}
	if secondPolicy.selectCalls != 0 {
		t.Fatalf("second worker ran policy %d times", secondPolicy.selectCalls)
	}

	close(held.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first runPass() error: %v", err)
	}
}

func TestConversationCleanupWorkerRenewsLeaseDuringLongPass(t *testing.T) {
	data := &recordingConversationCleanupData{}
	held := newHeldConversationCleanupPolicy()
	worker := newConversationCleanupWorker(data, Options{
		Enabled: true, BatchSize: 1, Timeout: time.Second,
	}, held)
	worker.leaseTTL = 200 * time.Millisecond
	worker.leaseRenewInterval = 20 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		_, err := worker.RunPass(context.Background())
		done <- err
	}()
	select {
	case <-held.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter policy")
	}
	deadline := time.Now().Add(time.Second)
	for {
		data.leaseMu.Lock()
		renewals := data.leaseRenewals
		data.leaseMu.Unlock()
		if renewals > 0 {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("worker did not renew lease")
		}
		time.Sleep(5 * time.Millisecond)
	}

	close(held.release)
	if err := <-done; err != nil {
		t.Fatalf("runPass() error: %v", err)
	}
}

func TestConversationCleanupWorkerStopsPoliciesWhenLeaseRenewalFails(t *testing.T) {
	data := &recordingConversationCleanupData{leaseRenewError: errors.New("renew unavailable")}
	blocking := &blockingConversationCleanupPolicy{}
	next := eligibleConversationCleanupPolicy("next", "candidate")
	worker := newConversationCleanupWorker(data, Options{
		Enabled: true, BatchSize: 1, Timeout: time.Second,
	}, blocking, next)
	worker.leaseTTL = 200 * time.Millisecond
	worker.leaseRenewInterval = 20 * time.Millisecond

	_, err := worker.RunPass(context.Background())
	if err == nil || !strings.Contains(err.Error(), "renew conversation cleanup lease: renew unavailable") {
		t.Fatalf("runPass() error = %v", err)
	}
	if next.selectCalls != 0 {
		t.Fatalf("worker continued after renewal failure; next selects=%d", next.selectCalls)
	}
}

type shutdownRenewData struct {
	recordingConversationCleanupData
	renew func(context.Context) (*coredata.MaintenanceLeaseRenewResult, error)
}

func (d *shutdownRenewData) RenewMaintenanceLease(ctx context.Context, _ coredata.MaintenanceLease, _ time.Duration) (*coredata.MaintenanceLeaseRenewResult, error) {
	return d.renew(ctx)
}

func TestConversationCleanupHeartbeatDoesNotCatchUpAfterSlowRenew(t *testing.T) {
	const interval = 20 * time.Millisecond
	first := make(chan struct{})
	release := make(chan struct{})
	second := make(chan time.Time, 1)
	var firstFinished time.Time
	calls := 0
	data := &shutdownRenewData{renew: func(ctx context.Context) (*coredata.MaintenanceLeaseRenewResult, error) {
		calls++
		if calls == 1 {
			close(first)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			firstFinished = time.Now()
		} else {
			second <- time.Now()
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &coredata.MaintenanceLeaseRenewResult{Renewed: true}, nil
	}}
	worker := newConversationCleanupWorker(data, Options{})
	worker.leaseRenewInterval = interval
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(errCleanupPassFinished)
	done := make(chan error, 1)
	go func() { done <- worker.renewConversationCleanupLease(ctx, coredata.MaintenanceLease{}) }()
	select {
	case <-first:
	case <-time.After(3 * time.Second):
		t.Fatal("first renewal did not start")
	}
	// Ensure a ticker would have accumulated a pending tick while Renew ran.
	time.Sleep(3 * interval)
	close(release)
	select {
	case started := <-second:
		if started.Sub(firstFinished) < interval {
			t.Errorf("renewals ran back-to-back: interval=%s", started.Sub(firstFinished))
		}
	case <-time.After(3 * time.Second):
		t.Error("second renewal did not start")
	}
	cancel(errCleanupPassFinished)
	if err := <-done; err != nil {
		t.Fatalf("normal heartbeat shutdown failed: %v", err)
	}
}

func TestConversationCleanupWorkerFinishDuringRenew(t *testing.T) {
	sqlFailure := errors.New("SQL renewal failed")
	for _, scenario := range []string{"own_cancel", "wrapped_cancel", "canceled_transaction", "transaction_error", "sql_error", "joined_sql_error", "lease_lost", "parent_cancel", "parent_deadline"} {
		t.Run(scenario, func(t *testing.T) {
			entered := make(chan struct{})
			data := &shutdownRenewData{renew: func(ctx context.Context) (*coredata.MaintenanceLeaseRenewResult, error) {
				close(entered)
				<-ctx.Done()
				switch scenario {
				case "sql_error":
					return nil, sqlFailure
				case "joined_sql_error":
					return nil, errors.Join(ctx.Err(), sqlFailure)
				case "canceled_transaction":
					return nil, errors.Join(ctx.Err(), sql.ErrTxDone)
				case "transaction_error":
					return nil, sql.ErrTxDone
				case "lease_lost":
					return &coredata.MaintenanceLeaseRenewResult{Renewed: false}, nil
				case "wrapped_cancel":
					return nil, fmt.Errorf("renew interrupted: %w", ctx.Err())
				default:
					return nil, ctx.Err()
				}
			}}
			held := newHeldConversationCleanupPolicy()
			worker := newConversationCleanupWorker(data, Options{Enabled: true, BatchSize: 1}, held)
			worker.leaseRenewInterval = time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "parent_deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			done := make(chan error, 1)
			go func() { _, err := worker.RunPass(ctx); done <- err }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("renewal did not start")
			}
			if scenario == "parent_cancel" {
				cancel()
			}
			if scenario == "parent_deadline" {
				<-ctx.Done()
			}
			close(held.release) // Finish only after Renew is definitely in flight.
			var err error
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("worker did not join heartbeat")
			}
			switch scenario {
			case "own_cancel", "wrapped_cancel", "canceled_transaction":
				if err != nil {
					t.Fatalf("normal shutdown reported failure: %v", err)
				}
			case "sql_error", "joined_sql_error":
				if !errors.Is(err, sqlFailure) {
					t.Fatalf("SQL error masked: %v", err)
				}
			case "transaction_error":
				if !errors.Is(err, sql.ErrTxDone) {
					t.Fatalf("transaction error masked: %v", err)
				}
			case "lease_lost":
				if !errors.Is(err, coredata.ErrMaintenanceLeaseLost) {
					t.Fatalf("lease loss masked: %v", err)
				}
			case "parent_cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("parent cancellation masked: %v", err)
				}
			case "parent_deadline":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("parent deadline masked: %v", err)
				}
			}
		})
	}
}

func TestConversationCleanupWorkerStopsImmediatelyOnCandidateLeaseLoss(t *testing.T) {
	for _, fromSelection := range []bool{false, true} {
		t.Run(fmt.Sprintf("selection=%t", fromSelection), func(t *testing.T) {
			first := eligibleConversationCleanupPolicy("first", "a", "b")
			first.failures = map[string]error{"a": fmt.Errorf("preflight: %w", coredata.ErrMaintenanceLeaseLost)}
			var policy conversationCleanupPolicy = first
			if fromSelection {
				policy = &failingConversationCleanupPolicy{name: "first", err: coredata.ErrMaintenanceLeaseLost}
			}
			next := eligibleConversationCleanupPolicy("next", "c")
			worker := newTestConversationCleanupWorker(Options{Enabled: true, BatchSize: 10}, policy, next)
			result, err := worker.RunPass(context.Background())
			if !errors.Is(err, coredata.ErrMaintenanceLeaseLost) {
				t.Fatalf("lease loss not propagated: %v", err)
			}
			if len(result.PolicyResults) != 1 || next.selectCalls != 0 {
				t.Fatalf("continued to next policy: %+v", result)
			}
			if !fromSelection && !reflect.DeepEqual(first.processed, []string{"a"}) {
				t.Fatalf("continued to next candidate: %v", first.processed)
			}
		})
	}
}

type recordingConversationCleanupPolicy struct {
	name        string
	selectCalls int
	pages       [][]conversationCleanupCandidate
	cursors     []conversationCleanupCursor
	processed   []string
	failures    map[string]error
	outcomes    map[string]conversationCleanupOutcome
}

func (p *recordingConversationCleanupPolicy) Name() string {
	if p.name != "" {
		return p.name
	}
	return "recording"
}

func (p *recordingConversationCleanupPolicy) SelectCandidates(_ context.Context, cursor conversationCleanupCursor, _ int) ([]conversationCleanupCandidate, error) {
	p.cursors = append(p.cursors, cursor)
	index := p.selectCalls
	p.selectCalls++
	if index >= len(p.pages) {
		return nil, nil
	}
	return p.pages[index], nil
}

func (p *recordingConversationCleanupPolicy) ProcessCandidate(_ context.Context, candidate conversationCleanupCandidate) (conversationCleanupOutcome, error) {
	p.processed = append(p.processed, candidate.RootID)
	if err := p.failures[candidate.RootID]; err != nil {
		return conversationCleanupOutcome{}, err
	}
	return p.outcomes[candidate.RootID], nil
}

type blockingConversationCleanupPolicy struct{}

func (p *blockingConversationCleanupPolicy) Name() string { return "blocking" }

func (p *blockingConversationCleanupPolicy) SelectCandidates(ctx context.Context, _ conversationCleanupCursor, _ int) ([]conversationCleanupCandidate, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *blockingConversationCleanupPolicy) ProcessCandidate(context.Context, conversationCleanupCandidate) (conversationCleanupOutcome, error) {
	return conversationCleanupOutcome{}, nil
}

type heldConversationCleanupPolicy struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newHeldConversationCleanupPolicy() *heldConversationCleanupPolicy {
	return &heldConversationCleanupPolicy{entered: make(chan struct{}), release: make(chan struct{})}
}

func (p *heldConversationCleanupPolicy) Name() string { return "held" }

func (p *heldConversationCleanupPolicy) SelectCandidates(context.Context, conversationCleanupCursor, int) ([]conversationCleanupCandidate, error) {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return nil, nil
}

func (p *heldConversationCleanupPolicy) ProcessCandidate(context.Context, conversationCleanupCandidate) (conversationCleanupOutcome, error) {
	return conversationCleanupOutcome{}, nil
}

type failingConversationCleanupPolicy struct {
	name string
	err  error
}

func (p *failingConversationCleanupPolicy) Name() string { return p.name }

func (p *failingConversationCleanupPolicy) SelectCandidates(context.Context, conversationCleanupCursor, int) ([]conversationCleanupCandidate, error) {
	return nil, p.err
}

func (p *failingConversationCleanupPolicy) ProcessCandidate(context.Context, conversationCleanupCandidate) (conversationCleanupOutcome, error) {
	return conversationCleanupOutcome{}, nil
}

func eligibleConversationCleanupPolicy(name string, candidateIDs ...string) *recordingConversationCleanupPolicy {
	candidates := make([]conversationCleanupCandidate, 0, len(candidateIDs))
	outcomes := make(map[string]conversationCleanupOutcome, len(candidateIDs))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, candidateID := range candidateIDs {
		candidates = append(candidates, conversationCleanupCandidate{RootID: candidateID, ActivityAt: base.Add(time.Duration(i) * time.Second)})
		outcomes[candidateID] = conversationCleanupOutcome{Eligible: true, Reason: "eligible"}
	}
	return &recordingConversationCleanupPolicy{name: name, pages: [][]conversationCleanupCandidate{candidates}, outcomes: outcomes}
}
