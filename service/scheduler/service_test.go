package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	cancels "github.com/viant/agently-core/app/store/conversation/cancel"
	mem "github.com/viant/agently-core/app/store/data/memory"
	iauth "github.com/viant/agently-core/internal/auth"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/scy"
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

	svc := New(nil, &agentsvc.Service{}, WithAuthConfig(&iauth.Config{
		OAuth: &iauth.OAuth{
			Mode: "bff",
			Client: &iauth.OAuthClient{
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
