package native_test

import (
	"context"
	"database/sql"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/conversation/read"
	convstore "github.com/viant/agently-core/internal/store/conversation"
	dexec "github.com/viant/datly/exec"
)

func TestWorkspaceRuntimeConversationSearchSQLite(t *testing.T) {
	store, _, db := orphanFixture(t)
	runConversationSearch(t, store.Invoker, db, "search-sqlite-")
}
func TestWorkspaceRuntimeConversationSearchMySQL(t *testing.T) {
	store, _, db, prefix := orphanMySQLFixture(t)
	runConversationSearch(t, store.Invoker, db, prefix+"search-")
}
func runConversationSearch(t *testing.T, invoker dexec.ComponentInvoker, db *sql.DB, prefix string) {
	t.Helper()
	owner := prefix + "owner"
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: owner})
	store := &convstore.Store{Invoker: invoker, OwnerID: authctx.EffectiveUserID}
	ids := []string{prefix + "needle-id", prefix + "title", prefix + "summary", prefix + "boundary-part", prefix + "plain", prefix + "foreign-needle"}
	rows := []struct {
		title, summary any
		user           string
	}{
		{"plain", nil, owner}, {"NeEdLe title", nil, owner}, {nil, "summary NEEDLE", owner}, {"two", nil, owner}, {nil, nil, owner}, {"needle", nil, prefix + "other"},
	}
	for i, row := range rows {
		_, err := db.Exec("INSERT INTO conversation(id,title,summary,visibility,created_by_user_id,created_at,status) VALUES(?,?,?,'private',?,?,'succeeded')", ids[i], row.title, row.summary, row.user, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		require.NoError(t, err)
	}
	for _, test := range []struct {
		name, query string
		present     bool
		want        []string
	}{
		{"trim and lower", "  NeEdLe  ", true, ids[:3]},
		{"no cross-column match", "boundary-part two", true, nil},
		{"literal bound text", "needle%' OR 1=1 --", true, nil},
		{"wildcard preserves owner scope", "%", true, ids[:5]},
		{"empty presence", "", true, ids[:5]},
		{"whitespace presence", "  \t ", true, ids[:5]},
		{"query absent", "", false, ids[:5]},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := &read.ConversationInput{}
			input.SetIds(ids)
			if test.present {
				input.SetQuery(test.query)
			}
			result, err := store.ListPage(ctx, input, 100, false, true)
			require.NoError(t, err)
			var got []string
			for _, row := range result {
				got = append(got, row.Id)
			}
			want := append([]string(nil), test.want...)
			sort.Strings(got)
			sort.Strings(want)
			require.Equal(t, want, got)
		})
	}
}
