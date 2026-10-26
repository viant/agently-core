package sdk

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	convsvc "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/datly/bootstrap/connector"
)

func TestHTTPClientTurnFeedback(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/conversations/conversation%2Fone/turns/turn%2Fone/feedback", r.URL.EscapedPath())
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			require.JSONEq(t, `{"rating":1,"comment":null}`, string(body))
			_, _ = io.WriteString(w, `{"rating":1,"comment":null,"updatedAt":"2026-10-24T12:00:00Z"}`)
			return
		}
		require.Equal(t, http.MethodDelete, r.Method)
		require.Empty(t, body)
		_, _ = io.WriteString(w, `{"rating":0,"comment":null,"updatedAt":null}`)
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL)
	require.NoError(t, err)
	saved, err := client.SetTurnFeedback(context.Background(), &SetTurnFeedbackInput{ConversationID: "conversation/one", TurnID: "turn/one", Rating: 1})
	require.NoError(t, err)
	require.Equal(t, 1, saved.Rating)
	require.NotNil(t, saved.UpdatedAt)
	cleared, err := client.ClearTurnFeedback(context.Background(), "conversation/one", "turn/one")
	require.NoError(t, err)
	require.Equal(t, &TurnFeedback{}, cleared)
	require.Equal(t, 2, calls)
}

// Exercise HTTP authorization, the generated Datly write path and canonical
// history together. In particular nil comment/time and rating=0 must reach SQL.
func TestHTTPTurnFeedbackNative(t *testing.T) {
	for _, driver := range []string{"sqlite3", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			var dsn string
			if driver == "mysql" {
				dsn = os.Getenv("AGENTLY_TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
				}
				var err error
				db, err = sql.Open(driver, dsn)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, db.Close()) })
			} else {
				var cleanup func()
				db, dsn, cleanup = dbtest.CreateTempSQLiteDB(t, "turn-feedback")
				t.Cleanup(cleanup)
				dbtest.LoadSQLiteSchema(t, db)
			}
			ctx := context.Background()
			_, file, _, _ := runtime.Caller(0)
			host, err := native.New(ctx, native.Options{SourceRoot: filepath.Join(filepath.Dir(file), ".."), Connectors: []connector.Config{{Name: "agently", Driver: driver, DSN: dsn}}})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, host.Shutdown(context.Background())) })
			conv, err := convsvc.New(ctx, host)
			require.NoError(t, err)
			backend := &backendClient{data: data.NewService(host), conv: conv}
			prefix := fmt.Sprintf("feedback-%d", time.Now().UnixNano())
			cid, tid, other := prefix+"-c", prefix+"-t", prefix+"-other"
			_, err = db.Exec("INSERT INTO conversation(id,created_by_user_id,visibility,shareable,created_at) VALUES(?,?,'public',1,CURRENT_TIMESTAMP)", cid, "feedback-owner")
			require.NoError(t, err)
			t.Cleanup(func() { _, err := db.Exec("DELETE FROM conversation WHERE id=?", cid); require.NoError(t, err) })
			for _, id := range []string{tid, other} {
				turn := convcli.NewTurn()
				turn.SetId(id)
				turn.SetConversationID(cid)
				turn.SetStatus("succeeded")
				require.NoError(t, conv.PatchTurn(ctx, turn))
				require.NotNil(t, turn.FeedbackRating)
				require.Zero(t, *turn.FeedbackRating)
			}
			_, err = db.Exec("INSERT INTO message(id,conversation_id,turn_id,role,type,content,mode,interim,status,created_at) VALUES(?,?,?,'assistant','text','Hello','final',0,'succeeded',CURRENT_TIMESTAMP)", prefix+"-m", cid, tid)
			require.NoError(t, err)
			mux := http.NewServeMux()
			mux.HandleFunc("PUT /v1/conversations/{id}/turns/{turnId}/feedback", handleTurnFeedback(backend, &svcauth.Config{Enabled: true}))
			mux.HandleFunc("DELETE /v1/conversations/{id}/turns/{turnId}/feedback", handleTurnFeedback(backend, &svcauth.Config{Enabled: true}))
			request := func(method, conversationID, turnID, user, body string, want int) TurnFeedback {
				t.Helper()
				req := httptest.NewRequest(method, "/v1/conversations/"+conversationID+"/turns/"+turnID+"/feedback", strings.NewReader(body))
				if user != "" {
					req = req.WithContext(authctx.WithUserInfo(req.Context(), &authctx.UserInfo{Subject: user}))
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				require.Equal(t, want, rec.Code, rec.Body.String())
				var out TurnFeedback
				if want == 200 {
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
				}
				return out
			}
			history := func(want TurnFeedback) {
				t.Helper()
				ownerCtx := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "feedback-owner"})
				out, err := backend.GetTranscript(ownerCtx, &GetTranscriptInput{ConversationID: cid})
				require.NoError(t, err)
				found := false
				for _, turn := range out.Conversation.Turns {
					if turn.TurnID == tid {
						found = true
						require.Equal(t, want, *turn.Feedback)
						require.True(t, turn.FeedbackEditable)
					}
				}
				require.True(t, found)
				var rating int
				var comment, updated sql.NullString
				require.NoError(t, db.QueryRow("SELECT feedback_rating,feedback_comment,feedback_updated_at FROM turn WHERE id=?", tid).Scan(&rating, &comment, &updated))
				require.Equal(t, want.Rating, rating)
				require.Equal(t, want.Comment != nil, comment.Valid)
				if want.Comment != nil {
					require.Equal(t, *want.Comment, comment.String)
				}
				require.Equal(t, want.UpdatedAt != nil, updated.Valid)
			}
			history(TurnFeedback{}) // Legacy/default state has an explicit zero rating.
			readerCtx := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "reader"})
			shared, err := backend.GetTranscript(readerCtx, &GetTranscriptInput{ConversationID: cid})
			require.NoError(t, err)
			for _, turn := range shared.Conversation.Turns {
				require.False(t, turn.FeedbackEditable)
			}
			request("PUT", cid, tid, "", `{"rating":1}`, 401)
			request("PUT", cid, tid, "reader", `{"rating":1}`, 403)
			for _, body := range []string{`{"rating":0}`, `{"rating":2}`, `{"rating":"1"}`, `{`, `{"rating":1,"comment":"` + strings.Repeat("ą", 4001) + `"}`} {
				request("PUT", cid, tid, "feedback-owner", body, 400)
			}
			request("PUT", cid, "missing", "feedback-owner", `{"rating":1}`, 404)
			request("PUT", "missing", tid, "feedback-owner", `{"rating":1}`, 404)
			request("PUT", cid, other, "feedback-owner", `{"rating":1}`, 409)
			for _, status := range []string{"running", "failed", "canceled", "queued"} {
				_, err = db.Exec("UPDATE turn SET status=? WHERE id=?", status, tid)
				require.NoError(t, err)
				request("PUT", cid, tid, "feedback-owner", `{"rating":1}`, 409)
			}
			_, err = db.Exec("UPDATE turn SET status='succeeded' WHERE id=?", tid)
			require.NoError(t, err)
			_, err = db.Exec("UPDATE turn SET origin='host_request' WHERE id=?", tid)
			require.NoError(t, err)
			request("PUT", cid, tid, "feedback-owner", `{"rating":1}`, 409)
			_, err = db.Exec("UPDATE turn SET origin=NULL WHERE id=?", tid)
			require.NoError(t, err)
			boundary := request("PUT", cid, tid, "feedback-owner", `{"rating":1,"comment":"`+strings.Repeat("👍", 4000)+`"}`, 200)
			history(boundary)
			positive := request("PUT", cid, tid, "feedback-owner", `{"rating":1,"comment":"Pomocne 👍"}`, 200)
			require.NotNil(t, positive.UpdatedAt)
			history(positive)
			negative := request("PUT", cid, tid, "feedback-owner", `{"rating":-1,"comment":"Pomocne 👍"}`, 200)
			require.Equal(t, -1, negative.Rating)
			history(negative)
			// Ordinary sparse turn patches must preserve a previously saved vote.
			statusPatch := convcli.NewTurn()
			statusPatch.SetId(tid)
			statusPatch.SetStatus("succeeded")
			require.NoError(t, conv.PatchTurn(ctx, statusPatch))
			history(negative)
			blank := request("PUT", cid, tid, "feedback-owner", `{"rating":1,"comment":"  \n "}`, 200)
			require.Nil(t, blank.Comment)
			history(blank)
			request("PUT", cid, tid, "feedback-owner", `{"rating":-1,"comment":"remove me"}`, 200)
			cleared := request("DELETE", cid, tid, "feedback-owner", "", 200)
			require.Equal(t, TurnFeedback{}, cleared)
			history(cleared)
			request("DELETE", cid, tid, "feedback-owner", "", 200)
			var untouched int
			require.NoError(t, db.QueryRow("SELECT feedback_rating FROM turn WHERE id=?", other).Scan(&untouched))
			require.Zero(t, untouched)
		})
	}
}
