package predicate

import (
	"context"
	"strings"
	"testing"

	"github.com/viant/datly/sql/fragment"
	"github.com/viant/sqlx/metadata/database"
	"github.com/viant/sqlx/metadata/info"
)

func TestTokenLeasePredicateUsesDialectClock(t *testing.T) {
	for _, tc := range []struct{ dialect, clock string }{{"mysql", "UTC_TIMESTAMP()"}, {"sqlite", "DATETIME('now')"}} {
		t.Run(tc.dialect, func(t *testing.T) {
			ctx := fragment.WithDialect(context.Background(), &info.Dialect{Product: database.Product{Name: tc.dialect}})
			criteria, err := (&TokenLeasePredicate{}).Compute(ctx, "claim")
			if err != nil || criteria == nil || !strings.Contains(criteria.Expression, tc.clock) || len(criteria.Placeholders) != 0 {
				t.Fatalf("criteria=%+v err=%v", criteria, err)
			}
		})
	}
	if _, err := (&TokenLeasePredicate{}).Compute(context.Background(), "claim"); err == nil {
		t.Fatal("claim accepted a missing database dialect")
	}
}
