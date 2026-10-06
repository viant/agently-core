package model

import (
	"context"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/runtime/usage"
	"time"
)

// Cached providers keep their configured callback. Turn accounting belongs to
// the invocation context, never the context that happened to populate the cache.
type invocationUsageModel struct {
	llm.Model
	name string
}

func (m *invocationUsageModel) record(ctx context.Context, name string, value *llm.Usage) {
	if aggregator := usage.FromContext(ctx); aggregator != nil && value != nil {
		if name == "" {
			name = m.name
		}
		aggregator.OnUsage(name, value)
	}
}

// Preserve endpoint-specific anchor behavior (notably ChatGPT HTTP).
func (m *invocationUsageModel) SupportsAnchorContinuation() bool {
	if aware, ok := m.Model.(interface{ SupportsAnchorContinuation() bool }); ok {
		return aware.SupportsAnchorContinuation()
	}
	return m.Model.Implements(base.SupportsContextContinuation)
}
func (m *invocationUsageModel) Generate(ctx context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	response, err := m.Model.Generate(ctx, request)
	if response != nil {
		m.record(ctx, response.Model, response.Usage)
	}
	return response, err
}

type invocationUsageStream struct {
	*invocationUsageModel
	stream llm.StreamingModel
}

func (m *invocationUsageStream) Stream(ctx context.Context, request *llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	source, err := m.stream.Stream(ctx, request)
	if err != nil {
		return nil, err
	}
	target := make(chan llm.StreamEvent)
	go func() {
		var last *llm.Usage
		name := m.name
		defer func() { m.record(ctx, name, last); close(target) }()
		for {
			select {
			case <-ctx.Done():
				return
			case event, open := <-source:
				if !open {
					return
				}
				if event.Response != nil {
					if event.Response.Model != "" {
						name = event.Response.Model
					}
					if event.Response.Usage != nil {
						copied := *event.Response.Usage
						last = &copied
					}
				}
				if event.Usage != nil {
					copied := *event.Usage
					last = &copied
				}
				select {
				case target <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return target, nil
}

type invocationUsageAdvisor struct {
	*invocationUsageModel
	advisor llm.BackoffAdvisor
}

func (m *invocationUsageAdvisor) AdviseBackoff(err error, attempt int) (time.Duration, bool) {
	return m.advisor.AdviseBackoff(err, attempt)
}

type invocationUsageStreamAdvisor struct {
	*invocationUsageStream
	advisor llm.BackoffAdvisor
}

func (m *invocationUsageStreamAdvisor) AdviseBackoff(err error, attempt int) (time.Duration, bool) {
	return m.advisor.AdviseBackoff(err, attempt)
}
func withInvocationUsage(model llm.Model, name string) llm.Model {
	base := &invocationUsageModel{Model: model, name: name}
	stream, streaming := model.(llm.StreamingModel)
	advisor, advising := model.(llm.BackoffAdvisor)
	if streaming {
		wrapped := &invocationUsageStream{invocationUsageModel: base, stream: stream}
		if advising {
			return &invocationUsageStreamAdvisor{invocationUsageStream: wrapped, advisor: advisor}
		}
		return wrapped
	}
	if advising {
		return &invocationUsageAdvisor{invocationUsageModel: base, advisor: advisor}
	}
	return base
}
