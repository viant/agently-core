package anthropic

import (
	"context"

	"github.com/viant/agently-core/genai/llm"
	vclaude "github.com/viant/agently-core/genai/llm/provider/vertexai/claude"
)

// ToRequest converts the generic llm request into the direct Anthropic
// Messages API payload while reusing the existing Claude content mapping.
func ToRequest(ctx context.Context, model string, request *llm.GenerateRequest) (*Request, error) {
	baseReq, err := vclaude.ToRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	return &Request{
		Model:         model,
		Messages:      baseReq.Messages,
		Tools:         baseReq.Tools,
		MaxTokens:     baseReq.MaxTokens,
		Temperature:   baseReq.Temperature,
		TopP:          baseReq.TopP,
		TopK:          baseReq.TopK,
		StopSequences: baseReq.StopSequences,
		Stream:        baseReq.Stream,
		Thinking:      baseReq.Thinking,
		System:        baseReq.System,
	}, nil
}
