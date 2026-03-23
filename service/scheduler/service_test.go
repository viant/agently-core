package scheduler

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	mem "github.com/viant/agently-core/app/store/data/memory"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	agentsvc "github.com/viant/agently-core/service/agent"
)

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
	svc := New(nil, &agentsvc.Service{}, WithConversationClient(conv))
	startedAt := time.Now().UTC().Add(-1 * time.Minute)

	seedRunningConversation(t, conv, "conv-direct", "turn-direct", startedAt)

	if err := svc.cancelConversationAndMark(context.Background(), "conv-direct", "canceled"); err != nil {
		t.Fatalf("cancelConversationAndMark() error: %v", err)
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
