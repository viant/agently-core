package auth

import (
	"testing"
	"time"

	"github.com/viant/agently-core/protocol/primitive/observe"
)

func TestDelegatedCredentialTimingKeepsActiveUserAndExactStoreChecks(t *testing.T) {
	store := newFakeDelegatedStore()
	store.seed(seededSampleToken(time.Now().Add(2 * time.Hour)))
	resolver := newTestResolver(t, store, sampleProviderDoc(false))
	resolver.users = &fakeUserLookup{users: map[string]*User{"uuid-1": {ID: "uuid-1"}}}
	var phases []observe.Phase
	ctx := observe.WithObserver(delegatedCtx("uuid-1"), observe.ObserverFunc(func(event observe.Event) {
		if event.Provider != "" || event.Namespace != "" || event.Kind != "" {
			t.Fatalf("auth timing leaked a label: %+v", event)
		}
		phases = append(phases, event.Phase)
	}))
	credential, err := resolver.Resolve(ctx, sampleRequirement())
	if err != nil || credential == nil || credential.Token != "sample-access" {
		t.Fatalf("delegated resolution changed: credential=%+v err=%v", credential, err)
	}
	for _, phase := range []observe.Phase{"mcp.auth.user.lookup", "mcp.auth.user", "mcp.auth.provider", "mcp.auth.schema", "mcp.auth.store", "mcp.auth.validate"} {
		found := false
		for _, observed := range phases {
			if observed == phase {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing auth timing phase %s: %v", phase, phases)
		}
	}
	resolver.users = &fakeUserLookup{users: map[string]*User{"uuid-1": {ID: "uuid-1", Disabled: true}}}
	if _, err := resolver.Resolve(ctx, sampleRequirement()); err == nil {
		t.Fatal("disabled user was allowed")
	}
	if len(phases) == 0 || phases[len(phases)-1] != "mcp.auth.user" {
		t.Fatalf("denial continued into credential store: %v", phases)
	}
}
