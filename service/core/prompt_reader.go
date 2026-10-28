package core

import (
	"context"
	"strings"

	"github.com/viant/agently-core/protocol/binding"
	identity "github.com/viant/agently-core/protocol/resource"
)

// PromptReader resolves explicit canonical prompt references. Materialized
// prompts contain text/engine only and cannot fall back to a mutable URI read.
type PromptReader interface {
	Load(context.Context, string) (*binding.Prompt, error)
}

func (s *Service) SetPromptReader(reader PromptReader) { s.promptReader = reader }
func (s *Service) resolvePrompt(ctx context.Context, prompt *binding.Prompt) (*binding.Prompt, error) {
	if prompt == nil || !strings.HasPrefix(prompt.URI, "prompt://") {
		return prompt, nil
	}
	if s.promptReader == nil {
		return nil, identity.ErrResourceDenied
	}
	return s.promptReader.Load(ctx, prompt.URI)
}
func (s *Service) resolvePromptInput(ctx context.Context, input *GenerateInput) (*GenerateInput, error) {
	if input == nil {
		return nil, nil
	}
	canonical := func(prompt *binding.Prompt) bool { return prompt != nil && strings.HasPrefix(prompt.URI, "prompt://") }
	if !canonical(input.Prompt) && !canonical(input.SystemPrompt) && !canonical(input.Instruction) {
		return input, nil
	}
	copy := *input
	var err error
	if copy.Prompt, err = s.resolvePrompt(ctx, input.Prompt); err != nil {
		return nil, err
	}
	if copy.SystemPrompt, err = s.resolvePrompt(ctx, input.SystemPrompt); err != nil {
		return nil, err
	}
	if copy.Instruction, err = s.resolvePrompt(ctx, input.Instruction); err != nil {
		return nil, err
	}
	return &copy, nil
}
