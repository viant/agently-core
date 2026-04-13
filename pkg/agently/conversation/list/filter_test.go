package conversation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
)

func TestFilterCompute(t *testing.T) {
	filter := &Filter{}

	t.Run("default predicate bypasses visibility", func(t *testing.T) {
		criteria, err := filter.Compute(context.Background(), "1")
		require.NoError(t, err)
		require.Equal(t, "1=1", criteria.Expression)
	})

	t.Run("anonymous allows public only", func(t *testing.T) {
		criteria, err := filter.Compute(context.Background(), "0")
		require.NoError(t, err)
		require.Equal(t, "COALESCE(c.visibility, '') <> ?", criteria.Expression)
		require.Equal(t, []interface{}{"private"}, criteria.Placeholders)
	})

	t.Run("user allows public or own private", func(t *testing.T) {
		ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
		criteria, err := filter.Compute(ctx, "0")
		require.NoError(t, err)
		require.Equal(t, "(COALESCE(c.visibility, '') <> ? OR c.created_by_user_id = ?)", criteria.Expression)
		require.Equal(t, []interface{}{"private", "u1"}, criteria.Placeholders)
	})
}
