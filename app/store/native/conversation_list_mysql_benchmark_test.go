package native_test

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/conversation/read"
	convstore "github.com/viant/agently-core/internal/store/conversation"
	"github.com/viant/datly/bootstrap/connector"
)

// TestConversationListMySQLBenchmark reads a disposable database seeded with
// 1,000 codex-list conversations, five turns and 15 messages per conversation.
// The caller owns schema setup; this test never changes the database. Run it
// only with AGENTLY_TEST_MYSQL_LIST_BENCH_OWNED=1 and the fixture DSN.
func TestConversationListMySQLBenchmark(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_LIST_BENCH_DSN")
	if dsn == "" || os.Getenv("AGENTLY_TEST_MYSQL_LIST_BENCH_OWNED") != "1" {
		t.Skip("disposable MySQL list fixture is not configured")
	}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bench-owner"})
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, check := range []struct {
		table string
		want  int
	}{{"conversation", 1000}, {"turn", 5000}, {"message", 15000}} {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+check.table+" WHERE id LIKE 'codex-list-%'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != check.want {
			t.Fatalf("%s fixture rows=%d, want %d", check.table, count, check.want)
		}
	}
	started := time.Now()
	server, err := native.New(ctx, native.Options{Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	t.Logf("startup=%s", time.Since(started))
	store := &convstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	firstStarted := time.Now()
	first, err := store.ListPage(ctx, nil, 20, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 20 {
		t.Fatalf("first page rows=%d, want 20", len(first))
	}
	t.Logf("cold-first-page=%s", time.Since(firstStarted))
	measure := func(name string, makeInput func(int) *read.ConversationInput) {
		var durations []time.Duration
		var cursor string
		for i := 0; i < 20; i++ {
			input := makeInput(i)
			if input != nil && cursor != "" {
				input.SetCursorBefore(cursor)
			}
			begin := time.Now()
			rows, err := store.ListPage(ctx, input, 20, false, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 20 {
				t.Fatalf("%s page=%d rows=%d, want 20", name, i, len(rows))
			}
			durations = append(durations, time.Since(begin))
			cursor = rows[len(rows)-1].Id
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		t.Logf("%s median=%s p95=%s min=%s max=%s", name, durations[9], durations[18], durations[0], durations[19])
	}
	measure("warm-first-page", func(int) *read.ConversationInput { return nil })
	measure("warm-cursor-pages", func(int) *read.ConversationInput { return &read.ConversationInput{} })
}

func TestConversationListMySQLWindowSemantics(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_LIST_BENCH_DSN")
	if dsn == "" || os.Getenv("AGENTLY_TEST_MYSQL_LIST_BENCH_OWNED") != "1" {
		t.Skip("disposable MySQL list fixture is not configured")
	}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bench-owner"})
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var adversarial int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation WHERE id LIKE 'adversarial-%'").Scan(&adversarial); err != nil {
		t.Fatal(err)
	}
	if adversarial != 4 {
		t.Fatalf("adversarial fixture rows=%d, want 4", adversarial)
	}
	server, err := native.New(ctx, native.Options{Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	store := &convstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	rows, err := store.ListPage(ctx, nil, 20, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 20 {
		t.Fatalf("owner list first page rows=%d", len(rows))
	}
	if rows[0].Id != "adversarial-private-owner" {
		t.Fatalf("owner list first ID=%q", rows[0].Id)
	}
	for _, row := range rows {
		if row.Id == "adversarial-private-other" || row.Id == "adversarial-child" || row.Id == "adversarial-orphan" {
			t.Fatalf("out-of-scope row %q entered page", row.Id)
		}
	}
	page, err := data.NewService(server).ListConversations(ctx, nil, &data.PageInput{Limit: 20}, data.WithPrincipal("bench-owner"))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 20 {
		t.Fatalf("service overfetch rows=%d", len(page.Rows))
	}
	if !page.HasMore || page.Rows[0].Id != "adversarial-private-owner" {
		t.Fatalf("service overfetch: rows=%d hasMore=%v", len(page.Rows), page.HasMore)
	}
	filtered := &read.ConversationInput{}
	filtered.SetAgentId("matched-agent")
	matched, err := store.ListPage(ctx, filtered, 20, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 20 {
		t.Fatalf("filtered page rows=%d", len(matched))
	}
	if matched[0].Id != "codex-list-c0924" || matched[19].Id != "codex-list-c0905" {
		t.Fatalf("filtered page IDs: first=%v last=%v", matched[0].Id, matched[19].Id)
	}
	filtered.SetCursorBefore(matched[19].Id)
	older, err := store.ListPage(ctx, filtered, 20, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(older) != 5 {
		t.Fatalf("filtered older page: count=%d", len(older))
	}
	if older[0].Id != "codex-list-c0904" || older[4].Id != "codex-list-c0900" {
		t.Fatalf("filtered older page IDs: first=%v last=%v", older[0].Id, older[4].Id)
	}
}
