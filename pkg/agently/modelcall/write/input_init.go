package write

import (
	"context"
	"database/sql"
	"strings"

	"github.com/viant/xdatly/handler"
)

func (i *Input) Init(ctx context.Context, sess handler.Session, _ *Output) error {
	if err := sess.Stater().Bind(ctx, i); err != nil {
		return err
	}
	if err := i.loadCurrentModelCalls(ctx, sess); err != nil {
		return err
	}
	// apply defaults for NOT NULL columns when inserting new records
	for range i.ModelCalls { /* no-op after column removals */
	}
	return nil
}

func (i *Input) indexSlice() {
	i.CurByID = map[string]*ModelCall{}
}

func (i *Input) indexCurrent(rows []*ModelCall) {
	i.CurByID = map[string]*ModelCall{}
	for _, it := range rows {
		if it != nil {
			id := strings.TrimSpace(it.MessageID)
			if id == "" {
				continue
			}
			i.CurByID[id] = it
		}
	}
}

func (i *Input) loadCurrentModelCalls(ctx context.Context, sess handler.Session) error {
	i.CurByID = map[string]*ModelCall{}
	if len(i.ModelCalls) == 0 {
		return nil
	}

	ids := make([]string, 0, len(i.ModelCalls))
	seen := make(map[string]struct{}, len(i.ModelCalls))
	for _, rec := range i.ModelCalls {
		if rec == nil {
			continue
		}
		id := strings.TrimSpace(rec.MessageID)
		if id == "" {
			continue
		}
		rec.MessageID = id
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}

	sqlxService, err := sess.Db()
	if err != nil {
		return err
	}
	db, err := sqlxService.Db(ctx)
	if err != nil {
		return err
	}

	query := "SELECT * FROM model_call WHERE message_id IN (" + strings.TrimRight(strings.Repeat("?,", len(ids)), ",") + ")"
	args := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	current := make([]*ModelCall, 0, len(ids))
	for rows.Next() {
		rec := &ModelCall{}
		if err := scanModelCall(rows, rec); err != nil {
			return err
		}
		current = append(current, rec)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	i.indexCurrent(current)
	return nil
}

func scanModelCall(rows *sql.Rows, rec *ModelCall) error {
	return rows.Scan(
		&rec.MessageID,
		&rec.TurnID,
		&rec.Provider,
		&rec.Model,
		&rec.ModelKind,
		&rec.ErrorCode,
		&rec.ErrorMessage,
		&rec.FinishReason,
		&rec.PromptTokens,
		&rec.PromptCachedTokens,
		&rec.CompletionTokens,
		&rec.TotalTokens,
		&rec.PromptAudioTokens,
		&rec.CompletionReasoningTokens,
		&rec.CompletionAudioTokens,
		&rec.CompletionAcceptedPredictionTokens,
		&rec.CompletionRejectedPredictionTokens,
		&rec.Status,
		&rec.StartedAt,
		&rec.CompletedAt,
		&rec.LatencyMS,
		&rec.Cost,
		&rec.TraceID,
		&rec.SpanID,
		&rec.RequestPayloadID,
		&rec.ResponsePayloadID,
		&rec.ProviderRequestPayloadID,
		&rec.ProviderResponsePayloadID,
		&rec.StreamPayloadID,
		&rec.RunID,
		&rec.Iteration,
	)
}
