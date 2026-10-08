package resource

import (
	"context"
	"github.com/stretchr/testify/require"
	window "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/agently-core/workspace"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalYAMLNestedCanonicalPathsArePreserved(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, workspace.KindForgeWindow, "deliver", "orders.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("windowKey: deliver/orders\nview:\n  content: {id: nested-orders}\n"), 0600))
	actor := identity.VerifiedActor{Subject: "alice", Issuer: "https://idp.example", TenantID: "platform", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Minute)}
	bindings, e := WorkspaceWindowBindings(root, "local-yaml", []window.ResourceBinding{{WindowKey: "deliver/orders", URI: "window://platform/deliver/orders"}}, func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return localFixturePolicy{actor: a}, nil
	}, nil)
	require.NoError(t, e)
	local, e := NewLocalProvider(LocalConfig{ProviderIdentity: "local-yaml", Actor: func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, Verify: func(context.Context, identity.VerifiedActor) error { return nil }, Authorize: func(_ context.Context, _ identity.VerifiedActor, u identity.ResourceURI, _ string) error {
		if u.Namespace != "platform" || u.Name != "deliver/orders" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: bindings, Validators: map[string]LocalResourceValidator{"window": ValidateWindowBundle}})
	require.NoError(t, e)
	got, e := local.Get(context.Background(), "window", primitive.GetRequest{URI: "window://platform/deliver/orders", Revision: "working"})
	require.NoError(t, e)
	require.Equal(t, "deliver/orders", got.Resource.Name)
	require.Equal(t, "window://platform/deliver/orders", got.ResolvedResource.URI)
	for _, name := range []string{"../orders", "deliver/../orders", "deliver//orders", "deliver/%2e%2e/orders", "/deliver/orders", "deliver/orders/"} {
		_, e = WorkspaceWindowBindings(root, "local-yaml", []window.ResourceBinding{{WindowKey: name, URI: "window://platform/deliver/orders"}}, func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
			return localFixturePolicy{actor: a}, nil
		}, nil)
		require.Error(t, e, name)
	}
}
