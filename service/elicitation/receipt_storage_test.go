package elicitation

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	conv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/native"
	auth "github.com/viant/agently-core/internal/auth"
	iscript "github.com/viant/agently-core/internal/script"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/datly/bootstrap/connector"
	"github.com/viant/mcp-protocol/schema"
)

func TestCheckedResolutionReceiptStorageSQLite(t *testing.T) {
	for _, schemaFile := range []string{"schema.ddl", "schema_versioned.ddl", "schema_versioned_steward.ddl"} {
		t.Run(schemaFile, func(t *testing.T) {
			runSQLiteCheckedResolutionReceiptStorage(t, schemaFile)
		})
	}
}

func runSQLiteCheckedResolutionReceiptStorage(t *testing.T, schemaFile string) {
	t.Helper()
	// Fresh MySQL schemas intentionally leave payload kinds unconstrained.
	// Verify the storage shape without manufacturing a CHECK in SQLite.
	_, source, _, _ := runtime.Caller(0)
	mysqlDDL, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../script/mysql", schemaFile))
	require.NoError(t, err)
	declarations := regexp.MustCompile("(?is)CREATE\\s+TABLE\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?`?call_payload`?\\s*\\((.*?)\\n\\);").FindAllStringSubmatch(string(mysqlDDL), -1)
	require.Len(t, declarations, 1, "fresh schema must declare call_payload once")
	definition := declarations[0][1]
	require.NotRegexp(t, `(?i)\bCHECK\s*\(`, definition)
	for _, column := range []string{"id", "tenant_id", "kind", "mime_type", "size_bytes", "storage", "inline_body", "compression"} {
		require.Regexp(t, `(?im)^\s*`+column+`\s+`, definition)
	}
	require.Regexp(t, `(?im)^\s*id\s+[^\n]*PRIMARY KEY`, definition)
	for _, index := range []string{"idx_payload_tenant_kind", "idx_payload_digest"} {
		require.Regexp(t, `(?i)CREATE\s+INDEX\s+`+index+`\s+ON\s+call_payload`, string(mysqlDDL))
	}

	dsn := "file:" + filepath.Join(t.TempDir(), "receipt.db") + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(iscript.SqlListScript)
	require.NoError(t, err)
	var payloadDefinition string
	require.NoError(t, db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='call_payload'").Scan(&payloadDefinition))
	require.NotRegexp(t, `(?i)\bCHECK\s*\(`, payloadDefinition)
	require.Regexp(t, `(?im)^\s*id\s+TEXT\s+PRIMARY KEY`, payloadDefinition)
	for _, index := range []string{"idx_payload_tenant_kind", "idx_payload_digest", "ux_payload_run_sequence"} {
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='call_payload' AND name=?", index).Scan(&count))
		require.Equal(t, 1, count, "canonical payload index %s must remain present", index)
	}
	var uniqueDefinition string
	require.NoError(t, db.QueryRow("SELECT sql FROM sqlite_master WHERE type='index' AND name='ux_payload_run_sequence'").Scan(&uniqueDefinition))
	require.Regexp(t, `(?i)^CREATE UNIQUE INDEX`, uniqueDefinition)
	var foreignKeyCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list('message') WHERE "table"='call_payload' AND "from"='elicitation_payload_id' AND "to"='id' AND on_delete='SET NULL'`).Scan(&foreignKeyCount))
	require.Equal(t, 1, foreignKeyCount, "canonical elicitation payload foreign key must remain present")
	runCheckedResolutionReceiptStorage(t, db, "sqlite", dsn)
}

func TestCheckedResolutionReceiptStorageMySQL(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN must select a disposable database with the canonical fresh schema")
	}
	config, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	require.Contains(t, config.DBName, "test", "only disposable test databases are supported")
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	runCheckedResolutionReceiptStorage(t, db, "mysql", dsn)
}

func runCheckedResolutionReceiptStorage(t *testing.T, db *sql.DB, driver, dsn string) {
	t.Helper()
	ctx := auth.WithUserInfo(context.Background(), &auth.UserInfo{Subject: "receipt-owner"})
	server, err := native.New(ctx, native.Options{Connectors: []connector.Config{{Name: "agently", Driver: driver, DSN: dsn}}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	client, err := convservice.New(ctx, server)
	require.NoError(t, err)
	prefix := "receipt-kind-test-" + uuid.NewString()
	t.Cleanup(func() {
		rows, err := db.Query("SELECT DISTINCT elicitation_payload_id FROM message WHERE conversation_id LIKE ? AND elicitation_payload_id IS NOT NULL", prefix+"%")
		require.NoError(t, err)
		var payloadIDs []string
		for rows.Next() {
			var id string
			require.NoError(t, rows.Scan(&id))
			payloadIDs = append(payloadIDs, id)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		for _, item := range []struct{ table, column string }{{"message", "conversation_id"}, {"turn", "conversation_id"}, {"conversation", "id"}, {"call_payload", "id"}} {
			_, cleanupErr := db.Exec("DELETE FROM "+item.table+" WHERE "+item.column+" LIKE ?", prefix+"%")
			require.NoError(t, cleanupErr)
		}
		for _, id := range payloadIDs {
			_, cleanupErr := db.Exec("DELETE FROM call_payload WHERE id=?", id)
			require.NoError(t, cleanupErr)
		}
	})
	// Unknown kinds are valid storage rows; receipt interpretation below still
	// rejects an ordinary payload masquerading as the exact transaction receipt.
	unclassified := conv.NewPayload()
	unclassified.SetId(prefix + "-unclassified")
	unclassified.SetKind("unknown_receipt_kind")
	unclassified.SetMimeType("application/json")
	unclassified.SetStorage("inline")
	unclassified.SetSizeBytes(2)
	unclassified.SetInlineBody([]byte("{}"))
	require.NoError(t, client.PatchPayload(ctx, unclassified), "fresh storage has no payload kind enum")
	var unclassifiedCount int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id=?", unclassified.Id).Scan(&unclassifiedCount))
	require.Equal(t, 1, unclassifiedCount)
	unknown, err := client.GetPayload(ctx, unclassified.Id)
	require.NoError(t, err)
	require.Equal(t, "unknown_receipt_kind", unknown.Kind)

	for _, action := range []string{"reject", "accept", "cancel"} {
		t.Run(action, func(t *testing.T) {
			thread := prefix + "-" + action
			root := conv.NewConversation()
			root.SetId(thread)
			root.SetCreatedByUserID("receipt-owner")
			root.SetStatus("active")
			require.NoError(t, client.PatchConversations(ctx, root))
			turn := conv.NewTurn()
			turn.SetId(thread + "-turn")
			turn.SetConversationID(thread)
			turn.SetStatus("waiting_for_user")
			require.NoError(t, client.PatchTurn(ctx, turn))
			r := router.New()
			service := New(client, nil, r, NoopAwaiterFactory())
			_, err := service.Record(ctx, &requestctx.TurnMeta{ConversationID: thread, TurnID: turn.Id}, "assistant", &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose", ElicitationId: "ask"}})
			require.NoError(t, err)
			wake := make(chan *schema.ElicitResult, 1)
			r.RegisterByElicitationID(thread, "ask", wake)
			foreign := auth.WithUserInfo(context.Background(), &auth.UserInfo{Subject: "foreign"})
			_, err = service.ResolveChecked(foreign, thread, "ask", action, nil, "")
			require.ErrorContains(t, err, "not owned by caller")
			pending, err := client.GetMessageByElicitation(ctx, thread, "ask")
			require.NoError(t, err)
			require.Equal(t, "pending", *pending.Status)
			uncommitted, err := readAnswerReceipt(ctx, client, thread, "ask")
			require.NoError(t, err)
			require.Nil(t, uncommitted)
			select {
			case <-wake:
				t.Fatal("denied foreign-owner resolution woke waiter")
			default:
			}
			var answer map[string]interface{}
			if action == "accept" {
				answer = map[string]interface{}{"color": "blue"}
			}
			resolution, err := service.ResolveChecked(ctx, thread, "ask", action, answer, "")
			require.NoError(t, err)
			require.Equal(t, ResolutionWokeExisting, resolution.Disposition)
			select {
			case <-wake:
			case <-time.After(time.Second):
				t.Fatal("committed resolution did not wake waiter")
			}
			stored, err := client.GetPayload(ctx, receiptID(thread, "ask"))
			require.NoError(t, err)
			require.Equal(t, "elicitation_answer_receipt", stored.Kind)
			// Track deterministic receipt IDs for cleanup without affecting identity.
			t.Cleanup(func() { _, err := db.Exec("DELETE FROM call_payload WHERE id=?", stored.Id); require.NoError(t, err) })
			message, err := client.GetMessageByElicitation(ctx, thread, "ask")
			require.NoError(t, err)
			expected := map[string]string{"reject": "rejected", "accept": "accepted", "cancel": "cancel"}[action]
			require.Equal(t, expected, *message.Status)
			receipt, err := service.InspectResolution(ctx, thread, "ask", action, answer, "")
			require.NoError(t, err)
			require.Equal(t, "receipt-owner", receipt.Principal)
			_, err = service.ResolveChecked(ctx, thread, "ask", action, answer, "")
			require.NoError(t, err, "exact committed answer replay must remain idempotent")
			_, err = service.ResolveChecked(ctx, thread, "ask", "accept", map[string]interface{}{"color": "red"}, "")
			require.Error(t, err, "a different answer must not replace the receipt")
			// An ordinary accepted-answer payload cannot masquerade as the
			// exact transaction receipt even when its JSON bytes are unchanged.
			_, err = db.Exec("UPDATE call_payload SET kind=? WHERE id=?", "elicitation_response", stored.Id)
			require.NoError(t, err)
			_, err = service.InspectResolution(ctx, thread, "ask", action, answer, "")
			require.ErrorContains(t, err, "invalid elicitation receipt")
		})
	}
}
