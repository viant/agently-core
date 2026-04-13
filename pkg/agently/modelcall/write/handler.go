package write

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/handler/response"
)

type Handler struct{}

func (h *Handler) Exec(ctx context.Context, sess handler.Session) (interface{}, error) {
	out := &Output{}
	out.Status.Status = "ok"
	if err := h.exec(ctx, sess, out); err != nil {
		var rErr *response.Error
		if errors.As(err, &rErr) {
			return out, err
		}
		out.setError(err)
	}
	if len(out.Violations) > 0 {
		out.setError(fmt.Errorf("failed validation"))
		return out, response.NewError(http.StatusBadRequest, "bad request")
	}
	return out, nil
}

func (h *Handler) exec(ctx context.Context, sess handler.Session, out *Output) error {
	in := &Input{}
	if err := in.Init(ctx, sess, out); err != nil {
		return err
	}
	out.Data = in.ModelCalls
	if err := in.Validate(ctx, sess, out); err != nil || len(out.Violations) > 0 {
		return err
	}
	sessionDB, err := sess.Db()
	if err != nil {
		return err
	}
	db, err := sessionDB.Db(ctx)
	if err != nil {
		return err
	}
	for _, rec := range in.ModelCalls {
		if rec == nil {
			continue
		}
		if _, ok := in.CurByID[rec.MessageID]; !ok {
			if err = insertModelCall(ctx, db, rec); err != nil {
				return err
			}
		} else {
			if err = updateModelCall(ctx, db, rec); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertModelCall(ctx context.Context, db *sql.DB, rec *ModelCall) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO model_call (
			message_id, turn_id, provider, model, model_kind, status,
			error_code, error_message,
			prompt_tokens, prompt_cached_tokens, completion_tokens, total_tokens,
			prompt_audio_tokens, completion_reasoning_tokens, completion_audio_tokens,
			completion_accepted_prediction_tokens, completion_rejected_prediction_tokens,
			finish_reason, started_at, completed_at, latency_ms, cost,
			trace_id, span_id,
			request_payload_id, response_payload_id,
			provider_request_payload_id, provider_response_payload_id,
			stream_payload_id, run_id, iteration
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.MessageID, rec.TurnID, rec.Provider, rec.Model, rec.ModelKind, rec.Status,
		rec.ErrorCode, rec.ErrorMessage,
		rec.PromptTokens, rec.PromptCachedTokens, rec.CompletionTokens, rec.TotalTokens,
		rec.PromptAudioTokens, rec.CompletionReasoningTokens, rec.CompletionAudioTokens,
		rec.CompletionAcceptedPredictionTokens, rec.CompletionRejectedPredictionTokens,
		rec.FinishReason, rec.StartedAt, rec.CompletedAt, rec.LatencyMS, rec.Cost,
		rec.TraceID, rec.SpanID,
		rec.RequestPayloadID, rec.ResponsePayloadID,
		rec.ProviderRequestPayloadID, rec.ProviderResponsePayloadID,
		rec.StreamPayloadID, rec.RunID, rec.Iteration,
	)
	return err
}

func updateModelCall(ctx context.Context, db *sql.DB, rec *ModelCall) error {
	if rec == nil || strings.TrimSpace(rec.MessageID) == "" {
		return fmt.Errorf("model call message id is required for update")
	}
	if rec.Has == nil {
		return fmt.Errorf("model call patch markers are required for update")
	}

	sets := make([]string, 0, 30)
	args := make([]interface{}, 0, 31)
	appendSet := func(enabled bool, column string, value interface{}) {
		if !enabled {
			return
		}
		sets = append(sets, column+" = ?")
		args = append(args, value)
	}

	appendSet(rec.Has.TurnID, "turn_id", rec.TurnID)
	appendSet(rec.Has.Provider, "provider", rec.Provider)
	appendSet(rec.Has.Model, "model", rec.Model)
	appendSet(rec.Has.ModelKind, "model_kind", rec.ModelKind)
	appendSet(rec.Has.Status, "status", rec.Status)
	appendSet(rec.Has.ErrorCode, "error_code", rec.ErrorCode)
	appendSet(rec.Has.ErrorMessage, "error_message", rec.ErrorMessage)
	appendSet(rec.Has.PromptTokens, "prompt_tokens", rec.PromptTokens)
	appendSet(rec.Has.PromptCachedTokens, "prompt_cached_tokens", rec.PromptCachedTokens)
	appendSet(rec.Has.CompletionTokens, "completion_tokens", rec.CompletionTokens)
	appendSet(rec.Has.TotalTokens, "total_tokens", rec.TotalTokens)
	appendSet(rec.Has.PromptAudioTokens, "prompt_audio_tokens", rec.PromptAudioTokens)
	appendSet(rec.Has.CompletionReasoningTokens, "completion_reasoning_tokens", rec.CompletionReasoningTokens)
	appendSet(rec.Has.CompletionAudioTokens, "completion_audio_tokens", rec.CompletionAudioTokens)
	appendSet(rec.Has.CompletionAcceptedPredictionTokens, "completion_accepted_prediction_tokens", rec.CompletionAcceptedPredictionTokens)
	appendSet(rec.Has.CompletionRejectedPredictionTokens, "completion_rejected_prediction_tokens", rec.CompletionRejectedPredictionTokens)
	appendSet(rec.Has.FinishReason, "finish_reason", rec.FinishReason)
	appendSet(rec.Has.StartedAt, "started_at", rec.StartedAt)
	appendSet(rec.Has.CompletedAt, "completed_at", rec.CompletedAt)
	appendSet(rec.Has.LatencyMS, "latency_ms", rec.LatencyMS)
	appendSet(rec.Has.Cost, "cost", rec.Cost)
	appendSet(rec.Has.TraceID, "trace_id", rec.TraceID)
	appendSet(rec.Has.SpanID, "span_id", rec.SpanID)
	appendSet(rec.Has.RequestPayloadID, "request_payload_id", rec.RequestPayloadID)
	appendSet(rec.Has.ResponsePayloadID, "response_payload_id", rec.ResponsePayloadID)
	appendSet(rec.Has.ProviderRequestPayloadID, "provider_request_payload_id", rec.ProviderRequestPayloadID)
	appendSet(rec.Has.ProviderResponsePayloadID, "provider_response_payload_id", rec.ProviderResponsePayloadID)
	appendSet(rec.Has.StreamPayloadID, "stream_payload_id", rec.StreamPayloadID)
	appendSet(rec.Has.RunID, "run_id", rec.RunID)
	appendSet(rec.Has.Iteration, "iteration", rec.Iteration)
	if len(sets) == 0 {
		return nil
	}

	query := "UPDATE model_call SET " + strings.Join(sets, ", ") + " WHERE message_id = ?"
	args = append(args, rec.MessageID)
	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}
