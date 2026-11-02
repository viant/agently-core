package conversationaudit

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
)

func TestAuditNativePageContentMatchesDetailsAndCSV(t *testing.T) {
	ctx := principal(context.Background(), "alice")
	s, db := fixture(ctx, t)
	input := strings.Repeat("Zażółć, example input\n", 30)
	output := strings.Repeat("Final output, with multiple lines\n", 30)
	_, err := db.ExecContext(ctx, "UPDATE message SET content=? WHERE id='u'", input)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE message SET content=? WHERE id='final'", output)
	require.NoError(t, err)
	for _, statement := range []string{
		`UPDATE turn SET agent_id_used='example-agent' WHERE id='t2'`,
		`UPDATE model_call SET provider='zeta',model='reasoning',turn_id=NULL WHERE message_id='a'`,
		`UPDATE model_call SET provider='alpha',model='chat' WHERE message_id='final'`,
		`INSERT INTO message(id,conversation_id,turn_id,sequence,role,interim,content) VALUES('interim','own','t2',5,'assistant',1,'Intermediate response'),('t1-user','own','t1',1,'user',0,'Other input'),('t1-final','own','t1',2,'assistant',0,'Other output')`,
		`INSERT INTO model_call(message_id,turn_id,provider,model,model_kind,status,prompt_tokens,completion_tokens,cost) VALUES('interim','t2','alpha','chat','chat','completed',2,3,0)`,
		`INSERT INTO run(id,turn_id,attempt,status,started_at,completed_at) VALUES('r1','t2',1,'completed','2026-10-26 13:00:00','2026-10-26 13:00:01'),('r2','t2',2,'completed','2026-10-26 13:01:00','2026-10-26 13:01:02')`,
	} {
		_, err = db.ExecContext(ctx, statement)
		require.NoError(t, err)
	}
	// Observe service-level reader calls while still exercising the real Datly SQL.
	native := s.reader
	var reads []*projection.Input
	s.reader = readerFunc(func(ctx context.Context, in *projection.Input) ([]*projection.Row, error) {
		copy := *in
		reads = append(reads, &copy)
		return native.Read(ctx, in)
	})
	f := Filter{ConversationID: "own", IncludeContent: true, Limit: 1}
	page, err := s.List(ctx, f)
	require.NoError(t, err)
	require.Len(t, reads, 2, "one selection and one batch read, without per-turn Detail calls")
	require.Equal(t, []string{"t2"}, reads[1].TurnIDs, "do not hydrate the lookahead turn")
	require.True(t, reads[1].Detail)
	require.Equal(t, "alice", reads[1].OwnerID)
	require.Len(t, page.Rows, 1)
	row := page.Rows[0]
	require.Equal(t, "example-agent", *row.AgentIdUsed)
	require.Equal(t, input+"\n\nMore detail", row.Input)
	require.Equal(t, output, row.Output, "interim content must not replace the final answer")
	require.Equal(t, []string{"alpha/chat", "zeta/reasoning"}, row.Models)
	require.Equal(t, 42, *row.InputTokens)
	require.Equal(t, 63, *row.OutputTokens)
	require.EqualValues(t, 62000, *row.ExecutionSpanMs)
	require.Empty(t, row.Message)
	require.Empty(t, row.Runs)
	b, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(b), `"message":`)
	require.NotContains(t, string(b), `"runs":`)
	require.NotContains(t, string(b), "Intermediate response")
	detail, err := s.Detail(ctx, "own", "t2")
	require.NoError(t, err)
	require.Equal(t, detail.Summary, *row.Summary)
	data, err := s.CSV(ctx, f)
	require.NoError(t, err)
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	require.NoError(t, err)
	require.Equal(t, []string{"turn_created_at", "turn_id", "conversation_id", "conversation_title", "input", "output", "user_id", "agent_id", "status", "models", "input_tokens", "output_tokens", "execution_span_ms", "feedback_rating", "feedback_comment", "model_calls", "tool_calls", "conversation_created_at"}, rows[0], "export only the audit table columns, in table order")
	values := map[string]string{}
	for n, name := range rows[0] {
		values[name] = rows[1][n]
	}
	require.Equal(t, row.Input, values["input"])
	require.Equal(t, row.Output, values["output"])
	require.Equal(t, strings.Join(row.Models, ", "), values["models"])
	require.Equal(t, "62000", values["execution_span_ms"])
	require.Equal(t, row.CreatedAt.Format(time.RFC3339Nano), values["turn_created_at"])
	require.Equal(t, row.Id, values["turn_id"])
	require.Equal(t, row.ConversationId, values["conversation_id"])
	require.Equal(t, *row.ConversationTitle, values["conversation_title"])
	require.Equal(t, row.OwnerID, values["user_id"])
	require.Equal(t, *row.AgentIdUsed, values["agent_id"])
	require.Equal(t, row.Status, values["status"])
	require.Equal(t, "42", values["input_tokens"])
	require.Equal(t, "63", values["output_tokens"])
	require.Equal(t, "1", values["feedback_rating"])
	require.Equal(t, *row.FeedbackComment, values["feedback_comment"])
	require.Equal(t, "3", values["model_calls"])
	require.Equal(t, "0", values["tool_calls"])
	require.Equal(t, row.ConversationCreatedAt.Format(time.RFC3339Nano), values["conversation_created_at"])
	// The lightweight contract stays unchanged and does not load any transcripts.
	reads = nil
	light, err := s.List(ctx, Filter{ConversationID: "own"})
	require.NoError(t, err)
	require.Len(t, reads, 1)
	require.Nil(t, light.Rows[0].Summary)
	b, err = json.Marshal(light)
	require.NoError(t, err)
	require.NotContains(t, string(b), `"input":`)
	// More rows still require only one batch read, with separate canonical content.
	reads = nil
	f.Limit = 50
	page, err = s.List(ctx, f)
	require.NoError(t, err)
	require.Len(t, reads, 2)
	require.ElementsMatch(t, []string{"t2", "t1"}, reads[1].TurnIDs)
	require.Len(t, page.Rows, 2)
	require.Equal(t, "Other input", page.Rows[1].Input)
	require.Equal(t, "Other output", page.Rows[1].Output)
	require.Empty(t, page.Rows[1].Models)
	require.Nil(t, page.Rows[1].ExecutionSpanMs)
	// Cursor navigation uses the same bounded batch path.
	f.Limit = 1
	first, err := s.List(ctx, Filter{ConversationID: "own", Limit: 1})
	require.NoError(t, err)
	f.Cursor = first.NextCursor
	page, err = s.List(ctx, f)
	require.NoError(t, err)
	require.Equal(t, "t1", page.Rows[0].Id)
	require.Equal(t, "Other output", page.Rows[0].Output)
	require.Empty(t, page.NextCursor)
	// A bounded auditor page may span multiple conversations and owners.
	f = dates()
	f.Limit, f.IncludeContent = 50, true
	page, err = s.List(principal(ctx, "auditor"), f)
	require.NoError(t, err)
	require.Len(t, page.Rows, 3)
	for _, item := range page.Rows {
		if item.Id == "foreign" {
			require.Equal(t, "bob", item.OwnerID)
			require.Equal(t, -1, *item.FeedbackRating)
		}
	}
	_, err = db.ExecContext(ctx, "UPDATE model_call SET prompt_tokens=NULL WHERE message_id='a'")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE run SET completed_at=NULL WHERE id='r2'")
	require.NoError(t, err)
	page, err = s.List(ctx, Filter{ConversationID: "own", IncludeContent: true})
	require.NoError(t, err)
	require.Nil(t, page.Rows[0].InputTokens)
	require.Equal(t, 63, *page.Rows[0].OutputTokens)
	require.Nil(t, page.Rows[0].ExecutionSpanMs)
	require.NotEmpty(t, page.Rows[0].Warnings)
}

func TestAuditPageContentRejectsChangedOwnershipAndMissingTurns(t *testing.T) {
	ctx := principal(context.Background(), "alice")
	for _, tc := range []string{"owner", "conversation", "missing", "canceled"} {
		t.Run(tc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			s := New(readerFunc(func(ctx context.Context, i *projection.Input) ([]*projection.Row, error) {
				row := &projection.Row{Id: "turn", ConversationId: "own", OwnerID: "alice", Status: "completed"}
				if i.Detail {
					require.Equal(t, []string{"turn"}, i.TurnIDs)
					switch tc {
					case "owner":
						row.OwnerID = "bob"
					case "conversation":
						row.ConversationId = "other"
					case "missing":
						return nil, nil
					case "canceled":
						cancel()
						return nil, ctx.Err()
					}
				}
				return []*projection.Row{row}, nil
			}), Options{}, nil)
			page, err := s.List(ctx, Filter{ConversationID: "own", IncludeContent: true})
			if tc == "canceled" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, ErrNotFound)
			}
			require.Nil(t, page)
		})
	}
}

func TestAuditPageContentRejectsWholeOversizedPage(t *testing.T) {
	ctx := principal(context.Background(), "alice")
	content := strings.Repeat("a", MaxExportBytes/2)
	s := New(readerFunc(func(_ context.Context, i *projection.Input) ([]*projection.Row, error) {
		rows := []*projection.Row{{Id: "one", ConversationId: "own", OwnerID: "alice", Status: "completed"}, {Id: "two", ConversationId: "own", OwnerID: "alice", Status: "completed"}}
		if i.Detail {
			for _, row := range rows {
				row.Message = []*projection.Message{{Id: row.Id + "-user", Role: "user", Content: &content, CreatedAt: time.Now()}}
			}
		}
		return rows, nil
	}), Options{}, nil)
	page, err := s.List(ctx, Filter{ConversationID: "own", IncludeContent: true})
	require.ErrorIs(t, err, ErrTooLarge)
	require.Nil(t, page, "never return a successful page with truncated content")
}

func TestAuditNativeBatchKeepsPerTurnLimits(t *testing.T) {
	for _, kind := range []string{"message", "run"} {
		t.Run(kind, func(t *testing.T) {
			ctx := principal(context.Background(), "alice")
			s, db := fixture(ctx, t)
			_, err := db.ExecContext(ctx, "DELETE FROM message")
			require.NoError(t, err)
			insert := func(turn string, start, end int) {
				tx, err := db.BeginTx(ctx, nil)
				require.NoError(t, err)
				defer tx.Rollback()
				query := "INSERT INTO message(id,conversation_id,turn_id,sequence,role,content) VALUES(?,'own',?,?,'user','input')"
				if kind == "run" {
					query = "INSERT INTO run(id,turn_id,attempt,status,started_at,completed_at) VALUES(?,?,?,'completed','2026-10-26 13:00:00','2026-10-26 13:00:01')"
				}
				statement, err := tx.PrepareContext(ctx, query)
				require.NoError(t, err)
				defer statement.Close()
				for n := start; n <= end; n++ {
					_, err = statement.ExecContext(ctx, fmt.Sprintf("%s-%d", turn, n), turn, n)
					require.NoError(t, err)
				}
				require.NoError(t, tx.Commit())
			}
			insert("t1", 1, 6000)
			insert("t2", 1, 6000)
			i := &projection.Input{Allowed: true, OwnerID: "alice", ConversationID: "own", Detail: true, Limit: 2,
				TurnIDs: []string{"t1", "t2", "foreign"}, Has: &projection.InputHas{TurnIDs: true}}
			rows, err := s.reader.Read(ctx, i)
			require.NoError(t, err)
			require.Len(t, rows, 2, "IDs must not bypass the owner and conversation scope")
			for _, row := range rows {
				if kind == "message" {
					require.Len(t, row.Message, 6000, "a batch must not apply one global message limit")
				} else {
					require.Len(t, row.Runs, 6000, "a batch must not apply one global run limit")
				}
			}
			page, err := s.List(ctx, Filter{ConversationID: "own", IncludeContent: true})
			require.NoError(t, err)
			require.Len(t, page.Rows, 2)
			insert("t2", 6001, 10001)
			page, err = s.List(ctx, Filter{ConversationID: "own", IncludeContent: true})
			require.ErrorIs(t, err, ErrTooLarge, "excess content must fail instead of being silently truncated")
			require.Nil(t, page)
			_, err = s.Detail(ctx, "own", "t2")
			require.ErrorIs(t, err, ErrTooLarge)
		})
	}
}
