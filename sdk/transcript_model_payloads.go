package sdk

import (
	"context"
	convstore "github.com/viant/agently-core/app/store/conversation"
)

// Header reads use the same authenticated native store while excluding its
// transcript/model/tool joins. Other Client implementations retain the fallback.
func (c *backendClient) getConversationHeader(ctx context.Context, id string) (*convstore.Conversation, error) {
	return c.conv.GetConversation(ctx, id, convstore.WithIncludeTranscript(false), convstore.WithIncludeModelCall(false), convstore.WithIncludeToolCall(false))
}

// Payload omission follows canonical derivation so execution roles inferred from
// stored model responses remain identical. Only the response projection changes;
// the native transcript and payload retrieval service remain untouched.
func omitCanonicalModelPayloads(state *ConversationState) {
	if state == nil {
		return
	}
	for _, turn := range state.Turns {
		if turn == nil || turn.Execution == nil {
			continue
		}
		for _, page := range turn.Execution.Pages {
			if page == nil {
				continue
			}
			for _, step := range page.ModelSteps {
				if step == nil {
					continue
				}
				step.RequestPayload = nil
				step.ResponsePayload = nil
				step.ProviderRequestPayload = nil
				step.ProviderResponsePayload = nil
				step.StreamPayload = nil
			}
		}
	}
}
