package data

import (
	"context"
	"database/sql"
	"testing"

	"github.com/viant/agently-core/internal/testutil/dbtest"
	agconvlist "github.com/viant/agently-core/pkg/agently/conversation/list"
)

func TestDataService_ListConversations_UsesConversationOrderKeyForCursors(t *testing.T) {
	svc := newSeededService(t, seedForConversationPageOrder)
	ctx := context.Background()

	latest, err := svc.ListConversations(ctx, &agconvlist.ConversationRowsInput{
		Has: &agconvlist.ConversationRowsInputHas{},
	}, &PageInput{Limit: 1, Direction: DirectionLatest})
	if err != nil {
		t.Fatalf("ListConversations(latest) error: %v", err)
	}
	if latest == nil || len(latest.Rows) != 1 {
		t.Fatalf("expected one latest row, got %#v", latest)
	}
	if latest.Rows[0].Id != "c-order-1" {
		t.Fatalf("expected latest row c-order-1 ordered by last_activity, got %s", latest.Rows[0].Id)
	}
	if !latest.HasOlder || latest.HasNewer {
		t.Fatalf("unexpected latest flags: older=%v newer=%v", latest.HasOlder, latest.HasNewer)
	}

	older, err := svc.ListConversations(ctx, &agconvlist.ConversationRowsInput{
		Has: &agconvlist.ConversationRowsInputHas{},
	}, &PageInput{Limit: 1, Direction: DirectionBefore, Cursor: latest.NextCursor})
	if err != nil {
		t.Fatalf("ListConversations(before) error: %v", err)
	}
	if older == nil || len(older.Rows) != 1 {
		t.Fatalf("expected one older row, got %#v", older)
	}
	if older.Rows[0].Id != "c-order-2" {
		t.Fatalf("expected older row c-order-2, got %s", older.Rows[0].Id)
	}
	if older.HasOlder || !older.HasNewer {
		t.Fatalf("unexpected older flags: older=%v newer=%v", older.HasOlder, older.HasNewer)
	}

	newer, err := svc.ListConversations(ctx, &agconvlist.ConversationRowsInput{
		Has: &agconvlist.ConversationRowsInputHas{},
	}, &PageInput{Limit: 1, Direction: DirectionAfter, Cursor: older.PrevCursor})
	if err != nil {
		t.Fatalf("ListConversations(after) error: %v", err)
	}
	if newer == nil || len(newer.Rows) != 1 {
		t.Fatalf("expected one newer row, got %#v", newer)
	}
	if newer.Rows[0].Id != "c-order-1" {
		t.Fatalf("expected newer row c-order-1, got %s", newer.Rows[0].Id)
	}
	if !newer.HasOlder || newer.HasNewer {
		t.Fatalf("unexpected newer flags: older=%v newer=%v", newer.HasOlder, newer.HasNewer)
	}
}

func TestDataService_ListConversations_NewerReturnsAdjacentPage(t *testing.T) {
	svc := newSeededService(t, seedForConversationPagerSequence)
	ctx := context.Background()

	latest, err := svc.ListConversations(ctx, &agconvlist.ConversationRowsInput{
		Has: &agconvlist.ConversationRowsInputHas{},
	}, &PageInput{Limit: 2, Direction: DirectionLatest})
	if err != nil {
		t.Fatalf("ListConversations(latest) error: %v", err)
	}
	assertConversationPage(t, latest, []string{"c-seq-5", "c-seq-4"}, false, true)

	middle, err := svc.ListConversations(ctx, &agconvlist.ConversationRowsInput{
		Has: &agconvlist.ConversationRowsInputHas{},
	}, &PageInput{Limit: 2, Direction: DirectionBefore, Cursor: latest.NextCursor})
	if err != nil {
		t.Fatalf("ListConversations(before middle) error: %v", err)
	}
	assertConversationPage(t, middle, []string{"c-seq-3", "c-seq-2"}, true, true)

	oldest, err := svc.ListConversations(ctx, &agconvlist.ConversationRowsInput{
		Has: &agconvlist.ConversationRowsInputHas{},
	}, &PageInput{Limit: 2, Direction: DirectionBefore, Cursor: middle.NextCursor})
	if err != nil {
		t.Fatalf("ListConversations(before oldest) error: %v", err)
	}
	assertConversationPage(t, oldest, []string{"c-seq-1"}, true, false)

	backToMiddle, err := svc.ListConversations(ctx, &agconvlist.ConversationRowsInput{
		Has: &agconvlist.ConversationRowsInputHas{},
	}, &PageInput{Limit: 2, Direction: DirectionAfter, Cursor: oldest.PrevCursor})
	if err != nil {
		t.Fatalf("ListConversations(after middle) error: %v", err)
	}
	assertConversationPage(t, backToMiddle, []string{"c-seq-3", "c-seq-2"}, true, true)

	backToNewest, err := svc.ListConversations(ctx, &agconvlist.ConversationRowsInput{
		Has: &agconvlist.ConversationRowsInputHas{},
	}, &PageInput{Limit: 2, Direction: DirectionAfter, Cursor: backToMiddle.PrevCursor})
	if err != nil {
		t.Fatalf("ListConversations(after newest) error: %v", err)
	}
	assertConversationPage(t, backToNewest, []string{"c-seq-5", "c-seq-4"}, false, true)
}

func seedForConversationPageOrder(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at, last_activity, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"c-order-1", "2026-01-01T09:00:00Z", "2026-01-01T11:00:00Z", "active"}},
		{SQL: `INSERT INTO conversation (id, created_at, last_activity, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"c-order-2", "2026-01-01T10:00:00Z", "2026-01-01T10:30:00Z", "active"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForConversationPagerSequence(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at, last_activity, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"c-seq-1", "2026-01-01T09:00:00Z", "2026-01-01T09:01:00Z", "active"}},
		{SQL: `INSERT INTO conversation (id, created_at, last_activity, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"c-seq-2", "2026-01-01T10:00:00Z", "2026-01-01T10:01:00Z", "active"}},
		{SQL: `INSERT INTO conversation (id, created_at, last_activity, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"c-seq-3", "2026-01-01T11:00:00Z", "2026-01-01T11:01:00Z", "active"}},
		{SQL: `INSERT INTO conversation (id, created_at, last_activity, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"c-seq-4", "2026-01-01T12:00:00Z", "2026-01-01T12:01:00Z", "active"}},
		{SQL: `INSERT INTO conversation (id, created_at, last_activity, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"c-seq-5", "2026-01-01T13:00:00Z", "2026-01-01T13:01:00Z", "active"}},
	}
	dbtest.ExecAll(t, db, items)
}

func assertConversationPage(t *testing.T, page *ConversationPage, wantIDs []string, wantNewer bool, wantOlder bool) {
	t.Helper()
	if page == nil {
		t.Fatalf("expected page")
	}
	got := make([]string, 0, len(page.Rows))
	for _, row := range page.Rows {
		got = append(got, row.Id)
	}
	assertIDs(t, got, wantIDs)
	if page.HasNewer != wantNewer {
		t.Fatalf("unexpected hasNewer: got=%v want=%v", page.HasNewer, wantNewer)
	}
	if page.HasOlder != wantOlder {
		t.Fatalf("unexpected hasOlder: got=%v want=%v", page.HasOlder, wantOlder)
	}
}
