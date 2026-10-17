package modelcall

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestRecorderObserver_PersistsReportedUsageDetailsAndLegacyFallback(t *testing.T) {
	ptr := func(value int) *int { return &value }
	cases := []struct {
		name              string
		usage             *llm.Usage
		cached, reasoning *int
	}{
		{name: "reported-positive", usage: &llm.Usage{PromptTokensDetails: &llm.PromptTokenDetails{CachedTokens: ptr(43452)}, CompletionTokensDetails: &llm.CompletionTokenDetails{ReasoningTokens: ptr(17)}}, cached: ptr(43452), reasoning: ptr(17)},
		{name: "reported-zero", usage: &llm.Usage{PromptTokensDetails: &llm.PromptTokenDetails{CachedTokens: ptr(0)}, CompletionTokensDetails: &llm.CompletionTokenDetails{ReasoningTokens: ptr(0)}}, cached: ptr(0), reasoning: ptr(0)},
		{name: "absent", usage: &llm.Usage{}},
		{name: "empty-details", usage: &llm.Usage{PromptTokensDetails: &llm.PromptTokenDetails{}, CompletionTokensDetails: &llm.CompletionTokenDetails{}}},
		{name: "legacy-aggregate", usage: &llm.Usage{CachedTokens: 43452, ReasoningTokens: 17}, cached: ptr(43452), reasoning: ptr(17)},
		{name: "legacy-detailed-scalars", usage: &llm.Usage{PromptCachedTokens: 43452, CompletionReasoningTokens: 17}, cached: ptr(43452), reasoning: ptr(17)},
		{name: "legacy-zero-is-unknown", usage: &llm.Usage{CachedTokens: 0, ReasoningTokens: 0}},
		{name: "reported-zero-is-authoritative", usage: &llm.Usage{CachedTokens: 19, PromptCachedTokens: 19, ReasoningTokens: 3, CompletionReasoningTokens: 3, PromptTokensDetails: &llm.PromptTokenDetails{CachedTokens: ptr(0)}, CompletionTokensDetails: &llm.CompletionTokenDetails{ReasoningTokens: ptr(0)}}, cached: ptr(0), reasoning: ptr(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := convmem.New()
			ctx := requestctx.WithConversationID(context.Background(), "usage-conversation")
			require.NoError(t, client.PatchConversations(ctx, conversationmodel.NewConversationStatus("usage-conversation", "")))
			ctx = WithRecorderObserver(ctx, client)
			observer := ObserverFromContext(ctx)
			require.NotNil(t, observer)
			started, err := observer.OnCallStart(ctx, Info{Provider: "fixture", Model: "fixture-model", LLMRequest: &llm.GenerateRequest{}})
			require.NoError(t, err)
			require.NoError(t, observer.OnCallEnd(started, Info{Model: "fixture-model", Usage: tc.usage, LLMResponse: &llm.GenerateResponse{Usage: tc.usage}}))
			message, err := client.GetMessage(ctx, requestctx.ModelMessageIDFromContext(started))
			require.NoError(t, err)
			require.NotNil(t, message)
			require.NotNil(t, message.ModelCall)
			require.Equal(t, tc.cached, message.ModelCall.PromptCachedTokens)
			require.Equal(t, tc.reasoning, message.ModelCall.CompletionReasoningTokens)
		})
	}
}
