package conversation

import (
	"context"
	"database/sql"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	"github.com/viant/xdatly/state"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/testutil/dbtest"

	_ "modernc.org/sqlite"
)

func TestService_ToolApprovalQueue_CreateReadUpdate(t *testing.T) {
	svc := newQueueService(t, func(t *testing.T, db *sql.DB) {
		t.Helper()
		seedQueueUser(t, db, "u1")
	})

	ctx := context.Background()
	create := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	create.SetId("q-service-1")
	create.SetUserId("u1")
	create.SetToolName("system/exec")
	create.SetTitle("Initial title")
	create.SetArguments([]byte(`{"cmd":"echo ok"}`))
	create.SetStatus("pending")
	require.NoError(t, svc.PatchToolApprovalQueue(ctx, create))

	rows, err := svc.ListToolApprovalQueues(ctx, &toolapprovalqueuemodel.QueueRowsInput{
		Id:  "q-service-1",
		Has: &toolapprovalqueuemodel.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "u1", rows[0].UserId)
	require.Equal(t, "system/exec", rows[0].ToolName)
	require.Equal(t, "pending", rows[0].Status)
	require.NotNil(t, rows[0].Title)
	require.Equal(t, "Initial title", *rows[0].Title)

	patch := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	patch.SetId("q-service-1")
	patch.SetUserId("u1")
	patch.SetToolName("system/exec")
	patch.SetArguments([]byte(`{"cmd":"echo ok"}`))
	patch.SetStatus("approved")
	require.NoError(t, svc.PatchToolApprovalQueue(ctx, patch))

	rows, err = svc.ListToolApprovalQueues(ctx, &toolapprovalqueuemodel.QueueRowsInput{
		Id:  "q-service-1",
		Has: &toolapprovalqueuemodel.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "approved", rows[0].Status)
	require.NotNil(t, rows[0].Title)
	require.Equal(t, "Initial title", *rows[0].Title, "unpatched title should be preserved")
}

func TestService_ToolApprovalQueue_UserScope(t *testing.T) {
	svc := newQueueService(t, func(t *testing.T, db *sql.DB) {
		t.Helper()
		seedQueueUser(t, db, "u1")
		seedQueueUser(t, db, "u2")
		_, err := db.Exec(
			`INSERT INTO tool_approval_queue (id, user_id, tool_name, title, arguments, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"q-u1",
			"u1",
			"system/exec",
			"User 1 row",
			[]byte(`{"cmd":"echo u1"}`),
			"pending",
			"2026-01-01T10:00:00Z",
		)
		require.NoError(t, err)
		_, err = db.Exec(
			`INSERT INTO tool_approval_queue (id, user_id, tool_name, title, arguments, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"q-u2",
			"u2",
			"system/exec",
			"User 2 row",
			[]byte(`{"cmd":"echo u2"}`),
			"pending",
			"2026-01-01T10:01:00Z",
		)
		require.NoError(t, err)
	})

	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	rows, err := svc.ListToolApprovalQueues(ctx, &toolapprovalqueuemodel.QueueRowsInput{
		QueueStatus: "pending",
		Has:         &toolapprovalqueuemodel.QueueRowsInputHas{QueueStatus: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "q-u1", rows[0].Id)

	err = svc.PatchToolApprovalQueue(ctx, func() *toolapprovalqueuemodel.ToolApprovalQueue {
		q := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
		q.SetId("q-u1")
		q.SetUserId("u1")
		q.SetToolName("system/exec")
		q.SetArguments([]byte(`{"cmd":"echo u1"}`))
		q.SetStatus("approved")
		return q
	}())
	require.NoError(t, err)

	rows, err = svc.ListToolApprovalQueues(ctx, &toolapprovalqueuemodel.QueueRowsInput{
		Id:  "q-u1",
		Has: &toolapprovalqueuemodel.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "approved", rows[0].Status)

	_, err = svc.ListToolApprovalQueues(ctx, &toolapprovalqueuemodel.QueueRowsInput{
		UserId: "u2",
		Has:    &toolapprovalqueuemodel.QueueRowsInputHas{UserId: true},
	})
	require.Error(t, err)
}

func TestService_ToolApprovalQueue_ExactCount(t *testing.T) {
	svc := newQueueService(t, func(t *testing.T, db *sql.DB) {
		t.Helper()
		seedQueueUser(t, db, "u1")
		for _, row := range []struct {
			id     string
			status string
		}{
			{id: "q-count-1", status: "pending"},
			{id: "q-count-2", status: "pending"},
			{id: "q-count-3", status: "approved"},
		} {
			_, err := db.Exec(
				`INSERT INTO tool_approval_queue (id, user_id, tool_name, title, arguments, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				row.id,
				"u1",
				"system/exec",
				row.id,
				[]byte(`{"cmd":"echo ok"}`),
				row.status,
				"2026-01-01T10:00:00Z",
			)
			require.NoError(t, err)
		}
	})

	total, err := svc.CountToolApprovalQueues(context.Background(), &toolapprovalqueuemodel.QueueTotalInput{
		UserId:      "u1",
		QueueStatus: "pending",
		Has:         &toolapprovalqueuemodel.QueueTotalInputHas{UserId: true, QueueStatus: true},
	})
	require.NoError(t, err)
	require.Equal(t, 2, total)
}

type queueSeedFn func(t *testing.T, db *sql.DB)

func newQueueService(t *testing.T, seeds ...queueSeedFn) *Service {
	t.Helper()
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-core-queue-service")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	for _, seed := range seeds {
		seed(t, db)
	}

	ctx := context.Background()
	dao := testNativeInvoker(t, dbPath)

	svc, err := New(ctx, dao)
	require.NoError(t, err)
	return svc
}

func seedQueueUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users (id, username) VALUES (?, ?)`, userID, userID)
	require.NoError(t, err)
}

func TestService_ToolApprovalQueue_NativeSelectorsAndOutcomes(t *testing.T) {
	svc := newQueueService(t, func(t *testing.T, db *sql.DB) {
		seedQueueUser(t, db, "u1")
		seedQueueUser(t, db, "u2")
		for _, row := range []struct{ id, owner, status string }{{"own-a", "u1", "approved"}, {"own-b", "u1", "pending"}, {"other", "u2", "approved"}} {
			_, err := db.Exec(`INSERT INTO tool_approval_queue (id, user_id, tool_name, title, arguments, status, created_at, approved_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, row.id, row.owner, "system/exec", "Title", []byte(`{"cmd":"echo ok"}`), row.status, "2026-01-01T10:00:00Z", "2026-01-02T10:00:00Z")
			require.NoError(t, err)
		}
	})
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	selector := &state.NamedSelector{Name: "queue_rows", Selector: state.Selector{OrderBy: "id ASC", Limit: 1, Fields: []string{"id"}}}
	rows, err := svc.ListToolApprovalQueuesWithSelectors(ctx, nil, selector)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "own-a", rows[0].Id)
	require.Nil(t, rows[0].Arguments)
	require.Equal(t, "queue_rows", selector.Name)
	require.Equal(t, []string{"id"}, selector.Fields)
	outcomes, err := svc.ListToolApprovalOutcomes(ctx, &toolapprovalqueuemodel.OutcomeRowsInput{Since: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Has: &toolapprovalqueuemodel.OutcomeRowsInputHas{Since: true}})
	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	require.Equal(t, "own-a", outcomes[0].Id)
	require.NotNil(t, outcomes[0].TransitionAt)
}
