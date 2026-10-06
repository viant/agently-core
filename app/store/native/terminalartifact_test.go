package native_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	artifact "github.com/viant/agently-core/internal/store/terminalartifact"
)

func terminalArtifactFixture(t *testing.T) (*artifact.Store, *sql.DB, time.Time) {
	t.Helper()
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	_, file, _, _ := runtime.Caller(0)
	root := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "..", "..", ".."), WorkspaceRoot: root})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite", filepath.Join(root, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Now().UTC()
	_, err = db.Exec("INSERT INTO conversation(id,created_at) VALUES('cleanup-conversation',?)", now.Add(-time.Hour))
	require.NoError(t, err)
	return &artifact.Store{Invoker: server}, db, now
}
func seedTerminalArtifact(t *testing.T, db *sql.DB, now time.Time, kind artifact.Kind, id string, linkage artifact.Linkage, status string) {
	t.Helper()
	turnID := id + "-turn"
	var artifactTurn, artifactRun, messageTurn, turnRun any
	switch linkage {
	case artifact.DirectTurn:
		artifactTurn = turnID
		messageTurn = turnID
	case artifact.MessageTurn:
		messageTurn = turnID
	case artifact.Run:
		artifactRun = id + "-run"
		turnRun = artifactRun
	case artifact.LegacyRun:
		artifactRun = turnID
	}
	_, err := db.Exec("INSERT INTO turn(id,conversation_id,status,run_id,error_message,created_at) VALUES(?,'cleanup-conversation',?,?,?,?)", turnID, status, turnRun, "failure reason", now.Add(-time.Minute))
	require.NoError(t, err)
	role := "assistant"
	if kind == artifact.Message {
		role = "tool"
		messageTurn = turnID
	}
	_, err = db.Exec("INSERT INTO message(id,conversation_id,turn_id,role,type,status,content,created_at) VALUES(?,'cleanup-conversation',?,?,'text','running','must remain unchanged',?)", id, messageTurn, role, now)
	require.NoError(t, err)
	if kind == artifact.ModelCall {
		_, err = db.Exec("INSERT INTO model_call(message_id,turn_id,run_id,provider,model,model_kind,status) VALUES(?,?,?,'openai','fixture','chat','thinking')", id, artifactTurn, artifactRun)
		require.NoError(t, err)
	}
	if kind == artifact.ToolCall {
		_, err = db.Exec("INSERT INTO tool_call(message_id,turn_id,run_id,op_id,tool_name,tool_kind,status) VALUES(?,?,?,?,'fixture-tool','general','queued')", id, artifactTurn, artifactRun, id+"-op")
		require.NoError(t, err)
	}
}
func TestWorkspaceRuntimeTerminalArtifactMatrix(t *testing.T) {
	store, db, now := terminalArtifactFixture(t)
	ctx := context.Background()
	i := 0
	for _, kind := range []artifact.Kind{artifact.ModelCall, artifact.ToolCall} {
		for _, linkage := range []artifact.Linkage{artifact.DirectTurn, artifact.MessageTurn, artifact.Run, artifact.LegacyRun} {
			id := string(kind) + "-" + string(linkage)
			statuses := []string{"failed", "succeeded", "canceled"}
			seedTerminalArtifact(t, db, now, kind, id, linkage, statuses[i%3])
			i++
		}
	}
	seedTerminalArtifact(t, db, now, artifact.Message, "message-only", artifact.DirectTurn, "failed")
	candidates, err := store.Snapshot(ctx, now.Add(-time.Hour), 5000)
	require.NoError(t, err)
	require.Len(t, candidates, 9)
	for _, candidate := range candidates {
		require.Equal(t, "cleanup-conversation", candidate.ConversationID)
		require.NotEmpty(t, candidate.ExpectedLink+candidate.ExpectedRun)
		require.NotEmpty(t, candidate.Reason)
	}
	completedAt := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	dispositions, err := store.Cleanup(ctx, candidates, completedAt)
	require.NoError(t, err)
	for _, disposition := range dispositions {
		require.Equal(t, artifact.Repaired, disposition)
	}
	for _, candidate := range candidates {
		if candidate.Kind == artifact.Message {
			var status, content string
			var updated time.Time
			var sequence sql.NullInt64
			require.NoError(t, db.QueryRow("SELECT status,content,updated_at,sequence FROM message WHERE id=?", candidate.ID).Scan(&status, &content, &updated, &sequence))
			require.Equal(t, "failed", status)
			require.Equal(t, "must remain unchanged", content)
			require.True(t, completedAt.Equal(updated))
			require.False(t, sequence.Valid, "cleanup must not allocate message sequence")
			continue
		}
		var status, reason string
		var completed time.Time
		require.NoError(t, db.QueryRow("SELECT status,error_message,completed_at FROM "+string(candidate.Kind)+" WHERE message_id=?", candidate.ID).Scan(&status, &reason, &completed))
		require.Equal(t, "failed", status)
		require.True(t, completedAt.Equal(completed))
		wantReason := candidate.Reason
		if candidate.Kind == artifact.ToolCall && candidate.Linkage == artifact.MessageTurn {
			wantReason = "tool message terminalized after turn ended"
		}
		require.Equal(t, wantReason, reason)
	}
	replay, err := store.Cleanup(ctx, candidates, completedAt)
	require.NoError(t, err)
	for _, disposition := range replay {
		require.Equal(t, artifact.AlreadyResolved, disposition)
	}
}
func TestWorkspaceRuntimeTerminalArtifactCaptureAndRollback(t *testing.T) {
	store, db, now := terminalArtifactFixture(t)
	ctx := context.Background()
	seedTerminalArtifact(t, db, now, artifact.ModelCall, "direct", artifact.DirectTurn, "failed")
	candidates, err := store.Snapshot(ctx, now.Add(-time.Hour), 5000)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	candidate := candidates[0]
	_, err = db.Exec("UPDATE turn SET status='succeeded' WHERE id=?", candidate.TurnID)
	require.NoError(t, err)
	got, err := store.Cleanup(ctx, candidates, now)
	require.NoError(t, err)
	require.Equal(t, []artifact.Disposition{artifact.NoLongerEligible}, got)
	_, err = db.Exec("UPDATE model_call SET status='completed' WHERE message_id=?", candidate.ID)
	require.NoError(t, err)
	got, err = store.Cleanup(ctx, candidates, now)
	require.NoError(t, err)
	require.Equal(t, []artifact.Disposition{artifact.AlreadyResolved}, got)
	_, err = db.Exec("UPDATE model_call SET status='running' WHERE message_id=?", candidate.ID)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE turn SET status='failed' WHERE id=?", candidate.TurnID)
	require.NoError(t, err)
	invalid := candidate
	invalid.ID = ""
	_, err = store.Cleanup(ctx, []artifact.Candidate{candidate, invalid}, now)
	require.ErrorContains(t, err, "identity is incomplete")
	var status string
	require.NoError(t, db.QueryRow("SELECT status FROM model_call WHERE message_id=?", candidate.ID).Scan(&status))
	require.Equal(t, "running", status, "later invalid candidate must roll back earlier repair")
	got, err = store.Cleanup(ctx, []artifact.Candidate{candidate, candidate}, now)
	require.NoError(t, err)
	require.Equal(t, []artifact.Disposition{artifact.Repaired, artifact.AlreadyResolved}, got, "duplicate classification must observe same-unit write")
}
func TestWorkspaceRuntimeTerminalArtifactLateWriterRollback(t *testing.T) {
	store, db, now := terminalArtifactFixture(t)
	ctx := context.Background()
	for _, id := range []string{"first", "second"} {
		seedTerminalArtifact(t, db, now, artifact.ModelCall, id, artifact.DirectTurn, "failed")
	}
	candidates, err := store.Snapshot(ctx, now.Add(-time.Hour), 5000)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	_, err = db.Exec("CREATE TRIGGER terminal_cleanup_failure BEFORE UPDATE OF status ON model_call WHEN NEW.message_id='second' BEGIN SELECT RAISE(ABORT,'late repair failure'); END")
	require.NoError(t, err)
	_, err = store.Cleanup(ctx, candidates, now)
	require.ErrorContains(t, err, "late repair failure")
	for _, id := range []string{"first", "second"} {
		var status string
		require.NoError(t, db.QueryRow("SELECT status FROM model_call WHERE message_id=?", id).Scan(&status))
		require.Equal(t, "thinking", status)
	}
}

func TestWorkspaceRuntimeTerminalArtifactIgnoredWriterIsUnresolved(t *testing.T) {
	store, db, now := terminalArtifactFixture(t)
	ctx := context.Background()
	seedTerminalArtifact(t, db, now, artifact.ModelCall, "ignored", artifact.DirectTurn, "failed")
	candidates, err := store.Snapshot(ctx, now.Add(-time.Hour), 5000)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	_, err = db.Exec("CREATE TRIGGER terminal_cleanup_ignore BEFORE UPDATE OF status ON model_call BEGIN SELECT RAISE(IGNORE); END")
	require.NoError(t, err)
	got, err := store.Cleanup(ctx, candidates, now)
	require.NoError(t, err)
	require.Equal(t, []artifact.Disposition{artifact.Unresolved}, got)
	var status string
	require.NoError(t, db.QueryRow("SELECT status FROM model_call WHERE message_id='ignored'").Scan(&status))
	require.Equal(t, "thinking", status)
}

func TestWorkspaceRuntimeTerminalArtifactSQLTrimClassification(t *testing.T) {
	for _, tc := range []struct {
		name, statement string
		want            artifact.Disposition
	}{
		{name: "artifact tabs remain SQL nonterminal", statement: "UPDATE model_call SET status=char(9)||'completed'||char(9) WHERE message_id='whitespace'", want: artifact.Repaired},
		{name: "turn tabs fail SQL guard but remain Go eligible", statement: "UPDATE turn SET status=char(9)||'failed'||char(9) WHERE id='whitespace-turn'", want: artifact.Unresolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, db, now := terminalArtifactFixture(t)
			ctx := context.Background()
			seedTerminalArtifact(t, db, now, artifact.ModelCall, "whitespace", artifact.DirectTurn, "failed")
			candidates, err := store.Snapshot(ctx, now.Add(-time.Hour), 5000)
			require.NoError(t, err)
			require.Len(t, candidates, 1)
			_, err = db.Exec(tc.statement)
			require.NoError(t, err)
			got, err := store.Cleanup(ctx, candidates, now)
			require.NoError(t, err)
			require.Equal(t, []artifact.Disposition{tc.want}, got)
		})
	}
}

func TestWorkspaceRuntimeTerminalArtifactSnapshotPrecedenceAndLimit(t *testing.T) {
	store, db, now := terminalArtifactFixture(t)
	ctx := context.Background()
	seedTerminalArtifact(t, db, now, artifact.ModelCall, "precedence", artifact.DirectTurn, "failed")
	for _, row := range []struct {
		id, status string
		ago        time.Duration
		run        any
	}{
		{id: "message-new", status: "succeeded", ago: 40 * time.Second},
		{id: "run-old", status: "failed", ago: 30 * time.Second, run: "shared-run"},
		{id: "run-new", status: "canceled", ago: 20 * time.Second, run: "shared-run"},
	} {
		_, err := db.Exec("INSERT INTO turn(id,conversation_id,status,run_id,created_at) VALUES(?,'cleanup-conversation',?,?,?)", row.id, row.status, row.run, now.Add(-row.ago))
		require.NoError(t, err)
	}
	_, err := db.Exec("UPDATE message SET turn_id='message-new' WHERE id='precedence'; UPDATE model_call SET run_id='shared-run' WHERE message_id='precedence'")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO message(id,conversation_id,role,type,status,created_at) VALUES('newest','cleanup-conversation','assistant','text','running',?)", now)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO model_call(message_id,run_id,provider,model,model_kind,status) VALUES('newest','shared-run','openai','fixture','chat','thinking')")
	require.NoError(t, err)
	candidates, err := store.Snapshot(ctx, now.Add(-time.Hour), 5000)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	byID := map[string]artifact.Candidate{}
	for _, candidate := range candidates {
		byID[candidate.ID] = candidate
	}
	require.Equal(t, artifact.DirectTurn, byID["precedence"].Linkage)
	require.Equal(t, "precedence-turn", byID["precedence"].TurnID)
	require.Equal(t, artifact.Run, byID["newest"].Linkage)
	require.Equal(t, "run-new", byID["newest"].TurnID)
	newestWindow, err := store.Snapshot(ctx, now.Add(-time.Hour), 1)
	require.NoError(t, err)
	require.Len(t, newestWindow, 2, "turn limit does not cap artifact count")
	for _, candidate := range newestWindow {
		require.Equal(t, artifact.Run, candidate.Linkage)
		require.Equal(t, "run-new", candidate.TurnID)
	}
	excluded, err := store.Snapshot(ctx, now.Add(-15*time.Second), 5000)
	require.NoError(t, err)
	require.Empty(t, excluded)
	_, err = store.Snapshot(ctx, now, 0)
	require.ErrorContains(t, err, "limit must be positive")
}
