package tools

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/toolselection"
)

func TestListDefaultsToSevenAndHonorsExplicitLimit(t *testing.T) {
	items := make([]llm.Tool, 12)
	for i := range items {
		items[i].Definition.Name = fmt.Sprintf("example/read%02d", i)
	}
	ctx := toolselection.WithState(context.Background(), items)
	list, _ := New().Method("list")
	first := &ListOutput{}
	require.NoError(t, list(ctx, &ListInput{}, first))
	require.Len(t, first.Tools, 7)
	require.Equal(t, 12, first.Total)
	require.False(t, first.Complete)
	next := &ListInput{}
	next.Cursor = first.NextCursor
	last := &ListOutput{}
	require.NoError(t, list(ctx, next, last))
	require.Len(t, last.Tools, 5)
	require.True(t, last.Complete)
	require.NoError(t, list(ctx, &ListInput{Limit: 3}, last))
	require.Len(t, last.Tools, 3)
}
