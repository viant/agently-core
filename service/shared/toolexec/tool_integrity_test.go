package toolexec

import (
	"context"
	"errors"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestExecuteToolStep_PreflightFailurePreservesIdentity(t *testing.T) {
	cause := errors.New("credential preflight unavailable")
	reg := &scriptedRegistry{preflight: []error{cause}}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "integrity", TurnID: "preflight"})
	call, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "original-call", Name: "forecast/cube", Args: map[string]interface{}{"year": 2027}}, &stubConv{})
	require.ErrorIs(t, err, cause)
	var infra *InfrastructureError
	require.ErrorAs(t, err, &infra)
	require.Equal(t, "original-call", call.ID)
	require.Equal(t, "forecast/cube", call.Name)
	require.Equal(t, 2027, call.Arguments["year"])
	require.Empty(t, call.Result)
}

func TestExecuteToolStep_ToolFailurePersistsOnePairedOutput(t *testing.T) {
	cause := errors.New("forecast tool rejected input")
	reg := &scriptedRegistry{script: []scriptedResult{{err: cause}}}
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "integrity", TurnID: "failed-tool"})
	call, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "original-call", Name: "forecast/cube"}, conv)
	require.ErrorIs(t, err, cause)
	var infra *InfrastructureError
	require.False(t, errors.As(err, &infra), "a durable truthful tool failure is replayable")
	require.Equal(t, "original-call", call.ID)
	require.Equal(t, cause.Error(), call.Result)
	outputs := 0
	for _, row := range conv.patchedToolCalls {
		if row.CompletedAt != nil {
			outputs++
			require.Equal(t, "original-call", row.OpID)
			require.Equal(t, "failed", row.Status)
			require.NotNil(t, row.ResponsePayloadID)
		}
	}
	require.Equal(t, 1, outputs)
}

func TestExecuteToolStep_TerminalPersistenceFailureIsInfrastructure(t *testing.T) {
	cause := errors.New("terminal tool store unavailable")
	conv := &stubConv{failPatchToolCallAt: map[int]error{3: cause, 4: cause, 5: cause}}
	reg := &scriptedRegistry{script: []scriptedResult{{result: "executed once"}}}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "integrity", TurnID: "terminal-failure"})
	call, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "terminal-call", Name: "forecast/cube", Args: map[string]interface{}{"index": 1}}, conv)
	require.ErrorIs(t, err, cause)
	var infra *InfrastructureError
	require.ErrorAs(t, err, &infra)
	require.Equal(t, "terminal-call", call.ID)
	require.Equal(t, "executed once", call.Result)
	require.Equal(t, 1, reg.calls, "the side effect must execute only once")
}

type failedResponsePayloadClient struct {
	apiconv.Client
	cause error
}

func (c *failedResponsePayloadClient) PatchPayload(context.Context, *apiconv.MutablePayload) error {
	return c.cause
}

func TestPersistCoalescedToolResult_DoesNotHidePersistenceFailureBehindToolError(t *testing.T) {
	cause := errors.New("response payload store unavailable")
	conv := &failedResponsePayloadClient{Client: &stubConv{}, cause: cause}
	err := persistCoalescedToolResult(context.Background(), conv, memory.TurnMeta{ConversationID: "integrity", TurnID: "coalesced-failure"}, time.Now(), StepInfo{ID: "coalesced-call", Name: "forecast/cube"}, nil, "", errors.New("tool failed"))
	require.ErrorIs(t, err, cause)
}

func TestExecuteToolStep_RequestPayloadLinkFailureStopsBeforeTool(t *testing.T) {
	cause := errors.New("request payload link write locked")
	conv := &stubConv{failPatchToolCallAt: map[int]error{2: cause}}
	reg := &scriptedRegistry{script: []scriptedResult{{result: "must not execute"}}}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "integrity", TurnID: "request-link-failure"})
	call, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "request-link-call", Name: "forecast/cube", Args: map[string]interface{}{"scope": []int{101}}}, conv)
	require.ErrorIs(t, err, cause)
	var infra *InfrastructureError
	require.ErrorAs(t, err, &infra)
	require.Zero(t, reg.calls, "tool must not run without its durable request link")
	require.Equal(t, "request-link-call", call.ID)
}
