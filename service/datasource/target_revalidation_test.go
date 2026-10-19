package datasource

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/forge/backend/types"
)

func TestFetchRevalidatesWithSelectedWindowTarget(t *testing.T) {
	target := &types.WindowTarget{Platform: "web", FormFactor: "desktop", Surface: "browser", SelectionToken: "original-selection"}
	pin := identity.ResolvedResource{URI: "window://example/advertisers", ProviderIdentity: "provider"}
	stop := errors.New("stop before data dispatch")
	called := false
	svc := New(Options{ResolveResource: func(ctx context.Context, got identity.ResolvedResource) (*identity.ResolvedResource, error) {
		called = true
		actual, ok := requestctx.WindowTargetFromContext(ctx)
		require.True(t, ok, "the first metadata revalidation must carry the target")
		require.Equal(t, target, actual)
		require.Equal(t, pin, got)
		return nil, stop
	}})
	_, err := svc.Fetch(context.Background(), "records", nil, FetchOptions{Resource: &pin, Target: target})
	require.ErrorIs(t, err, stop)
	require.True(t, called)
	require.Equal(t, "original-selection", target.SelectionToken)
}
