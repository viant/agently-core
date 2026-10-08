package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/viant/agently-core/protocol/mcp/manager"
)

func TestAuthorizationGuardRunsBeforeToolDispatch(t *testing.T) {
	mgr, err := manager.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewDefaultRegistry(mgr)
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("backend action denied")
	calls := 0
	if !SetAuthorizationGuard(registry, func(_ context.Context, name string, args map[string]interface{}) error {
		calls++
		if name != "missing:mutate" || args["id"] != "42" {
			t.Fatalf("unexpected action binding: %q %+v", name, args)
		}
		return denied
	}) {
		t.Fatal("default registry rejected authorization guard")
	}
	if _, err := registry.Execute(context.Background(), "missing:mutate|data.result", map[string]interface{}{"id": "42"}); !errors.Is(err, denied) || calls != 1 {
		t.Fatalf("tool dispatched before authorization: calls=%d err=%v", calls, err)
	}
}
