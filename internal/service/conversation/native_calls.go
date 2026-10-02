package conversation

import (
	"context"
	"fmt"
	"strings"

	convcli "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	modelwrite "github.com/viant/agently-core/internal/datly/modelcall/write"
	toolwrite "github.com/viant/agently-core/internal/datly/toolcall/write"
	store "github.com/viant/agently-core/internal/store/conversation"
)

func (s *Service) callsStore() *store.CallsStore {
	return &store.CallsStore{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
}

func (s *Service) patchModelCallNative(ctx context.Context, call *convcli.MutableModelCall) error {
	if call == nil {
		return fmt.Errorf("model call mutation is required")
	}
	row := &modelwrite.ModelCall{}
	row.SetMessageId(strings.TrimSpace(call.MessageID))
	if h := call.Has; h != nil {
		if h.TurnID {
			row.SetTurnId(call.TurnID)
		}
		if h.Provider {
			row.SetProvider(call.Provider)
		}
		if h.Model {
			row.SetModel(call.Model)
		}
		if h.ModelKind {
			row.SetModelKind(call.ModelKind)
		}
		if h.Status {
			row.SetStatus(call.Status)
		}
		if h.ErrorCode {
			row.SetErrorCode(call.ErrorCode)
		}
		if h.ErrorMessage {
			row.SetErrorMessage(call.ErrorMessage)
		}
		if h.PromptTokens {
			row.SetPromptTokens(call.PromptTokens)
		}
		if h.PromptCachedTokens {
			row.SetPromptCachedTokens(call.PromptCachedTokens)
		}
		if h.CompletionTokens {
			row.SetCompletionTokens(call.CompletionTokens)
		}
		if h.TotalTokens {
			row.SetTotalTokens(call.TotalTokens)
		}
		if h.PromptAudioTokens {
			row.SetPromptAudioTokens(call.PromptAudioTokens)
		}
		if h.CompletionReasoningTokens {
			row.SetCompletionReasoningTokens(call.CompletionReasoningTokens)
		}
		if h.CompletionAudioTokens {
			row.SetCompletionAudioTokens(call.CompletionAudioTokens)
		}
		if h.CompletionAcceptedPredictionTokens {
			row.SetCompletionAcceptedPredictionTokens(call.CompletionAcceptedPredictionTokens)
		}
		if h.CompletionRejectedPredictionTokens {
			row.SetCompletionRejectedPredictionTokens(call.CompletionRejectedPredictionTokens)
		}
		if h.FinishReason {
			row.SetFinishReason(call.FinishReason)
		}
		if h.StartedAt {
			row.SetStartedAt(call.StartedAt)
		}
		if h.CompletedAt {
			row.SetCompletedAt(call.CompletedAt)
		}
		if h.LatencyMS {
			row.SetLatencyMs(call.LatencyMS)
		}
		if h.Cost {
			row.SetCost(call.Cost)
		}
		if h.TraceID {
			row.SetTraceId(call.TraceID)
		}
		if h.SpanID {
			row.SetSpanId(call.SpanID)
		}
		if h.RequestPayloadID {
			row.SetRequestPayloadId(call.RequestPayloadID)
		}
		if h.ResponsePayloadID {
			row.SetResponsePayloadId(call.ResponsePayloadID)
		}
		if h.ProviderRequestPayloadID {
			row.SetProviderRequestPayloadId(call.ProviderRequestPayloadID)
		}
		if h.ProviderResponsePayloadID {
			row.SetProviderResponsePayloadId(call.ProviderResponsePayloadID)
		}
		if h.StreamPayloadID {
			row.SetStreamPayloadId(call.StreamPayloadID)
		}
		if h.RunID {
			row.SetRunId(call.RunID)
		}
		if h.Iteration {
			row.SetIteration(call.Iteration)
		}
	}
	initial := nativePresence(row)
	output, err := s.callsStore().PatchModelTrustedResult(ctx, row)
	if err != nil {
		return err
	}
	return applyNativeMutation(call, output.Data, initial)
}

func (s *Service) patchToolCallNative(ctx context.Context, call *convcli.MutableToolCall) error {
	if call == nil {
		return fmt.Errorf("tool call mutation is required")
	}
	row := &toolwrite.ToolCall{}
	row.SetMessageId(strings.TrimSpace(call.MessageID))
	if h := call.Has; h != nil {
		if h.TurnID {
			row.SetTurnId(call.TurnID)
		}
		if h.OpID {
			row.SetOpId(call.OpID)
		}
		if h.Attempt {
			row.SetAttempt(call.Attempt)
		}
		if h.ToolName {
			row.SetToolName(call.ToolName)
		}
		if h.ToolKind {
			row.SetToolKind(call.ToolKind)
		}
		if h.Status {
			row.SetStatus(call.Status)
		}
		if h.RequestHash {
			row.SetRequestHash(call.RequestHash)
		}
		if h.ErrorCode {
			row.SetErrorCode(call.ErrorCode)
		}
		if h.ErrorMessage {
			row.SetErrorMessage(call.ErrorMessage)
		}
		if h.Retriable {
			row.SetRetriable(call.Retriable)
		}
		if h.StartedAt {
			row.SetStartedAt(call.StartedAt)
		}
		if h.CompletedAt {
			row.SetCompletedAt(call.CompletedAt)
		}
		if h.LatencyMS {
			row.SetLatencyMs(call.LatencyMS)
		}
		if h.Cost {
			row.SetCost(call.Cost)
		}
		if h.TraceID {
			row.SetTraceId(call.TraceID)
		}
		if h.SpanID {
			row.SetSpanId(call.SpanID)
		}
		if h.RequestPayloadID {
			row.SetRequestPayloadId(call.RequestPayloadID)
		}
		if h.ResponsePayloadID {
			row.SetResponsePayloadId(call.ResponsePayloadID)
		}
		if h.RunID {
			row.SetRunId(call.RunID)
		}
		if h.Iteration {
			row.SetIteration(call.Iteration)
		}
		if h.ResponseOverflow {
			row.ResponseOverflow = call.ResponseOverflow
		}
	}
	initial := nativePresence(row)
	output, err := s.callsStore().PatchToolTrustedResult(ctx, row)
	if err != nil {
		return err
	}
	return applyNativeMutation(call, output.Data, initial)
}
