package core

import (
	"context"
	"fmt"

	"github.com/viant/agently-core/genai/llm"
)

// CountInputTokens prepares once and keeps that exact request for Generate or
// Stream. GenerateInput.Init appends history/tools, so preparing it twice would
// change both the counted input and the actual input.
func (s *Service) CountInputTokens(ctx context.Context, input *GenerateInput) (int, error) {
	request, model, err := s.prepareGenerateRequest(ctx, input)
	if err != nil {
		return 0, err
	}
	input.preparedRequest, input.preparedModel = request, model
	counter, ok := model.(llm.InputTokenCounter)
	if !ok {
		return 0, fmt.Errorf("model %q does not support reliable input token counting required by contextCompactionPercent", input.Model)
	}
	tokens, err := counter.CountInputTokens(ctx, request)
	if err != nil {
		return 0, fmt.Errorf("count input tokens for proactive compaction: %w", err)
	}
	if tokens < 0 {
		return 0, fmt.Errorf("input token counter returned a negative count for model %q", input.Model)
	}
	return tokens, nil
}
