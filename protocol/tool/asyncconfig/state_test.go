package asyncconfig

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	asynccfg "github.com/viant/agently-core/protocol/async"
)

func TestAsyncConfigState(t *testing.T) {
	ctx := WithState(context.Background())
	cfg := &asynccfg.Config{
		Run:    asynccfg.RunConfig{Tool: "forecasting/start"},
		Status: asynccfg.StatusConfig{Tool: "forecasting/status", OperationIDArg: "taskId"},
	}
	MarkTool(ctx, "forecasting/start", cfg)
	got, ok := ConfigFor(ctx, "forecasting/start")
	require.True(t, ok)
	require.Same(t, cfg, got)
}
