package schedule

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
)

func TestFilterCompute(t *testing.T) {
	filter := &Filter{}

	t.Run("anonymous", func(t *testing.T) {
		criteria, err := filter.Compute(context.Background(), nil)
		require.NoError(t, err)
		require.Equal(t, "COALESCE(t.internal, 0) = 0 AND COALESCE(t.visibility, '') <> ?", criteria.Expression)
		require.Equal(t, []interface{}{"private"}, criteria.Placeholders)
	})

	t.Run("authenticated", func(t *testing.T) {
		ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user-1"})
		criteria, err := filter.Compute(ctx, nil)
		require.NoError(t, err)
		require.Equal(t, "COALESCE(t.internal, 0) = 0 AND (COALESCE(t.visibility, '') <> ? OR t.created_by_user_id = ?)", criteria.Expression)
		require.Equal(t, []interface{}{"private", "user-1"}, criteria.Placeholders)
	})
}
