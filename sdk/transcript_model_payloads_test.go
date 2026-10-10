package sdk

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestCompactCanonicalModelPayloadsPreserveMetadataAndNativeResponse(t *testing.T) {
	now := time.Now()
	content := "Visible assistant history"
	id := "payload-original"
	model := &conversationmodel.ModelCallView{MessageId: "message", Status: "completed", StartedAt: &now, RequestPayloadId: &id, ResponsePayloadId: &id, ProviderRequestPayloadId: &id, ProviderResponsePayloadId: &id, StreamPayloadId: &id}
	fields := []string{"ModelCallRequestPayload", "ModelCallResponsePayload", "ModelCallProviderRequestPayload", "ModelCallProviderResponsePayload", "ModelCallStreamPayload"}
	for _, name := range fields {
		field := reflect.ValueOf(model).Elem().FieldByName(name)
		field.Set(reflect.New(field.Type().Elem()))
	}
	turns := convstore.Transcript{&convstore.Turn{Id: "turn", Message: []*conversationmodel.MessageView{{Id: "message", Role: "assistant", Content: &content, ModelCall: model}}}}
	full := BuildCanonicalState("conversation", turns)
	compact := BuildCanonicalState("conversation", turns)
	omitCanonicalModelPayloads(compact)
	raw, err := json.Marshal(full)
	require.NoError(t, err)
	var expected map[string]any
	require.NoError(t, json.Unmarshal(raw, &expected))
	for _, turn := range expected["turns"].([]any) {
		for _, page := range turn.(map[string]any)["execution"].(map[string]any)["pages"].([]any) {
			for _, step := range page.(map[string]any)["modelSteps"].([]any) {
				for _, key := range []string{"requestPayload", "responsePayload", "providerRequestPayload", "providerResponsePayload", "streamPayload"} {
					delete(step.(map[string]any), key)
				}
			}
		}
	}
	actual, err := json.Marshal(compact)
	require.NoError(t, err)
	require.JSONEq(t, string(mustJSON(t, expected)), string(actual))
	for _, name := range fields {
		require.False(t, reflect.ValueOf(model).Elem().FieldByName(name).IsNil(), "native original payload must remain available for detail reads")
	}
	require.Equal(t, id, compact.Turns[0].Execution.Pages[0].ModelSteps[0].RequestPayloadID)
	require.Equal(t, "completed", compact.Turns[0].Execution.Pages[0].ModelSteps[0].Status)
}
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
}

type compactConversationFixture struct {
	Client
	original *convstore.Conversation
}

func (f compactConversationFixture) GetConversation(context.Context, string) (*convstore.Conversation, error) {
	return f.original, nil
}
func TestConversationMetadataCompactResponseCopiesWithoutChangingDefault(t *testing.T) {
	original := &convstore.Conversation{Id: "owned", Transcript: []*conversationmodel.TranscriptView{{Id: "turn"}}}
	client := compactConversationFixture{original: original}
	for _, query := range []string{"?includeTranscript=false", ""} {
		request := httptest.NewRequest("GET", "/conversation/owned"+query, nil)
		request.SetPathValue("id", "owned")
		response := httptest.NewRecorder()
		handleGetConversation(client)(response, request)
		require.Equal(t, 200, response.Code)
		var body map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		var projected convstore.Conversation
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &projected))
		if query != "" {
			require.Empty(t, projected.Transcript)
		} else {
			require.Len(t, projected.Transcript, 1)
		}
	}
	require.Len(t, original.Transcript, 1, "metadata projection must not mutate native transcript")
}

type compactNativeTranscriptFixture struct {
	*payloadOnlyConversationClient
	original *convstore.Conversation
}

func (f *compactNativeTranscriptFixture) GetConversation(context.Context, string, ...convstore.Option) (*convstore.Conversation, error) {
	return f.original, nil
}
func TestNativeTranscriptAndLiveCompactOptionsPreserveDefault(t *testing.T) {
	id := "original-payload"
	model := &conversationmodel.ModelCallView{MessageId: "message", Status: "completed", RequestPayloadId: &id}
	field := reflect.ValueOf(model).Elem().FieldByName("ModelCallRequestPayload")
	field.Set(reflect.New(field.Type().Elem()))
	original := &convstore.Conversation{Id: "owned", Transcript: []*conversationmodel.TranscriptView{{Id: "turn", Message: []*conversationmodel.MessageView{{Id: "message", Role: "assistant", ModelCall: model}}}}}
	client := &backendClient{conv: &compactNativeTranscriptFixture{payloadOnlyConversationClient: newPayloadOnlyConversationClient(nil), original: original}}
	compact := false
	full, err := client.GetTranscript(context.Background(), &GetTranscriptInput{ConversationID: "owned", IncludeModelCalls: true})
	require.NoError(t, err)
	omitted, err := client.GetTranscript(context.Background(), &GetTranscriptInput{ConversationID: "owned", IncludeModelCalls: true, IncludeModelPayloads: &compact})
	require.NoError(t, err)
	live, err := client.GetLiveState(context.Background(), "owned", WithIncludeModelCalls(), WithIncludeModelPayloads(false))
	require.NoError(t, err)
	require.NotEmpty(t, full.Conversation.Turns[0].Execution.Pages[0].ModelSteps[0].RequestPayload)
	require.Empty(t, omitted.Conversation.Turns[0].Execution.Pages[0].ModelSteps[0].RequestPayload)
	require.Empty(t, live.Conversation.Turns[0].Execution.Pages[0].ModelSteps[0].RequestPayload)
	require.Equal(t, id, omitted.Conversation.Turns[0].Execution.Pages[0].ModelSteps[0].RequestPayloadID)
	require.False(t, field.IsNil())
}

func TestNativeDatlyHTTPCompactBootstrapAndLazyPayloadDetail(t *testing.T) {
	c := newDatlyObservedClient(t, 8)
	ctx := recoveryContext()
	require.NoError(t, nativeTestTurn(ctx, c, "saved", "succeeded"))
	require.NoError(t, nativeTestMessage(ctx, c, "saved", "assistant", "assistant", "Owned assistant history"))
	raw := []byte(`{"messages":[{"role":"user","content":"Owned model request"}]}`)
	payload := convstore.NewPayload()
	payload.SetId("owned-model-payload")
	payload.SetKind("model_request")
	payload.SetMimeType("application/json")
	payload.SetStorage("inline")
	payload.SetInlineBody(raw)
	payload.SetSizeBytes(len(raw))
	require.NoError(t, c.conv.PatchPayload(ctx, payload))
	model := convstore.NewModelCall()
	model.SetMessageID("assistant")
	model.SetTurnID("saved")
	model.SetProvider("fixture")
	model.SetModel("owned")
	model.SetModelKind("chat")
	model.SetStatus("completed")
	id := "owned-model-payload"
	model.RequestPayloadID = &id
	model.Has.RequestPayloadID = true
	require.NoError(t, c.conv.PatchModelCall(ctx, model))
	var full, compact *ConversationStateResponse
	for _, omit := range []bool{false, true} {
		query := "?includeModelCalls=true&includeToolCalls=true"
		if omit {
			query += "&includeModelPayloads=false"
		}
		request := httptest.NewRequest("GET", "/transcript/thread"+query, nil).WithContext(ctx)
		request.SetPathValue("id", "thread")
		response := httptest.NewRecorder()
		handleGetTranscript(c.native)(response, request)
		require.Equal(t, 200, response.Code, response.Body.String())
		var state ConversationStateResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &state))
		if omit {
			compact = &state
		} else {
			full = &state
		}
	}
	require.NotEmpty(t, full.Conversation.Turns[0].Execution.Pages[0].ModelSteps[0].RequestPayload)
	require.Empty(t, compact.Conversation.Turns[0].Execution.Pages[0].ModelSteps[0].RequestPayload)
	require.Equal(t, id, compact.Conversation.Turns[0].Execution.Pages[0].ModelSteps[0].RequestPayloadID)
	detail, err := c.native.GetPayload(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, detail.InlineBody)
	require.JSONEq(t, string(raw), string(*detail.InlineBody))
	original, err := c.native.GetConversation(ctx, "thread")
	require.NoError(t, err)
	require.NotEmpty(t, original.Transcript)
	request := httptest.NewRequest("GET", "/conversation/thread?includeTranscript=false", nil).WithContext(ctx)
	request.SetPathValue("id", "thread")
	response := httptest.NewRecorder()
	handleGetConversation(c.native)(response, request)
	require.Equal(t, 200, response.Code)
	var metadata convstore.Conversation
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &metadata))
	require.Empty(t, metadata.Transcript)
	restored, err := c.native.GetTranscript(ctx, &GetTranscriptInput{ConversationID: "thread", IncludeModelCalls: true})
	require.NoError(t, err)
	require.NotEmpty(t, restored.Conversation.Turns[0].Execution.Pages[0].ModelSteps[0].RequestPayload)
}

func TestCompactMetadataNativeReadDisablesGraphSelectors(t *testing.T) {
	store := &transcriptOptionConversationClient{payloadOnlyConversationClient: newPayloadOnlyConversationClient(nil)}
	client := &backendClient{conv: store}
	request := httptest.NewRequest("GET", "/conversation/owned?includeTranscript=false", nil)
	request.SetPathValue("id", "owned")
	response := httptest.NewRecorder()
	handleGetConversation(client)(response, request)
	require.Equal(t, 200, response.Code)
	require.NotNil(t, store.input.Has)
	require.True(t, store.input.Has.IncludeTranscript)
	require.False(t, store.input.IncludeTranscript)
	require.True(t, store.input.Has.IncludeModelCal)
	require.False(t, store.input.IncludeModelCal)
	require.True(t, store.input.Has.IncludeToolCall)
	require.False(t, store.input.IncludeToolCall)
}
