package conversation

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
		input := &ConversationRowsInput{
			DefaultPredicate: "1",
			Has:              &ConversationRowsInputHas{DefaultPredicate: true},
		}
		ctx := context.WithValue(context.Background(), inputKey, input)
		criteria, err := filter.Compute(ctx, nil)
		require.NoError(t, err)
		require.Equal(t, "1=1", criteria.Expression)
	})

	t.Run("anonymous allows public only", func(t *testing.T) {
		input := &ConversationRowsInput{Has: &ConversationRowsInputHas{}}
		ctx := context.WithValue(context.Background(), inputKey, input)
		criteria, err := filter.Compute(ctx, nil)
		require.NoError(t, err)
		require.Equal(t, "COALESCE(c.visibility, '') <> ?", criteria.Expression)
		require.Equal(t, []interface{}{"private"}, criteria.Placeholders)
	})

	t.Run("user allows public or own", func(t *testing.T) {
		input := &ConversationRowsInput{Has: &ConversationRowsInputHas{}}
		ctx := context.WithValue(context.Background(), inputKey, input)
		ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
		criteria, err := filter.Compute(ctx, nil)
		require.NoError(t, err)
		require.Equal(t, "(COALESCE(c.visibility, '') <> ? OR c.created_by_user_id = ?)", criteria.Expression)
		require.Equal(t, []interface{}{"private", "u1"}, criteria.Placeholders)
	})
}
