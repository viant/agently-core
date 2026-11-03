package conversationaudit

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
)

func TestAuditExplicitGrants(t *testing.T) {
	for _, tc := range []struct {
		name           string
		opts           Options
		user           string
		allowed, admin bool
	}{
		{name: "no configuration", user: "alice"},
		{name: "explicit reader", opts: Options{UserIDs: []string{"alice"}}, user: "alice", allowed: true},
		{name: "other reader", opts: Options{UserIDs: []string{"bob"}}, user: "alice"},
		{name: "wildcard reader", opts: Options{UserIDs: []string{"*"}}, user: "alice", allowed: true},
		{name: "admin alone", opts: Options{AuditorUserIDs: []string{"alice"}}, user: "alice", allowed: true, admin: true},
		{name: "overlap", opts: Options{UserIDs: []string{"*", "alice"}, AuditorUserIDs: []string{"alice"}}, user: "alice", allowed: true, admin: true},
		{name: "admin wildcard never grants", opts: Options{AuditorUserIDs: []string{"*"}}, user: "alice"},
		{name: "anonymous wildcard", opts: Options{UserIDs: []string{"*"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.user != "" {
				ctx = principal(ctx, tc.user)
			}
			reads := 0
			s := New(readerFunc(func(context.Context, *projection.Input) ([]*projection.Row, error) { reads++; return nil, nil }), tc.opts, nil)
			caps, err := s.Capabilities(ctx)
			if tc.user == "" {
				require.ErrorIs(t, err, ErrUnauthenticated)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.allowed, caps.CanAudit)
				require.Equal(t, tc.admin, caps.CanAuditAllUsers)
			}
			_, err = s.List(ctx, Filter{ConversationID: "own"})
			if tc.allowed {
				require.NoError(t, err)
				require.Equal(t, 1, reads)
				return
			}
			if tc.user == "" {
				require.ErrorIs(t, err, ErrUnauthenticated)
			} else {
				require.ErrorIs(t, err, ErrForbidden)
			}
			_, err = s.CSV(ctx, Filter{ConversationID: "own"})
			require.Error(t, err)
			_, err = s.JSON(ctx, Filter{ConversationID: "own"})
			require.Error(t, err)
			_, err = s.Detail(ctx, "own", "t2")
			require.Error(t, err)
			_, err = s.Payload(ctx, "own", "t2", "payload")
			require.Error(t, err)
			require.Zero(t, reads, "denied callers cannot consult the reader")
		})
	}
}

func TestAuditOwnerFilterAuthorizationAndCursorScope(t *testing.T) {
	s := New(nil, Options{UserIDs: []string{"alice"}, AuditorUserIDs: []string{"auditor"}}, nil)
	for _, fragment := range []string{"alice", "", "*"} {
		_, _, err := s.input(principal(context.Background(), "alice"), Filter{ConversationID: "own", UserIDContains: ptr(fragment)})
		require.ErrorIs(t, err, ErrForbidden)
	}
	ctx := principal(context.Background(), "auditor")
	for _, f := range []Filter{
		{ConversationID: "own", UserIDContains: ptr("")},
		{ConversationID: "own", UserIDContains: ptr(" ")},
		{ConversationID: "own", UserID: "alice", UserIDContains: ptr("ali")},
		{ConversationID: "own", UserIDContains: ptr(strings.Repeat("a", 257))},
	} {
		_, _, err := s.input(ctx, f)
		require.ErrorIs(t, err, ErrFilter)
	}
	s, _ = fixture(ctx, t)
	f := dates()
	f.UserIDContains = ptr("ali")
	page, err := s.List(ctx, f)
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)
	f.Cursor = page.NextCursor
	f.UserIDContains = ptr("al")
	_, err = s.List(ctx, f)
	require.ErrorIs(t, err, ErrFilter)
	f.UserIDContains = nil
	f.UserID = "alice"
	_, err = s.List(ctx, f)
	require.ErrorIs(t, err, ErrFilter)
	f.UserID = ""
	f.UserIDContains = ptr("ali")
	otherAdmin := New(s.reader, Options{AuditorUserIDs: []string{"other-admin"}}, nil)
	_, err = otherAdmin.List(principal(ctx, "other-admin"), f)
	require.ErrorIs(t, err, ErrFilter)
}

func TestAuditNativeLiteralOwnerFragments(t *testing.T) {
	ctx := principal(context.Background(), "auditor")
	s, db := fixture(ctx, t)
	checkLiteralOwnerFragments(ctx, t, s, db)
}

// The optional MySQL check creates and drops its own uniquely named synthetic
// database, never reusing the database in the supplied connection settings.
func TestAuditMySQLLiteralOwnerFragments(t *testing.T) {
	ctx := principal(context.Background(), "auditor")
	s, db := mysqlFixture(ctx, t)
	checkLiteralOwnerFragments(ctx, t, s, db)
}

func mysqlFixture(ctx context.Context, t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("AGENTLY_AUDIT_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set AGENTLY_AUDIT_TEST_MYSQL_DSN for an isolated MySQL check")
	}
	cfg, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	cfg.DBName = ""
	cfg.ParseTime, cfg.MultiStatements = true, true
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	database := fmt.Sprintf("agently_audit_synthetic_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+database)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := admin.ExecContext(ctx, "DROP DATABASE "+database)
		require.NoError(t, cleanupErr)
	})
	cfg.DBName = database
	return databaseFixture(ctx, t, "mysql", cfg.FormatDSN(), "../../script/mysql/schema.ddl")
}

func checkLiteralOwnerFragments(ctx context.Context, t *testing.T, s *Service, db *sql.DB) {
	t.Helper()
	for n, owner := range []string{"team_Alice", "team_bob", "ops%user", "ops_user", "ops*user", "ops!user", "ops'user"} {
		id := fmt.Sprintf("owner-%d", n)
		_, err := db.ExecContext(ctx, "INSERT INTO conversation(id,created_by_user_id,created_at) VALUES(?,?,'2026-10-25 12:00:00')", id, owner)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, "INSERT INTO turn(id,conversation_id,created_at,status) VALUES(?,?,'2026-10-27 13:00:00','completed')", id, id)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, "INSERT INTO message(id,conversation_id,turn_id,role,content) VALUES(?,?,?,'user',?)", id, id, id, "Input for "+owner)
		require.NoError(t, err)
	}
	for _, tc := range []struct {
		fragment string
		owners   []string
	}{
		{"ALI", []string{"alice", "team_Alice"}}, {"team_", []string{"team_Alice", "team_bob"}},
		{"%", []string{"ops%user"}}, {"_", []string{"team_Alice", "team_bob", "ops_user"}},
		{"*", []string{"ops*user"}}, {"!", []string{"ops!user"}}, {"'", []string{"ops'user"}},
		{"' OR 1=1 --", nil}, {"no-such-owner", nil},
	} {
		t.Run(tc.fragment, func(t *testing.T) {
			f := dates()
			f.UserIDContains = ptr(tc.fragment)
			f.Limit = 1
			f.IncludeContent = true
			f.IncludeTotal = true
			var ids []string
			owners := map[string]bool{}
			for {
				page, err := s.List(ctx, f)
				require.NoError(t, err)
				var expected int64
				for _, owner := range tc.owners {
					if owner == "alice" {
						expected += 2
					} else {
						expected++
					}
				}
				require.Equal(t, expected, *page.TotalCount)
				for _, row := range page.Rows {
					ids = append(ids, row.Id)
					owners[row.OwnerID] = true
				}
				if page.NextCursor == "" {
					break
				}
				f.Cursor = page.NextCursor
			}
			actual := []string{}
			for owner := range owners {
				actual = append(actual, owner)
			}
			require.ElementsMatch(t, tc.owners, actual)
			f.Cursor = ""
			data, err := s.CSV(ctx, f)
			require.NoError(t, err)
			records, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
			require.NoError(t, err)
			require.Len(t, records, len(ids)+1)
			data, err = s.JSON(ctx, f)
			require.NoError(t, err)
			var jsonRows []exportRecord
			require.NoError(t, json.Unmarshal(data, &jsonRows))
			require.Len(t, jsonRows, len(ids))
			for n, row := range jsonRows {
				require.Equal(t, ids[n], row.TurnID)
				require.True(t, owners[row.UserID])
			}
			exported := []string{}
			for _, record := range records[1:] {
				exported = append(exported, record[1])
				require.True(t, owners[record[6]])
			}
			require.ElementsMatch(t, ids, exported, "CSV and the paged table must use the same owner scope")
		})
	}
	f := dates()
	f.UserID = "team_Alice"
	f.Limit = 50
	page, err := s.List(ctx, f)
	require.NoError(t, err)
	require.Len(t, page.Rows, 1)
	require.Equal(t, "team_Alice", page.Rows[0].OwnerID)
	// Database collations must not widen an exact identity grant on MySQL.
	for _, owner := range []string{"TEAM_ALICE", "team_alice"} {
		own := New(s.reader, Options{UserIDs: []string{owner}}, nil)
		ownFilter := dates()
		ownFilter.Limit = 50
		page, err := own.List(principal(ctx, owner), ownFilter)
		require.NoError(t, err)
		require.Empty(t, page.Rows)
		_, err = own.Detail(principal(ctx, owner), "owner-0", "owner-0")
		require.ErrorIs(t, err, ErrNotFound)
	}
}
