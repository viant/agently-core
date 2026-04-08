package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/prompt"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
)

// GenerateOutput represents output from extraction
type GenerateOutput struct {
	Response  *llm.GenerateResponse
	Content   string
	MessageID string
}

// ExpandUserPromptInput represents a lightweight request to expand only the
// user prompt template given a binding, without constructing a full
// GenerateRequest or calling the model. It mirrors the user-facing portion
// of GenerateInput.
type ExpandUserPromptInput struct {
	Prompt  *prompt.Prompt  `json:"prompt,omitempty"`
	Binding *prompt.Binding `json:"binding,omitempty"`
}

// ExpandUserPromptOutput carries the expanded user prompt text.
type ExpandUserPromptOutput struct {
	ExpandedUserPrompt string `json:"expandedUserPrompt"`
}

// generate processes LLM responses to generate structured data
func (s *Service) generate(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GenerateInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*GenerateOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	return s.Generate(ctx, input, output)
}

// expandUserPrompt expands only the user prompt template for the provided
// binding and returns the resulting text without invoking any model call.
func (s *Service) expandUserPrompt(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ExpandUserPromptInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ExpandUserPromptOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	p := input.Prompt
	if p == nil {
		p = &prompt.Prompt{}
	}
	if err := p.Init(ctx); err != nil {
		return fmt.Errorf("failed to init prompt: %w", err)
	}
	if input.Binding == nil {
		input.Binding = &prompt.Binding{}
	}
	expanded, err := p.Generate(ctx, input.Binding)
	if err != nil {
		return fmt.Errorf("failed to expand user prompt: %w", err)
	}
	output.ExpandedUserPrompt = expanded
	return nil
}

func (s *Service) Generate(ctx context.Context, input *GenerateInput, output *GenerateOutput) (retErr error) {
	if input != nil && input.Options != nil {
		ctx = runtimerequestctx.WithRequestMode(ctx, input.Options.Mode)
	}
	if tp, ok := s.llmFinder.(modelcallctx.TokenPriceProvider); ok {
		declared := strings.TrimSpace(input.Model)
		if declared != "" {
			tp = modelcallctx.NewFixedModelPriceProvider(tp, declared)
		}
		ctx = modelcallctx.WithRecorderObserverWithPrice(ctx, s.convClient, tp)
	} else {
		ctx = modelcallctx.WithRecorderObserver(ctx, s.convClient)
	}
	defer func() {
		if r := recover(); r != nil {
			_ = modelcallctx.CloseIfOpen(ctx, modelcallctx.Info{
				CompletedAt: time.Now(),
				Err:         fmt.Sprintf("panic: %v", r),
			})
			panic(r)
		}
		if retErr == nil {
			return
		}
		_ = modelcallctx.CloseIfOpen(ctx, modelcallctx.Info{
			CompletedAt: time.Now(),
			Err:         strings.TrimSpace(retErr.Error()),
		})
	}()
	request, model, err := s.prepareGenerateRequest(ctx, input)
	if err != nil {
		return err
	}
	if IsAnchorContinuationEnabled(model) {
		if lr, handled, cerr := s.tryGenerateContinuationByAnchor(ctx, model, request); handled || cerr != nil {
			if cerr != nil {
				return cerr
			}
			output.Response = lr
			if lr != nil {
				var builder strings.Builder
				for _, choice := range lr.Choices {
					if len(choice.Message.ToolCalls) > 0 {
						continue
					}
					if txt := strings.TrimSpace(choice.Message.Content); txt != "" {
						builder.WriteString(txt)
						continue
					}
					for _, item := range choice.Message.Items {
						if item.Type != llm.ContentTypeText {
							continue
						}
						if item.Data != "" {
							builder.WriteString(item.Data)
						} else if item.Text != "" {
							builder.WriteString(item.Text)
						}
					}
				}
				output.Content = strings.TrimSpace(builder.String())
				if msgID := runtimerequestctx.ModelMessageIDFromContext(ctx); msgID != "" {
					output.MessageID = msgID
				}
			}
			return nil
		}
	}

	ctx, _ = modelcallctx.WithFinishBarrier(ctx)
	if s.streamPub != nil {
		if input == nil || input.Options == nil || strings.ToLower(strings.TrimSpace(input.Options.Mode)) != "plan" {
			ctx = modelcallctx.WithStreamPublisher(ctx, s.streamPub)
		}
	}
	var response *llm.GenerateResponse
	for attempt := 0; attempt < 3; attempt++ {
		response, err = model.Generate(ctx, request)
		if err == nil {
			break
		}
		if isContextLimitError(err) {
			return fmt.Errorf("%w: %v", ErrContextLimitExceeded, err)
		}
		if advisor, ok := model.(llm.BackoffAdvisor); ok {
			if delay, retry := advisor.AdviseBackoff(err, attempt); retry {
				if attempt == 2 || ctx.Err() != nil {
					return fmt.Errorf("failed to generate content: %w", err)
				}
				s.setModelCallStatus(ctx, "retrying")
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return fmt.Errorf("failed to generate content: %w", err)
				}
				continue
			}
		}
		if !isTransientNetworkError(err) || attempt == 2 || ctx.Err() != nil {
			return fmt.Errorf("failed to generate content: %w", err)
		}
		delay := time.Second << attempt
		s.setModelCallStatus(ctx, "retrying")
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("failed to generate content: %w", err)
		}
	}
	output.Response = response
	var builder strings.Builder
	for _, choice := range response.Choices {
		if len(choice.Message.ToolCalls) > 0 {
			continue
		}
		if txt := strings.TrimSpace(choice.Message.Content); txt != "" {
			builder.WriteString(txt)
			continue
		}
		for _, item := range choice.Message.Items {
			if item.Type != llm.ContentTypeText {
				continue
			}
			if item.Data != "" {
				builder.WriteString(item.Data)
			} else if item.Text != "" {
				builder.WriteString(item.Text)
			}
		}
	}
	output.Content = strings.TrimSpace(builder.String())
	if msgID := runtimerequestctx.ModelMessageIDFromContext(ctx); msgID != "" {
		output.MessageID = msgID
	}
	return nil
}

var ErrContextLimitExceeded = errors.New("llm/core: context limit exceeded")

type ContinuationContextLimitError struct {
	Err error
}

func (e ContinuationContextLimitError) Error() string {
	return fmt.Sprintf("llm/core: continuation context limit exceeded: %v", e.Err)
}

func (e ContinuationContextLimitError) Unwrap() error { return ErrContextLimitExceeded }

func IsContinuationContextLimit(err error) bool {
	var e ContinuationContextLimitError
	return errors.As(err, &e)
}

func isContextLimitError(err error) bool {
	if err == nil {
		return false
	}
	return ContainsContextLimitError(strings.ToLower(err.Error()))
}

func ContainsContextLimitError(input string) bool {
	switch {
	case strings.Contains(input, "context length exceeded"),
		strings.Contains(input, "maximum context length"),
		strings.Contains(input, "exceeds context length"),
		strings.Contains(input, "exceeds the context window"),
		strings.Contains(input, "context window is") && strings.Contains(input, "exceeded"),
		strings.Contains(input, "prompt is too long"),
		strings.Contains(input, "prompt too long"),
		strings.Contains(input, "token limit"),
		strings.Contains(input, "too many tokens"),
		strings.Contains(input, "input is too long"),
		strings.Contains(input, "request too large"),
		strings.Contains(input, "context_length_exceeded"),
		strings.Contains(input, "resourceexhausted") && strings.Contains(input, "context"),
		strings.Contains(input, "request too large"):
		return true
	}
	return false
}
