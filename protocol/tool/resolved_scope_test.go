package tool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestResolvedToolAllowed(t *testing.T) {
	ctx := WithResolvedToolDefinitions(context.Background(), []*llm.ToolDefinition{
		{Name: "resources:read"},
		{Name: "system/patch:apply"},
	})

	allowed, scoped := ResolvedToolAllowed(ctx, "resources-read")
	require.True(t, scoped)
	require.True(t, allowed)

	allowed, scoped = ResolvedToolAllowed(ctx, "system_patch-apply")
	require.True(t, scoped)
	require.True(t, allowed)

	allowed, scoped = ResolvedToolAllowed(ctx, "system/exec:execute")
	require.True(t, scoped)
	require.False(t, allowed)
}

func TestResolvedToolAllowed_EmptyScopeAllowsNothingButReportsScoped(t *testing.T) {
	ctx := WithResolvedToolNames(context.Background(), nil)

	allowed, scoped := ResolvedToolAllowed(ctx, "resources:read")
	require.True(t, scoped)
	require.False(t, allowed)
}

func TestResolvedToolAllowed_NoScope(t *testing.T) {
	allowed, scoped := ResolvedToolAllowed(context.Background(), "resources:read")
	require.False(t, scoped)
	require.True(t, allowed)
}
