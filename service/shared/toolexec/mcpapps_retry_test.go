package toolexec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/mcpapps"
)

func TestMCPAppsCancellationNeverRetriesNativeStep(t *testing.T) {
	reg := &scriptedRegistry{script: []scriptedResult{{err: context.Canceled}, {result: "should never execute"}}}
	ctx, _ := mcpapps.WithCapture(context.Background(), "server", "tool", "op")
	_, _, err := executeToolWithRetry(ctx, reg, StepInfo{Name: "server/tool", ID: "native-op"}, nil)
	require.Error(t, err)
	require.Equal(t, 1, reg.calls)
}
