package data

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	tree "github.com/viant/agently-core/internal/store/conversationtree"
)

func TestCollectDeletePlanOmitsLargeRowContents(t *testing.T) {
	svc, _ := newSeededServiceWithDB(t, seedStage1CurrentDependencies, func(t *testing.T, db *sql.DB) {
		body := strings.Repeat("large body ", 8192)
		for _, statement := range []string{
			`INSERT INTO message (id, conversation_id, turn_id, role, type, content, raw_content) VALUES ('message-large', 'conv-current', 'turn-current', 'assistant', 'text', ?, ?)`,
			`UPDATE report_run SET report_spec_json = ?, report_fill_json = ? WHERE report_run_id = 'report-run-current'`,
			`UPDATE report_export_job SET report_spec_json = ?, report_fill_json = ? WHERE job_id = 'report-job-current'`,
		} {
			if _, err := db.Exec(statement, body, body); err != nil {
				t.Fatal(err)
			}
		}
	})
	ctx := deleteTestContext()
	d := &tree.Discoverer{Invoker: svc.(*datlyService).native, OwnerID: func(context.Context) string { return "u1" }}
	graph, err := d.DiscoverAuthorized(ctx, "conv-current")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := d.CollectDeletePlan(ctx, graph, time.Now().UTC(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Messages) != 1 || len(plan.ReportRuns) != 1 || len(plan.ReportJobs) != 1 {
		t.Fatalf("messages=%d report_runs=%d jobs=%d", len(plan.Messages), len(plan.ReportRuns), len(plan.ReportJobs))
	}
	if row := plan.Messages[0]; row.Id != "message-large" || row.Content != nil || row.RawContent != nil {
		t.Fatal("message planning loaded row contents or lost identity")
	}
	if row := plan.ReportRuns[0]; row.OwnerId != "u1" || row.Revision != 1 || row.ReportSpecJson != nil || row.ReportFillJson != nil {
		t.Fatal("report planning loaded JSON or lost ownership/revision evidence")
	}
	if row := plan.ReportJobs[0]; row.OwnerId != "u1" || row.Status != "succeeded" || row.ReportSpecJson != nil || row.ReportFillJson != nil {
		t.Fatal("export planning loaded JSON or lost ownership/status evidence")
	}
}
