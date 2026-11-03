package conversationaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	countview "github.com/viant/agently-core/internal/datly/conversationaudit/count"
	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
)

func TestAuditNativeTotalScopeAndPagination(t *testing.T) {
	ctx := context.Background()
	s, db := fixture(ctx, t)
	for _, tc := range []struct {
		name, user, owner, conversation string
		contains                        *string
		want                            int64
	}{
		{name: "ordinary", user: "alice", want: 2},
		{name: "admin all", user: "auditor", want: 3},
		{name: "admin exact", user: "auditor", owner: "bob", want: 1},
		{name: "admin exact case", user: "auditor", owner: "Alice", want: 0},
		{name: "admin fragment", user: "auditor", contains: ptr("ALI"), want: 2},
		{name: "literal wildcard", user: "auditor", contains: ptr("%"), want: 0},
		{name: "exact old conversation", user: "alice", conversation: "old", want: 1},
		{name: "foreign conversation", user: "alice", conversation: "other", want: 0},
		{name: "empty", user: "alice", conversation: "missing", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := dates()
			f.IncludeTotal, f.UserID, f.UserIDContains, f.ConversationID = true, tc.owner, tc.contains, tc.conversation
			page, err := s.List(principal(ctx, tc.user), f)
			require.NoError(t, err)
			require.NotNil(t, page.TotalCount)
			require.Equal(t, tc.want, *page.TotalCount)
			if page.NextCursor != "" {
				f.Cursor = page.NextCursor
				next, err := s.List(principal(ctx, tc.user), f)
				require.NoError(t, err)
				require.Equal(t, tc.want, *next.TotalCount, "count ignores the cursor")
			}
		})
	}
	// Verify that totals do not inherit the projection's 201 candidate cap.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	for n := 0; n < 250; n++ {
		_, err = tx.ExecContext(ctx, "INSERT INTO turn(id,conversation_id,created_at,status) VALUES(?,'own','2026-10-26 14:00:00','completed')", fmt.Sprintf("count-%03d", n))
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	f := dates()
	f.IncludeTotal = true
	page, err := s.List(principal(ctx, "alice"), f)
	require.NoError(t, err)
	require.EqualValues(t, 252, *page.TotalCount)
	require.Len(t, page.Rows, 1)
	f.IncludeTotal = false
	page, err = s.List(principal(ctx, "alice"), f)
	require.NoError(t, err)
	b, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(b), "totalCount")
}

func TestAuditCountFailsClosed(t *testing.T) {
	ctx := context.Background()
	require.Error(t, (&countview.Input{ConversationID: "own"}).Init(ctx))
	require.Error(t, (&countview.Input{Allowed: true}).Init(ctx))
	reads := 0
	s := New(readerFunc(func(context.Context, *projection.Input) ([]*projection.Row, error) {
		reads++
		return nil, nil
	}), Options{UserIDs: []string{"alice"}}, nil)
	for _, user := range []string{"", "outsider"} {
		_, err := s.List(principal(ctx, user), Filter{ConversationID: "own", IncludeTotal: true})
		require.Error(t, err)
	}
	_, err := s.List(principal(ctx, "alice"), Filter{ConversationID: "own", UserID: "bob", IncludeTotal: true})
	require.ErrorIs(t, err, ErrForbidden)
	require.Zero(t, reads)
}
