package deleteexpired

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/handler/response"
)

// Handler removes expired oauth_link_state rows and reports the deleted-row
// count plus the oldest expiry among them for maintenance metrics.
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
		return out, response.NewError(http.StatusInternalServerError, "oauth link state cleanup failed")
	}
	return out, nil
}

func (h *Handler) exec(ctx context.Context, sess handler.Session, out *Output) error {
	in := &Input{}
	if err := sess.Stater().Bind(ctx, in); err != nil {
		return err
	}
	if in.Cleanup == nil || strings.TrimSpace(in.Cleanup.Before) == "" {
		return response.NewError(http.StatusBadRequest, "cleanup horizon is required")
	}
	before := strings.TrimSpace(in.Cleanup.Before)
	sqlx, err := sess.Db()
	if err != nil {
		return err
	}
	db, err := sqlx.Db(ctx)
	if err != nil {
		return err
	}
	var oldest sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT MIN(expires_at) FROM oauth_link_state WHERE expires_at <= ?`, before).Scan(&oldest); err != nil {
		return err
	}
	res, err := db.ExecContext(ctx,
		`DELETE FROM oauth_link_state WHERE expires_at <= ?`, before)
	if err != nil {
		return err
	}
	if n, aErr := res.RowsAffected(); aErr == nil {
		out.Deleted = n
	}
	if out.Deleted > 0 && oldest.Valid {
		out.OldestExpiresAt = strings.TrimSpace(oldest.String)
	}
	return nil
}
