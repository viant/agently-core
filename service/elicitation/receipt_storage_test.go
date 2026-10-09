package elicitation

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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

func TestCheckedResolutionPayloadKindConstraintSQLite(t *testing.T) {
	for _, schemaFile := range []string{"schema.ddl", "schema_versioned.ddl", "schema_versioned_steward.ddl"} {
		t.Run(schemaFile, func(t *testing.T) {
			runSQLiteCheckedResolutionPayloadKind(t, schemaFile)
		})
	}
}

func runSQLiteCheckedResolutionPayloadKind(t *testing.T, schemaFile string) {
	t.Helper()
	// Apply the production MySQL kind CHECK when creating this fresh SQLite
	// fixture. The normal SQLite schema intentionally has no kind enum.
	_, source, _, _ := runtime.Caller(0)
	mysqlDDL, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../script/mysql", schemaFile))
	require.NoError(t, err)
	checks := regexp.MustCompile(`CHECK \(kind IN\s+(\([\s\S]*?\))\)`).FindAllStringSubmatch(string(mysqlDDL), -1)
	require.NotEmpty(t, checks)
	// Versioned fresh provisioning finishes with a replacement kind CHECK.
	// Exercise the effective final declaration, not an overridden CREATE list.
	check := checks[len(checks)-1]
	original := "CREATE TABLE IF NOT EXISTS call_payload (\n    id TEXT PRIMARY KEY,\n    tenant_id TEXT,\n    kind TEXT NOT NULL,"
	ddl := strings.Replace(iscript.SqlListScript, original, strings.TrimSuffix(original, ",")+" CHECK (kind IN "+check[1]+"),", 1)
	require.NotEqual(t, iscript.SqlListScript, ddl)
	dsn := "file:" + filepath.Join(t.TempDir(), "receipt.db") + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(ddl)
	require.NoError(t, err)
	runCheckedResolutionPayloadConstraint(t, db, "sqlite", dsn)
}

func TestCheckedResolutionPayloadKindConstraintMySQL(t *testing.T) {
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
	runCheckedResolutionPayloadConstraint(t, db, "mysql", dsn)
}

func runCheckedResolutionPayloadConstraint(t *testing.T, db *sql.DB, driver, dsn string) {
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
	invalid := conv.NewPayload()
	invalid.SetId(prefix + "-invalid")
	invalid.SetKind("unknown_receipt_kind")
	invalid.SetMimeType("application/json")
	invalid.SetStorage("inline")
	invalid.SetSizeBytes(2)
	invalid.SetInlineBody([]byte("{}"))
	require.Error(t, client.PatchPayload(ctx, invalid), "the kind CHECK must remain enforced")
	var invalidCount int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id=?", invalid.Id).Scan(&invalidCount))
	require.Zero(t, invalidCount)

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
