package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	resources "github.com/viant/agently-core/service/resource"
)

// The same native payload and tool/message records used by normal execution
// are read through the real Datly transcript and lazy payload endpoints.
func TestNativeStoredLargeToolHistoryCompactProjectionAndLazyDetail(t *testing.T) {
	c := newDatlyObservedClient(t, 8)
	ctx := recoveryContext()
	require.NoError(t, nativeTestTurn(ctx, c, "saved", "succeeded"))
	require.NoError(t, nativeTestMessage(ctx, c, "saved", "assistant", "assistant", "Visible final answer"))
	var body strings.Builder
	body.Grow(131072)
	for chunk := 0; body.Len() < 131072; chunk++ {
		fmt.Fprintf(&body, "CHUNK-%04d LARGE_RESULT_SENTINEL %s\n", chunk, strings.Repeat("x", 48))
	}
	raw, err := json.Marshal(map[string]string{"body": body.String()})
	require.NoError(t, err)
	require.NoError(t, nativeTestMessage(ctx, c, "saved", "large-tool", "tool", string(raw)))
	payload := convstore.NewPayload()
	payload.SetId("large-tool-response")
	payload.SetKind("tool_response")
	payload.SetMimeType("application/json")
	payload.SetStorage("inline")
	payload.SetInlineBody(raw)
	payload.SetSizeBytes(len(raw))
	require.NoError(t, c.conv.PatchPayload(ctx, payload))
	call := convstore.NewToolCall()
	call.SetMessageID("large-tool")
	call.SetOpID("large-call")
	call.SetToolName("ownedhistorylarge:large_result")
	call.SetStatus("completed")
	call.SetToolKind("function")
	call.ResponsePayloadID = &payload.Id
	call.Has.ResponsePayloadID = true
	require.NoError(t, c.conv.PatchToolCall(ctx, call))
	read := func(query string) (*ConversationStateResponse, int) {
		request := httptest.NewRequest("GET", "/transcript/thread?includeModelCalls=true&includeToolCalls=true&includeModelPayloads=false"+query, nil).WithContext(ctx)
		request.SetPathValue("id", "thread")
		response := httptest.NewRecorder()
		handleGetTranscript(c.native)(response, request)
		require.Equal(t, 200, response.Code, response.Body.String())
		var state ConversationStateResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &state))
		return &state, response.Body.Len()
	}
	find := func(state *ConversationStateResponse) *ToolStepState {
		for _, turn := range state.Conversation.Turns {
			if turn.Execution != nil {
				for _, page := range turn.Execution.Pages {
					for _, step := range page.ToolSteps {
						if step.ToolMessageID == "large-tool" {
							return step
						}
					}
				}
			}
		}
		t.Fatal("missing native tool step")
		return nil
	}
	full, fullSize := read("")
	compact, compactSize := read("&includeToolPayloads=false")
	fullStep, compactStep := find(full), find(compact)
	require.Equal(t, string(raw), fullStep.Content)
	require.Contains(t, string(fullStep.ResponsePayload), "LARGE_RESULT_SENTINEL")
	require.Less(t, compactSize, 8192, "initial history must not duplicate opaque tool bodies")
	require.Empty(t, compactStep.Content)
	require.Empty(t, compactStep.ResponsePayload)
	require.Equal(t, payload.Id, compactStep.ResponsePayloadID)
	require.Equal(t, fullStep.ToolCallID, compactStep.ToolCallID)
	require.Equal(t, fullStep.Status, compactStep.Status)
	detail, err := c.native.GetPayload(ctx, payload.Id)
	require.NoError(t, err)
	require.Equal(t, raw, []byte(*detail.InlineBody))
	restored, _ := read("")
	require.Equal(t, fullStep.Content, find(restored).Content)
	original, err := c.native.GetConversation(ctx, "thread")
	require.NoError(t, err)
	require.NotEmpty(t, original.Transcript)
	t.Logf("stored tool bytes=%d full canonical bytes=%d compact canonical bytes=%d; detail byte-identical", len(raw), fullSize, compactSize)
}
func TestCompactToolProjectionPreservesRendererFeedErrorsAndPending(t *testing.T) {
	payload := json.RawMessage(`{"inlineBody":"renderer or hook original body","id":"payload"}`)
	steps := []*ToolStepState{
		{ToolName: "opaque/large_result", Status: "completed", Content: "bulk", ResponsePayloadID: "payload", ResponsePayload: payload, RequestPayloadID: "args", RequestPayload: payload},
		{ToolName: "ui/window/get", Status: "completed", Content: "window", ResponsePayloadID: "window", ResponsePayload: payload},
		{ToolName: "unfamiliar/producer", Status: "completed", Content: "report document", ResponsePayloadID: "report", ResponsePayload: json.RawMessage(`{"inlineBody":"{\"reportSpec\":{\"kind\":\"reportSpec\"},\"reportFill\":{\"kind\":\"reportFill\"},\"reportPrint\":{\"kind\":\"reportPrint\"},\"provenance\":{\"reportRunId\":\"original\"}}"}`)},
		{ToolName: "opaque/operation", Status: "completed", Content: "mcpui", UIResourceURI: "ui://selected", ResponsePayloadID: "ui", ResponsePayload: payload},
		{ToolName: "custom/plot", Status: "completed", Content: "feed data", ResponsePayloadID: "feed", ResponsePayload: payload},
		{ToolName: "opaque/failed", Status: "failed", Content: "original error", ResponsePayloadID: "failed", ResponsePayload: payload},
		{ToolName: "opaque/pending", Status: "pending", Content: "approval args", ResponsePayloadID: "pending", ResponsePayload: payload},
		{ToolName: "opaque/app", Status: "completed", ResponsePayloadID: "app", ResponsePayload: json.RawMessage(`{"inlineBody":"{\"_meta\":{\"ui\":{\"resourceUri\":\"ui://app\"}}}"}`)},
		{ToolName: "opaque/no-reference", Status: "completed", Content: "only remaining result", ResponsePayload: payload},
	}
	state := &ConversationState{Turns: []*TurnState{{Execution: &ExecutionState{Pages: []*ExecutionPageState{{ToolSteps: steps}}}}}}
	client := &backendClient{feeds: &FeedRegistry{specs: []*FeedSpec{{ID: "custom", Match: FeedMatch{Service: "custom", Method: "plot"}}}}}
	before := mustJSON(t, steps[1:])
	client.omitCanonicalToolPayloads(state)
	require.Empty(t, steps[0].Content)
	require.Empty(t, steps[0].ResponsePayload)
	require.Empty(t, steps[0].RequestPayload)
	require.Equal(t, "payload", steps[0].ResponsePayloadID)
	require.JSONEq(t, string(before), string(mustJSON(t, steps[1:])), "renderer, configured feed, error and pending carriers must remain exact")
	ordinary := &ToolStepState{ToolName: "my-report-analysis", Status: "completed", Content: "large opaque ordinary result", ResponsePayloadID: "ordinary", ResponsePayload: payload}
	ordinaryState := &ConversationState{Turns: []*TurnState{{Execution: &ExecutionState{Pages: []*ExecutionPageState{{ToolSteps: []*ToolStepState{ordinary}}}}}}}
	client.omitCanonicalToolPayloads(ordinaryState)
	require.Empty(t, ordinary.Content)
	require.Empty(t, ordinary.ResponsePayload, "tool names containing report words do not classify renderer data")
	fastState := &ConversationState{Turns: []*TurnState{{Execution: &ExecutionState{Pages: []*ExecutionPageState{{ToolSteps: []*ToolStepState{{ToolName: "ordinary/read", Status: "completed", Content: "bulk", ResponsePayloadID: "body", ResponsePayload: payload}}}}}}}}
	client.primitiveProviders = &resources.Gateway{} // Any dynamic catalog call would fail; the initial compact history read must not make one.
	client.omitCanonicalToolPayloadsWithCatalog(fastState, context.Background(), false)
	require.Empty(t, fastState.Turns[0].Execution.Pages[0].ToolSteps[0].Content)
	require.Equal(t, "body", fastState.Turns[0].Execution.Pages[0].ToolSteps[0].ResponsePayloadID)
}
