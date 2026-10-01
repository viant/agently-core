package tests

import (
	"context"
	"database/sql"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	submit "github.com/viant/agently-core/internal/store/reporting/exportsubmit"
	"os"
	"testing"
	"time"
)

func TestReportExportSubmitMySQLCallerSnapshotAndTransactionOwnership(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	must(t, err)
	defer db.Close()
	other, err := sql.Open("mysql", dsn)
	must(t, err)
	defer other.Close()
	prefix := fmt.Sprintf("submitmysql-%d-", time.Now().UnixNano())
	owner := prefix + "owner"
	runID := prefix + "run"
	conversation := prefix + "conversation"
	defer func() {
		_, err := db.Exec("DELETE FROM report_export_job WHERE job_id LIKE ?", prefix+"%")
		must(t, err)
		_, err = db.Exec("DELETE FROM report_run WHERE report_run_id=?", runID)
		must(t, err)
		_, err = db.Exec("DELETE FROM conversation WHERE id=?", conversation)
		must(t, err)
	}()
	_, err = db.Exec("INSERT INTO conversation(id,created_by_user_id,status) VALUES(?,?,'succeeded')", conversation, owner)
	must(t, err)
	_, err = db.Exec(`INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,origin,status,started_at,completed_at,revision,ui_run_request_id,report_spec_json,report_fill_json,report_print_json,created_at,updated_at) VALUES(?,?,?,'test','manual','completed',NOW(),NOW(),2,?,X'7B7D',X'7B7D',X'7B7D',NOW(),NOW())`, runID, owner, conversation, prefix+"ui")
	must(t, err)
	candidate := func(operation, job string) *submit.Input {
		input := reportExportCandidate()
		input.JobID = prefix + job
		input.OwnerID = owner
		input.ConversationID = conversation
		input.ReportRunID = runID
		input.ArtifactRef = "report-run://" + runID
		input.ExportRequestID = prefix + operation
		return input
	}
	t.Run("replay uses current read after caller snapshot", func(t *testing.T) {
		tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		must(t, err)
		defer tx.Rollback()
		var before int
		must(t, tx.QueryRow("SELECT COUNT(*) FROM report_export_job WHERE export_request_id=?", prefix+"snapshot").Scan(&before))
		if before != 0 {
			t.Fatal(before)
		}
		fresh, key := reportExportSubmitRuntime(t, other, owner, nil)
		first, err := invokeReportExportSubmit(context.Background(), fresh, key, candidate("snapshot", "fresh"))
		must(t, err)
		joined, joinedKey := reportExportSubmitRuntime(t, db, owner, tx)
		replay, err := invokeReportExportSubmit(context.Background(), joined, joinedKey, candidate("snapshot", "joined"))
		must(t, err)
		if !replay.Replay || replay.Job.JobId != first.Job.JobId {
			t.Fatalf("replay=%+v first=%+v", replay, first)
		}
		must(t, tx.Rollback())
	})
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("caller commit=%v", commit), func(t *testing.T) {
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, key := reportExportSubmitRuntime(t, db, owner, tx)
			input := candidate(fmt.Sprintf("owned-%v", commit), fmt.Sprintf("owned-%v", commit))
			output, err := invokeReportExportSubmit(context.Background(), rt, key, input)
			must(t, err)
			if output.Replay {
				t.Fatal("new operation replayed")
			}
			var pending int
			must(t, tx.QueryRow("SELECT COUNT(*) FROM report_export_job WHERE job_id=?", input.JobID).Scan(&pending))
			if pending != 1 {
				t.Fatalf("pending=%d", pending)
			}
			if commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored int
			must(t, db.QueryRow("SELECT COUNT(*) FROM report_export_job WHERE job_id=?", input.JobID).Scan(&stored))
			expected := 0
			if commit {
				expected = 1
			}
			if stored != expected {
				t.Fatalf("stored=%d expected=%d", stored, expected)
			}
		})
	}
}
