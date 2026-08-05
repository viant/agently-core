package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
	cancels "github.com/viant/agently-core/app/store/conversation/cancel"
	mem "github.com/viant/agently-core/app/store/data/memory"
	iauth "github.com/viant/agently-core/internal/auth"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	schrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/scy"
	scyauth "github.com/viant/scy/auth"
	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/cred"
	_ "github.com/viant/scy/kms/blowfish"
	"golang.org/x/oauth2"
)

type testCancelRegistry struct {
	cancelConversationCalls []string
}

func (t *testCancelRegistry) Register(string, string, context.CancelFunc) {}
func (t *testCancelRegistry) Complete(string, string, context.CancelFunc) {}
func (t *testCancelRegistry) CancelTurn(string) bool                      { return false }
func (t *testCancelRegistry) CancelConversation(conversationID string) bool {
	t.cancelConversationCalls = append(t.cancelConversationCalls, strings.TrimSpace(conversationID))
	return true
}

var _ cancels.Registry = (*testCancelRegistry)(nil)

type fakeOAuthAuthorizer struct {
	tok       *oauth2.Token
	err       error
	lastCmd   *authorizer.Command
	callCount int
}

func (f *fakeOAuthAuthorizer) Authorize(_ context.Context, command *authorizer.Command) (*oauth2.Token, error) {
	f.callCount++
	f.lastCmd = command
	if f.err != nil {
		return nil, f.err
	}
	return f.tok, nil
}

func TestService_RunDue_ReapsStaleActiveRunWhenScheduleNotDue(t *testing.T) {
	store, db := newTestStore(t)
	ensureRunWriteComponent(t, store)
	conv := mem.New()
	svc := New(store, &agentsvc.Service{}, WithConversationClient(conv))

	now := time.Now().UTC()
	nextRunAt := now.Add(30 * time.Minute)
	createdAt := now.Add(-3 * time.Hour)
	startedAt := now.Add(-3 * time.Minute)
	scheduledFor := now.Add(-4 * time.Minute)

	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO schedule (
			id, name, visibility, agent_ref, enabled, schedule_type, timezone,
			next_run_at, timeout_seconds, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "sched-stale", "Stale Scheduled Run", "public", "simple", 1, "adhoc", "UTC", nextRunAt, 1, createdAt, createdAt); err != nil {
		t.Fatalf("insert schedule error: %v", err)
	}
	insertConversationRow(t, db, "conv-stale", "private")
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO run (
			id, schedule_id, conversation_id, conversation_kind, status,
			created_at, started_at, scheduled_for
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "run-stale", "sched-stale", "conv-stale", "scheduled", "running", startedAt.Add(-10*time.Second), startedAt, scheduledFor); err != nil {
		t.Fatalf("insert run error: %v", err)
	}

	seedRunningConversation(t, conv, "conv-stale", "turn-stale", startedAt)

	started, err := svc.RunDue(context.Background())
	if err != nil {
		t.Fatalf("RunDue() error: %v", err)
	}
	if started != 0 {
		t.Fatalf("expected no new runs to start, got %d", started)
	}

	var status string
	var errorMessage sql.NullString
	var completedAt sql.NullTime
	if err := db.QueryRowContext(context.Background(), `
		SELECT status, error_message, completed_at
		FROM run
		WHERE id = ?
	`, "run-stale").Scan(&status, &errorMessage, &completedAt); err != nil {
		t.Fatalf("query run error: %v", err)
	}
	if status != "failed" {
		t.Fatalf("expected failed run, got %q", status)
	}
	if !completedAt.Valid {
		t.Fatalf("expected completed_at to be set")
	}
	if !strings.Contains(errorMessage.String, "stale scheduled run detected") {
		t.Fatalf("expected stale-run error message, got %q", errorMessage.String)
	}
	for _, want := range []string{"cause=timeout_exceeded", "timeout=1s", "deadline=", "detected_at=", "run_age=", "overdue_by="} {
		if !strings.Contains(errorMessage.String, want) {
			t.Fatalf("expected stale-run message to contain %q, got %q", want, errorMessage.String)
		}
	}

	var lastStatus sql.NullString
	var lastError sql.NullString
	if err := db.QueryRowContext(context.Background(), `
		SELECT last_status, last_error
		FROM schedule
		WHERE id = ?
	`, "sched-stale").Scan(&lastStatus, &lastError); err != nil {
		t.Fatalf("query schedule error: %v", err)
	}
	if lastStatus.String != "failed" {
		t.Fatalf("expected schedule last_status failed, got %q", lastStatus.String)
	}
	if !strings.Contains(lastError.String, "stale scheduled run detected") {
		t.Fatalf("expected schedule last_error to mention stale run, got %q", lastError.String)
	}

	got, err := conv.GetConversation(
		context.Background(),
		"conv-stale",
		convcli.WithIncludeTranscript(true),
		convcli.WithIncludeModelCall(true),
		convcli.WithIncludeToolCall(true),
	)
	if err != nil {
		t.Fatalf("GetConversation() error: %v", err)
	}
	if got == nil || got.Stage != "canceled" {
		t.Fatalf("expected conversation status canceled, got %#v", got)
	}
	transcript := got.GetTranscript()
	if len(transcript) != 1 || transcript[0] == nil {
		t.Fatalf("expected one transcript turn, got %#v", transcript)
	}
	if transcript[0].Status != "canceled" {
		t.Fatalf("expected turn status canceled, got %q", transcript[0].Status)
	}

	var modelOK, toolOK bool
	for _, msg := range transcript[0].Message {
		if msg == nil {
			continue
		}
		if msg.ModelCall != nil {
			modelOK = msg.ModelCall.Status == "canceled" && msg.ModelCall.CompletedAt != nil && !msg.ModelCall.CompletedAt.IsZero()
		}
		for _, toolMsg := range msg.ToolMessage {
			if toolMsg == nil || toolMsg.ToolCall == nil {
				continue
			}
			toolOK = toolMsg.ToolCall.Status == "canceled" && toolMsg.ToolCall.CompletedAt != nil && !toolMsg.ToolCall.CompletedAt.IsZero()
		}
	}
	if !modelOK {
		t.Fatalf("expected model call to be canceled with completed_at set")
	}
	if !toolOK {
		t.Fatalf("expected tool call to be canceled with completed_at set")
	}
}

func TestService_staleActiveRunReason_UsesLeaseCause(t *testing.T) {
	svc := New(nil, nil)
	now := time.Now().UTC()
	startedAt := now.Add(-5 * time.Minute)
	leaseUntil := now.Add(-20 * time.Second)
	run := &schrun.RunView{
		Status:     "running",
		CreatedAt:  startedAt.Add(-5 * time.Second),
		StartedAt:  &startedAt,
		LeaseUntil: &leaseUntil,
	}

	reason, stale := svc.staleActiveRunReason(nil, run, now)
	if !stale {
		t.Fatalf("expected stale active run")
	}
	for _, want := range []string{"cause=lease_expired", "run_start=", "timeout=20m0s", "lease_until=", "grace=15s", "detected_at=", "run_age=", "overdue_by="} {
		if !strings.Contains(reason, want) {
			t.Fatalf("expected lease stale reason to contain %q, got %q", want, reason)
		}
	}
}

func TestService_cancelConversationAndMark_TerminatesRunningExecutions(t *testing.T) {
	conv := mem.New()
	reg := &testCancelRegistry{}
	agentSvc := agentsvc.New(nil, nil, nil, nil, nil, conv, agentsvc.WithCancelRegistry(reg))
	svc := New(nil, agentSvc, WithConversationClient(conv))
	startedAt := time.Now().UTC().Add(-1 * time.Minute)

	seedRunningConversation(t, conv, "conv-direct", "turn-direct", startedAt)

	if err := svc.cancelConversationAndMark(context.Background(), "conv-direct", "canceled"); err != nil {
		t.Fatalf("cancelConversationAndMark() error: %v", err)
	}
	if len(reg.cancelConversationCalls) != 1 || reg.cancelConversationCalls[0] != "conv-direct" {
		t.Fatalf("expected live cancel for conv-direct, got %#v", reg.cancelConversationCalls)
	}

	got, err := conv.GetConversation(
		context.Background(),
		"conv-direct",
		convcli.WithIncludeTranscript(true),
		convcli.WithIncludeModelCall(true),
		convcli.WithIncludeToolCall(true),
	)
	if err != nil {
		t.Fatalf("GetConversation() error: %v", err)
	}
	if got == nil || got.Stage != "canceled" {
		t.Fatalf("expected conversation status canceled, got %#v", got)
	}
	transcript := got.GetTranscript()
	if len(transcript) != 1 || transcript[0] == nil {
		t.Fatalf("expected one transcript turn, got %#v", transcript)
	}
	if transcript[0].Status != "canceled" {
		t.Fatalf("expected turn status canceled, got %q", transcript[0].Status)
	}

	var modelOK, toolOK bool
	for _, msg := range transcript[0].Message {
		if msg == nil {
			continue
		}
		if msg.ModelCall != nil {
			modelOK = msg.ModelCall.Status == "canceled" && msg.ModelCall.CompletedAt != nil && !msg.ModelCall.CompletedAt.IsZero()
		}
		for _, toolMsg := range msg.ToolMessage {
			if toolMsg == nil || toolMsg.ToolCall == nil {
				continue
			}
			toolOK = toolMsg.ToolCall.Status == "canceled" && toolMsg.ToolCall.CompletedAt != nil && !toolMsg.ToolCall.CompletedAt.IsZero()
		}
	}
	if !modelOK {
		t.Fatalf("expected model call to be canceled with completed_at set")
	}
	if !toolOK {
		t.Fatalf("expected tool call to be canceled with completed_at set")
	}
}

func TestService_RunDue_DoesNotFailLongRunningRunWhenLeaseIsFresh(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	now := time.Now().UTC()
	nextRunAt := now.Add(30 * time.Minute)
	createdAt := now.Add(-2 * time.Hour)
	startedAt := now.Add(-40 * time.Minute)
	scheduledFor := now.Add(-41 * time.Minute)
	leaseUntil := now.Add(2 * time.Minute)

	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO schedule (
			id, name, visibility, agent_ref, enabled, schedule_type, timezone,
			next_run_at, timeout_seconds, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "sched-lease-fresh", "Lease Fresh", "public", "simple", 1, "adhoc", "UTC", nextRunAt, 1, createdAt, createdAt); err != nil {
		t.Fatalf("insert schedule error: %v", err)
	}

	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO run (
			id, schedule_id, conversation_kind, status, created_at, started_at, scheduled_for, lease_owner, lease_until
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "run-lease-fresh", "sched-lease-fresh", "scheduled", "running", startedAt.Add(-10*time.Second), startedAt, scheduledFor, "other-owner", leaseUntil); err != nil {
		t.Fatalf("insert run error: %v", err)
	}

	started, err := svc.RunDue(context.Background())
	if err != nil {
		t.Fatalf("RunDue() error: %v", err)
	}
	if started != 0 {
		t.Fatalf("expected no new runs to start, got %d", started)
	}

	var status string
	var errorMessage sql.NullString
	var completedAt sql.NullTime
	if err := db.QueryRowContext(context.Background(), `
		SELECT status, error_message, completed_at
		FROM run
		WHERE id = ?
	`, "run-lease-fresh").Scan(&status, &errorMessage, &completedAt); err != nil {
		t.Fatalf("query run error: %v", err)
	}
	if status != "running" {
		t.Fatalf("expected running run, got %q (error=%q)", status, errorMessage.String)
	}
	if completedAt.Valid {
		t.Fatalf("expected completed_at to be null for a live leased run")
	}
}

func TestService_IsDue_RecomputesMissingCronNextRunInFuture(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	now := time.Date(2026, 6, 17, 15, 10, 53, 0, time.UTC)
	createdAt := time.Date(2026, 5, 8, 10, 51, 49, 0, time.UTC)
	updatedAt := now
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO schedule (
			id, name, visibility, agent_ref, enabled, schedule_type, cron_expr, timezone,
			timeout_seconds, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "sched-cron-recompute", "Cron Recompute", "public", "simple", 1, "cron", "0 9 * * *", "UTC", 0, createdAt, updatedAt); err != nil {
		t.Fatalf("insert schedule error: %v", err)
	}
	row, err := store.Get(context.Background(), "sched-cron-recompute")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	due, scheduledFor, err := svc.isDue(context.Background(), row, now)
	if err != nil {
		t.Fatalf("isDue() error: %v", err)
	}
	if due {
		t.Fatalf("expected recomputed cron schedule not to be due")
	}
	want := time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC)
	if !scheduledFor.Equal(want) {
		t.Fatalf("scheduledFor = %s, want %s", scheduledFor, want)
	}
	var nextRunAt time.Time
	if err := db.QueryRowContext(context.Background(), `SELECT next_run_at FROM schedule WHERE id = ?`, "sched-cron-recompute").Scan(&nextRunAt); err != nil {
		t.Fatalf("query next_run_at error: %v", err)
	}
	if !nextRunAt.Equal(want) {
		t.Fatalf("next_run_at = %s, want %s", nextRunAt, want)
	}
}

func TestService_IsDue_RecomputesMissingIntervalNextRunInFuture(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	now := time.Date(2026, 6, 17, 15, 10, 0, 0, time.UTC)
	createdAt := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	updatedAt := now
	lastRunAt := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	intervalSeconds := 2 * 60 * 60
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO schedule (
			id, name, visibility, agent_ref, enabled, schedule_type, interval_seconds, timezone,
			last_run_at, timeout_seconds, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "sched-interval-recompute", "Interval Recompute", "public", "simple", 1, "interval", intervalSeconds, "UTC", lastRunAt, 0, createdAt, updatedAt); err != nil {
		t.Fatalf("insert schedule error: %v", err)
	}
	row, err := store.Get(context.Background(), "sched-interval-recompute")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	due, scheduledFor, err := svc.isDue(context.Background(), row, now)
	if err != nil {
		t.Fatalf("isDue() error: %v", err)
	}
	if due {
		t.Fatalf("expected recomputed interval schedule not to be due")
	}
	want := nextIntervalAfter(lastRunAt, 2*time.Hour, now)
	if !scheduledFor.Equal(want) {
		t.Fatalf("scheduledFor = %s, want %s", scheduledFor, want)
	}
	if !scheduledFor.After(now) {
		t.Fatalf("expected scheduledFor to be in the future, got %s <= %s", scheduledFor, now)
	}
	var nextRunAt time.Time
	if err := db.QueryRowContext(context.Background(), `SELECT next_run_at FROM schedule WHERE id = ?`, "sched-interval-recompute").Scan(&nextRunAt); err != nil {
		t.Fatalf("query next_run_at error: %v", err)
	}
	if !nextRunAt.Equal(want) {
		t.Fatalf("next_run_at = %s, want %s", nextRunAt, want)
	}
}

func TestService_IsDue_ExistingPastNextRunRemainsDue(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	now := time.Date(2026, 6, 17, 15, 10, 0, 0, time.UTC)
	nextRunAt := now.Add(-1 * time.Minute)
	createdAt := now.Add(-24 * time.Hour)
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO schedule (
			id, name, visibility, agent_ref, enabled, schedule_type, cron_expr, timezone,
			next_run_at, timeout_seconds, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "sched-past-next", "Past Next", "public", "simple", 1, "cron", "0 9 * * *", "UTC", nextRunAt, 0, createdAt, createdAt); err != nil {
		t.Fatalf("insert schedule error: %v", err)
	}
	row, err := store.Get(context.Background(), "sched-past-next")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	due, scheduledFor, err := svc.isDue(context.Background(), row, now)
	if err != nil {
		t.Fatalf("isDue() error: %v", err)
	}
	if !due {
		t.Fatalf("expected existing past next_run_at to remain due")
	}
	if !scheduledFor.Equal(nextRunAt) {
		t.Fatalf("scheduledFor = %s, want %s", scheduledFor, nextRunAt)
	}
}

func TestService_tryClaimRunLeaseAndRelease(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, &agentsvc.Service{})
	svc.ensureLeaseConfig()

	now := time.Now().UTC()
	insertScheduleRow(t, db, "sched-lease-1", "Lease Schedule")
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO run (
			id, schedule_id, conversation_kind, status, created_at
		) VALUES (?, ?, ?, ?, ?)
	`, "run-lease-1", "sched-lease-1", "scheduled", "pending", now); err != nil {
		t.Fatalf("insert run error: %v", err)
	}

	claimed, err := svc.tryClaimRunLease(context.Background(), "run-lease-1", now)
	if err != nil {
		t.Fatalf("tryClaimRunLease() error: %v", err)
	}
	if !claimed {
		t.Fatalf("expected lease to be claimed")
	}

	var leaseOwner sql.NullString
	var leaseUntil sql.NullTime
	if err := db.QueryRowContext(context.Background(), `
		SELECT lease_owner, lease_until FROM run WHERE id = ?
	`, "run-lease-1").Scan(&leaseOwner, &leaseUntil); err != nil {
		t.Fatalf("query claimed lease error: %v", err)
	}
	if !leaseOwner.Valid || strings.TrimSpace(leaseOwner.String) == "" {
		t.Fatalf("expected lease_owner to be populated")
	}
	if !leaseUntil.Valid || !leaseUntil.Time.After(now) {
		t.Fatalf("expected lease_until to be in the future, got %v", leaseUntil)
	}

	svc.releaseRunLease(context.Background(), "run-lease-1")

	if err := db.QueryRowContext(context.Background(), `
		SELECT lease_owner, lease_until FROM run WHERE id = ?
	`, "run-lease-1").Scan(&leaseOwner, &leaseUntil); err != nil {
		t.Fatalf("query released lease error: %v", err)
	}
	if leaseOwner.Valid {
		t.Fatalf("expected lease_owner to be cleared, got %q", leaseOwner.String)
	}
	if leaseUntil.Valid {
		t.Fatalf("expected lease_until to be cleared, got %v", leaseUntil.Time)
	}
}

func TestService_applyUserCred_LegacyBasicSecretUsesOOB(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "user_cred.enc.json")

	basicSecret := scy.NewSecret(&cred.Basic{
		Username: "agently_scheduler",
		Password: "viant12345678",
	}, scy.NewResource(&cred.Basic{}, secretFile, "blowfish://default"))
	if err := scy.New().Store(ctx, basicSecret); err != nil {
		t.Fatalf("Store() error: %v", err)
	}

	fakeAuthz := &fakeOAuthAuthorizer{
		tok: &oauth2.Token{
			AccessToken:  "oob-access",
			RefreshToken: "oob-refresh",
			Expiry:       time.Now().Add(30 * time.Minute),
		},
	}
	fakeAuthz.tok = fakeAuthz.tok.WithExtra(map[string]interface{}{"id_token": "oob-id"})

	svc := New(nil, &agentsvc.Service{}, WithAuthConfig(&svcauth.Config{
		OAuth: &svcauth.OAuth{
			Mode: "bff",
			Client: &svcauth.OAuthClient{
				ConfigURL: "file:///tmp/oauth.client.json",
				Scopes:    []string{"openid", "profile"},
			},
		},
	}))
	svc.oauthAuthz = fakeAuthz

	gotCtx, err := svc.applyUserCred(ctx, secretFile+"|blowfish://default")
	if err != nil {
		t.Fatalf("applyUserCred() error: %v", err)
	}
	if fakeAuthz.callCount != 1 {
		t.Fatalf("expected one authorize call, got %d", fakeAuthz.callCount)
	}
	if fakeAuthz.lastCmd == nil {
		t.Fatalf("expected authorize command")
	}
	if got := strings.TrimSpace(fakeAuthz.lastCmd.SecretsURL); got != secretFile+"|blowfish://default" {
		t.Fatalf("SecretsURL = %q, want %q", got, secretFile+"|blowfish://default")
	}
	if got := strings.TrimSpace(fakeAuthz.lastCmd.OAuthConfig.ConfigURL); got != "file:///tmp/oauth.client.json" {
		t.Fatalf("ConfigURL = %q, want %q", got, "file:///tmp/oauth.client.json")
	}

	if got := iauth.Bearer(gotCtx); got != "oob-access" {
		t.Fatalf("Bearer() = %q, want %q", got, "oob-access")
	}
	if got := iauth.IDToken(gotCtx); got != "oob-id" {
		t.Fatalf("IDToken() = %q, want %q", got, "oob-id")
	}
}

func TestService_applyUserCred_PublicUserCredAuthConfigUsesOOB(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "user_cred.enc.json")

	basicSecret := scy.NewSecret(&cred.Basic{
		Username: "agently_scheduler",
		Password: "viant12345678",
	}, scy.NewResource(&cred.Basic{}, secretFile, "blowfish://default"))
	if err := scy.New().Store(ctx, basicSecret); err != nil {
		t.Fatalf("Store() error: %v", err)
	}

	fakeAuthz := &fakeOAuthAuthorizer{
		tok: &oauth2.Token{
			AccessToken: "public-oob-access",
			Expiry:      time.Now().Add(30 * time.Minute),
		},
	}
	fakeAuthz.tok = fakeAuthz.tok.WithExtra(map[string]interface{}{"id_token": "public-oob-id"})

	svc := New(nil, &agentsvc.Service{}, WithUserCredAuthConfig(&UserCredAuthConfig{
		Mode:            "bff",
		ClientConfigURL: "file:///tmp/oauth.client.json",
		Scopes:          []string{"openid", "email"},
	}))
	svc.oauthAuthz = fakeAuthz

	gotCtx, err := svc.applyUserCred(ctx, secretFile+"|blowfish://default")
	if err != nil {
		t.Fatalf("applyUserCred() error: %v", err)
	}
	if fakeAuthz.lastCmd == nil {
		t.Fatalf("expected authorize command")
	}
	if got := strings.TrimSpace(fakeAuthz.lastCmd.OAuthConfig.ConfigURL); got != "file:///tmp/oauth.client.json" {
		t.Fatalf("ConfigURL = %q, want %q", got, "file:///tmp/oauth.client.json")
	}
	if len(fakeAuthz.lastCmd.Scopes) != 2 || fakeAuthz.lastCmd.Scopes[1] != "email" {
		t.Fatalf("Scopes = %v, want [openid email]", fakeAuthz.lastCmd.Scopes)
	}
	if got := iauth.Bearer(gotCtx); got != "public-oob-access" {
		t.Fatalf("Bearer() = %q, want %q", got, "public-oob-access")
	}
	if got := iauth.IDToken(gotCtx); got != "public-oob-id" {
		t.Fatalf("IDToken() = %q, want %q", got, "public-oob-id")
	}
}

func TestService_applyUserCred_IncludesConfiguredHeadlessScope(t *testing.T) {
	ctx := context.Background()
	fakeAuthz := &fakeOAuthAuthorizer{
		tok: &oauth2.Token{
			AccessToken:  "scoped-access",
			RefreshToken: "scoped-refresh",
			Expiry:       time.Now().Add(30 * time.Minute),
		},
	}
	svc := New(nil, &agentsvc.Service{},
		WithAuthConfig(&svcauth.Config{
			OAuth: &svcauth.OAuth{
				Mode: "bff",
				Client: &svcauth.OAuthClient{
					ConfigURL:   "file:///tmp/oauth.client.json",
					Scopes:      []string{"openid", "profile", "email"},
					WebUIScopes: []string{"ROLE_STEWARD_WEB"},
				},
			},
		}),
		WithUserCredAuthConfig(&UserCredAuthConfig{
			Mode:            "bff",
			ClientConfigURL: "file:///tmp/oauth.client.json",
			Scopes:          []string{"openid", "profile", "email"},
		}),
	)
	svc.oauthAuthz = fakeAuthz

	if _, err := svc.applyUserCred(ctx, "scheduler-user-credential"); err != nil {
		t.Fatalf("applyUserCred() error = %v", err)
	}
	if fakeAuthz.lastCmd == nil {
		t.Fatal("expected authorize command")
	}
	got := strings.Join(fakeAuthz.lastCmd.Scopes, " ")
	if got != "openid profile email ROLE_STEWARD_WEB" {
		t.Fatalf("scheduler OAuth scopes = %q, want scoped headless grant", got)
	}
}

func TestService_applyUserCred_ValidatesFreshOOBScopesBeforeStore(t *testing.T) {
	fakeAuthz := &fakeOAuthAuthorizer{
		tok: (&oauth2.Token{
			AccessToken:  "valid-scoped-access",
			RefreshToken: "valid-scoped-refresh",
			Expiry:       time.Now().Add(30 * time.Minute),
		}).WithExtra(map[string]interface{}{"scope": "openid profile"}),
	}
	tokens := &fakeSchedulerTokenProvider{}
	svc := New(nil, &agentsvc.Service{},
		WithUserCredAuthConfig(&UserCredAuthConfig{
			Mode:            "bff",
			ClientConfigURL: "file:///tmp/oauth.client.json",
			Scopes:          []string{"openid", "profile"},
		}),
		WithTokenProvider(tokens),
	)
	svc.oauthAuthz = fakeAuthz

	gotCtx, err := svc.applyUserCred(context.Background(), "scheduler-user-credential")
	if err != nil {
		t.Fatalf("applyUserCred() error = %v", err)
	}
	if tokens.storeCalls != 1 || tokens.ensureCalls != 1 {
		t.Fatalf("token provider calls Store=%d Ensure=%d, want 1 each", tokens.storeCalls, tokens.ensureCalls)
	}
	if tokens.stored == nil || tokens.stored.AccessToken != "valid-scoped-access" {
		t.Fatalf("stored OOB token = %#v", tokens.stored)
	}
	if got := iauth.TokensFromContext(gotCtx); got == nil || got.AccessToken != "translated-access" {
		t.Fatalf("ensured token = %#v, want translated access token", got)
	}
}

func TestService_applyUserCred_UnderScopedFreshOOBIsNotStoredOrInjected(t *testing.T) {
	const canary = "under-scoped-access-canary"
	var logs bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})
	t.Setenv("AGENTLY_DEBUG", "")

	fakeAuthz := &fakeOAuthAuthorizer{
		tok: (&oauth2.Token{
			AccessToken:  canary,
			RefreshToken: "under-scoped-refresh-canary",
			Expiry:       time.Now().Add(30 * time.Minute),
		}).WithExtra(map[string]interface{}{"scope": "openid"}),
	}
	tokens := &fakeSchedulerTokenProvider{}
	svc := New(nil, &agentsvc.Service{},
		WithUserCredAuthConfig(&UserCredAuthConfig{
			Mode:            "bff",
			ClientConfigURL: "file:///tmp/oauth.client.json",
			Scopes:          []string{"openid", "profile"},
		}),
		WithTokenProvider(tokens),
	)
	svc.oauthAuthz = fakeAuthz

	gotCtx, err := svc.applyUserCred(context.Background(), "scheduler-user-credential")
	if err == nil {
		t.Fatal("applyUserCred() error = nil, want scope validation failure")
	}
	if tokens.storeCalls != 0 || tokens.ensureCalls != 0 {
		t.Fatalf("under-scoped token reached provider: Store=%d Ensure=%d", tokens.storeCalls, tokens.ensureCalls)
	}
	if iauth.TokensFromContext(gotCtx) != nil || iauth.Bearer(gotCtx) != "" {
		t.Fatal("under-scoped OOB token reached downstream context")
	}
	output := logs.String()
	if !strings.Contains(output, "[auth-token]") ||
		!strings.Contains(output, `op="scheduler_oob_scope_validate"`) ||
		!strings.Contains(output, `classification="scope_validation"`) {
		t.Fatalf("missing always-on scope failure log: %s", output)
	}
	if strings.Contains(output, canary) || strings.Contains(output, "under-scoped-refresh-canary") {
		t.Fatalf("OOB token leaked in log: %s", output)
	}
}

func TestService_AuthDirectFallbackRejectsExpiredOOBTokenWithoutProvider(t *testing.T) {
	fakeAuthz := &fakeOAuthAuthorizer{
		tok: (&oauth2.Token{
			AccessToken:  "expired-direct-access",
			RefreshToken: "expired-direct-refresh",
			Expiry:       time.Now().Add(-time.Minute),
		}).WithExtra(map[string]interface{}{"scope": "openid profile"}),
	}
	svc := New(nil, &agentsvc.Service{}, WithUserCredAuthConfig(&UserCredAuthConfig{
		Mode:            "bff",
		ClientConfigURL: "file:///tmp/oauth.client.json",
		Scopes:          []string{"openid", "profile"},
	}))
	svc.oauthAuthz = fakeAuthz

	gotCtx, err := svc.applyUserCred(context.Background(), "scheduler-user-credential")
	if err == nil {
		t.Fatal("applyUserCred() error = nil, want expired token rejection")
	}
	if iauth.TokensFromContext(gotCtx) != nil || iauth.Bearer(gotCtx) != "" {
		t.Fatal("expired direct-fallback token was injected")
	}
}

func TestService_AuthDirectFallbackRejectsExpiredOOBTokenWithoutRefreshToken(t *testing.T) {
	fakeAuthz := &fakeOAuthAuthorizer{
		tok: (&oauth2.Token{
			AccessToken: "expired-no-refresh-access",
			Expiry:      time.Now().Add(-time.Minute),
		}).WithExtra(map[string]interface{}{"scope": "openid profile"}),
	}
	tokens := &fakeSchedulerTokenProvider{}
	svc := New(nil, &agentsvc.Service{},
		WithUserCredAuthConfig(&UserCredAuthConfig{
			Mode:            "bff",
			ClientConfigURL: "file:///tmp/oauth.client.json",
			Scopes:          []string{"openid", "profile"},
		}),
		WithTokenProvider(tokens),
	)
	svc.oauthAuthz = fakeAuthz

	gotCtx, err := svc.applyUserCred(context.Background(), "scheduler-user-credential")
	if err == nil {
		t.Fatal("applyUserCred() error = nil, want expired token rejection")
	}
	if tokens.storeCalls != 0 || tokens.ensureCalls != 0 {
		t.Fatalf("expired no-refresh token reached provider: Store=%d Ensure=%d", tokens.storeCalls, tokens.ensureCalls)
	}
	if iauth.TokensFromContext(gotCtx) != nil || iauth.Bearer(gotCtx) != "" {
		t.Fatal("expired no-refresh token was injected")
	}
}

func TestService_AuthDirectFallbackAllowsLiveOOBTokenWithoutRefreshToken(t *testing.T) {
	fakeAuthz := &fakeOAuthAuthorizer{
		tok: (&oauth2.Token{
			AccessToken: "live-no-refresh-access",
			Expiry:      time.Now().Add(30 * time.Minute),
		}).WithExtra(map[string]interface{}{"scope": "openid profile"}),
	}
	tokens := &fakeSchedulerTokenProvider{}
	svc := New(nil, &agentsvc.Service{},
		WithUserCredAuthConfig(&UserCredAuthConfig{
			Mode:            "bff",
			ClientConfigURL: "file:///tmp/oauth.client.json",
			Scopes:          []string{"openid", "profile"},
		}),
		WithTokenProvider(tokens),
	)
	svc.oauthAuthz = fakeAuthz

	gotCtx, err := svc.applyUserCred(context.Background(), "scheduler-user-credential")
	if err != nil {
		t.Fatalf("applyUserCred() error = %v", err)
	}
	if tokens.storeCalls != 0 || tokens.ensureCalls != 0 {
		t.Fatalf("live no-refresh token unexpectedly reached provider: Store=%d Ensure=%d", tokens.storeCalls, tokens.ensureCalls)
	}
	if got := iauth.TokensFromContext(gotCtx); got == nil || got.AccessToken != "live-no-refresh-access" {
		t.Fatalf("live no-refresh token was not injected: %#v", got)
	}
	if got := iauth.Bearer(gotCtx); got != "live-no-refresh-access" {
		t.Fatalf("Bearer() = %q, want live no-refresh access token", got)
	}
}

func TestService_applyUserCred_PropagatesTokenStoreFailure(t *testing.T) {
	fakeAuthz := &fakeOAuthAuthorizer{
		tok: (&oauth2.Token{
			AccessToken:  "valid-scoped-access",
			RefreshToken: "valid-scoped-refresh",
			Expiry:       time.Now().Add(30 * time.Minute),
		}).WithExtra(map[string]interface{}{"scope": "openid profile"}),
	}
	tokens := &fakeSchedulerTokenProvider{storeErr: errors.New("store failure")}
	svc := New(nil, &agentsvc.Service{},
		WithUserCredAuthConfig(&UserCredAuthConfig{
			Mode:            "bff",
			ClientConfigURL: "file:///tmp/oauth.client.json",
			Scopes:          []string{"openid", "profile"},
		}),
		WithTokenProvider(tokens),
	)
	svc.oauthAuthz = fakeAuthz

	gotCtx, err := svc.applyUserCred(context.Background(), "scheduler-user-credential")
	if err == nil {
		t.Fatal("applyUserCred() error = nil, want Store failure")
	}
	if tokens.storeCalls != 1 || tokens.ensureCalls != 0 {
		t.Fatalf("token provider calls Store=%d Ensure=%d, want 1 and 0", tokens.storeCalls, tokens.ensureCalls)
	}
	if iauth.TokensFromContext(gotCtx) != nil || iauth.Bearer(gotCtx) != "" {
		t.Fatal("Store failure injected OAuth credentials")
	}
}

func TestService_applyUserCred_RejectsUnusableEnsuredAuth(t *testing.T) {
	tests := []struct {
		name         string
		configure    func(*fakeSchedulerTokenProvider)
		incomingAuth bool
	}{
		{
			name: "empty context",
			configure: func(provider *fakeSchedulerTokenProvider) {
				provider.ensureEmpty = true
			},
			incomingAuth: true,
		},
		{
			name: "ensure failure",
			configure: func(provider *fakeSchedulerTokenProvider) {
				provider.ensureErr = errors.New("ensure failure")
			},
		},
		{
			name: "expired token",
			configure: func(provider *fakeSchedulerTokenProvider) {
				provider.ensureToken = &scyauth.Token{Token: oauth2.Token{
					AccessToken: "expired-ensured-access",
					Expiry:      time.Now().Add(-time.Minute),
				}}
			},
		},
		{
			name: "under-scoped token",
			configure: func(provider *fakeSchedulerTokenProvider) {
				underScoped := (&oauth2.Token{
					AccessToken: "under-scoped-ensured-access",
					Expiry:      time.Now().Add(time.Hour),
				}).WithExtra(map[string]interface{}{"scope": "openid"})
				provider.ensureToken = &scyauth.Token{Token: *underScoped}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeAuthz := &fakeOAuthAuthorizer{
				tok: (&oauth2.Token{
					AccessToken:  "valid-scoped-access",
					RefreshToken: "valid-scoped-refresh",
					Expiry:       time.Now().Add(30 * time.Minute),
				}).WithExtra(map[string]interface{}{"scope": "openid profile"}),
			}
			tokens := &fakeSchedulerTokenProvider{}
			test.configure(tokens)
			svc := New(nil, &agentsvc.Service{},
				WithUserCredAuthConfig(&UserCredAuthConfig{
					Mode:            "bff",
					ClientConfigURL: "file:///tmp/oauth.client.json",
					Scopes:          []string{"openid", "profile"},
				}),
				WithTokenProvider(tokens),
			)
			svc.oauthAuthz = fakeAuthz
			ctx := context.Background()
			if test.incomingAuth {
				ctx = iauth.WithTokens(ctx, &scyauth.Token{Token: oauth2.Token{
					AccessToken: "stale-incoming-access",
					Expiry:      time.Now().Add(-time.Minute),
				}})
				ctx = iauth.WithBearer(ctx, "stale-incoming-access")
			}

			gotCtx, err := svc.applyUserCred(ctx, "scheduler-user-credential")
			if err == nil {
				t.Fatal("applyUserCred() error = nil, want unusable ensured auth rejection")
			}
			if tokens.storeCalls != 1 || tokens.ensureCalls != 1 {
				t.Fatalf("token provider calls Store=%d Ensure=%d, want 1 each", tokens.storeCalls, tokens.ensureCalls)
			}
			if iauth.TokensFromContext(gotCtx) != nil || iauth.Bearer(gotCtx) != "" {
				t.Fatal("unusable ensured auth or stale incoming auth reached downstream context")
			}
		})
	}
}

func TestService_executeRun_UserCredAuthFailureContinuesWithoutTokens(t *testing.T) {
	store, db := newTestStore(t)
	ensureRunWriteComponent(t, store)
	const scheduleID = "sched-user-cred-auth-failure"
	const subject = "scheduler-user"
	insertScheduleRow(t, db, scheduleID, "User credential auth failure")
	queryCalled := false
	svc := New(store, &agentsvc.Service{},
		WithUserCredAuthConfig(&UserCredAuthConfig{
			Mode:            "bff",
			ClientConfigURL: "file:///tmp/oauth.client.json",
			Scopes:          []string{"openid", "profile"},
		}),
	)
	svc.queryRunner = func(ctx context.Context, input *agentsvc.QueryInput, _ *agentsvc.QueryOutput) error {
		queryCalled = true
		if iauth.TokensFromContext(ctx) != nil || iauth.Bearer(ctx) != "" || iauth.IDToken(ctx) != "" {
			t.Fatal("query received credentials after user_cred auth failure")
		}
		if got := strings.TrimSpace(iauth.EffectiveUserID(ctx)); got != subject {
			t.Fatalf("query effective user = %q, want %q", got, subject)
		}
		if input == nil || input.UserId != subject {
			t.Fatalf("query input user = %#v, want %q", input, subject)
		}
		return nil
	}
	fakeAuthz := &fakeOAuthAuthorizer{err: errors.New("authorization failed")}
	svc.oauthAuthz = fakeAuthz
	credRef := "scheduler-user-credential"
	row := &schedulepkg.ScheduleView{
		Id:           scheduleID,
		Name:         "User credential auth failure",
		UserCredURL:  &credRef,
		AgentRef:     "steward",
		ScheduleType: "adhoc",
		Timezone:     "UTC",
		Enabled:      true,
	}

	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: subject})
	ctx = iauth.WithTokens(ctx, &scyauth.Token{
		Token: oauth2.Token{
			AccessToken: "stale-access",
			Expiry:      time.Now().Add(-time.Minute),
		},
		IDToken: "stale-id",
	})
	ctx = iauth.WithBearer(ctx, "stale-access")
	ctx = iauth.WithIDToken(ctx, "stale-id")
	svc.executeRun(ctx, row, "run-user-cred-auth-failure", time.Now().UTC())

	if !queryCalled {
		t.Fatal("query was not called after user credential auth failure")
	}
	if fakeAuthz.callCount != 1 {
		t.Fatalf("Authorize() calls = %d, want 1", fakeAuthz.callCount)
	}
	assertScheduleLastResult(t, db, scheduleID, "succeeded")
}

func seedRunningConversation(t *testing.T, client convcli.Client, conversationID, turnID string, startedAt time.Time) {
	t.Helper()

	conv := convcli.NewConversation()
	conv.SetId(conversationID)
	conv.SetCreatedAt(startedAt.Add(-1 * time.Minute))
	conv.SetStatus("running")
	if err := client.PatchConversations(context.Background(), conv); err != nil {
		t.Fatalf("PatchConversations() error: %v", err)
	}

	turn := convcli.NewTurn()
	turn.SetId(turnID)
	turn.SetConversationID(conversationID)
	turn.SetStatus("running")
	turn.SetCreatedAt(startedAt)
	if err := client.PatchTurn(context.Background(), turn); err != nil {
		t.Fatalf("PatchTurn() error: %v", err)
	}

	userMsg := convcli.NewMessage()
	userMsg.SetId(turnID)
	userMsg.SetConversationID(conversationID)
	userMsg.SetTurnID(turnID)
	userMsg.SetRole("user")
	userMsg.SetType("task")
	userMsg.SetContent("run task")
	userMsg.SetRawContent("run task")
	userMsg.SetCreatedAt(startedAt)
	if err := client.PatchMessage(context.Background(), userMsg); err != nil {
		t.Fatalf("PatchMessage(user) error: %v", err)
	}

	assistantMsg := convcli.NewMessage()
	assistantMsg.SetId("msg-" + turnID)
	assistantMsg.SetConversationID(conversationID)
	assistantMsg.SetTurnID(turnID)
	assistantMsg.SetRole("assistant")
	assistantMsg.SetType("text")
	assistantMsg.SetContent("working")
	assistantMsg.SetRawContent("working")
	assistantMsg.SetCreatedAt(startedAt.Add(2 * time.Second))
	if err := client.PatchMessage(context.Background(), assistantMsg); err != nil {
		t.Fatalf("PatchMessage(assistant) error: %v", err)
	}

	modelCall := convcli.NewModelCall()
	modelCall.SetMessageID("msg-" + turnID)
	modelCall.SetTurnID(turnID)
	modelCall.SetProvider("openai")
	modelCall.SetModel("gpt-5.2")
	modelCall.SetModelKind("chat")
	modelCall.SetStatus("running")
	modelCall.SetStartedAt(startedAt.Add(2 * time.Second))
	if err := client.PatchModelCall(context.Background(), modelCall); err != nil {
		t.Fatalf("PatchModelCall() error: %v", err)
	}

	toolMsg := convcli.NewMessage()
	toolMsg.SetId("tool-" + turnID)
	toolMsg.SetConversationID(conversationID)
	toolMsg.SetTurnID(turnID)
	toolMsg.SetRole("assistant")
	toolMsg.SetType("tool")
	toolMsg.SetContent("tool execution")
	toolMsg.SetCreatedAt(startedAt.Add(3 * time.Second))
	if err := client.PatchMessage(context.Background(), toolMsg); err != nil {
		t.Fatalf("PatchMessage(tool) error: %v", err)
	}

	toolCall := convcli.NewToolCall()
	toolCall.SetMessageID("tool-" + turnID)
	toolCall.SetTurnID(turnID)
	toolCall.SetOpID("op-" + turnID)
	toolCall.SetAttempt(1)
	toolCall.SetToolName("search")
	toolCall.SetToolKind("mcp")
	toolCall.SetStatus("running")
	if err := client.PatchToolCall(context.Background(), toolCall); err != nil {
		t.Fatalf("PatchToolCall() error: %v", err)
	}
}

func ensureRunWriteComponent(t *testing.T, store Store) {
	t.Helper()
	datlyStore, ok := store.(*datlyStore)
	if !ok || datlyStore == nil || datlyStore.dao == nil {
		t.Fatalf("expected datlyStore with dao, got %#v", store)
	}
	if _, err := agrunwrite.DefineComponent(context.Background(), datlyStore.dao); err != nil {
		t.Fatalf("DefineComponent(run write) error: %v", err)
	}
}

func assertScheduleLastResult(t *testing.T, db *sql.DB, scheduleID, wantStatus string) {
	t.Helper()
	var status sql.NullString
	var errorMessage sql.NullString
	if err := db.QueryRowContext(context.Background(), `
		SELECT last_status, last_error
		FROM schedule
		WHERE id = ?
	`, scheduleID).Scan(&status, &errorMessage); err != nil {
		t.Fatalf("query schedule result: %v", err)
	}
	if !status.Valid || status.String != wantStatus {
		t.Fatalf("schedule last_status = %q, want %q", status.String, wantStatus)
	}
	if errorMessage.Valid && strings.TrimSpace(errorMessage.String) != "" {
		t.Fatalf("schedule last_error = %q, want empty", errorMessage.String)
	}
}

func scheduleRunCount(t *testing.T, db *sql.DB, scheduleID string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), `
		SELECT COUNT(1)
		FROM run
		WHERE schedule_id = ?
	`, scheduleID).Scan(&count); err != nil {
		t.Fatalf("schedule run count error: %v", err)
	}
	return count
}

func scheduledForRunCount(t *testing.T, db *sql.DB, scheduleID string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), `
		SELECT COUNT(1)
		FROM run
		WHERE schedule_id = ?
		  AND scheduled_for IS NOT NULL
	`, scheduleID).Scan(&count); err != nil {
		t.Fatalf("scheduled_for run count error: %v", err)
	}
	return count
}

func scheduleLastRunSet(t *testing.T, db *sql.DB, scheduleID string) bool {
	t.Helper()
	var lastRunAt sql.NullTime
	if err := db.QueryRowContext(context.Background(), `SELECT last_run_at FROM schedule WHERE id = ?`, scheduleID).Scan(&lastRunAt); err != nil {
		return false
	}
	return lastRunAt.Valid
}

func assertScheduleNextRunAt(t *testing.T, db *sql.DB, scheduleID string, want time.Time) {
	t.Helper()
	var got sql.NullTime
	if err := db.QueryRowContext(context.Background(), `SELECT next_run_at FROM schedule WHERE id = ?`, scheduleID).Scan(&got); err != nil {
		t.Fatalf("schedule next_run_at query error: %v", err)
	}
	if !got.Valid {
		t.Fatalf("next_run_at is NULL, want %s", want)
	}
	require.WithinDuration(t, want.UTC(), got.Time.UTC(), time.Second)
}

func TestService_ScheduleGoalWakeup_HidesInternalWakeupFromPublicList(t *testing.T) {
	store, _ := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	scheduled, err := svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-goal",
		GoalID:         "goal-conv-goal",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         time.Now().UTC().Add(2 * time.Minute),
		Preview:        "Continue goal later",
		Payload:        "Continue working toward the active goal.",
	})
	if err != nil {
		t.Fatalf("ScheduleGoalWakeup() error: %v", err)
	}
	if !scheduled {
		t.Fatalf("expected wakeup to schedule")
	}

	publicRows, err := svc.List(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "devuser"}))
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(publicRows) != 0 {
		t.Fatalf("expected internal wakeup to stay hidden from public list, got %#v", publicRows)
	}

	internalRows, err := store.ListForRunDue(context.Background())
	if err != nil {
		t.Fatalf("ListForRunDue() error: %v", err)
	}
	if len(internalRows) != 1 || !internalRows[0].Internal {
		t.Fatalf("expected one internal wakeup row, got %#v", internalRows)
	}
	if got := strings.TrimSpace(valueOrEmpty(internalRows[0].ConversationId)); got != "conv-goal" {
		t.Fatalf("ConversationId = %q, want %q", got, "conv-goal")
	}
	if got := strings.TrimSpace(valueOrEmpty(internalRows[0].GoalId)); got != "goal-conv-goal" {
		t.Fatalf("GoalId = %q, want %q", got, "goal-conv-goal")
	}
}

func TestService_GetInternalWakeupReturnsNil(t *testing.T) {
	store, _ := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	scheduled, err := svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-goal",
		GoalID:         "goal-conv-goal",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         time.Now().UTC().Add(2 * time.Minute),
		Preview:        "Continue goal later",
		Payload:        "Continue working toward the active goal.",
	})
	if err != nil {
		t.Fatalf("ScheduleGoalWakeup() error: %v", err)
	}
	if !scheduled {
		t.Fatalf("expected wakeup to schedule")
	}

	got, err := svc.Get(context.Background(), "goal-wakeup-goal-conv-goal")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected internal wakeup to stay hidden, got %#v", got)
	}
}

func TestService_RunNowAndDeleteRejectInternalWakeup(t *testing.T) {
	store, db := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	scheduled, err := svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-goal",
		GoalID:         "goal-conv-goal",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         time.Now().UTC().Add(2 * time.Minute),
		Preview:        "Continue goal later",
		Payload:        "Continue working toward the active goal.",
	})
	if err != nil {
		t.Fatalf("ScheduleGoalWakeup() error: %v", err)
	}
	if !scheduled {
		t.Fatalf("expected wakeup to schedule")
	}

	if err := svc.RunNow(context.Background(), "goal-wakeup-goal-conv-goal"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("RunNow() error = %v, want not found", err)
	}
	if err := svc.Delete(context.Background(), "goal-wakeup-goal-conv-goal"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Delete() error = %v, want not found", err)
	}
	assertScheduleCount(t, db, "goal-wakeup-goal-conv-goal", 1)
}

func TestService_RunNowOnDemandReturnsBeforeQueryCompletes(t *testing.T) {
	store, db := newTestStore(t)
	ensureRunWriteComponent(t, store)
	insertScheduleRow(t, db, "sched-run-now-async", "Run Now Async")
	nextRunAt := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second)
	if _, err := db.ExecContext(context.Background(), `UPDATE schedule SET next_run_at = ? WHERE id = ?`, nextRunAt, "sched-run-now-async"); err != nil {
		t.Fatalf("set next_run_at error: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	svc := New(store, &agentsvc.Service{})
	svc.queryRunner = func(_ context.Context, _ *agentsvc.QueryInput, _ *agentsvc.QueryOutput) error {
		close(started)
		<-release
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- svc.RunNow(context.Background(), "sched-run-now-async")
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunNow() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunNow() did not return while query was blocked")
	}
	require.Eventually(t, func() bool {
		return scheduleRunCount(t, db, "sched-run-now-async") == 1 &&
			scheduledForRunCount(t, db, "sched-run-now-async") == 1
	}, 3*time.Second, 20*time.Millisecond)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("expected Run Now query to start")
	}
	close(release)
	require.Eventually(t, func() bool {
		return scheduleLastRunSet(t, db, "sched-run-now-async")
	}, 3*time.Second, 20*time.Millisecond)
	assertScheduleNextRunAt(t, db, "sched-run-now-async", nextRunAt)
}

func TestService_RunNowRateLimitsManualRunsPerMinute(t *testing.T) {
	store, db := newTestStore(t)
	ensureRunWriteComponent(t, store)
	insertScheduleRow(t, db, "sched-run-now-limit", "Run Now Limit")

	release := make(chan struct{})
	svc := New(store, &agentsvc.Service{})
	svc.queryRunner = func(context.Context, *agentsvc.QueryInput, *agentsvc.QueryOutput) error {
		<-release
		return nil
	}
	if err := svc.RunNow(context.Background(), "sched-run-now-limit"); err != nil {
		t.Fatalf("first RunNow() error: %v", err)
	}
	require.Eventually(t, func() bool {
		return scheduleRunCount(t, db, "sched-run-now-limit") == 1 &&
			scheduledForRunCount(t, db, "sched-run-now-limit") == 1
	}, 3*time.Second, 20*time.Millisecond)

	err := svc.RunNow(context.Background(), "sched-run-now-limit")
	if !errors.Is(err, ErrRunNowRateLimited) {
		t.Fatalf("second RunNow() error = %v, want ErrRunNowRateLimited", err)
	}
	close(release)
	require.Eventually(t, func() bool {
		return scheduleLastRunSet(t, db, "sched-run-now-limit")
	}, 3*time.Second, 20*time.Millisecond)
}

func TestService_RunNowAllowsSecondManualRunAfterRateLimitWindow(t *testing.T) {
	store, db := newTestStore(t)
	ensureRunWriteComponent(t, store)
	insertScheduleRow(t, db, "sched-run-now-repeat", "Run Now Repeat")

	release := make(chan struct{})
	svc := New(store, &agentsvc.Service{})
	svc.queryRunner = func(context.Context, *agentsvc.QueryInput, *agentsvc.QueryOutput) error {
		<-release
		return nil
	}

	if err := svc.RunNow(context.Background(), "sched-run-now-repeat"); err != nil {
		t.Fatalf("first RunNow() error: %v", err)
	}
	require.Eventually(t, func() bool {
		return scheduleRunCount(t, db, "sched-run-now-repeat") == 1 &&
			scheduledForRunCount(t, db, "sched-run-now-repeat") == 1
	}, 3*time.Second, 20*time.Millisecond)
	close(release)
	require.Eventually(t, func() bool {
		return scheduleLastRunSet(t, db, "sched-run-now-repeat")
	}, 3*time.Second, 20*time.Millisecond)

	oldCreatedAt := time.Now().UTC().Add(-2 * time.Minute)
	if _, err := db.ExecContext(context.Background(), `UPDATE run SET created_at = ?, scheduled_for = ? WHERE schedule_id = ?`, oldCreatedAt, oldCreatedAt, "sched-run-now-repeat"); err != nil {
		t.Fatalf("age first run error: %v", err)
	}
	svc.reserveRunNow("sched-run-now-repeat", oldCreatedAt)

	if err := svc.RunNow(context.Background(), "sched-run-now-repeat"); err != nil {
		t.Fatalf("second RunNow() after window error: %v", err)
	}
	require.Eventually(t, func() bool {
		return scheduleRunCount(t, db, "sched-run-now-repeat") == 2 &&
			scheduledForRunCount(t, db, "sched-run-now-repeat") == 2
	}, 3*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		return scheduleLastRunSet(t, db, "sched-run-now-repeat")
	}, 3*time.Second, 20*time.Millisecond)
}

func TestService_UpsertRejectsInternalScheduleFields(t *testing.T) {
	store, _ := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	err := svc.Upsert(context.Background(), &Schedule{
		ID:             "sched-public",
		Name:           "Public",
		Visibility:     "private",
		Internal:       true,
		ConversationID: stringPtr("conv-goal"),
		GoalID:         stringPtr("goal-conv-goal"),
		AgentRef:       "coder",
		Enabled:        true,
		ScheduleType:   "adhoc",
		Timezone:       "UTC",
	})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Upsert() error = %v, want reserved-field rejection", err)
	}
}

func TestService_CancelGoalWakeups_DisablesPendingWakeup(t *testing.T) {
	store, _ := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	scheduled, err := svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-goal",
		GoalID:         "goal-conv-goal",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         time.Now().UTC().Add(2 * time.Minute),
		Preview:        "Continue goal later",
		Payload:        "Continue working toward the active goal.",
	})
	if err != nil {
		t.Fatalf("ScheduleGoalWakeup() error: %v", err)
	}
	if !scheduled {
		t.Fatalf("expected wakeup to schedule")
	}
	if err := svc.CancelGoalWakeups(context.Background(), "conv-goal", "goal-conv-goal"); err != nil {
		t.Fatalf("CancelGoalWakeups() error: %v", err)
	}

	internalRows, err := store.ListForRunDue(context.Background())
	if err != nil {
		t.Fatalf("ListForRunDue() error: %v", err)
	}
	if len(internalRows) != 1 {
		t.Fatalf("expected one internal row, got %#v", internalRows)
	}
	if internalRows[0].Enabled {
		t.Fatalf("expected wakeup to be disabled after cancel")
	}
	if internalRows[0].NextRunAt != nil {
		t.Fatalf("expected NextRunAt to be cleared after cancel, got %v", internalRows[0].NextRunAt)
	}
}

func TestService_ScheduleGoalWakeup_RespectsConversationWakeupBudget(t *testing.T) {
	prevRoot := workspace.Root()
	tempRoot := t.TempDir()
	workspace.SetRoot(tempRoot)
	defer workspace.SetRoot(prevRoot)
	require.NoError(t, os.WriteFile(filepath.Join(tempRoot, "config.yaml"), []byte(`
features:
  wakeups:
    enabled: true
    maxConversationWakeups: 1
`), 0o644))

	store, _ := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	scheduled, err := svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-goal",
		GoalID:         "goal-1",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         time.Now().UTC().Add(10 * time.Minute),
		Preview:        "first",
		Payload:        "first",
	})
	if err != nil {
		t.Fatalf("first ScheduleGoalWakeup() error: %v", err)
	}
	if !scheduled {
		t.Fatalf("expected first wakeup to schedule")
	}

	scheduled, err = svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-goal",
		GoalID:         "goal-2",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         time.Now().UTC().Add(20 * time.Minute),
		Preview:        "second",
		Payload:        "second",
	})
	if err != nil {
		t.Fatalf("second ScheduleGoalWakeup() error: %v", err)
	}
	if scheduled {
		t.Fatalf("expected second wakeup to be rejected by conversation budget")
	}
}

func TestService_ScheduleGoalWakeup_RespectsGlobalWakeupBudgetWindow(t *testing.T) {
	prevRoot := workspace.Root()
	tempRoot := t.TempDir()
	workspace.SetRoot(tempRoot)
	defer workspace.SetRoot(prevRoot)
	require.NoError(t, os.WriteFile(filepath.Join(tempRoot, "config.yaml"), []byte(`
features:
  wakeups:
    enabled: true
    maxGlobalWakeupsPerHour: 1
`), 0o644))

	store, _ := newTestStore(t)
	svc := New(store, &agentsvc.Service{})

	scheduled, err := svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-a",
		GoalID:         "goal-a",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         time.Now().UTC().Add(10 * time.Minute),
		Preview:        "first",
		Payload:        "first",
	})
	if err != nil {
		t.Fatalf("first ScheduleGoalWakeup() error: %v", err)
	}
	if !scheduled {
		t.Fatalf("expected first wakeup to schedule")
	}

	scheduled, err = svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-b",
		GoalID:         "goal-b",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         time.Now().UTC().Add(20 * time.Minute),
		Preview:        "second",
		Payload:        "second",
	})
	if err != nil {
		t.Fatalf("second ScheduleGoalWakeup() error: %v", err)
	}
	if scheduled {
		t.Fatalf("expected second wakeup to be rejected by global budget")
	}
}

func TestScheduleQueryInput_InternalGoalWakeupTargetsSameConversation(t *testing.T) {
	row := &schedulepkg.ScheduleView{
		Id:             "goal-wakeup-goal-1",
		AgentRef:       "coder",
		Internal:       true,
		ConversationId: stringPtr("conv-goal"),
		GoalId:         stringPtr("goal-conv-goal"),
		Description:    stringPtr("Continue goal later"),
		TaskPrompt:     stringPtr("Continue working toward the active goal."),
	}

	input := scheduleQueryInput(row, "run-1", "devuser")

	if input.ConversationID != "conv-goal" {
		t.Fatalf("ConversationID = %q, want %q", input.ConversationID, "conv-goal")
	}
	if input.ScheduleId != "goal-wakeup-goal-1" {
		t.Fatalf("ScheduleId = %q, want %q", input.ScheduleId, "goal-wakeup-goal-1")
	}
	if input.DisplayQuery != "Continue goal later" {
		t.Fatalf("DisplayQuery = %q, want %q", input.DisplayQuery, "Continue goal later")
	}
	if input.Query != "Continue working toward the active goal." {
		t.Fatalf("Query = %q, want %q", input.Query, "Continue working toward the active goal.")
	}
	raw, ok := input.Context[agentsvc.AutonomousGoalWakeupContextKey()]
	if !ok {
		t.Fatalf("expected autonomous goal wakeup context")
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("expected goal wakeup context map, got %#v", raw)
	}
	if payload["goalId"] != "goal-conv-goal" {
		t.Fatalf("goalId = %#v, want %q", payload["goalId"], "goal-conv-goal")
	}
}

func TestService_RunDue_ExecutesScheduledGoalWakeupInSameConversation(t *testing.T) {
	store, db := newTestStore(t)
	ensureRunWriteComponent(t, store)

	var captured *agentsvc.QueryInput
	svc := New(store, &agentsvc.Service{})
	svc.execSem = make(chan struct{}, 1)
	svc.queryRunner = func(_ context.Context, input *agentsvc.QueryInput, output *agentsvc.QueryOutput) error {
		cp := *input
		if input.Context != nil {
			cp.Context = map[string]any{}
			for k, v := range input.Context {
				cp.Context[k] = v
			}
		}
		captured = &cp
		output.ConversationID = input.ConversationID
		output.Content = "wakeup resumed"
		return nil
	}

	initialWakeAt := time.Now().UTC().Add(2 * time.Minute)
	scheduled, err := svc.ScheduleGoalWakeup(context.Background(), agentsvc.GoalWakeupRequest{
		ConversationID: "conv-goal",
		GoalID:         "goal-conv-goal",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         initialWakeAt,
		Preview:        "Continue goal later",
		Payload:        "Continue working toward the active goal.",
	})
	if err != nil {
		t.Fatalf("ScheduleGoalWakeup() error: %v", err)
	}
	if !scheduled {
		t.Fatalf("expected wakeup to schedule")
	}

	pending := svc.CurrentGoalWakeup(context.Background(), "conv-goal", "goal-conv-goal")
	if pending == nil {
		t.Fatalf("expected pending wakeup state before RunDue")
	}
	if pending.Mode != "wakeup" {
		t.Fatalf("pending.Mode = %q, want %q", pending.Mode, "wakeup")
	}
	if pending.Preview == nil || strings.TrimSpace(*pending.Preview) != "Continue goal later" {
		t.Fatalf("pending.Preview = %#v, want %q", pending.Preview, "Continue goal later")
	}
	if pending.WakeAt == nil || pending.WakeAt.IsZero() {
		t.Fatalf("expected pending wakeup to include wakeAt")
	}
	if !pending.WakeAt.UTC().Equal(initialWakeAt.UTC()) {
		t.Fatalf("pending.WakeAt = %v, want %v", pending.WakeAt.UTC(), initialWakeAt.UTC())
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE schedule SET next_run_at = ? WHERE id = ?`, time.Now().UTC().Add(-1*time.Minute), "goal-wakeup-goal-conv-goal"); err != nil {
		t.Fatalf("update schedule next_run_at error: %v", err)
	}

	started, err := svc.RunDue(context.Background())
	if err != nil {
		t.Fatalf("RunDue() error: %v", err)
	}
	if started != 1 {
		t.Fatalf("expected one scheduled wakeup run to start, got %d", started)
	}

	require.Eventually(t, func() bool {
		return captured != nil
	}, 3*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		return len(svc.execSem) == 0
	}, 3*time.Second, 20*time.Millisecond)

	if captured.ConversationID != "conv-goal" {
		t.Fatalf("ConversationID = %q, want %q", captured.ConversationID, "conv-goal")
	}
	if captured.ScheduleId != "goal-wakeup-goal-conv-goal" {
		t.Fatalf("ScheduleId = %q, want %q", captured.ScheduleId, "goal-wakeup-goal-conv-goal")
	}
	if captured.DisplayQuery != "Continue goal later" {
		t.Fatalf("DisplayQuery = %q, want %q", captured.DisplayQuery, "Continue goal later")
	}
	raw, ok := captured.Context[agentsvc.AutonomousGoalWakeupContextKey()]
	if !ok {
		t.Fatalf("expected autonomous wakeup context")
	}
	payload, ok := raw.(map[string]any)
	if !ok || payload["goalId"] != "goal-conv-goal" {
		t.Fatalf("unexpected wakeup payload %#v", raw)
	}

	var runCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(1) FROM run WHERE schedule_id = ?`, "goal-wakeup-goal-conv-goal").Scan(&runCount); err != nil {
		t.Fatalf("query run count error: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("expected one run row for scheduled wakeup, got %d", runCount)
	}

	require.Eventually(t, func() bool {
		return svc.CurrentGoalWakeup(context.Background(), "conv-goal", "goal-conv-goal") == nil
	}, 3*time.Second, 20*time.Millisecond)
}

func TestService_ExecuteRun_InternalGoalWakeupResumesSameConversation(t *testing.T) {
	store, db := newTestStore(t)
	ensureRunWriteComponent(t, store)

	var captured *agentsvc.QueryInput
	svc := New(store, &agentsvc.Service{})
	svc.queryRunner = func(_ context.Context, input *agentsvc.QueryInput, output *agentsvc.QueryOutput) error {
		cp := *input
		if input.Context != nil {
			cp.Context = map[string]any{}
			for k, v := range input.Context {
				cp.Context[k] = v
			}
		}
		captured = &cp
		output.ConversationID = input.ConversationID
		output.Content = "wakeup resumed"
		return nil
	}

	row := &schedulepkg.ScheduleView{
		Id:             "goal-wakeup-goal-1",
		Name:           "autonomous::goal-wakeup::goal-1",
		Visibility:     "private",
		Internal:       true,
		ConversationId: stringPtr("conv-goal"),
		GoalId:         stringPtr("goal-conv-goal"),
		AgentRef:       "coder",
		Description:    stringPtr("Continue goal later"),
		TaskPrompt:     stringPtr("Continue working toward the active goal."),
		ScheduleType:   "adhoc",
		Timezone:       "UTC",
		Enabled:        true,
	}

	svc.executeRun(context.Background(), row, "run-1", time.Now().UTC())

	if captured == nil {
		t.Fatalf("expected query runner to capture input")
	}
	if captured.ConversationID != "conv-goal" {
		t.Fatalf("ConversationID = %q, want %q", captured.ConversationID, "conv-goal")
	}
	if captured.ScheduleId != "goal-wakeup-goal-1" {
		t.Fatalf("ScheduleId = %q, want %q", captured.ScheduleId, "goal-wakeup-goal-1")
	}
	if captured.DisplayQuery != "Continue goal later" {
		t.Fatalf("DisplayQuery = %q, want %q", captured.DisplayQuery, "Continue goal later")
	}
	if captured.UserId != "system" {
		t.Fatalf("UserId = %q, want %q", captured.UserId, "system")
	}
	raw, ok := captured.Context[agentsvc.AutonomousGoalWakeupContextKey()]
	if !ok {
		t.Fatalf("expected autonomous wakeup context")
	}
	payload, ok := raw.(map[string]any)
	if !ok || payload["goalId"] != "goal-conv-goal" {
		t.Fatalf("unexpected wakeup payload %#v", raw)
	}

	var conversationID sql.NullString
	if err := db.QueryRowContext(context.Background(), `SELECT conversation_id FROM run WHERE id = ?`, "run-1").Scan(&conversationID); err != nil {
		t.Fatalf("query run conversation_id error: %v", err)
	}
	if !conversationID.Valid || strings.TrimSpace(conversationID.String) != "conv-goal" {
		t.Fatalf("run conversation_id = %#v, want conv-goal", conversationID)
	}
}
