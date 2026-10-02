package native_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	convstore "github.com/viant/agently-core/internal/store/conversation"
	dexec "github.com/viant/datly/exec"
)

func TestWorkspaceRuntimeEmbeddedPayloadHookSQLite(t *testing.T) {
	store, _, db := orphanFixture(t)
	runEmbeddedPayloadHook(t, store.Invoker, db, "embedded-payload-sqlite-")
}

func TestWorkspaceRuntimeEmbeddedPayloadHookMySQL(t *testing.T) {
	store, _, db, prefix := orphanMySQLFixture(t)
	runEmbeddedPayloadHook(t, store.Invoker, db, prefix+"payload-")
}

func runEmbeddedPayloadHook(t *testing.T, invoker dexec.ComponentInvoker, db *sql.DB, prefix string) {
	t.Helper()
	body := []byte(`{"marker":"ORANGE-42", "text":"café 日本語"}`)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write(body)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = db.Exec("INSERT INTO conversation(id,created_at,status) VALUES(?,?,'succeeded')", prefix+"conversation", now)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO turn(id,conversation_id,created_at,status) VALUES(?,?,?,'succeeded')", prefix+"turn", prefix+"conversation", now)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO message(id,conversation_id,turn_id,role,type,created_at) VALUES(?,?,?,'assistant','text',?)", prefix+"message", prefix+"conversation", prefix+"turn", now)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression,created_at) VALUES(?,'model_request','application/json',?,'inline',?,'gzip',?)", prefix+"payload", len(body), compressed.Bytes(), now)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO model_call(message_id,turn_id,provider,model,model_kind,status,request_payload_id) VALUES(?,?,'fixture','fixture','chat','completed',?)", prefix+"message", prefix+"turn", prefix+"payload")
	require.NoError(t, err)
	store := &convstore.Store{Invoker: invoker, OwnerID: authctx.EffectiveUserID}
	row, err := store.GetInternal(context.Background(), prefix+"conversation", nil)
	require.NoError(t, err)
	raw, err := json.Marshal(row)
	require.NoError(t, err)
	var value any
	require.NoError(t, json.Unmarshal(raw, &value))
	found := 0
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["id"] == prefix+"payload" {
				require.Equal(t, "", x["compression"])
				require.Equal(t, string(body), x["inlineBody"])
				found++
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(value)
	require.Greater(t, found, 0, "native related payload must invoke the canonical decode hook")
}
