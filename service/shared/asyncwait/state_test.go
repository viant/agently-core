package asyncwait

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAsyncWaitState(t *testing.T) {
	ctx := WithState(context.Background())
	MarkAfterStatus(ctx, "sess-1")
	MarkAfterStatus(ctx, "sess-1")
	MarkAfterStatus(ctx, "sess-2")
	require.Equal(t, []string{"sess-1", "sess-2"}, ConsumeAfterStatus(ctx))
	require.Nil(t, ConsumeAfterStatus(ctx))
}
