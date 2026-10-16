package sdk

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/runtime/streaming"
)

func TestNativeModelWrapperRoleParityAndPayloadPreservation(t *testing.T) {
	for _, signals := range []struct{ phase, mode, want string }{{"", "", "react"}, {"intake", "", "intake"}, {"summary", "", "summary"}, {"", "router", "router"}, {"", "summary", "summary"}, {"intake", "router", "intake"}} {
		t.Run(signals.phase+"/"+signals.mode, func(t *testing.T) {
			for _, body := range []string{`{"metadata":{"asyncNarrator":true}}`, `{"options":{"metadata":{"asyncNarrator":true}}}`, `not JSON; an opaque stored body`} {
				id := "original-payload"
				payload := &conversationmodel.ModelCallStreamPayloadView{Id: id, Compression: "none", InlineBody: &body}
				message := &conversationmodel.MessageView{Id: "message", Phase: &signals.phase, Mode: &signals.mode, ModelCall: &conversationmodel.ModelCallView{
					RequestPayloadId: &id, ResponsePayloadId: &id, ProviderRequestPayloadId: &id, ProviderResponsePayloadId: &id, StreamPayloadId: &id,
					ModelCallRequestPayload: payload, ModelCallResponsePayload: payload, ModelCallProviderRequestPayload: payload, ModelCallProviderResponsePayload: payload, ModelCallStreamPayload: payload,
				}}
				step := buildModelStep(message, true)
				// Compare with the exact prior inference inputs. Root metadata is absent
				// from the typed wrapper even when the opaque inline body contains it.
				previous := executionRoleFromSignals("", signals.phase, signals.mode, "", step.RequestPayload, step.ProviderRequestPayload, step.ResponsePayload, step.ProviderResponsePayload, step.StreamPayload)
				require.Equal(t, signals.want, step.ExecutionRole)
				require.Equal(t, previous, step.ExecutionRole)
				raw, err := json.Marshal(payload)
				require.NoError(t, err)
				for _, got := range []json.RawMessage{step.RequestPayload, step.ResponsePayload, step.ProviderRequestPayload, step.ProviderResponsePayload, step.StreamPayload} {
					require.Equal(t, raw, []byte(got))
				}
				state := &ConversationState{Turns: []*TurnState{{Execution: &ExecutionState{Pages: []*ExecutionPageState{{ModelSteps: []*ModelStepState{step}}}}}}}
				omitCanonicalModelPayloads(state)
				require.Empty(t, step.RequestPayload)
				require.Empty(t, step.ResponsePayload)
				require.Empty(t, step.ProviderRequestPayload)
				require.Empty(t, step.ProviderResponsePayload)
				require.Empty(t, step.StreamPayload)
				require.Equal(t, id, step.RequestPayloadID)
				require.Equal(t, id, step.ResponsePayloadID)
				require.Equal(t, id, step.ProviderRequestPayloadID)
				require.Equal(t, id, step.ProviderResponsePayloadID)
				require.Equal(t, id, step.StreamPayloadID)
				require.Equal(t, body, *payload.InlineBody, "saved detail source must remain byte-identical")
			}
		})
	}
}

func TestLiveModelReducerStillUsesRootNarratorMetadata(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage(`{"metadata":{"asyncNarrator":true}}`), json.RawMessage(`{"options":{"metadata":{"asyncNarrator":true}}}`)} {
		step := &ModelStepState{RequestPayload: raw}
		applyModelStart(step, &streaming.Event{Phase: "intake", Mode: "router"})
		require.Equal(t, "narrator", step.ExecutionRole, "generic live metadata inference keeps its existing precedence")
		require.Equal(t, raw, step.RequestPayload)
	}
}
