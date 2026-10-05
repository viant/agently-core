package mcpapps

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

func TestCaptureRetainsFullHostResultAndChecksScope(t *testing.T) {
	ctx, sink := WithCapture(context.Background(), "server", "tool", "host-op")
	var result mcpschema.CallToolResult
	require.NoError(t, json.Unmarshal([]byte(`{"resultType":"complete","content":[{"type":"text","text":"safe"}],"structuredContent":{"n":1},"_meta":{"secret":{"token":"host-only"}},"isError":false}`), &result))
	require.Error(t, Record(ctx, "other", "tool", "native-op", &result))
	require.NoError(t, Record(ctx, "server", "tool", "native-op", &result))
	raw, native := sink.Snapshot()
	require.Equal(t, "native-op", native)
	require.Contains(t, string(raw), `host-only`)
	require.Contains(t, string(raw), `structuredContent`)
	require.Contains(t, string(raw), `"isError":false`)
	result.Content = nil
	again, _ := sink.Snapshot()
	require.Equal(t, raw, again, "capture must be detached from mutable SDK result")
	require.NoError(t, Record(context.Background(), "unrelated", "tool", "", &result))
}
