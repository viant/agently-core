package conversationaudit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Opt-in measurements use only an isolated synthetic database. They exercise
// the real Datly projection and count independently, without timing assertions
// that would turn host load into test failures. No index migrations are applied.
func TestAuditMySQLSyntheticLoad(t *testing.T) {
	if os.Getenv("AGENTLY_AUDIT_TEST_MYSQL_LOAD") != "1" {
		t.Skip("set AGENTLY_AUDIT_TEST_MYSQL_LOAD=1 for synthetic load measurements")
	}
	ctx := context.Background()
	s, db := mysqlFixture(ctx, t)
	for batch := 0; batch < 10; batch++ {
		var values []string
		for n := batch * 1000; n < (batch+1)*1000; n++ {
			values = append(values, fmt.Sprintf("('load-conv-%05d','load-owner-%d','2026-10-25 12:00:00')", n, n%10))
		}
		_, err := db.ExecContext(ctx, "INSERT INTO conversation(id,created_by_user_id,created_at) VALUES"+strings.Join(values, ","))
		require.NoError(t, err)
	}
	seeded := 0
	for _, size := range []int{10000, 100000} {
		for seeded < size {
			var values []string
			for n := seeded; n < seeded+1000; n++ {
				values = append(values, fmt.Sprintf("('load-%06d','load-conv-%05d','2026-10-26 14:00:00','completed')", n, n/10))
			}
			_, err := db.ExecContext(ctx, "INSERT INTO turn(id,conversation_id,created_at,status) VALUES"+strings.Join(values, ","))
			require.NoError(t, err)
			seeded += 1000
		}
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			for _, tc := range []struct {
				name, caller, owner string
				contains            *string
				want                int64
			}{
				{name: "own", caller: "load-owner-0", want: int64(size / 10)},
				{name: "admin_all", caller: "auditor", want: int64(size + 3)},
				{name: "admin_exact", caller: "auditor", owner: "load-owner-0", want: int64(size / 10)},
				{name: "admin_fragment", caller: "auditor", contains: ptr("owner-0"), want: int64(size / 10)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					caller := principal(ctx, tc.caller)
					f := dates()
					f.Limit, f.IncludeContent, f.UserID, f.UserIDContains = 10, true, tc.owner, tc.contains
					i, scope, err := s.input(caller, f)
					require.NoError(t, err)
					measure := func(label string, read func()) {
						read() // component compilation and pool warmup are not query latency
						var times []time.Duration
						for n := 0; n < 5; n++ {
							start := time.Now()
							read()
							times = append(times, time.Since(start))
						}
						sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
						t.Logf("%s median=%s max=%s", label, times[len(times)/2], times[len(times)-1])
					}
					measure("count", func() {
						total, err := s.reader.(Counter).Count(caller, i)
						require.NoError(t, err)
						require.Equal(t, tc.want, total)
					})
					measure("first_page", func() {
						page, err := s.List(caller, f)
						require.NoError(t, err)
						require.Len(t, page.Rows, 10)
					})
					// Construct an authorized midpoint cursor without traversing all pages.
					at := time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC)
					encoded, err := json.Marshal(cursor{At: at, ID: fmt.Sprintf("load-%06d", size/2), Scope: scope})
					require.NoError(t, err)
					f.Cursor = base64.RawURLEncoding.EncodeToString(encoded)
					measure("deep_page", func() {
						page, err := s.List(caller, f)
						require.NoError(t, err)
						require.Len(t, page.Rows, 10)
					})
				})
			}
		})
	}
}
