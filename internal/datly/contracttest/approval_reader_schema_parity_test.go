package tests

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/toolapprovalqueue/read"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

// Exercise the runtime schema, rather than the smaller discovery fixture that
// previously concealed an unmapped nullable completed_at column.
func TestApprovalReaderRuntimeSchemaNullCompletion(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "runtime.db"))
	must(t, err)
	t.Cleanup(func() { db.Close() })
	ddl, err := os.ReadFile(filepath.Join(project, "script/schema.ddl"))
	must(t, err)
	_, err = db.Exec(string(ddl))
	must(t, err)
	approvalReaderCompletionCases(t, db)
}

func TestApprovalReaderMySQLNullCompletion(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	must(t, err)
	t.Cleanup(func() { db.Close() })
	approvalReaderCompletionCases(t, db)
}

func approvalReaderCompletionCases(t *testing.T, db *sql.DB) {
	t.Helper()
	prefix := fmt.Sprintf("approval-null-%d-", time.Now().UnixNano())
	owner, other := prefix+"owner", prefix+"other"
	completed := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		id, user, status string
		completion       any
	}{
		{prefix + "pending", owner, "pending", nil},
		{prefix + "done", owner, "executed", completed},
		{prefix + "foreign", other, "pending", nil},
	} {
		_, err := db.Exec(`INSERT INTO tool_approval_queue(id,user_id,tool_name,arguments,status,created_at,completed_at) VALUES(?,?,?,?,?,?,?)`, row.id, row.user, "test/tool", []byte("{}"), row.status, completed, row.completion)
		must(t, err)
		id := row.id
		t.Cleanup(func() {
			_, err := db.Exec("DELETE FROM tool_approval_queue WHERE id=?", id)
			must(t, err)
		})
	}
	for _, tc := range []struct {
		name, mode, status string
		want               int
	}{
		{"owner rows", "rows", "", 2},
		{"pending NULL", "rows", "pending", 1},
		{"completed outcome", "outcome", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, key := approvalReaderRuntime(t, db, owner, tc.mode, false, true)
			filters := map[string]any{"userId": owner}
			if tc.status != "" {
				filters["status"] = tc.status
			}
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-approval"}}, Input: approvalReadInput(t, filters)})
			must(t, err)
			rows := result.(*read.ApprovalRowsOutput).Data
			if len(rows) != tc.want {
				t.Fatalf("rows=%d want=%d", len(rows), tc.want)
			}
			for _, row := range rows {
				if row.UserId != owner {
					t.Fatal("foreign owner admitted")
				}
				field := reflect.ValueOf(row).Elem().FieldByName("CompletedAt")
				if !field.IsValid() || field.Type() != reflect.TypeFor[*time.Time]() {
					t.Fatal("completion is not a nullable generated time")
				}
				if row.Status == "pending" {
					if !field.IsNil() {
						t.Fatal("NULL completion became a value")
					}
				} else if field.IsNil() || !field.Interface().(*time.Time).Equal(completed) {
					t.Fatal("completed timestamp lost")
				}
			}
		})
	}
}
