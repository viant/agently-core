package modelcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/runtime/memory"
)

// recorderObserver writes model-call data directly using conversation client.
type recorderObserver struct {
	client          apiconv.Client
	start           Info
	hasBeg          bool
	mu              sync.Mutex
	msgID           string
	ended           bool
	acc             strings.Builder
	streamPayloadID string
	streamLinked    bool
	streamStatusSet bool
	// Optional: resolve token prices for a model (per 1k tokens).
	priceProvider TokenPriceProvider
}

const finalizePersistTimeout = 15 * time.Second

func (o *recorderObserver) OnCallStart(ctx context.Context, info Info) (context.Context, error) {
	o.start = info
	o.hasBeg = true
	o.acc.Reset()
	o.streamPayloadID = ""
	o.streamLinked = false
	o.streamStatusSet = false
	if info.StartedAt.IsZero() {
		o.start.StartedAt = time.Now()
	}
	// Persist a redacted request payload for transcript/logging purposes so large
	// base64 attachments don't overwhelm the conversation payload store.
	if info.LLMRequest != nil {
		if redacted := RedactGenerateRequestForTranscript(info.LLMRequest); len(redacted) > 0 {
			info.Payload = redacted
		}
	}
	// Attach finish barrier so downstream can wait for persistence before emitting final message.
	ctx, _ = WithFinishBarrier(ctx)
	msgID := uuid.NewString()
	ctx = context.WithValue(ctx, memory.ModelMessageIDKey, msgID)
	o.mu.Lock()
	o.msgID = msgID
	o.ended = false
	o.mu.Unlock()
	turn, _ := memory.TurnMetaFromContext(ctx)
	// Store the assistant message ID at the turn level so the stream handler
	// (which uses the original non-enriched context) can read it for
	// parent_message_id on tool_op messages.
	memory.SetTurnModelMessageID(turn.TurnID, msgID)

	// Create interim assistant message to capture request payload in transcript
	if turn.ConversationID != "" {
		mode := ""
		if info.LLMRequest != nil && info.LLMRequest.Options != nil {
			mode = info.LLMRequest.Options.Mode
		}
		if err := o.patchInterimRequestMessage(ctx, turn, msgID, info.Payload, mode); err != nil {
			return ctx, err
		}
	}
	// Defer assigning stream payload id until first stream chunk,
	// so we can align it with message id to simplify lookups.

	// Start model call and persist request/provider request payloads
	if err := o.beginModelCall(ctx, msgID, turn, info); err != nil {
		return ctx, err
	}
	return ctx, nil
}

func (o *recorderObserver) OnCallEnd(ctx context.Context, info Info) error {
	// Ensure finish barrier is always released to avoid deadlocks.
	defer signalFinish(ctx)

	if !o.hasBeg { // tolerate missing start
		o.start = Info{}
	}
	if info.CompletedAt.IsZero() {
		info.CompletedAt = time.Now()
	}
	// attach to message/turn from context
	msgID := o.resolveMessageID(ctx)
	if msgID == "" {
		return nil
	}
	if o.isEnded(msgID) {
		return nil
	}

	return o.finalizeOpenCall(ctx, msgID, info)
}

// CloseIfOpen force-closes the current model call when it was started but did not
// reach a terminal state. It is used as a fallback from upper layers when providers
// exit early without invoking OnCallEnd.
func (o *recorderObserver) CloseIfOpen(ctx context.Context, info Info) error {
	msgID := o.resolveMessageID(ctx)
	if msgID == "" || o.isEnded(msgID) {
		return nil
	}
	if info.CompletedAt.IsZero() {
		info.CompletedAt = time.Now()
	}
	if strings.TrimSpace(info.Err) == "" {
		if cerr := ctx.Err(); cerr != nil {
			info.Err = cerr.Error()
		} else {
			info.Err = "forced close"
		}
	}
	return o.finalizeOpenCall(ctx, msgID, info)
}

func (o *recorderObserver) resolveMessageID(ctx context.Context) string {
	if msgID := strings.TrimSpace(memory.ModelMessageIDFromContext(ctx)); msgID != "" {
		return msgID
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.TrimSpace(o.msgID)
}

func (o *recorderObserver) isEnded(msgID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ended && strings.TrimSpace(o.msgID) == strings.TrimSpace(msgID)
}

func (o *recorderObserver) markEnded(msgID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if strings.TrimSpace(o.msgID) == strings.TrimSpace(msgID) {
		o.ended = true
	}
}

func (o *recorderObserver) finalizeOpenCall(ctx context.Context, msgID string, info Info) error {
	persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), finalizePersistTimeout)
	defer cancelPersist()
	turn, _ := memory.TurnMetaFromContext(persistCtx)

	// Prefer provider-supplied stream text; fall back to accumulated chunks.
	// Compute this BEFORE patchAssistantMessageFromInfo so it can use it as fallback.
	streamTxt := info.StreamText
	if strings.TrimSpace(streamTxt) == "" {
		streamTxt = o.acc.String()
	}

	// Persist assistant content. Use stream text as fallback when the LLM
	// response object doesn't have content (typed streaming providers).
	{
		infoWithStream := info
		if strings.TrimSpace(infoWithStream.StreamText) == "" {
			infoWithStream.StreamText = streamTxt
		}
		madeVisible, err := o.patchAssistantMessageFromInfo(persistCtx, msgID, infoWithStream)
		if err != nil {
			warnf("patchAssistantMessageFromInfo failed message=%q err=%v", strings.TrimSpace(msgID), err)
		} else if !madeVisible {
			if err := o.patchInterimFlag(persistCtx, msgID); err != nil {
				warnf("patchInterimFlag failed message=%q err=%v", strings.TrimSpace(msgID), err)
			}
		}
	}

	// Finish model call with response/providerResponse and stream payload.
	// Conversation terminal status is owned by turn finalization, not per-call
	// model lifecycle events.
	status := "completed"
	// Treat context cancellation and deadlines as terminated.
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status = "canceled"
	} else if strings.TrimSpace(info.Err) != "" {
		lowErr := strings.ToLower(strings.TrimSpace(info.Err))
		if strings.Contains(lowErr, "context canceled") || strings.Contains(lowErr, "context deadline exceeded") {
			status = "canceled"
		} else {
			status = "failed"
		}
	}

	errs := make([]error, 0, 1)
	if err := o.finishModelCall(persistCtx, msgID, status, info, streamTxt); err != nil {
		errs = append(errs, fmt.Errorf("finish model call: %w", err))
	}
	if err := o.persistOpenAIGeneratedFiles(persistCtx, msgID, turn, info); err != nil {
		warnf("persistOpenAIGeneratedFiles failed message=%q err=%v", strings.TrimSpace(msgID), err)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	o.markEnded(msgID)
	return nil
}

func (o *recorderObserver) patchAssistantMessageFromInfo(ctx context.Context, msgID string, info Info) (bool, error) {
	if strings.TrimSpace(msgID) == "" {
		log.Printf("[patchAssistant] SKIP empty msgID")
		return false, nil
	}
	resp := info.LLMResponse
	if resp == nil && len(info.ResponseJSON) > 0 {
		var decoded llm.GenerateResponse
		if err := json.Unmarshal(info.ResponseJSON, &decoded); err == nil {
			resp = &decoded
		}
	}
	respChoices := 0
	if resp != nil {
		respChoices = len(resp.Choices)
	}
	content, hasToolCalls := AssistantContentFromResponse(resp)
	content = strings.TrimSpace(content)
	streamTxt := strings.TrimSpace(info.StreamText)
	log.Printf("[patchAssistant] msg=%s respChoices=%d contentFromResp=%d streamText=%d hasToolCalls=%v finishReason=%q",
		msgID, respChoices, len(content), len(streamTxt), hasToolCalls, info.FinishReason)
	// Fall back to accumulated stream text when LLMResponse has no content
	if content == "" && streamTxt != "" {
		content = streamTxt
		log.Printf("[patchAssistant] msg=%s using streamText fallback contentLen=%d", msgID, len(content))
	}
	if !hasToolCalls && looksLikeElicitationContent(content) {
		log.Printf("[patchAssistant] msg=%s SKIP elicitation content", msgID)
		return false, nil
	}
	if hasToolCalls && o.isLikelyUserEcho(ctx, content) {
		content = ""
	}
	preamble := strings.TrimSpace(AssistantPreambleFromResponse(resp, content))
	if content == "" && preamble != "" {
		content = preamble
	}
	if content == "" && !hasToolCalls {
		log.Printf("[patchAssistant] msg=%s SKIP empty content after all fallbacks", msgID)
		return false, nil
	}
	// When the model response has tool calls but no text content, synthesize
	// a preamble from the tool names so the interim assistant message exists
	// in the transcript. This allows tool_op messages to reference it as
	// their parent_message_id, enabling the UI to group tool calls under the
	// correct model-call iteration.
	if content == "" && hasToolCalls {
		content = synthesizeToolPreamble(resp)
		log.Printf("[patchAssistant] msg=%s synthesized preamble for tool-only response: %q", msgID, content)
	}
	msg := apiconv.NewMessage()
	msg.SetId(msgID)
	if turn, ok := memory.TurnMetaFromContext(ctx); ok && strings.TrimSpace(turn.ConversationID) != "" {
		msg.SetConversationID(turn.ConversationID)
		if strings.TrimSpace(turn.TurnID) != "" {
			msg.SetTurnID(turn.TurnID)
		}
	}
	if runMeta, ok := memory.RunMetaFromContext(ctx); ok && runMeta.Iteration > 0 {
		msg.SetIteration(runMeta.Iteration)
	}
	// Store content always. Store raw_content only for tool-call responses so
	// transcripts can distinguish tool-driven interim content from normal replies.
	// Use finish reason as the authoritative signal — hasToolCalls from the
	// response object may be unreliable for typed streaming providers.
	msg.SetContent(content)
	// Determine finish reason from response object or from the Info struct
	// (which captures finish reason from typed streaming events).
	finishReason := strings.TrimSpace(info.FinishReason)
	if finishReason == "" && resp != nil && len(resp.Choices) > 0 {
		finishReason = strings.TrimSpace(resp.Choices[0].FinishReason)
	}
	finishLower := strings.ToLower(finishReason)
	isToolCallResponse := hasToolCalls || strings.Contains(finishLower, "tool")
	log.Printf("[patchAssistant] msg=%s finishReason=%q isToolCall=%v -> interim=%d contentHead=%q",
		msgID, finishReason, isToolCallResponse, func() int {
			if isToolCallResponse {
				return 1
			}
			return 0
		}(), content[:min(len(content), 60)])
	if isToolCallResponse {
		if preamble == "" {
			preamble = content
		}
		msg.SetPreamble(preamble)
		msg.SetRawContent(content)
		msg.SetInterim(1)
	} else {
		msg.SetInterim(0)
	}
	if err := o.client.PatchMessage(ctx, msg); err != nil {
		return false, err
	}
	return true, nil
}

// OnStreamDelta aggregates streamed chunks. Persistence strategy is controlled
// by AGENTLY_STREAM_PERSIST_MODE:
//   - legacy (default): append+upsert on every delta
//   - final: persist only once on FinishModelCall
func (o *recorderObserver) OnStreamDelta(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	o.publishStreamDelta(ctx, data)
	o.acc.Write(data)
	msgID := memory.ModelMessageIDFromContext(ctx)

	// Attempt to detect provider response id early from OpenAI Responses events.
	// We look for {"type":"response.created|response.completed","response":{"id":"..."}}.
	var probe struct {
		Type     string `json:"type"`
		Response struct {
			ID string `json:"id"`
		} `json:"response"`
	}
	// Fast-path check to avoid expensive JSON unmarshal on tiny chunks.
	// The minimal JSON that would satisfy the probe is:
	// {"type":"response.created","response":{"id":"x"}} 49 bytes or 48 when id is empty
	if len(data) >= 48 {
		if err := json.Unmarshal(data, &probe); err == nil {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(probe.Type)), "response.") && strings.TrimSpace(probe.Response.ID) != "" {
				// Always cache in-memory per-turn for quick reuse.
				if turn, ok := memory.TurnMetaFromContext(ctx); ok {
					memory.SetTurnTrace(turn.TurnID, strings.TrimSpace(probe.Response.ID))
					if debugtrace.Enabled() {
						debugtrace.Write("modelcall", "stream_response_anchor", map[string]any{
							"turnID":       strings.TrimSpace(turn.TurnID),
							"messageID":    strings.TrimSpace(msgID),
							"eventType":    strings.TrimSpace(probe.Type),
							"responseID":   strings.TrimSpace(probe.Response.ID),
							"turnTraceNow": strings.TrimSpace(memory.TurnTrace(turn.TurnID)),
						})
					}
				}
				// In legacy mode, also persist trace id early (one DB write).
				if !streamPersistFinalOnly() && strings.TrimSpace(msgID) != "" {
					upd := apiconv.NewModelCall()
					upd.SetMessageID(msgID)
					upd.SetTraceID(strings.TrimSpace(probe.Response.ID))
					_ = o.client.PatchModelCall(ctx, upd)
				}
			}
		}
	}

	// (1) Per-delta persistence is best-effort. If the turn context is already
	// canceled, keep accumulating in-memory only. Finalization uses a detached
	// context and will persist the full partial stream from o.acc.
	if ctx.Err() != nil {
		return nil
	}

	// (2) Mark model call as streaming once for status visibility. This remains
	// best-effort because a status write failure should not abort the stream.
	if !o.streamStatusSet {
		o.streamStatusSet = true
		if strings.TrimSpace(msgID) != "" {
			upd := apiconv.NewModelCall()
			upd.SetMessageID(msgID)
			upd.SetStatus("streaming")
			if err := o.client.PatchModelCall(ctx, upd); err != nil {
				warnf("patchModelCall streaming status failed message=%q err=%v", strings.TrimSpace(msgID), err)
			}
		}
	}
	if streamPersistFinalOnly() {
		return nil
	}

	// Legacy mode: per-delta persistence still happens, but we now rebuild from
	// the in-memory accumulator instead of read-append-rewrite via GetPayload.
	// This avoids extra DB reads and keeps the full partial stream available
	// even when an intermediate write is skipped or fails.

	// (3) Resolve stream payload id (message id or new UUID).
	id := strings.TrimSpace(o.streamPayloadID)
	if id == "" {
		if msgID := strings.TrimSpace(memory.ModelMessageIDFromContext(ctx)); msgID != "" {
			id = msgID
		} else {
			id = uuid.New().String()
		}
		o.streamPayloadID = id
	}

	// (4) Upsert the full accumulated stream body. Failures are logged and
	// ignored so persistence issues do not abort the provider stream.
	if _, err := o.upsertInlinePayload(ctx, id, "model_stream", "text/plain", []byte(o.acc.String())); err != nil {
		warnf("stream delta payload update failed message=%q err=%v", strings.TrimSpace(msgID), err)
		return nil
	}

	// (5) Link stream payload to model call once. This is also best-effort.
	if !o.streamLinked {
		if strings.TrimSpace(msgID) != "" {
			upd := apiconv.NewModelCall()
			upd.SetMessageID(msgID)
			upd.SetStreamPayloadID(id)
			if err := o.client.PatchModelCall(ctx, upd); err != nil {
				warnf("stream payload link failed message=%q payload=%q err=%v", strings.TrimSpace(msgID), strings.TrimSpace(id), err)
				return nil
			}
			o.streamLinked = true
		}
	}
	return nil
}

// WithRecorderObserver injects a recorder-backed Observer into context.
func WithRecorderObserver(ctx context.Context, client apiconv.Client) context.Context {
	_, ok := memory.TurnMetaFromContext(ctx) //ensure turn is in context
	if !ok {
		ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{
			TurnID:          uuid.New().String(),
			ConversationID:  memory.ConversationIDFromContext(ctx),
			ParentMessageID: memory.ModelMessageIDFromContext(ctx),
		})
	}
	return WithObserver(ctx, &recorderObserver{client: client})
}

// WithRecorderObserverWithPrice injects a recorder-backed Observer with an optional
// price resolver used to compute per-call cost from token usage.
// TokenPriceProvider exposes per-1k token pricing for a model id/name.
type TokenPriceProvider interface {
	TokenPrices(model string) (in float64, out float64, cached float64, ok bool)
}

func WithRecorderObserverWithPrice(ctx context.Context, client apiconv.Client, provider TokenPriceProvider) context.Context {
	_, ok := memory.TurnMetaFromContext(ctx)
	if !ok {
		ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{
			TurnID:          uuid.New().String(),
			ConversationID:  memory.ConversationIDFromContext(ctx),
			ParentMessageID: memory.ModelMessageIDFromContext(ctx),
		})
	}
	return WithObserver(ctx, &recorderObserver{client: client, priceProvider: provider})
}
