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
		return out, response.NewError(http.StatusBadRequest, "bad request: "+out.Violations[0].Message)
	}
	return out, nil
}

func (h *Handler) exec(ctx context.Context, sess handler.Session, out *Output) error {
	in := &Input{}
	if err := in.Init(ctx, sess, out); err != nil {
		return err
	}
	out.Data = in.Queues
	if err := in.Validate(ctx, sess, out); err != nil || len(out.Violations) > 0 {
		return err
	}
	sqlSvc, err := sess.Db()
	if err != nil {
		return err
	}
	for _, rec := range in.Queues {
		if rec == nil {
			continue
		}
		if err = h.updateByID(ctx, sess, rec); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err = sqlSvc.Insert("turn_queue", rec); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *Handler) updateByID(ctx context.Context, sess handler.Session, rec *TurnQueue) error {
	if rec == nil || strings.TrimSpace(rec.Id) == "" {
		return fmt.Errorf("queue id is required for update")
	}
	if rec.Has == nil {
		return fmt.Errorf("queue patch markers are required for update")
	}
	sessionDb, err := sess.Db()
	if err != nil {
		return err
	}
	db, err := sessionDb.Db(ctx)
	if err != nil {
		return err
	}
	sets := make([]string, 0, 8)
	args := make([]interface{}, 0, 9)
	if rec.Has.ConversationId {
		sets = append(sets, "conversation_id = ?")
		args = append(args, rec.ConversationId)
	}
	if rec.Has.TurnId {
		sets = append(sets, "turn_id = ?")
		args = append(args, rec.TurnId)
	}
	if rec.Has.MessageId {
		sets = append(sets, "message_id = ?")
		args = append(args, rec.MessageId)
	}
	if rec.Has.QueueSeq {
		sets = append(sets, "queue_seq = ?")
		if rec.QueueSeq != nil {
			args = append(args, *rec.QueueSeq)
		} else {
			args = append(args, nil)
		}
	}
	if rec.Has.Status {
		sets = append(sets, "status = ?")
		args = append(args, rec.Status)
	}
	if rec.Has.CreatedAt {
		sets = append(sets, "created_at = ?")
		args = append(args, rec.CreatedAt)
	}
	if rec.Has.UpdatedAt {
		sets = append(sets, "updated_at = ?")
		args = append(args, rec.UpdatedAt)
	}
	if len(sets) == 0 {
		return nil
	}
	query := "UPDATE turn_queue SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	args = append(args, rec.Id)
	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, nErr := res.RowsAffected(); nErr == nil && n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
