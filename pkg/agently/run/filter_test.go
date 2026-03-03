package run

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
)

func TestFilterCompute(t *testing.T) {
	filter := &Filter{}

	t.Run("missing input denies", func(t *testing.T) {
		criteria, err := filter.Compute(context.Background(), nil)
		require.NoError(t, err)
		require.Equal(t, "0=1", criteria.Expression)
	})

	t.Run("disabled default predicate bypasses", func(t *testing.T) {
		input := &RunRowsInput{
			DefaultPredicate: "1",
			Has:              &RunRowsInputHas{DefaultPredicate: true},
		}
		ctx := context.WithValue(context.Background(), inputKey, input)
		criteria, err := filter.Compute(ctx, nil)
		require.NoError(t, err)
		require.Equal(t, "1=1", criteria.Expression)
	})

	t.Run("anonymous sees public runs", func(t *testing.T) {
		input := &RunRowsInput{Has: &RunRowsInputHas{}}
		ctx := context.WithValue(context.Background(), inputKey, input)
		criteria, err := filter.Compute(ctx, nil)
		require.NoError(t, err)
		require.Equal(t, "COALESCE(t.effective_user_id, '') = ''", criteria.Expression)
	})

	t.Run("user sees public or own runs", func(t *testing.T) {
		input := &RunRowsInput{Has: &RunRowsInputHas{}}
		ctx := context.WithValue(context.Background(), inputKey, input)
		ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
		criteria, err := filter.Compute(ctx, nil)
		require.NoError(t, err)
		require.Equal(t, "(COALESCE(t.effective_user_id, '') = '' OR t.effective_user_id = ?)", criteria.Expression)
		require.Equal(t, []interface{}{"u1"}, criteria.Placeholders)
	})
}
