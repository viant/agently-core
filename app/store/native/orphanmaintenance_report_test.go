package native_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	orphan "github.com/viant/agently-core/internal/store/orphanmaintenance"
)

func TestWorkspaceRuntimeOrphanReportPagination(t *testing.T) {
	store, _, db := orphanFixture(t)
	seedOrphanFixture(t, db)
	owner, conversation, report := "zażółć%s", "missing-🙂'conversation", "missing-🙂'report"
	_, err := db.Exec("INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision,updated_at) VALUES(?,?,?,1,'2026-01-01T00:00:00Z')", owner, conversation, report)
	require.NoError(t, err)
	rows := orphanReportPages(t, store)
	require.Len(t, rows, 7)
	for _, rule := range []string{"conversation_report_context.missing_active_report_run", "conversation_report_context.missing_conversation"} {
		found := false
		for _, row := range rows {
			if row.RuleID == rule && row.RecordID == owner+orphan.RecordSeparator+conversation {
				found = true
				wantReference := conversation
				if rule == "conversation_report_context.missing_active_report_run" {
					wantReference = owner + orphan.RecordSeparator + report
				}
				require.Equal(t, wantReference, row.ReferenceID)
			}
		}
		require.True(t, found, "missing composite-key orphan: %s", rule)
	}
}

func TestWorkspaceRuntimeOrphanMySQLReportPagination(t *testing.T) {
	store, _, db, prefix := orphanMySQLFixture(t)
	owner, conversation := prefix+"owner", prefix+"missing-conversation"
	_, err := db.Exec("INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision,updated_at) VALUES(?,?,?,1,'2026-01-01 00:00:00')", owner, conversation, prefix+"missing-report")
	require.NoError(t, err)
	rows := orphanReportPages(t, store)
	count := 0
	for _, row := range rows {
		if row.Table == "conversation_report_context" && row.RecordID == owner+orphan.RecordSeparator+conversation {
			count++
		}
	}
	require.Equal(t, 2, count)
}

// The first List on a fresh runtime exercises discovery; subsequent reads
// verify that the encoded composite keys still round-trip through cursors.
func orphanReportPages(t *testing.T, store *orphan.Store) []orphan.Candidate {
	t.Helper()
	ctx := context.Background()
	cutoff := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	all, err := store.List(ctx, orphan.CandidateRequest{OlderThan: cutoff, Limit: 1000})
	require.NoError(t, err)
	var pages []orphan.Candidate
	cursor := ""
	for {
		page, err := store.List(ctx, orphan.CandidateRequest{OlderThan: cutoff, AfterCursor: cursor, Limit: 1})
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		require.Len(t, page, 1)
		require.NotEqual(t, cursor, page[0].CursorID)
		pages = append(pages, page[0])
		require.LessOrEqual(t, len(pages), len(all), "pagination returned duplicate or unexpected candidates")
		cursor = page[0].CursorID
	}
	require.Equal(t, all, pages)
	return all
}
