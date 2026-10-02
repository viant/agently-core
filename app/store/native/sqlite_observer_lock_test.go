package native_test

import (
	"context"
	"database/sql"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	write "github.com/viant/agently-core/internal/datly/message/write"
	convsvc "github.com/viant/agently-core/internal/service/conversation"
	sqlite "github.com/viant/agently-core/internal/service/sqlite"
	store "github.com/viant/agently-core/internal/store/conversation"
	memory "github.com/viant/agently-core/runtime/requestctx"
	observer "github.com/viant/agently-core/service/core/modelcall"

	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProvisionedSQLiteObserverWaitsForConcurrentGraphReader(t *testing.T) {
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	root := t.TempDir()
	dsn, err := sqlite.New(root).Ensure(ctx)
	require.NoError(t, err)
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec("INSERT INTO conversation(id,created_at) VALUES('c',CURRENT_TIMESTAMP);INSERT INTO turn(id,conversation_id,status,created_at) VALUES('t','c','succeeded',CURRENT_TIMESTAMP);INSERT INTO message(id,conversation_id,role,type,content,created_at) VALUES('m','c','assistant','text','before',CURRENT_TIMESTAMP)")
	require.NoError(t, err)
	_, file, _, _ := runtime.Caller(0)
	server, err := native.New(ctx, native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "..", "..", ".."), WorkspaceRoot: root})
	require.NoError(t, err)
	defer server.Shutdown(context.Background())
	warm := &write.Message{}
	warm.SetId("m")
	wc := "warm"
	warm.SetContent(&wc)
	require.NoError(t, (&store.MessageStore{Invoker: server}).PatchTrusted(ctx, warm))
	client, err := convsvc.New(ctx, server)
	require.NoError(t, err)
	baseCtx := memory.WithTurnMeta(memory.WithConversationID(ctx, "c"), memory.TurnMeta{ConversationID: "c", TurnID: "t"})
	warmCtx := observer.WithRecorderObserver(baseCtx, client)
	_, err = observer.ObserverFromContext(warmCtx).OnCallStart(warmCtx, observer.Info{Provider: "test", Model: "test", ModelKind: "chat"})
	require.NoError(t, err)
	_, err = client.GetConversation(ctx, "c")
	require.NoError(t, err)
	reader, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer reader.Rollback()
	var count int
	require.NoError(t, reader.QueryRow("SELECT COUNT(*) FROM message").Scan(&count))
	done := make(chan error, 1)
	go func() {
		_, err := client.GetConversation(ctx, "c")
		if err != nil {
			done <- err
			return
		}
		callCtx := observer.WithRecorderObserver(baseCtx, client)
		_, err = observer.ObserverFromContext(callCtx).OnCallStart(callCtx, observer.Info{Provider: "test", Model: "test", ModelKind: "chat"})
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("observer completed before held reader released: %v", err)
	case <-time.After(100 * time.Millisecond):

		require.NoError(t, reader.Rollback())
		require.NoError(t, <-done)
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM model_call").Scan(&count))
		require.Equal(t, 2, count)
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM message WHERE turn_id='t' AND sequence IS NOT NULL").Scan(&count))
		require.Equal(t, 2, count)
		t.Log("original SQLite driver waits for reader then persists model-call placeholder")
	}
}
