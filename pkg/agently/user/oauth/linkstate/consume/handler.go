package consume

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	linkstate "github.com/viant/agently-core/pkg/agently/user/oauth/linkstate"
	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/handler/response"
)

// Handler performs the atomic pending-to-consumed transition with
// affected-row semantics: exactly one caller (across pods) can consume a
// state hash, and only when the row is unexpired, unconsumed, owned by the
// same canonical user and bound to the same workspace session. Every failed
// transition is classified after the fact for audit purposes only.
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
		return out, response.NewError(http.StatusInternalServerError, "oauth link state consume failed")
	}
	return out, nil
}

func (h *Handler) exec(ctx context.Context, sess handler.Session, out *Output) error {
	in := &Input{}
	if err := sess.Stater().Bind(ctx, in); err != nil {
		return err
	}
	if in.Consume == nil {
		return response.NewError(http.StatusBadRequest, "consume payload is required")
	}
	stateHash := strings.TrimSpace(in.Consume.StateHash)
	userID := strings.TrimSpace(in.Consume.CanonicalUserID)
	sessionHash := strings.TrimSpace(in.Consume.SessionHash)
	if stateHash == "" || userID == "" || sessionHash == "" {
		return response.NewError(http.StatusBadRequest, "stateHash, canonicalUserId and sessionHash are required")
	}
	now := strings.TrimSpace(in.Consume.Now)
	if now == "" {
		now = time.Now().UTC().Format(linkstate.TimeLayout)
	}
	sqlx, err := sess.Db()
	if err != nil {
		return err
	}
	db, err := sqlx.Db(ctx)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx,
		`UPDATE oauth_link_state
		 SET consumed_at = ?
		 WHERE state_hash = ? AND user_id = ? AND session_hash = ?
		   AND consumed_at IS NULL AND expires_at > ?`,
		now, stateHash, userID, sessionHash, now)
	if err != nil {
		return err
	}
	if n, aErr := res.RowsAffected(); aErr == nil && n == 1 {
		out.Outcome = OutcomeConsumed
		return nil
	}
	outcome, err := classifyFailure(ctx, db, stateHash, userID, sessionHash, now)
	if err != nil {
		return err
	}
	out.Outcome = outcome
	return nil
}

// classifyFailure explains a rejected transition for audit records. The order
// matters: replay (already consumed) and expiry win over ownership mismatches
// so a replayed state is never reported as another user's row.
func classifyFailure(ctx context.Context, db *sql.DB, stateHash, userID, sessionHash, now string) (string, error) {
	row := db.QueryRowContext(ctx,
		`SELECT user_id, session_hash, expires_at, consumed_at FROM oauth_link_state WHERE state_hash = ?`,
		stateHash)
	var (
		rowUser, rowSession, expiresAt string
		consumedAt                     sql.NullString
	)
	if err := row.Scan(&rowUser, &rowSession, &expiresAt, &consumedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OutcomeAbsent, nil
		}
		return "", fmt.Errorf("oauth link state classify: %w", err)
	}
	switch {
	case consumedAt.Valid && strings.TrimSpace(consumedAt.String) != "":
		return OutcomeAlreadyConsumed, nil
	case strings.TrimSpace(expiresAt) <= now:
		return OutcomeExpired, nil
	case strings.TrimSpace(rowUser) != userID:
		return OutcomeUserMismatch, nil
	case strings.TrimSpace(rowSession) != sessionHash:
		return OutcomeSessionMismatch, nil
	default:
		// The atomic update lost a race that has since resolved; report replay.
		return OutcomeAlreadyConsumed, nil
	}
}
