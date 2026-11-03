package conversationaudit

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
	"github.com/viant/datly/bootstrap/connector"
	_ "modernc.org/sqlite"
)

func fixture(ctx context.Context, t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	schema, err := os.ReadFile("../../internal/script/sqlite/schema.ddl")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(schema))
	require.NoError(t, err)
	statements := []string{
		`INSERT INTO conversation(id,created_by_user_id,created_at,title) VALUES('own','alice','2026-10-25 12:00:00','Own'),('other','bob','2026-10-25 12:00:00','Other'),('old','alice','2026-01-01 12:00:00','Old')`,
		`INSERT INTO turn(id,conversation_id,created_at,status,feedback_rating,feedback_comment) VALUES('t2','own','2026-10-26 13:00:00','completed',1,'Helpful'),('t1','own','2026-10-26 13:00:00','completed',0,NULL),('foreign','other','2026-10-26 13:00:00','failed',-1,'Check'),('old-turn','old','2026-10-26 13:00:00','completed',0,NULL)`,
		`INSERT INTO message(id,conversation_id,turn_id,sequence,role,content) VALUES('u','own','t2',1,'user','=SUM(1,2)'),('a','own','t2',2,'assistant','Hello, world'),('s','own','t2',3,'user','More detail'),('final','own','t2',4,'assistant','Final response')`,
		`INSERT INTO model_call(message_id,turn_id,provider,model,model_kind,status,prompt_tokens,completion_tokens,cost) VALUES('a','t2','example','model','chat','completed',10,20,0.1),('final','t2','example','model','chat','completed',30,40,0.2)`,
	}
	for _, q := range statements {
		_, err = db.ExecContext(ctx, q)
		require.NoError(t, err)
	}
	server, err := native.New(ctx, native.Options{Connectors: []connector.Config{{Name: "agently", Driver: "sqlite", DSN: path}}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(ctx)) })
	return New(NativeReader{server}, Options{AuditorUserIDs: []string{"auditor"}}, nil), db
}

func principal(ctx context.Context, id string) context.Context {
	return authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: id})
}
func dates() Filter {
	a := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	b := a.Add(24 * time.Hour)
	return Filter{From: &a, To: &b, Limit: 1}
}

func TestAuditNativeScopePaginationAndCanonical(t *testing.T) {
	ctx := context.Background()
	s, db := fixture(ctx, t)
	owner := principal(ctx, "alice")
	_, err := s.List(owner, Filter{})
	require.ErrorIs(t, err, ErrFilter)
	f := dates()
	page, err := s.List(owner, f)
	require.NoError(t, err)
	require.Len(t, page.Rows, 1)
	require.Equal(t, "t2", page.Rows[0].Id)
	require.Empty(t, page.Rows[0].Message)
	require.NotEmpty(t, page.NextCursor)
	require.Equal(t, 2, page.Rows[0].ModelCalls)
	require.Equal(t, 40, *page.Rows[0].InputTokens)
	require.Equal(t, 60, *page.Rows[0].OutputTokens)
	f.Cursor = page.NextCursor
	next, err := s.List(owner, f)
	require.NoError(t, err)
	require.Len(t, next.Rows, 1)
	require.Equal(t, "t1", next.Rows[0].Id)
	require.Empty(t, next.NextCursor)
	f.UserID = "bob"
	_, err = s.List(owner, f)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = s.Detail(owner, "other", "foreign")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.Detail(owner, "own", "foreign")
	require.ErrorIs(t, err, ErrNotFound)
	d, err := s.Detail(owner, "own", "t2")
	require.NoError(t, err)
	require.Contains(t, d.Input, "More detail")
	require.Equal(t, "Final response", d.Output)
	require.Equal(t, 1, *d.Row.FeedbackRating)
	require.Len(t, d.Row.Message, 4)
	all := dates()
	all.Limit = 200
	p, err := s.List(principal(ctx, "auditor"), all)
	require.NoError(t, err)
	require.Len(t, p.Rows, 3)
	all.UserID = "bob"
	p, err = s.List(principal(ctx, "auditor"), all)
	require.NoError(t, err)
	require.Len(t, p.Rows, 1)
	_, err = s.List(ctx, dates())
	require.ErrorIs(t, err, ErrForbidden)
	f = dates()
	f.Cursor = page.NextCursor
	f.ConversationID = "old"
	_, err = s.List(owner, f)
	require.ErrorIs(t, err, ErrFilter)
	data, err := s.CSV(owner, Filter{ConversationID: "own"})
	require.NoError(t, err)
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Contains(t, rows[1], "'=SUM(1,2)\n\nMore detail")
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM turn").Scan(&count))
	require.Equal(t, 4, count)
}

func TestAuditInputBounds(t *testing.T) {
	s := New(nil, Options{}, nil)
	ctx := principal(context.Background(), "alice")
	for _, f := range []Filter{{}, {UserID: "alice"}, {Limit: 201, ConversationID: "own"}, {Limit: -1, ConversationID: "own"}} {
		_, _, err := s.input(ctx, f)
		require.Error(t, err)
	}
	f := dates()
	end := f.From.Add(32 * 24 * time.Hour)
	f.To = &end
	_, _, err := s.input(ctx, f)
	require.ErrorIs(t, err, ErrFilter)
	i, _, err := s.input(ctx, Filter{ConversationID: "own"})
	require.NoError(t, err)
	require.Equal(t, "alice", i.OwnerID)
	for _, value := range []string{"=1", "  +1", "\t@a", "\ufeff-2"} {
		require.True(t, strings.HasPrefix(csvCell(value), "'"))
	}
	require.Equal(t, "ordinary text", csvCell("ordinary text"))
	require.Error(t, (&projection.Input{Limit: 1, ConversationID: "own"}).Init(ctx))
}

func TestAuditNativeFullPageCursorsWithContent(t *testing.T) {
	ctx := principal(context.Background(), "alice")
	s, db := fixture(ctx, t)
	for n := 0; n < 101; n++ {
		_, err := db.ExecContext(ctx, `INSERT INTO turn(id,conversation_id,created_at,status) VALUES(?,'own','2026-10-27 13:00:00','completed')`, fmt.Sprintf("page-%03d", n))
		require.NoError(t, err)
	}
	f := dates()
	f.Limit, f.IncludeContent = 50, true
	seen := map[string]bool{}
	for pageNumber, count := range []int{50, 50, 3} {
		page, err := s.List(ctx, f)
		require.NoError(t, err)
		require.Len(t, page.Rows, count, "page %d", pageNumber+1)
		for _, row := range page.Rows {
			require.False(t, seen[row.Id], "duplicate turn %s", row.Id)
			require.Equal(t, "alice", row.OwnerID)
			seen[row.Id] = true
		}
		if pageNumber < 2 {
			require.NotEmpty(t, page.NextCursor)
		} else {
			require.Empty(t, page.NextCursor)
		}
		f.Cursor = page.NextCursor
	}
	require.Len(t, seen, 103)
}

func TestAuditPrivilegesFollowEffectiveConversationOwner(t *testing.T) {
	s := New(nil, Options{AuditorUserIDs: []string{"auditor"}}, nil)
	ctx := authctx.WithCanonicalUserID(principal(context.Background(), "alice"), "auditor")
	caps, err := s.Capabilities(ctx)
	require.NoError(t, err)
	require.Equal(t, "alice", caps.CurrentUserID)
	require.False(t, caps.CanAuditAllUsers, "canonical persistence identity must not grant another effective principal's access")
	for _, owner := range []string{"", "alice"} {
		i, _, err := s.input(ctx, Filter{ConversationID: "own", UserID: owner})
		require.NoError(t, err)
		require.Equal(t, "alice", i.OwnerID)
	}
	_, _, err = s.input(ctx, Filter{ConversationID: "own", UserID: "bob"})
	require.ErrorIs(t, err, ErrForbidden)
	ctx = authctx.WithCanonicalUserID(principal(context.Background(), "auditor"), "different-canonical-id")
	caps, err = s.Capabilities(ctx)
	require.NoError(t, err)
	require.True(t, caps.CanAuditAllUsers)
	for _, owner := range []string{"", "bob"} {
		i, _, err := s.input(ctx, Filter{ConversationID: "own", UserID: owner})
		require.NoError(t, err)
		require.Equal(t, owner, i.OwnerID)
	}
}

func TestAuditConversationDateBoundaries(t *testing.T) {
	ctx := context.Background()
	s, db := fixture(ctx, t)
	owner := principal(ctx, "alice")
	f := dates()
	for _, item := range []struct{ id, at string }{{"at-start", "2026-10-25 00:00:00"}, {"at-end", "2026-10-26 00:00:00"}} {
		_, err := db.ExecContext(ctx, "INSERT INTO conversation(id,created_by_user_id,created_at) VALUES(?,?,?)", item.id, "alice", item.at)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, "INSERT INTO turn(id,conversation_id,status) VALUES(?,?,?)", item.id, item.id, "completed")
		require.NoError(t, err)
	}
	for _, item := range []struct {
		id string
		at time.Time
	}{{"go-at-start", *f.From}, {"go-at-end", *f.To}} {
		_, err := db.ExecContext(ctx, "INSERT INTO conversation(id,created_by_user_id,created_at) VALUES(?,?,?)", item.id, "alice", item.at)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, "INSERT INTO turn(id,conversation_id,status) VALUES(?,?,?)", item.id, item.id, "completed")
		require.NoError(t, err)
	}
	f.Limit = 200
	page, err := s.List(owner, f)
	require.NoError(t, err)
	ids := []string{}
	for _, row := range page.Rows {
		ids = append(ids, row.Id)
	}
	require.Contains(t, ids, "at-start")
	require.NotContains(t, ids, "at-end")
	require.NotContains(t, ids, "old-turn")
	require.Contains(t, ids, "go-at-start")
	require.NotContains(t, ids, "go-at-end")
}

func TestAuditNativeResumptionFeedbackAndMetrics(t *testing.T) {
	ctx := principal(context.Background(), "alice")
	s, db := fixture(ctx, t)
	for _, q := range []string{
		`INSERT INTO run(id,turn_id,attempt,status,effective_user_id,started_at,completed_at,security_context,user_cred_url) VALUES('r1','t2',1,'completed','executor','2026-10-26 13:00:00','2026-10-26 13:00:01','synthetic-private-context','synthetic-private-credential')`,
		`INSERT INTO run(id,turn_id,attempt,resumed_from_run_id,status,effective_user_id,started_at,completed_at) VALUES('r2','t2',2,'r1','completed','executor','2026-10-26 13:01:00','2026-10-26 13:01:02')`,
		`INSERT INTO message(id,conversation_id,turn_id,role,type,parent_message_id,sequence,content,tool_name) VALUES('tool','own','t2','tool','tool_op','a',5,'Saved result','example/read')`,
		`INSERT INTO tool_call(message_id,op_id,tool_name,tool_kind,status) VALUES('tool','op','example/read','function','completed')`,
		`UPDATE model_call SET turn_id=NULL WHERE message_id='a'`,
	} {
		_, err := db.ExecContext(ctx, q)
		require.NoError(t, err)
	}
	d, err := s.Detail(ctx, "own", "t2")
	require.NoError(t, err)
	require.Len(t, d.Row.Runs, 2)
	require.Equal(t, "r1", *d.Row.Runs[1].ResumedFromRunID)
	require.EqualValues(t, 62000, *d.ExecutionSpanMs)
	require.Equal(t, "alice", d.Row.OwnerID, "run executor must not become conversation owner")
	require.Equal(t, 2, d.Row.ModelCalls)
	require.Equal(t, 1, d.Row.ToolCalls)
	require.Equal(t, 40, *d.Row.InputTokens, "tool join must not multiply model usage")
	b, err := json.Marshal(d)
	require.NoError(t, err)
	require.NotContains(t, string(b), "synthetic-private")
	_, err = db.ExecContext(ctx, `UPDATE turn SET feedback_rating=-1,feedback_comment='Needs correction' WHERE id='t2'`)
	require.NoError(t, err)
	d, err = s.Detail(ctx, "own", "t2")
	require.NoError(t, err)
	require.Equal(t, -1, *d.Row.FeedbackRating)
	data, err := s.CSV(ctx, Filter{ConversationID: "own"})
	require.NoError(t, err)
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	require.NoError(t, err)
	for i, column := range rows[0] {
		if column == "feedback_rating" {
			require.Equal(t, "-1", rows[1][i], "native numeric feedback remains numeric")
		}
	}
	_, err = db.ExecContext(ctx, `UPDATE model_call SET prompt_tokens=NULL,cost=NULL WHERE message_id='a'`)
	require.NoError(t, err)
	d, err = s.Detail(ctx, "own", "t2")
	require.NoError(t, err)
	require.Nil(t, d.Row.InputTokens)
	require.Nil(t, d.Row.Cost)
	require.Equal(t, 60, *d.Row.OutputTokens)
	require.NotEmpty(t, d.Warnings)
}

func TestAuditNativeBoundedPageOnLargeFixture(t *testing.T) {
	ctx := principal(context.Background(), "alice")
	s, db := fixture(ctx, t)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO turn(id,conversation_id,status) VALUES(?,'own','completed')")
	require.NoError(t, err)
	for n := 0; n < 10000; n++ {
		_, err = stmt.ExecContext(ctx, fmt.Sprintf("synthetic-%05d", n))
		require.NoError(t, err)
	}
	require.NoError(t, stmt.Close())
	require.NoError(t, tx.Commit())
	started := time.Now()
	page, err := s.List(ctx, Filter{ConversationID: "own", Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Rows, 50)
	require.NotEmpty(t, page.NextCursor)
	t.Logf("50-row page from 10,002 synthetic turns: %s", time.Since(started))
	for _, row := range page.Rows {
		require.Empty(t, row.Message)
		require.Empty(t, row.Runs)
	}
}
