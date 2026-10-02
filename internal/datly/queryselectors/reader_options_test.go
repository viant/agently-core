package queryselectors

import (
	"context"
	"reflect"
	"testing"

	dexec "github.com/viant/datly/exec"
	sqlxread "github.com/viant/sqlx/io/read"
)

func TestForUpdateOptionsPreserveScopeAndCache(t *testing.T) {
	scope := &sqlxread.QueryScope{}
	original := dexec.ReaderOptions{RefreshCache: true, CacheOnly: true, QueryScope: scope, ForUpdate: []string{"existing"}}
	ctx := original.Context(context.Background())
	for _, enabled := range []bool{false, true} {
		got := ForUpdateOptions(ctx, enabled)
		if !got.RefreshCache || !got.CacheOnly || got.QueryScope != scope {
			t.Fatal("caller cache/query scope settings lost")
		}
		if enabled && !reflect.DeepEqual(got.ForUpdate, []string{dexec.RootView}) {
			t.Fatalf("root selection %v", got.ForUpdate)
		}
		if !enabled && len(got.ForUpdate) > 0 {
			t.Fatal("discovery read unexpectedly locks")
		}
		if enabled {
			got.ForUpdate[0] = "mutated"
		}
		if !reflect.DeepEqual(dexec.ReaderOptionsFromContext(ctx).ForUpdate, []string{"existing"}) {
			t.Fatal("caller context mutated")
		}
	}
}
