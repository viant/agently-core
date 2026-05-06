package planner

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOutput(t *testing.T) {
	t.Run("plain json", func(t *testing.T) {
		out, err := Parse(`{"strategyFamily":"troubleshoot","toolBundles":["analyst-tools"]}`)
		require.NoError(t, err)
		require.NotNil(t, out)
		require.Equal(t, "troubleshoot", out.StrategyFamily)
		require.Equal(t, []string{"analyst-tools"}, out.ToolBundles)
	})

	t.Run("fenced json", func(t *testing.T) {
		out, err := Parse("```json\n{\"templateId\":\"dashboard\"}\n```")
		require.NoError(t, err)
		require.NotNil(t, out)
		require.Equal(t, "dashboard", out.TemplateID)
	})

	t.Run("embedded json", func(t *testing.T) {
		out, err := Parse("planner output:\n{\"requiredEvidence\":[\"baseline\"],\"executionOrder\":[\"baseline\"]}\nthanks")
		require.NoError(t, err)
		require.NotNil(t, out)
		require.Equal(t, []string{"baseline"}, out.RequiredEvidence)
		require.Equal(t, []string{"baseline"}, out.ExecutionOrder)
	})
}

func TestService_Run(t *testing.T) {
	svc := New()
	out, errs, err := svc.Run(`{"strategyFamily":"troubleshoot"}`, ValidationContext{})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Empty(t, errs)
	require.Equal(t, "troubleshoot", out.StrategyFamily)
}
