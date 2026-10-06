package toolexec

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/runtime/evidence"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type evidenceTestHook struct {
	prepare   func(context.Context, string, string, json.RawMessage) (json.RawMessage, bool, error)
	completed func(context.Context, string, string) (json.RawMessage, bool, error)
}

func (h evidenceTestHook) Prepare(ctx context.Context, name, id string, b json.RawMessage) (json.RawMessage, bool, error) {
	return h.prepare(ctx, name, id, b)
}
func (h evidenceTestHook) Completed(ctx context.Context, name, id string) (json.RawMessage, bool, error) {
	return h.completed(ctx, name, id)
}

func TestEvidenceEffectiveRequestIsCapturedAndDispatchedBeforeReceipt(t *testing.T) {
	conv := &stubConv{}
	reg := &scriptedRegistry{script: []scriptedResult{{result: `{"status":"ok"}`}}}
	original := map[string]interface{}{"Request": map[string]interface{}{"from": "2026-10-02"}}
	completed := false
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "evidence", TurnID: "effective"})
	ctx = evidence.WithTools(ctx, evidenceTestHook{
		prepare: func(_ context.Context, name, id string, raw json.RawMessage) (json.RawMessage, bool, error) {
			require.Equal(t, "op", id)
			require.JSONEq(t, `{"Request":{"from":"2026-10-02"}}`, string(raw))
			return json.RawMessage(`{"Request":{"from":"2026-10-02","evidenceAudienceId":9007199254740993}}`), true, nil
		},
		completed: func(_ context.Context, name, id string) (json.RawMessage, bool, error) {
			require.Equal(t, "op", id)
			found := false
			for _, call := range conv.patchedToolCalls {
				if call.OpID == id && call.Status == "completed" {
					found = true
				}
			}
			require.True(t, found, "receipt before terminal durable write")
			completed = true
			return json.RawMessage(`{"receipt":"op"}`), true, nil
		},
	})
	out, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "op", Name: "example/Convert", Args: original}, conv)
	require.NoError(t, err)
	require.True(t, completed)
	require.Equal(t, `{"receipt":"op"}`, out.Result)
	dispatched, err := json.Marshal(reg.lastArgs)
	require.NoError(t, err)
	require.JSONEq(t, `{"Request":{"from":"2026-10-02","evidenceAudienceId":9007199254740993}}`, string(dispatched))
	found := false
	for _, payload := range conv.patchedPayloads {
		if payload.Kind == "tool_request" && payload.InlineBody != nil {
			found = true
			require.JSONEq(t, string(dispatched), apiconv.DecodeInlineBody(string(*payload.InlineBody), payload.Compression))
		}
	}
	require.True(t, found)
	require.Len(t, original["Request"], 1, "server injection mutated original model input")
}

func TestEvidenceRejectedRequestNeverDispatchesOrPersists(t *testing.T) {
	conv := &stubConv{}
	reg := &scriptedRegistry{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "evidence", TurnID: "rejected"})
	ctx = evidence.WithTools(ctx, evidenceTestHook{
		prepare: func(context.Context, string, string, json.RawMessage) (json.RawMessage, bool, error) {
			return nil, true, fmt.Errorf("wrong selected audience")
		},
		completed: func(context.Context, string, string) (json.RawMessage, bool, error) {
			t.Fatal("rejected request completed")
			return nil, false, nil
		},
	})
	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "rejected", Name: "example/Convert"}, conv)
	require.ErrorContains(t, err, "wrong selected audience")
	require.Zero(t, reg.calls)
	require.Empty(t, conv.patchedPayloads)
}
