package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider"
	"github.com/viant/agently-core/runtime/usage"
)

func TestCachedProviderUsageBelongsToEachInvocation(t *testing.T) {
	var callbacks atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream   bool
			Messages []struct{ Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		tokens, _ := strconv.Atoi(request.Messages[len(request.Messages)-1].Content)
		metrics := map[string]any{"prompt_tokens": tokens, "completion_tokens": 2, "total_tokens": tokens + 2, "prompt_tokens_details": map[string]any{"cached_tokens": 1}}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintln(w, `data: {"model":"fixture-model","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`)
			fmt.Fprintln(w, `data: {"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
			wire, _ := json.Marshal(map[string]any{"model": "fixture-model", "choices": []any{}, "usage": metrics})
			fmt.Fprintf(w, "\ndata: %s\n\ndata: [DONE]\n\n", wire)
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture-model", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "answer"}, "finish_reason": "stop"}}, "usage": metrics})
		}
	}))
	defer endpoint.Close()
	continuation := false
	config := &provider.Config{ID: "cached", Options: provider.Options{Provider: "openai", Model: "gpt-4o-mini", URL: endpoint.URL, EnvKey: "AGUI_USAGE_TEST_KEY", ContextContinuation: &continuation}}
	config.Options.UsageListener = func(string, *llm.Usage) { callbacks.Add(1) }
	t.Setenv("AGUI_USAGE_TEST_KEY", "fixture-only")
	finder := New(WithInitial(config))
	firstCtx, firstAgg := usage.WithAggregator(context.Background())
	cached, err := finder.Find(firstCtx, "cached")
	require.NoError(t, err)
	execute := func(ctx context.Context, model llm.Model, tokens int, stream bool) error {
		request := &llm.GenerateRequest{Messages: []llm.Message{{Role: "user", Content: strconv.Itoa(tokens)}}}
		if stream {
			events, err := model.(llm.StreamingModel).Stream(ctx, request)
			if err != nil {
				return err
			}
			for event := range events {
				if event.Err != nil {
					return event.Err
				}
			}
			return nil
		}
		_, err := model.Generate(ctx, request)
		return err
	}
	require.NoError(t, execute(firstCtx, cached, 3, false))
	prompt, completion, embed, cache := firstAgg.Totals()
	require.Equal(t, []int{3, 2, 0, 1}, []int{prompt, completion, embed, cache})
	secondCtx, secondAgg := usage.WithAggregator(context.Background())
	again, err := finder.Find(secondCtx, "cached")
	require.NoError(t, err)
	require.Same(t, cached, again)
	require.NoError(t, execute(secondCtx, again, 7, true))
	prompt, completion, embed, cache = secondAgg.Totals()
	require.Equal(t, []int{7, 2, 0, 1}, []int{prompt, completion, embed, cache})
	prompt, _, _, _ = firstAgg.Totals()
	require.Equal(t, 3, prompt, "cache hit must not charge the previous turn")
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, agg := usage.WithAggregator(context.Background())
			model, err := finder.Find(ctx, "cached")
			if err != nil {
				errors <- err
				return
			}
			if model != cached {
				errors <- fmt.Errorf("provider cache changed")
				return
			}
			tokens := 20 + i
			if err = execute(ctx, model, tokens, i%2 == 0); err != nil {
				errors <- err
				return
			}
			p, c, e, k := agg.Totals()
			if p != tokens || c != 2 || e != 0 || k != 1 {
				errors <- fmt.Errorf("request %d attribution: %d/%d/%d/%d", i, p, c, e, k)
			}
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, int32(10), callbacks.Load(), "configured provider callback remains exactly once per invocation")
}

type usageFixtureModel struct{ response *llm.GenerateResponse }

func (m *usageFixtureModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return m.response, nil
}
func (m *usageFixtureModel) Implements(string) bool { return false }

type usageFixtureStream struct {
	*usageFixtureModel
	events []llm.StreamEvent
	block  bool
}

func (m *usageFixtureStream) Stream(ctx context.Context, _ *llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	out := make(chan llm.StreamEvent)
	go func() {
		defer close(out)
		for _, event := range m.events {
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
		if m.block {
			<-ctx.Done()
		}
	}()
	return out, nil
}

type usageFixtureAdvisor struct{ *usageFixtureModel }

func (m *usageFixtureAdvisor) AdviseBackoff(error, int) (time.Duration, bool) {
	return time.Second, true
}

type usageFixtureStreamAdvisor struct{ *usageFixtureStream }

func (m *usageFixtureStreamAdvisor) AdviseBackoff(error, int) (time.Duration, bool) {
	return time.Second, true
}
func (m *usageFixtureStreamAdvisor) SupportsAnchorContinuation() bool { return true }
func TestInvocationUsageOptionalInterfacesAndCumulativeStream(t *testing.T) {
	plain := withInvocationUsage(&usageFixtureModel{}, "fixture")
	_, streaming := plain.(llm.StreamingModel)
	require.False(t, streaming)
	_, advising := plain.(llm.BackoffAdvisor)
	require.False(t, advising)
	advisor := withInvocationUsage(&usageFixtureAdvisor{&usageFixtureModel{}}, "fixture")
	_, streaming = advisor.(llm.StreamingModel)
	require.False(t, streaming)
	require.Implements(t, (*llm.BackoffAdvisor)(nil), advisor)
	both := withInvocationUsage(&usageFixtureStreamAdvisor{&usageFixtureStream{usageFixtureModel: &usageFixtureModel{}}}, "fixture")
	require.Implements(t, (*llm.StreamingModel)(nil), both)
	require.Implements(t, (*llm.BackoffAdvisor)(nil), both)
	require.True(t, both.(interface{ SupportsAnchorContinuation() bool }).SupportsAnchorContinuation())
	ctx, agg := usage.WithAggregator(context.Background())
	wrapped := withInvocationUsage(&usageFixtureStream{usageFixtureModel: &usageFixtureModel{}, events: []llm.StreamEvent{{Usage: &llm.Usage{PromptTokens: 3, TotalTokens: 3}}, {Response: &llm.GenerateResponse{Model: "fixture", Usage: &llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}}}, {Usage: &llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}}}}, "fixture")
	events, err := wrapped.(llm.StreamingModel).Stream(ctx, &llm.GenerateRequest{})
	require.NoError(t, err)
	for range events {
	}
	p, c, _, _ := agg.Totals()
	require.Equal(t, 3, p)
	require.Equal(t, 5, c, "usage snapshot/terminal duplicates must not double count")
	missingCtx, missing := usage.WithAggregator(context.Background())
	_, err = plain.Generate(missingCtx, &llm.GenerateRequest{})
	require.NoError(t, err)
	p, c, _, _ = missing.Totals()
	require.Zero(t, p+c)
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelCtx, cancelAgg := usage.WithAggregator(cancelCtx)
	blocked := withInvocationUsage(&usageFixtureStream{usageFixtureModel: &usageFixtureModel{}, events: []llm.StreamEvent{{Usage: &llm.Usage{PromptTokens: 4, TotalTokens: 4}}}, block: true}, "fixture")
	events, err = blocked.(llm.StreamingModel).Stream(cancelCtx, &llm.GenerateRequest{})
	require.NoError(t, err)
	<-events
	cancel()
	for range events {
	}
	p, _, _, _ = cancelAgg.Totals()
	require.Equal(t, 4, p)
}
