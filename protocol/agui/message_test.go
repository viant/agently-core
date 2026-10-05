package agui

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMessageSnapshotRoundTripPreservesAllStandardFields(t *testing.T) {
	original := []byte(`{"threadId":"thread","runId":"resume","messages":[{"id":"activity","role":"activity","activityType":"agently.turn","content":{"version":"1","nativeTurnId":"native","phase":"running"},"subagentRunId":"child","metadata":{"false":false,"n":9007199254740993}},{"id":"reasoning","role":"reasoning","content":"","encryptedValue":"opaque"},{"id":"user","role":"user","name":"","content":[{"type":"image","source":{"type":"data","value":"AA==","mimeType":"image/png"}}]}]}`)
	require.NoError(t, ValidateInput(original))
	var input RunAgentInput
	require.NoError(t, json.Unmarshal(original, &input))
	roundtrip, err := json.Marshal(input)
	require.NoError(t, err)
	require.NoError(t, ValidateInput(roundtrip))
	require.JSONEq(t, string(original), string(roundtrip))
	input.Messages[1].Content = json.RawMessage(`"updated"`)
	modified, err := json.Marshal(input)
	require.NoError(t, err)
	require.Contains(t, string(modified), `"content":"updated"`)
	require.Contains(t, string(modified), `"encryptedValue":"opaque"`)
}
