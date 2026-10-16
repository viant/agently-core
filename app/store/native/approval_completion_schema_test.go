package native_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/toolapprovalqueue/read"
	write "github.com/viant/agently-core/internal/datly/toolapprovalqueue/write"
	convstore "github.com/viant/agently-core/internal/store/conversation"
)

func TestWorkspaceRuntimeApprovalCompletionSchema(t *testing.T) {
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "completion-owner"})
	workspace := t.TempDir()
	runtime, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite", filepath.Join(workspace, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	// Probe the actual provisioned schema, not the discovery fixture. There is
	// no ALTER or compatibility migration in this fresh-database regression.
	var completion any
	err = db.QueryRowContext(ctx, `SELECT completed_at FROM tool_approval_queue LIMIT 1`).Scan(&completion)
	require.ErrorIs(t, err, sql.ErrNoRows, "canonical fresh queue must declare its nullable completion column")
	store := &convstore.ApprovalStore{Invoker: runtime, OwnerID: authctx.EffectiveUserID}
	row := &write.ToolApprovalQueue{}
	row.SetId("completion-original")
	row.SetUserId("completion-owner")
	row.SetToolName("fixture/original-tool")
	row.SetArguments([]byte(`{}`))
	require.NoError(t, store.PatchTrusted(ctx, row))
	query := &read.ApprovalRowsInput{}
	query.SetUserId("completion-owner")
	rows, err := store.List(ctx, "rows", query, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "pending", rows[0].Status)
	require.Nil(t, rows[0].CompletedAt, "default completion must remain SQL NULL")
	completed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// Persist a real synthetic completion fixture, then exercise the native
	// sparse writer and reader on that SAME original queue row. The completion
	// must survive the writer's CurrentWriter merge, including regenerated SQL.
	_, err = db.ExecContext(ctx, `UPDATE tool_approval_queue SET completed_at=? WHERE id=?`, completed, "completion-original")
	require.NoError(t, err)
	patch := &write.ToolApprovalQueue{}
	patch.SetId("completion-original")
	patch.SetStatus("executed")
	decision := "approve"
	patch.SetDecision(&decision)
	patch.SetExecutedAt(&completed)
	require.NoError(t, store.PatchTrusted(ctx, patch))
	rows, err = store.List(ctx, "outcome", query, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "completion-original", rows[0].Id)
	require.Equal(t, "executed", rows[0].Status)
	require.NotNil(t, rows[0].CompletedAt)
	require.True(t, rows[0].CompletedAt.Equal(completed), "native reader/writer roundtrip lost stored completion")
}
