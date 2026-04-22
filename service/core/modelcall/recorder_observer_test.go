package modelcall

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/debugtrace"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

type captureModelCallClient struct {
	apiconv.Client
	patches []*apiconv.MutableModelCall
}

type streamAwareConversationClient struct {
	apiconv.Client
	bus *streaming.MemoryBus
}

func (c *streamAwareConversationClient) PatchConversations(ctx context.Context, conv *apiconv.MutableConversation) error {
	if err := c.Client.PatchConversations(ctx, conv); err != nil {
		return err
	}
	if c.bus != nil && conv != nil {
		_ = c.bus.Publish(ctx, &streaming.Event{
			Type:                 streaming.EventTypeUsage,
			ConversationID:       conv.Id,
			StreamID:             conv.Id,
			UsageInputTokens:     intValuePtr(conv.UsageInputTokens),
			UsageOutputTokens:    intValuePtr(conv.UsageOutputTokens),
			UsageEmbeddingTokens: intValuePtr(conv.UsageEmbeddingTokens),
		})
	}
	return nil
}

func intValuePtr(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func (c *captureModelCallClient) PatchModelCall(ctx context.Context, modelCall *apiconv.MutableModelCall) error {
	c.patches = append(c.patches, modelCall)
	return c.Client.PatchModelCall(ctx, modelCall)
}

// TestFinishModelCallSetsCost_DataDriven verifies cost calculation is stored
// with per-1k pricing using a data-driven table of scenarios.
func TestFinishModelCallSetsCost_DataDriven(t *testing.T) {
	type tc struct {
		name   string
		model  string
		inP    float64
		outP   float64
		cacheP float64
		pt     int
		ct     int
		cached int
	}

	cases := []tc{
		{
			name:   "openai o3",
			model:  "openai_o3",
			inP:    0.002, // $2 per 1M → 0.002 per 1k
			outP:   0.008, // $8 per 1M → 0.008 per 1k
			cacheP: 0,
			pt:     1000,
			ct:     500,
			cached: 0,
		},
		{
			name:   "bedrock claude 4-5 with cache",
			model:  "bedrock_claude_4-5",
			inP:    0.003,  // $3 per 1M → 0.003 per 1k
			outP:   0.015,  // $15 per 1M → 0.015 per 1k
			cacheP: 0.0003, // 10% of input per 1k (≈ $0.30 per 1M)
			pt:     200,
			ct:     300,
			cached: 100,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// In-memory conversation client
			client := convmem.New()

			// Conversation context with id
			base := memory.WithConversationID(context.Background(), "conv-1")
			// Price provider returns per-1k prices
			provider := staticPriceProvider{model: c.model, inP: c.inP, outP: c.outP, cacheP: c.cacheP}
			// Ensure conversation exists in the client store
			if err := client.PatchConversations(base, convw.NewConversationStatus("conv-1", "")); err != nil {
				t.Fatalf("failed to seed conversation: %v", err)
			}

			ctx := WithRecorderObserverWithPrice(base, client, provider)

			// Start the call and capture ctx with message id set
			ob := ObserverFromContext(ctx)
			if ob == nil {
				t.Fatalf("observer not injected")
			}
			ctx2, err := ob.OnCallStart(ctx, Info{Provider: "test", Model: c.model, LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}}})
			if err != nil {
				t.Fatalf("OnCallStart error: %v", err)
			}

			// Finish the call with usage
			usage := &llm.Usage{PromptTokens: c.pt, CompletionTokens: c.ct, CachedTokens: c.cached}
			if err := ob.OnCallEnd(ctx2, Info{Model: c.model, LLMResponse: &llm.GenerateResponse{}, Usage: usage}); err != nil {
				t.Fatalf("OnCallEnd error: %v", err)
			}

			// Fetch message and verify stored cost
			msgID := memory.ModelMessageIDFromContext(ctx2)
			if msgID == "" {
				t.Fatalf("message id not set in context")
			}
			msg, err := client.GetMessage(context.Background(), msgID)
			if err != nil || msg == nil || msg.ModelCall == nil || msg.ModelCall.Cost == nil {
				t.Fatalf("missing model call cost: %v", err)
			}

			// Expected cost formula with per-1k prices
			expected := (float64(c.pt)*c.inP + float64(c.ct)*c.outP + float64(c.cached)*c.cacheP) / 1000.0
			assert.EqualValues(t, expected, *msg.ModelCall.Cost)
		})
	}
}

type staticPriceProvider struct {
	model             string
	inP, outP, cacheP float64
}

func (s staticPriceProvider) TokenPrices(model string) (float64, float64, float64, bool) {
	if model == s.model {
		return s.inP, s.outP, s.cacheP, true
	}
	return 0, 0, 0, false
}

func TestRecorderObserver_PersistsAssistantContent_DataDriven(t *testing.T) {
	type testCase struct {
		name          string
		resp          *llm.GenerateResponse
		responseJSON  []byte
		expected      string
		expectRaw     bool
		expectInterim int
	}

	cases := []testCase{
		{
			name:     "content field",
			resp:     &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "hello"}}}},
			expected: "hello",
		},
		{
			name:     "content items",
			resp:     &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, ContentItems: []llm.ContentItem{{Type: llm.ContentTypeText, Text: "from items"}}}}}},
			expected: "from items",
		},
		{
			name:          "tool calls store raw_content",
			resp:          &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "plan", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "resources-roots"}}}}}},
			expected:      "plan",
			expectRaw:     true,
			expectInterim: 1,
		},
		{
			name:          "tool calls without model-authored text keep empty preamble",
			resp:          &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "resources-roots"}}}}}},
			expected:      "",
			expectRaw:     true,
			expectInterim: 1,
		},
		{
			name: "response json fallback",
			responseJSON: func() []byte {
				raw, _ := json.Marshal(&llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "from json"}}}})
				return raw
			}(),
			expected: "from json",
		},
		{
			name:          "elicitation json is not persisted as assistant text",
			resp:          &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: `{"type":"elicitation","message":"Need favorite color","requestedSchema":{"type":"object"}}`}}}},
			expected:      "",
			expectInterim: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := convmem.New()
			base := memory.WithConversationID(context.Background(), "conv-1")
			if err := client.PatchConversations(base, convw.NewConversationStatus("conv-1", "")); err != nil {
				t.Fatalf("failed to seed conversation: %v", err)
			}

			ctx := WithRecorderObserver(base, client)
			ob := ObserverFromContext(ctx)
			if ob == nil {
				t.Fatalf("observer not injected")
			}
			ctx2, err := ob.OnCallStart(ctx, Info{Provider: "test", Model: "test-model", LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}}})
			if err != nil {
				t.Fatalf("OnCallStart error: %v", err)
			}

			if err := ob.OnCallEnd(ctx2, Info{Model: "test-model", LLMResponse: tc.resp, ResponseJSON: tc.responseJSON}); err != nil {
				t.Fatalf("OnCallEnd error: %v", err)
			}

			msgID := memory.ModelMessageIDFromContext(ctx2)
			if msgID == "" {
				t.Fatalf("message id not set in context")
			}
			msg, err := client.GetMessage(context.Background(), msgID)
			if err != nil || msg == nil {
				t.Fatalf("failed to fetch message: %v", err)
			}
			actualContent := ""
			if msg.Content != nil {
				actualContent = *msg.Content
			}
			actualRaw := ""
			if msg.RawContent != nil {
				actualRaw = *msg.RawContent
			}
			actualPreamble := ""
			if msg.Preamble != nil {
				actualPreamble = *msg.Preamble
			}
			assert.EqualValues(t, tc.expected, actualContent)
			if tc.expectRaw {
				assert.EqualValues(t, tc.expected, actualRaw)
				assert.EqualValues(t, tc.expected, actualPreamble)
			} else {
				assert.EqualValues(t, "", actualRaw)
				assert.EqualValues(t, "", actualPreamble)
			}
			assert.EqualValues(t, tc.expectInterim, msg.Interim)
		})
	}
}

func TestRecorderObserver_PropagatesUsageToParentChainWithoutCycling(t *testing.T) {
	client := convmem.New()

	convA := apiconv.NewConversation()
	convA.SetId("conv-a")
	convA.SetConversationParentId("conv-b")
	require.NoError(t, client.PatchConversations(context.Background(), convA))

	convB := apiconv.NewConversation()
	convB.SetId("conv-b")
	convB.SetConversationParentId("conv-a")
	require.NoError(t, client.PatchConversations(context.Background(), convB))

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-a",
		TurnID:         "turn-a",
	})

	rec := &recorderObserver{client: client}
	err := rec.propagateConversationUsage(ctx, &llm.Usage{
		PromptTokens:     11,
		CompletionTokens: 7,
		TotalTokens:      18,
	})
	require.NoError(t, err)

	gotA, err := client.GetConversation(context.Background(), "conv-a")
	require.NoError(t, err)
	require.NotNil(t, gotA)
	require.NotNil(t, gotA.UsageInputTokens)
	require.NotNil(t, gotA.UsageOutputTokens)
	assert.Equal(t, 11, *gotA.UsageInputTokens)
	assert.Equal(t, 7, *gotA.UsageOutputTokens)

	gotB, err := client.GetConversation(context.Background(), "conv-b")
	require.NoError(t, err)
	require.NotNil(t, gotB)
	require.NotNil(t, gotB.UsageInputTokens)
	require.NotNil(t, gotB.UsageOutputTokens)
	assert.Equal(t, 11, *gotB.UsageInputTokens)
	assert.Equal(t, 7, *gotB.UsageOutputTokens)
}

func TestRecorderObserver_ParentUsagePropagationPublishesToParentStream(t *testing.T) {
	mem := convmem.New()
	bus := streaming.NewMemoryBus(4)
	client := &streamAwareConversationClient{Client: mem, bus: bus}

	parent := apiconv.NewConversation()
	parent.SetId("parent-conv")
	require.NoError(t, client.PatchConversations(context.Background(), parent))

	child := apiconv.NewConversation()
	child.SetId("child-conv")
	child.SetConversationParentId("parent-conv")
	require.NoError(t, client.PatchConversations(context.Background(), child))

	sub, err := bus.Subscribe(context.Background(), func(ev *streaming.Event) bool {
		return ev != nil && ev.ConversationID == "parent-conv" && ev.Type == streaming.EventTypeUsage
	})
	require.NoError(t, err)
	defer sub.Close()

	rec := &recorderObserver{client: client}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "child-conv",
		TurnID:         "turn-child",
	})

	require.NoError(t, rec.propagateConversationUsage(ctx, &llm.Usage{
		PromptTokens:     9,
		CompletionTokens: 4,
		TotalTokens:      13,
	}))

	select {
	case ev := <-sub.C():
		require.NotNil(t, ev)
		assert.Equal(t, "parent-conv", ev.ConversationID)
		assert.Equal(t, 9, ev.UsageInputTokens)
		assert.Equal(t, 4, ev.UsageOutputTokens)
	case <-time.After(2 * time.Second):
		t.Fatal("expected parent usage event")
	}
}

func TestRecorderObserver_OnCallEnd_PreservesModelKind(t *testing.T) {
	baseClient := convmem.New()
	client := &captureModelCallClient{Client: baseClient}
	base := memory.WithConversationID(context.Background(), "conv-1")
	require.NoError(t, client.PatchConversations(base, convw.NewConversationStatus("conv-1", "")))

	ctx := WithRecorderObserver(base, client)
	ob := ObserverFromContext(ctx)
	require.NotNil(t, ob)

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "openai",
		Model:      "gpt-4o-mini",
		ModelKind:  "chat",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	require.NoError(t, err)

	err = ob.OnCallEnd(ctx2, Info{
		Model:       "gpt-4o-mini",
		ModelKind:   "chat",
		LLMResponse: &llm.GenerateResponse{},
	})
	require.NoError(t, err)
	require.Len(t, client.patches, 2)
	require.NotNil(t, client.patches[1].Has)
	require.True(t, client.patches[1].Has.ModelKind)
	require.Equal(t, "chat", client.patches[1].ModelKind)
}

func TestCloseIfOpen_ClosesStartedModelCall(t *testing.T) {
	cases := []struct {
		name           string
		cancelContext  bool
		expectedStatus string
	}{
		{name: "failed fallback", cancelContext: false, expectedStatus: "failed"},
		{name: "canceled fallback", cancelContext: true, expectedStatus: "canceled"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := convmem.New()
			base := memory.WithConversationID(context.Background(), "conv-1")
			if err := client.PatchConversations(base, convw.NewConversationStatus("conv-1", "")); err != nil {
				t.Fatalf("failed to seed conversation: %v", err)
			}

			runCtx := base
			var cancel func()
			if tc.cancelContext {
				runCtx, cancel = context.WithCancel(base)
			}
			if cancel != nil {
				defer cancel()
			}

			ctx := WithRecorderObserver(runCtx, client)
			ob := ObserverFromContext(ctx)
			if ob == nil {
				t.Fatalf("observer not injected")
			}
			ctx2, err := ob.OnCallStart(ctx, Info{Provider: "test", Model: "test-model", LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}}})
			if err != nil {
				t.Fatalf("OnCallStart error: %v", err)
			}
			if tc.cancelContext && cancel != nil {
				cancel()
			}

			if err := CloseIfOpen(ctx2, Info{Err: "forced close", CompletedAt: time.Now()}); err != nil {
				t.Fatalf("CloseIfOpen error: %v", err)
			}

			msgID := memory.ModelMessageIDFromContext(ctx2)
			if msgID == "" {
				t.Fatalf("message id not set in context")
			}
			msg, err := client.GetMessage(context.Background(), msgID)
			if err != nil || msg == nil || msg.ModelCall == nil {
				t.Fatalf("missing model call after CloseIfOpen: %v", err)
			}
			assert.EqualValues(t, tc.expectedStatus, msg.ModelCall.Status)
		})
	}
}

func TestOnCallEnd_DoesNotPatchConversationWhenFinishModelCallFails(t *testing.T) {
	baseClient := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-1")
	if err := baseClient.PatchConversations(base, convw.NewConversationStatus("conv-1", "")); err != nil {
		t.Fatalf("failed to seed conversation: %v", err)
	}

	client := &failingPayloadClient{
		Client:      baseClient,
		failAtCount: 2, // first payload in OnCallStart, second in OnCallEnd
	}
	ctx := WithRecorderObserver(base, client)
	ob := ObserverFromContext(ctx)
	if ob == nil {
		t.Fatalf("observer not injected")
	}
	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "test",
		Model:      "test-model",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	if err != nil {
		t.Fatalf("OnCallStart error: %v", err)
	}

	endErr := ob.OnCallEnd(ctx2, Info{
		Model:        "test-model",
		LLMResponse:  &llm.GenerateResponse{},
		ResponseJSON: []byte(`{"response":{"id":"r1"}}`),
	})
	if endErr == nil {
		t.Fatalf("expected OnCallEnd error")
	}
	assert.Contains(t, strings.ToLower(endErr.Error()), "finish model call")
	assert.EqualValues(t, 0, client.patchConversationCount)
}

func TestRecorderObserver_OnStreamDelta_IgnoresCanceledPersistenceAndFinalizesAccumulatedStream(t *testing.T) {
	t.Setenv(streamPersistModeEnv, "")

	baseClient := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-1")
	if err := baseClient.PatchConversations(base, convw.NewConversationStatus("conv-1", "")); err != nil {
		t.Fatalf("failed to seed conversation: %v", err)
	}

	runCtx, cancel := context.WithCancel(base)
	client := &cancelAwarePayloadClient{Client: baseClient}
	ctx := WithRecorderObserver(runCtx, client)
	ob := ObserverFromContext(ctx)
	if ob == nil {
		t.Fatalf("observer not injected")
	}

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "test",
		Model:      "test-model",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	if err != nil {
		t.Fatalf("OnCallStart error: %v", err)
	}

	cancel()

	if err := ob.OnStreamDelta(ctx2, []byte("Hello")); err != nil {
		t.Fatalf("OnStreamDelta first chunk error: %v", err)
	}
	if err := ob.OnStreamDelta(ctx2, []byte(" world")); err != nil {
		t.Fatalf("OnStreamDelta second chunk error: %v", err)
	}

	if err := CloseIfOpen(ctx2, Info{CompletedAt: time.Now()}); err != nil {
		t.Fatalf("CloseIfOpen error: %v", err)
	}

	msgID := memory.ModelMessageIDFromContext(ctx2)
	if msgID == "" {
		t.Fatalf("message id not set in context")
	}
	msg, err := baseClient.GetMessage(context.Background(), msgID)
	if err != nil || msg == nil || msg.ModelCall == nil {
		t.Fatalf("missing model call after CloseIfOpen: %v", err)
	}
	assert.EqualValues(t, "canceled", msg.ModelCall.Status)
	if msg.ModelCall.StreamPayloadId == nil || strings.TrimSpace(*msg.ModelCall.StreamPayloadId) == "" {
		t.Fatalf("expected stream payload id")
	}
	payload, err := baseClient.GetPayload(context.Background(), *msg.ModelCall.StreamPayloadId)
	if err != nil || payload == nil || payload.InlineBody == nil {
		t.Fatalf("missing stream payload: %v", err)
	}
	assert.Equal(t, "Hello world", string(*payload.InlineBody))
}

func TestRecorderObserver_OnStreamDelta_DefaultBufferedFlushesOnInterval(t *testing.T) {
	t.Setenv(streamPersistModeEnv, "")

	client := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-1")
	require.NoError(t, client.PatchConversations(base, convw.NewConversationStatus("conv-1", "")))

	ctx := WithRecorderObserver(base, client)
	ob := ObserverFromContext(ctx)
	require.NotNil(t, ob)
	recorder, ok := ob.(*recorderObserver)
	require.True(t, ok)

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "test",
		Model:      "test-model",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	require.NoError(t, err)

	require.NoError(t, ob.OnStreamDelta(ctx2, []byte("Hello")))
	require.NotEmpty(t, recorder.streamPayloadID)

	payload, err := client.GetPayload(context.Background(), recorder.streamPayloadID)
	require.NoError(t, err)
	assert.Nil(t, payload)

	recorder.lastFlushAt = time.Now().Add(-streamPersistBufferedInterval)
	require.NoError(t, ob.OnStreamDelta(ctx2, []byte(" world")))

	payload, err = client.GetPayload(context.Background(), recorder.streamPayloadID)
	require.NoError(t, err)
	require.NotNil(t, payload)
	require.NotNil(t, payload.InlineBody)
	assert.Equal(t, "Hello world", string(*payload.InlineBody))

	msgID := memory.ModelMessageIDFromContext(ctx2)
	require.NotEmpty(t, msgID)
	msg, err := client.GetMessage(context.Background(), msgID)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.NotNil(t, msg.ModelCall)
	require.NotNil(t, msg.ModelCall.StreamPayloadId)
	assert.Equal(t, recorder.streamPayloadID, strings.TrimSpace(*msg.ModelCall.StreamPayloadId))
}

func TestRecorderObserver_OnStreamDelta_ImmediateModePersistsEachDelta(t *testing.T) {
	t.Setenv(streamPersistModeEnv, "immediate")

	client := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-1")
	require.NoError(t, client.PatchConversations(base, convw.NewConversationStatus("conv-1", "")))

	ctx := WithRecorderObserver(base, client)
	ob := ObserverFromContext(ctx)
	require.NotNil(t, ob)
	recorder, ok := ob.(*recorderObserver)
	require.True(t, ok)

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "test",
		Model:      "test-model",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	require.NoError(t, err)

	require.NoError(t, ob.OnStreamDelta(ctx2, []byte("Hello")))
	require.NotEmpty(t, recorder.streamPayloadID)

	payload, err := client.GetPayload(context.Background(), recorder.streamPayloadID)
	require.NoError(t, err)
	require.NotNil(t, payload)
	require.NotNil(t, payload.InlineBody)
	assert.Equal(t, "Hello", string(*payload.InlineBody))

	require.NoError(t, ob.OnStreamDelta(ctx2, []byte(" world")))
	payload, err = client.GetPayload(context.Background(), recorder.streamPayloadID)
	require.NoError(t, err)
	require.NotNil(t, payload)
	require.NotNil(t, payload.InlineBody)
	assert.Equal(t, "Hello world", string(*payload.InlineBody))
}

func TestRecorderObserver_OnStreamDelta_FinalModePersistsOnlyOnFinalize(t *testing.T) {
	t.Setenv(streamPersistModeEnv, "final")

	client := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-1")
	require.NoError(t, client.PatchConversations(base, convw.NewConversationStatus("conv-1", "")))

	ctx := WithRecorderObserver(base, client)
	ob := ObserverFromContext(ctx)
	require.NotNil(t, ob)

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "test",
		Model:      "test-model",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	require.NoError(t, err)

	require.NoError(t, ob.OnStreamDelta(ctx2, []byte("Hello")))
	require.NoError(t, ob.OnStreamDelta(ctx2, []byte(" world")))

	msgID := memory.ModelMessageIDFromContext(ctx2)
	require.NotEmpty(t, msgID)
	msg, err := client.GetMessage(context.Background(), msgID)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.NotNil(t, msg.ModelCall)
	assert.Nil(t, msg.ModelCall.StreamPayloadId)

	require.NoError(t, ob.OnCallEnd(ctx2, Info{Model: "test-model", StreamText: "Hello world"}))

	msg, err = client.GetMessage(context.Background(), msgID)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.NotNil(t, msg.ModelCall)
	require.NotNil(t, msg.ModelCall.StreamPayloadId)

	payload, err := client.GetPayload(context.Background(), strings.TrimSpace(*msg.ModelCall.StreamPayloadId))
	require.NoError(t, err)
	require.NotNil(t, payload)
	require.NotNil(t, payload.InlineBody)
	assert.Equal(t, "Hello world", string(*payload.InlineBody))
}

func TestRecorderObserver_OnStreamDelta_InvalidModeFallsBackToBuffered(t *testing.T) {
	t.Setenv(streamPersistModeEnv, "not-a-mode")

	client := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-1")
	require.NoError(t, client.PatchConversations(base, convw.NewConversationStatus("conv-1", "")))

	ctx := WithRecorderObserver(base, client)
	ob := ObserverFromContext(ctx)
	require.NotNil(t, ob)
	recorder, ok := ob.(*recorderObserver)
	require.True(t, ok)

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "test",
		Model:      "test-model",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	require.NoError(t, err)

	require.NoError(t, ob.OnStreamDelta(ctx2, []byte("Hello")))
	require.NotEmpty(t, recorder.streamPayloadID)

	payload, err := client.GetPayload(context.Background(), recorder.streamPayloadID)
	require.NoError(t, err)
	assert.Nil(t, payload)
}

func TestCloseIfOpen_CanceledBeforeFirstDeltaDoesNotPersistStreamPayload(t *testing.T) {
	client := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-1")
	require.NoError(t, client.PatchConversations(base, convw.NewConversationStatus("conv-1", "")))

	runCtx, cancel := context.WithCancel(base)
	ctx := WithRecorderObserver(runCtx, client)
	ob := ObserverFromContext(ctx)
	require.NotNil(t, ob)

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "test",
		Model:      "test-model",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	require.NoError(t, err)

	cancel()

	require.NoError(t, CloseIfOpen(ctx2, Info{
		CompletedAt: time.Now(),
		Err:         `failed to start Stream: failed to send request: Post "https://api.openai.com/v1/responses": context canceled`,
	}))

	msgID := memory.ModelMessageIDFromContext(ctx2)
	require.NotEmpty(t, msgID)

	msg, err := client.GetMessage(context.Background(), msgID)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.NotNil(t, msg.ModelCall)
	assert.EqualValues(t, "canceled", msg.ModelCall.Status)
	assert.Nil(t, msg.ModelCall.StreamPayloadId)
}

func TestRecorderObserver_SuppressesToolEchoAndPersistsRunMeta(t *testing.T) {
	baseClient := convmem.New()
	client := &capturingModelCallClient{Client: baseClient}
	base := memory.WithConversationID(context.Background(), "conv-echo")
	require.NoError(t, client.PatchConversations(base, convw.NewConversationStatus("conv-echo", "")))

	user := apiconv.NewMessage()
	user.SetId("user-1")
	user.SetConversationID("conv-echo")
	user.SetTurnID("turn-1")
	user.SetRole("user")
	user.SetType("task")
	user.SetContent("Call the tool")
	user.SetRawContent("Call the tool")
	require.NoError(t, client.PatchMessage(base, user))

	ctx := memory.WithTurnMeta(base, memory.TurnMeta{
		ConversationID:  "conv-echo",
		TurnID:          "turn-1",
		ParentMessageID: "user-1",
		Assistant:       "agent-1",
	})
	ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn-1", Iteration: 2})
	ctx = WithRecorderObserver(ctx, client)
	ob := ObserverFromContext(ctx)
	require.NotNil(t, ob)

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "test",
		Model:      "test-model",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	require.NoError(t, err)

	resp := &llm.GenerateResponse{
		Choices: []llm.Choice{{
			Message: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "Call the tool",
				ToolCalls: []llm.ToolCall{{
					ID:   "call-1",
					Name: "resources-roots",
				}},
			},
		}},
	}
	require.NoError(t, ob.OnCallEnd(ctx2, Info{Model: "test-model", LLMResponse: resp}))

	msgID := memory.ModelMessageIDFromContext(ctx2)
	require.NotEmpty(t, msgID)
	msg, err := client.GetMessage(context.Background(), msgID)
	require.NoError(t, err)
	require.NotNil(t, msg)
	// After echo suppression the original content is cleared and no synthetic
	// "Calling ..." preamble is invented for tool-only responses. The recorder
	// still persists an empty interim assistant message so tool rows can attach
	// to the iteration cleanly.
	if msg.Content != nil {
		assert.Equal(t, "", *msg.Content)
	}
	if msg.Preamble != nil {
		assert.Equal(t, "", *msg.Preamble)
	}

	var persisted *apiconv.MutableModelCall
	for _, call := range client.modelCalls {
		if call != nil && call.RunID != nil && call.Iteration != nil {
			persisted = call
			break
		}
	}
	require.NotNil(t, persisted)
	require.Equal(t, "turn-1", *persisted.RunID)
	require.EqualValues(t, 2, *persisted.Iteration)
}

func TestRecorderObserver_WritesProviderPayloadFiles(t *testing.T) {
	client := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-payloads")
	require.NoError(t, client.PatchConversations(base, convw.NewConversationStatus("conv-payloads", "")))

	payloadDir := filepath.Join(t.TempDir(), "payloads")
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)

	ctx := WithRecorderObserver(base, client)
	ob := ObserverFromContext(ctx)
	require.NotNil(t, ob)

	requestBody := []byte(`{"model":"gpt-5.2","input":"hello"}`)
	responseBody := []byte(`{"response":{"id":"resp-1"},"output":"done"}`)

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:    "openai",
		Model:       "gpt-5.2",
		LLMRequest:  &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
		RequestJSON: requestBody,
	})
	require.NoError(t, err)

	require.NoError(t, ob.OnCallEnd(ctx2, Info{
		Model:        "gpt-5.2",
		LLMResponse:  &llm.GenerateResponse{ResponseID: "resp-1"},
		ResponseJSON: responseBody,
	}))

	msgID := memory.ModelMessageIDFromContext(ctx2)
	require.NotEmpty(t, msgID)

	requestPath := filepath.Join(payloadDir, "llm-provider-request-"+msgID+".json")
	responsePath := filepath.Join(payloadDir, "llm-provider-response-"+msgID+".json")

	gotRequest, err := os.ReadFile(requestPath)
	require.NoError(t, err)
	require.JSONEq(t, string(requestBody), string(gotRequest))

	gotResponse, err := os.ReadFile(responsePath)
	require.NoError(t, err)
	require.JSONEq(t, string(responseBody), string(gotResponse))

	require.NotEmpty(t, debugtrace.PayloadDir())
}

func TestRecorderObserver_PatchesAssistantRoleTypeAndMode(t *testing.T) {
	client := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-assistant-meta")
	require.NoError(t, client.PatchConversations(base, convw.NewConversationStatus("conv-assistant-meta", "")))

	ctx := memory.WithTurnMeta(base, memory.TurnMeta{
		ConversationID:  "conv-assistant-meta",
		TurnID:          "turn-1",
		ParentMessageID: "user-1",
		Assistant:       "agent-1",
	})
	ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn-1", Iteration: 1})
	ctx = WithRecorderObserver(ctx, client)
	ob := ObserverFromContext(ctx)
	require.NotNil(t, ob)

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "test",
		Model:      "test-model",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	require.NoError(t, err)

	resp := &llm.GenerateResponse{
		Choices: []llm.Choice{{
			Message: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "Final answer",
			},
		}},
	}
	require.NoError(t, ob.OnCallEnd(ctx2, Info{Model: "test-model", LLMResponse: resp}))

	msgID := memory.ModelMessageIDFromContext(ctx2)
	require.NotEmpty(t, msgID)
	msg, err := client.GetMessage(context.Background(), msgID)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.Equal(t, "assistant", msg.Role)
	require.Equal(t, "text", msg.Type)
	require.NotNil(t, msg.Content)
	require.Equal(t, "Final answer", *msg.Content)
}

func TestRecorderObserver_OnCallStart_PersistsInterimAssistantPlaceholder(t *testing.T) {
	client := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-no-blank")
	require.NoError(t, client.PatchConversations(base, convw.NewConversationStatus("conv-no-blank", "")))

	ctx := memory.WithTurnMeta(base, memory.TurnMeta{
		ConversationID:  "conv-no-blank",
		TurnID:          "turn-1",
		ParentMessageID: "user-1",
		Assistant:       "agent-1",
	})
	ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn-1", Iteration: 1})
	ctx = WithRecorderObserver(ctx, client)
	ob := ObserverFromContext(ctx)
	require.NotNil(t, ob)

	ctx2, err := ob.OnCallStart(ctx, Info{
		Provider:   "test",
		Model:      "test-model",
		LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}},
	})
	require.NoError(t, err)

	msgID := memory.ModelMessageIDFromContext(ctx2)
	require.NotEmpty(t, msgID)

	msg, err := client.GetMessage(context.Background(), msgID)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.Equal(t, "assistant", msg.Role)
	require.Equal(t, "text", msg.Type)
	require.EqualValues(t, 1, msg.Interim)
	require.Nil(t, msg.Content)
	require.Nil(t, msg.RawContent)
	require.Nil(t, msg.Preamble)
}

type failingPayloadClient struct {
	apiconv.Client
	failAtCount            int
	callCount              int
	patchConversationCount int
}

func (f *failingPayloadClient) PatchPayload(ctx context.Context, payload *apiconv.MutablePayload) error {
	f.callCount++
	if f.failAtCount > 0 && f.callCount == f.failAtCount {
		return fmt.Errorf("simulated payload patch failure")
	}
	return f.Client.PatchPayload(ctx, payload)
}

func (f *failingPayloadClient) PatchConversations(ctx context.Context, conversations *apiconv.MutableConversation) error {
	f.patchConversationCount++
	return f.Client.PatchConversations(ctx, conversations)
}

type cancelAwarePayloadClient struct {
	apiconv.Client
}

func (c *cancelAwarePayloadClient) PatchPayload(ctx context.Context, payload *apiconv.MutablePayload) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.Client.PatchPayload(ctx, payload)
}

type capturingModelCallClient struct {
	apiconv.Client
	modelCalls []*apiconv.MutableModelCall
}

func (c *capturingModelCallClient) PatchModelCall(ctx context.Context, modelCall *apiconv.MutableModelCall) error {
	c.modelCalls = append(c.modelCalls, modelCall)
	return c.Client.PatchModelCall(ctx, modelCall)
}
