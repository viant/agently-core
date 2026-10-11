package mcpapps

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

func completedReceipt(t *testing.T) (*CompletionReceipt, json.RawMessage) {
	t.Helper()
	ctx, r := WithCompletionReceipt(context.Background(), "owner", "conversation", "turn", "host-op", "server", "tool")
	ctx, c := WithCapture(ctx, "server", "tool", "host-op")
	ctx = WithRecorder(ctx, func(result json.RawMessage, native string) error { r.RecordDurable(native, result); return nil })
	var result mcpschema.CallToolResult
	require.NoError(t, json.Unmarshal([]byte(`{"resultType":"complete","content":[{"type":"text","text":"done"}],"structuredContent":{"count":1}}`), &result))
	require.NoError(t, Record(ctx, "server", "tool", "native-op", &result))
	RecordCompletion(ctx, "owner", "conversation", "turn")
	raw, _ := c.Snapshot()
	return r, raw
}
func TestCompletionReceiptRejectsDifferentIdentityEffectAndResult(t *testing.T) {
	r, raw := completedReceipt(t)
	valid := []string{"owner", "conversation", "turn", "host-op", "native-op", "succeeded"}
	require.True(t, r.Matches(valid[0], valid[1], valid[2], valid[3], valid[4], valid[5], raw))
	for i, label := range []string{"principal", "conversation", "turn", "host operation", "native effect", "status"} {
		t.Run(label, func(t *testing.T) {
			changed := append([]string(nil), valid...)
			changed[i] = "different"
			require.False(t, r.Matches(changed[0], changed[1], changed[2], changed[3], changed[4], changed[5], raw))
		})
	}
	require.False(t, r.Matches("owner", "conversation", "turn", "host-op", "native-op", "succeeded", json.RawMessage(`{"structuredContent":{"count":2}}`)))
	_, another := WithCompletionReceipt(context.Background(), "owner", "conversation", "turn", "host-op", "server", "tool")
	require.False(t, another.Matches("owner", "conversation", "turn", "host-op", "native-op", "succeeded", raw), "proof never transfers to a new request")
}
func TestCompletionReceiptRequiresDurableCaptureAndSuccessfulFinalization(t *testing.T) {
	for _, mode := range []string{"no recorder", "failed capture", "not finalized", "foreign principal", "foreign turn", "foreign operation", "foreign server", "foreign tool", "tool error", "conflicting receipt"} {
		t.Run(mode, func(t *testing.T) {
			ctx, r := WithCompletionReceipt(context.Background(), "owner", "conversation", "turn", "host-op", "server", "tool")
			operation := "host-op"
			if mode == "foreign operation" {
				operation = "other-op"
			}
			server, tool := "server", "tool"
			if mode == "foreign server" {
				server = "other-server"
			}
			if mode == "foreign tool" {
				tool = "other-tool"
			}
			ctx, c := WithCapture(ctx, server, tool, operation)
			if mode != "no recorder" {
				ctx = WithRecorder(ctx, func(raw json.RawMessage, native string) error {
					if mode == "failed capture" {
						return errors.New("durable append failed")
					}
					r.RecordDurable(native, raw)
					return nil
				})
			}
			var result mcpschema.CallToolResult
			require.NoError(t, json.Unmarshal([]byte(`{"resultType":"complete","content":[],"isError":false}`), &result))
			if mode == "tool error" {
				yes := true
				result.IsError = &yes
			}
			err := Record(ctx, server, tool, "native-op", &result)
			if mode == "failed capture" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			principal, turn := "owner", "turn"
			if mode == "foreign principal" {
				principal = "foreign"
			}
			if mode == "foreign turn" {
				turn = "foreign"
			}
			if mode != "not finalized" {
				RecordCompletion(ctx, principal, "conversation", turn)
			}
			raw, _ := c.Snapshot()
			if mode == "conflicting receipt" {
				r.RecordDurable("other-native", raw)
			}
			require.False(t, r.Matches("owner", "conversation", "turn", "host-op", "native-op", "succeeded", raw))
		})
	}
}
