package approvalqueue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestStateApprovalQueue(t *testing.T) {
	ctx := WithState(context.Background())
	MarkTool(ctx, "system/exec:run", &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue})
	cfg, ok := ConfigFor(ctx, "system/exec:run")
	require.True(t, ok)
	require.NotNil(t, cfg)
	require.True(t, cfg.IsQueue())
	require.True(t, RequiresQueue(ctx, "system/exec:run"))
	require.False(t, RequiresPrompt(ctx, "system/exec:run"))
}

func TestStatePromptApproval(t *testing.T) {
	ctx := WithState(context.Background())
	MarkTool(ctx, "message:project", &llm.ApprovalConfig{Mode: llm.ApprovalModePrompt})
	cfg, ok := ConfigFor(ctx, "message:project")
	require.True(t, ok)
	require.NotNil(t, cfg)
	require.True(t, cfg.IsPrompt())
	require.False(t, RequiresQueue(ctx, "message:project"))
	require.True(t, RequiresPrompt(ctx, "message:project"))
}
