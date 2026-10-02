package predicate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConversationSearchBindsValues(t *testing.T) {
	criteria, err := (&ConversationSearch{}).Compute(context.Background(), "  MiXeD%'  ")
	require.NoError(t, err)
	require.Equal(t, []any{"%mixed%'%", "%mixed%'%", "%mixed%'%"}, criteria.Placeholders)
	require.NotContains(t, criteria.Expression, "mixed")
	require.Equal(t, 3, len(criteria.Placeholders))
	criteria, err = (&ConversationSearch{}).Compute(context.Background(), " \t ")
	require.NoError(t, err)
	require.Equal(t, "1=1", criteria.Expression)
	require.Empty(t, criteria.Placeholders)
	_, err = (&ConversationSearch{}).Compute(context.Background(), true)
	require.ErrorContains(t, err, "requires a string")
}
