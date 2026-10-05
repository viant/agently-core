package sdk

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	convsvc "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	"github.com/viant/datly/bootstrap/connector"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSteeringClientIdentityNativeSQLite(t *testing.T) {
	db, dsn, cleanup := dbtest.CreateTempSQLiteDB(t, "steering-identity")
	defer cleanup()
	dbtest.LoadSQLiteSchema(t, db)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "steering-owner"})
	_, file, _, _ := runtime.Caller(0)
	host, err := native.New(ctx, native.Options{SourceRoot: filepath.Join(filepath.Dir(file), ".."), Connectors: []connector.Config{{Name: "agently", Driver: "sqlite3", DSN: dsn}}})
	require.NoError(t, err)
	defer host.Shutdown(context.Background())
	conv, err := convsvc.New(ctx, host)
	require.NoError(t, err)
	backend := &backendClient{data: data.NewService(host), conv: conv}
	_, err = db.Exec("INSERT INTO conversation(id,created_by_user_id,created_at) VALUES('steering-c','steering-owner',CURRENT_TIMESTAMP)")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO turn(id,conversation_id,status,created_at) VALUES('steering-t','steering-c','running',CURRENT_TIMESTAMP)")
	require.NoError(t, err)
	ids := map[string]string{}
	for _, id := range []string{"client-one", "client-two"} {
		result, err := backend.SteerTurn(ctx, &SteerTurnInput{ConversationID: "steering-c", TurnID: "steering-t", Role: "user", Content: "Same steering prompt", ClientRequestID: id})
		require.NoError(t, err)
		require.NotEmpty(t, result.MessageID)
		ids[id] = result.MessageID
		var tags string
		require.NoError(t, db.QueryRow("SELECT tags FROM message WHERE id=?", result.MessageID).Scan(&tags))
		require.Equal(t, id, steeringClientRequestID(&tags))
	}
	require.NotEqual(t, ids["client-one"], ids["client-two"])
	// Existing Datly1 transcript read must retain correlation after a cold load.
	conversation, err := conv.GetConversation(ctx, "steering-c", convstore.WithIncludeTranscript(true))
	require.NoError(t, err)
	canonical := BuildCanonicalState("steering-c", conversation.GetTranscript())
	encoded, err := json.Marshal(canonical)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"clientRequestId":"client-one"`)
	require.Contains(t, string(encoded), `"clientRequestId":"client-two"`)
	require.Contains(t, string(encoded), ids["client-one"])
	require.Contains(t, string(encoded), ids["client-two"])
}
