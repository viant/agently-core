package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/debugtrace"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/runtime/streaming"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
	stream "github.com/viant/agently-core/service/core/stream"
)

type StreamInput struct {
	*GenerateInput
	StreamID string
}

// StreamOutput aggregates streaming events into a slice.
type StreamOutput struct {
	Events    []streaming.Event `json:"events"`
	MessageID string            `json:"messageId,omitempty"`
}

func nop() {}

// Stream handles streaming LLM responses, structuring JSON output for text chunks,
// function calls and finish reasons.
func (s *Service) Stream(ctx context.Context, in, out interface{}) (func(), error) {
	input, output, err := s.validateStreamIO(in, out)
	if err != nil {
		return nop, err
	}
	if input != nil && input.GenerateInput != nil && input.GenerateInput.Options != nil {
		ctx = memory.WithRequestMode(ctx, input.GenerateInput.Options.Mode)
	}
	// StreamOutput is an accumulator for this invocation only. Reject reused
	// outputs to keep retry behavior deterministic and avoid mixing old events.
	if len(output.Events) > 0 {
		return nop, fmt.Errorf("stream output must be empty at start")
	}
	handler, cleanup, err := stream.PrepareStreamHandler(ctx, input.StreamID)
	if err != nil {
		return nop, err
	}

	s.ensureStreamingOption(input)
	req, model, err := s.prepareGenerateRequest(ctx, input.GenerateInput)
	if err != nil {
		return cleanup, err
	}
	streamer, ok := model.(llm.StreamingModel)
	if !ok {
		return cleanup, fmt.Errorf("model %T does not support streaming", model)
	}
	// Attach finish barrier so final message waits for model-call persistence.
	ctx, _ = modelcallctx.WithFinishBarrier(ctx)
	if s.streamPub != nil {
		ctx = modelcallctx.WithStreamPublisher(ctx, s.streamPub)
	}
	// Inject recorder with price resolver when available so cost gets computed.
	if tp, ok := s.llmFinder.(modelcallctx.TokenPriceProvider); ok {
		declared := ""
		if input != nil && input.GenerateInput != nil {
			declared = strings.TrimSpace(input.GenerateInput.Model)
		}
		if declared != "" {
			tp = modelcallctx.NewFixedModelPriceProvider(tp, declared)
		}
		ctx = modelcallctx.WithRecorderObserverWithPrice(ctx, s.convClient, tp)
	} else {
		ctx = modelcallctx.WithRecorderObserver(ctx, s.convClient)
	}
	var retErr error
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

	// Try anchor continuation when history provides a valid last response.
	// BuildContinuationRequest already skips multi-tool anchors. For streaming,
	// we apply an additional guard: only use continuation when there are NO
	// tool-call messages in the request (single-tool anchors where all outputs
	// are materialized). This avoids "No tool output found" provider errors
	// when tool results haven't been fully persisted to the anchor.
	var continuationRequest *llm.GenerateRequest
	if input.Binding != nil {
		candidate := s.BuildContinuationRequest(ctx, req, &input.Binding.History)
		debugtrace.LogToFile("core", "stream_continuation_check", map[string]interface{}{
			"hasCandidate":    candidate != nil,
			"hasLastResponse": input.Binding.History.LastResponse != nil,
			"lastResponseID": func() string {
				if input.Binding.History.LastResponse != nil {
					return input.Binding.History.LastResponse.ID
				}
				return ""
			}(),
			"traceCount":   len(input.Binding.History.Traces),
			"fullMsgCount": len(req.Messages),
		})
		if candidate != nil {
			hasToolResults := false
			for _, m := range candidate.Messages {
				if m.ToolCallId != "" {
					hasToolResults = true
					break
				}
			}
			// Only use continuation when tool results are present (all outputs
			// materialized) or when there are no tool calls at all (pure text
			// continuation). Skip if continuation has tool-call assistant messages
			// but no matching tool results — that's the fragile case.
			hasToolCalls := false
			for _, m := range candidate.Messages {
				if len(m.ToolCalls) > 0 {
					hasToolCalls = true
					break
				}
			}
			if !hasToolCalls || hasToolResults {
				continuationRequest = candidate
			} else if debugtrace.Enabled() {
				debugtrace.Write("core", "stream_continuation_skipped", map[string]any{
					"reason": "tool_calls_without_results_in_streaming",
				})
			}
		}
	}
	if debugtrace.Enabled() {
		activeReq := req
		mode := "full"
		if continuationRequest != nil {
			activeReq = continuationRequest
			mode = "continuation"
		}
		debugtrace.Write("core", "stream_request", map[string]any{
			"mode":               mode,
			"messageCount":       len(activeReq.Messages),
			"previousResponseID": strings.TrimSpace(activeReq.PreviousResponseID),
			"messages":           debugtrace.SummarizeMessages(activeReq.Messages),
		})
	}

	// Log final continuation decision
	debugtrace.LogToFile("core", "stream_mode", map[string]interface{}{
		"mode": func() string {
			if continuationRequest != nil {
				return "continuation"
			}
			return "full"
		}(),
		"msgCount": func() int {
			if continuationRequest != nil {
				return len(continuationRequest.Messages)
			}
			return len(req.Messages)
		}(),
		"previousResponseID": func() string {
			if continuationRequest != nil {
				return continuationRequest.PreviousResponseID
			}
			return ""
		}(),
	})

	// Retry starting stream up to 3 attempts. Consult provider-specific
	// BackoffAdvisor (e.g., Bedrock ThrottlingException -> 30s) when available.
	// For retry-safe transient failures while consuming events (before meaningful
	// output), restart the stream using the same retry budget.
	const maxStreamAttempts = 3
	var streamCh <-chan llm.StreamEvent
	for attempt := 0; attempt < maxStreamAttempts; attempt++ {
		llmRequest := req
		if continuationRequest != nil {
			llmRequest = continuationRequest
		}

		// Phase 1: establish provider stream for this attempt.
		streamCh, err = streamer.Stream(ctx, llmRequest)
		if err != nil {
			if isContextLimitError(err) {
				if continuationRequest != nil {
					retErr = ContinuationContextLimitError{Err: err}
					return cleanup, retErr
				}
				retErr = fmt.Errorf("%w: %v", ErrContextLimitExceeded, err)
				return cleanup, retErr
			}
			delay, retry := modelRetryDelay(model, err, attempt)
			if !retry || attempt == maxStreamAttempts-1 || ctx.Err() != nil {
				retErr = fmt.Errorf("failed to start Stream: %w", err)
				return cleanup, retErr
			}
			// Set model_call status to retrying before waiting
			s.setModelCallStatus(ctx, "retrying")
			if !waitRetryDelay(ctx, delay) {
				retErr = fmt.Errorf("failed to start Stream: %w", err)
				return cleanup, retErr
			}
			continue
		}

		// Phase 2: consume events from this stream attempt.
		consumeErr := s.consumeEvents(ctx, streamCh, handler, output)
		if consumeErr == nil {
			break
		}
		if attempt == maxStreamAttempts-1 || ctx.Err() != nil || !canRetryStreamConsume(consumeErr, output) {
			retErr = consumeErr
			return cleanup, retErr
		}
		delay, retry := modelRetryDelay(model, consumeErr, attempt)
		if !retry {
			retErr = consumeErr
			return cleanup, retErr
		}
		// Safe-retry consume failures can only carry empty/error-only attempt
		// output, so clear accumulated events before starting the next stream.
		if len(output.Events) > 0 {
			output.Events = output.Events[:0]
		}
		s.setModelCallStatus(ctx, "retrying")
		if !waitRetryDelay(ctx, delay) {
			retErr = consumeErr
			return cleanup, retErr
		}
	}

	var b strings.Builder
	for _, ev := range output.Events {
		if ev.Type == streaming.EventTypeTextDelta && strings.TrimSpace(ev.Content) != "" {
			b.WriteString(ev.Content)
		}
	}

	// Provide the shared assistant message ID to the caller; orchestrator writes the final assistant message.
	if msgID := memory.ModelMessageIDFromContext(ctx); msgID != "" {
		output.MessageID = msgID
	}

	return cleanup, nil
}

// moved to continuation.go

// validateStreamIO validates and unwraps inputs.
func (s *Service) validateStreamIO(in, out interface{}) (*StreamInput, *StreamOutput, error) {
	input, ok := in.(*StreamInput)
	if !ok {
		return nil, nil, svc.NewInvalidInputError(in)
	}
	output, ok := out.(*StreamOutput)
	if !ok {
		return nil, nil, svc.NewInvalidOutputError(out)
	}
	if input.StreamID == "" {
		return nil, nil, fmt.Errorf("streamID was empty")
	}
	return input, output, nil
}

// ensureStreamingOption turns on streaming at the request level.
func (s *Service) ensureStreamingOption(input *StreamInput) {
	if input.Options == nil {
		input.Options = &llm.Options{}
	}
	input.Options.Stream = true
}

// consumeEvents pulls from provider Stream channel, dispatches to handler and
// appends structured events to output. Stops on error or done.
func (s *Service) consumeEvents(ctx context.Context, ch <-chan llm.StreamEvent, handler stream.Handler, output *StreamOutput) error {
	var resErr error
	var ignore bool

	for event := range ch {
		if ignore {
			continue
		}

		if err := handler(ctx, &event); err != nil {
			// If the handler surfaced a provider error that indicates a context/window overflow,
			// wrap it with ErrContextLimitExceeded so callers can reliably detect it via errors.Is.
			if isContextLimitError(err) {
				// Preserve the human-friendly prefix while keeping the sentinel in the wrap chain.
				resErr = fmt.Errorf("failed to handle Stream event: %w", fmt.Errorf("%w: %v", ErrContextLimitExceeded, err))
			} else {
				resErr = fmt.Errorf("failed to handle Stream event: %w", err)
			}
			ignore = true
			continue
		}

		if err := s.appendStreamEvent(ctx, &event, output); err != nil {
			resErr = err
			ignore = true
			continue
		}

		// Stop on terminal events.
		if len(output.Events) > 0 {
			last := output.Events[len(output.Events)-1]
			switch last.Type {
			case streaming.EventTypeTurnCompleted:
				// finish_reason == "tool_calls" means model is requesting tools,
				// not that the stream is done. Continue consuming.
				if strings.TrimSpace(last.Status) != "tool_calls" {
					ignore = true
				}
			case streaming.EventTypeError:
				ignore = true
			}
		}
	}

	return resErr
}

// appendStreamEvent converts provider event to canonical streaming.Event(s).
// When the event carries typed Kind fields, those take precedence.
func (s *Service) appendStreamEvent(ctx context.Context, event *llm.StreamEvent, output *StreamOutput) error {
	now := time.Now()
	streamID := strings.TrimSpace(memory.ConversationIDFromContext(ctx))
	turnMeta, _ := memory.TurnMetaFromContext(ctx)
	mode := strings.TrimSpace(memory.RequestModeFromContext(ctx))
	if streamID == "" {
		streamID = strings.TrimSpace(turnMeta.ConversationID)
	}
	if event.Err != nil {
		output.Events = append(output.Events, streaming.Event{
			Type:           streaming.EventTypeError,
			StreamID:       streamID,
			ConversationID: streamID,
			Error:          event.Err.Error(),
			CreatedAt:      now,
		})
		if isContextLimitError(event.Err) {
			return fmt.Errorf("%w: %v", ErrContextLimitExceeded, event.Err)
		}
		return event.Err
	}

	// Typed delta path — map each provider Kind to a distinct domain event type.
	// All events are preserved in StreamOutput (no dropping).
	if event.Kind != "" {
		ev := streaming.Event{
			ID:              event.ItemID,
			StreamID:        streamID,
			ConversationID:  streamID,
			TurnID:          strings.TrimSpace(turnMeta.TurnID),
			AgentIDUsed:     strings.TrimSpace(turnMeta.Assistant),
			UserMessageID:   strings.TrimSpace(turnMeta.ParentMessageID),
			ParentMessageID: strings.TrimSpace(turnMeta.ParentMessageID),
			Mode:            mode,
			ResponseID:      event.ResponseID,
			ToolCallID:      event.ToolCallID,
			CreatedAt:       now,
		}
		switch event.Kind {
		case llm.StreamEventTextDelta:
			ev.Type = streaming.EventTypeTextDelta
			ev.AssistantMessageID = strings.TrimSpace(event.ItemID)
			ev.ModelCallID = strings.TrimSpace(event.ItemID)
			ev.Content = event.Delta
		case llm.StreamEventReasoningDelta:
			ev.Type = streaming.EventTypeReasoningDelta
			ev.AssistantMessageID = strings.TrimSpace(event.ItemID)
			ev.ModelCallID = strings.TrimSpace(event.ItemID)
			ev.Content = event.Delta
		case llm.StreamEventToolCallStarted:
			ev.Type = streaming.EventTypeToolCallStarted
			ev.AssistantMessageID = strings.TrimSpace(event.ItemID)
			ev.ToolName = event.ToolName
		case llm.StreamEventToolCallDelta:
			ev.Type = streaming.EventTypeToolCallDelta
			ev.AssistantMessageID = strings.TrimSpace(event.ItemID)
			ev.ToolName = event.ToolName
			ev.Content = event.Delta
		case llm.StreamEventToolCallCompleted:
			ev.Type = streaming.EventTypeToolCallCompleted
			ev.AssistantMessageID = strings.TrimSpace(event.ItemID)
			ev.ToolName = event.ToolName
			ev.Arguments = event.Arguments
		case llm.StreamEventUsage:
			ev.Type = streaming.EventTypeUsage
		case llm.StreamEventItemCompleted:
			ev.Type = streaming.EventTypeItemCompleted
		case llm.StreamEventTurnCompleted:
			ev.Type = streaming.EventTypeTurnCompleted
			ev.Status = event.FinishReason
		case llm.StreamEventError:
			ev.Type = streaming.EventTypeError
			ev.Error = event.Delta
		default:
			return nil
		}
		output.Events = append(output.Events, ev)
		return nil
	}

	// Legacy Response path — map to canonical types.
	resp := event.Response
	if resp == nil || len(resp.Choices) == 0 {
		return nil
	}
	choice := resp.Choices[0]
	// Tool calls → tool_call_completed
	if len(choice.Message.ToolCalls) > 0 {
		output.Events = append(output.Events, s.toolCallEvents(streamID, resp.ResponseID, &choice)...)
	}
	// Text → text_delta
	if content := choice.Message.Content; content != "" {
		output.Events = append(output.Events, streaming.Event{
			Type:            streaming.EventTypeTextDelta,
			StreamID:        streamID,
			ConversationID:  streamID,
			TurnID:          strings.TrimSpace(turnMeta.TurnID),
			AgentIDUsed:     strings.TrimSpace(turnMeta.Assistant),
			UserMessageID:   strings.TrimSpace(turnMeta.ParentMessageID),
			ParentMessageID: strings.TrimSpace(turnMeta.ParentMessageID),
			Mode:            mode,
			Content:         content,
			CreatedAt:       now,
		})
	}
	// Finish → turn_completed
	if choice.FinishReason != "" {
		output.Events = append(output.Events, streaming.Event{
			Type:           streaming.EventTypeTurnCompleted,
			StreamID:       streamID,
			ConversationID: streamID,
			Status:         choice.FinishReason,
			CreatedAt:      now,
		})
	}
	return nil
}

func (s *Service) toolCallEvents(streamID, responseID string, choice *llm.Choice) []streaming.Event {
	out := make([]streaming.Event, 0, len(choice.Message.ToolCalls))
	now := time.Now()
	for _, tc := range choice.Message.ToolCalls {
		name := tc.Name
		args := tc.Arguments
		if name == "" && tc.Function.Name != "" {
			name = tc.Function.Name
		}
		if args == nil && tc.Function.Arguments != "" {
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsed); err == nil {
				args = parsed
			}
		}
		out = append(out, streaming.Event{
			ID:             tc.ID,
			Type:           streaming.EventTypeToolCallCompleted,
			StreamID:       strings.TrimSpace(streamID),
			ConversationID: strings.TrimSpace(streamID),
			ToolName:       name,
			Arguments:      args,
			ResponseID:     strings.TrimSpace(responseID),
			CreatedAt:      now,
		})
	}
	return out
}

func waitRetryDelay(ctx context.Context, delay time.Duration) bool {
	select {
	case <-time.After(delay):
		return true
	case <-ctx.Done():
		return false
	}
}

func modelRetryDelay(model llm.Model, err error, attempt int) (time.Duration, bool) {
	if advisor, ok := model.(llm.BackoffAdvisor); ok {
		if delay, retry := advisor.AdviseBackoff(err, attempt); retry {
			return delay, true
		}
	}
	if isTransientNetworkError(err) {
		// Backoff: 1s, 2s, 4s
		return time.Second << attempt, true
	}
	return 0, false
}

// canRetryStreamConsume returns true when a consume-time failure can be retried
// by restarting the stream without risking duplicate meaningful output.
func canRetryStreamConsume(err error, output *StreamOutput) bool {
	if isContextLimitError(err) || output == nil {
		return false
	}
	if len(output.Events) == 0 {
		return true
	}
	for _, ev := range output.Events {
		switch ev.Type {
		case streaming.EventTypeError, "":
			// Error-only or empty events are safe to retry
		default:
			// Any content-bearing event means we've produced meaningful output
			return false
		}
	}
	return true
}
