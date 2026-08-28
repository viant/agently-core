package write

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

// Handler implements CreateOrGetPending for oauth_link_state with
// affected-row/CAS semantics: exactly one caller (across pods) creates or
// replaces the flow row and owns the flow; every concurrent caller receives
// the stored row with Created=false. The unique flow_hash constraint is the
// cross-pod anchor; a replaced row must be the exact row this call observed.
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
		return out, response.NewError(http.StatusInternalServerError, "oauth link state write failed")
	}
	if len(out.Violations) > 0 {
		out.setError(fmt.Errorf("failed validation"))
		return out, response.NewError(http.StatusBadRequest, "bad request")
	}
	return out, nil
}

func (h *Handler) exec(ctx context.Context, sess handler.Session, out *Output) error {
	in := &Input{}
	if err := sess.Stater().Bind(ctx, in); err != nil {
		return err
	}
	if in.State == nil {
		return nil
	}
	if err := validateState(in.State); err != nil {
		return response.NewError(http.StatusBadRequest, err.Error())
	}
	sqlx, err := sess.Db()
	if err != nil {
		return err
	}
	db, err := sqlx.Db(ctx)
	if err != nil {
		return err
	}
	now := strings.TrimSpace(in.State.Now)
	if now == "" {
		now = time.Now().UTC().Format(linkstate.TimeLayout)
	}
	cur, err := loadByFlowHash(ctx, db, in.State.FlowHash)
	if err != nil {
		return err
	}
	// One CAS retry absorbs the create/replace races (unique-violation on
	// insert, concurrent replace); the loser adopts the winner's row.
	for attempt := 0; attempt < 2; attempt++ {
		if cur != nil && isPending(cur, now) {
			out.Data = cur
			out.Created = false
			return nil
		}
		if cur == nil {
			res, insErr := db.ExecContext(ctx,
				`INSERT INTO oauth_link_state (state_hash, flow_hash, user_id, session_hash, provider, expires_at, consumed_at, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, NULL, ?)`,
				in.State.StateHash, in.State.FlowHash, in.State.UserID, in.State.SessionHash,
				in.State.Provider, in.State.ExpiresAt, now)
			if insErr == nil {
				if n, aErr := res.RowsAffected(); aErr == nil && n == 1 {
					out.Data = stampCreated(in.State, now)
					out.Created = true
					return nil
				}
			}
			// Unique flow_hash race: another pod created the flow first. Adopt
			// its row; any other insert failure surfaces after the reload miss.
			reloaded, rErr := loadByFlowHash(ctx, db, in.State.FlowHash)
			if rErr != nil {
				return rErr
			}
			if reloaded == nil {
				if insErr != nil {
					return insErr
				}
				return fmt.Errorf("oauth link state insert affected no row")
			}
			cur = reloaded
			continue
		}
		// Replace a consumed or expired row atomically: the WHERE clause pins
		// the exact observed state_hash so only one replacer can win.
		res, upErr := db.ExecContext(ctx,
			`UPDATE oauth_link_state
			 SET state_hash = ?, user_id = ?, session_hash = ?, provider = ?, expires_at = ?, consumed_at = NULL, created_at = ?
			 WHERE flow_hash = ? AND state_hash = ? AND (consumed_at IS NOT NULL OR expires_at <= ?)`,
			in.State.StateHash, in.State.UserID, in.State.SessionHash, in.State.Provider,
			in.State.ExpiresAt, now, in.State.FlowHash, cur.StateHash, now)
		if upErr != nil {
			return upErr
		}
		if n, aErr := res.RowsAffected(); aErr == nil && n == 1 {
			out.Data = stampCreated(in.State, now)
			out.Created = true
			return nil
		}
		reloaded, rErr := loadByFlowHash(ctx, db, in.State.FlowHash)
		if rErr != nil {
			return rErr
		}
		if reloaded == nil {
			// The row vanished between the CAS miss and the reload (cleanup
			// race); retry as creation.
			cur = nil
			continue
		}
		cur = reloaded
	}
	// Both attempts lost the race: report the winner's row as pending.
	out.Data = cur
	out.Created = false
	return nil
}

func validateState(state *LinkState) error {
	switch {
	case strings.TrimSpace(state.StateHash) == "":
		return fmt.Errorf("stateHash is required")
	case strings.TrimSpace(state.FlowHash) == "":
		return fmt.Errorf("flowHash is required")
	case strings.TrimSpace(state.UserID) == "":
		return fmt.Errorf("userId is required")
	case strings.TrimSpace(state.SessionHash) == "":
		return fmt.Errorf("sessionHash is required")
	case strings.TrimSpace(state.Provider) == "":
		return fmt.Errorf("provider is required")
	}
	if _, err := time.Parse(linkstate.TimeLayout, strings.TrimSpace(state.ExpiresAt)); err != nil {
		return fmt.Errorf("expiresAt must use layout %q", linkstate.TimeLayout)
	}
	return nil
}

func stampCreated(state *LinkState, now string) *LinkState {
	created := *state
	created.ConsumedAt = nil
	created.CreatedAt = now
	return &created
}

// isPending reports whether the row is unconsumed and unexpired at now.
// Timestamps are parsed rather than compared as strings: rows travel through
// drivers/views that may re-format the stored DATETIME (e.g. RFC3339).
func isPending(state *LinkState, now string) bool {
	if state == nil || state.ConsumedAt != nil {
		return false
	}
	expiresAt, err := parseStateTime(state.ExpiresAt)
	if err != nil {
		return false
	}
	anchor, err := parseStateTime(now)
	if err != nil {
		return false
	}
	return expiresAt.After(anchor)
}

// parseStateTime accepts the canonical layout plus the RFC3339 variants some
// drivers/views produce when round-tripping DATETIME values.
func parseStateTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(linkstate.TimeLayout, value); err == nil {
		return parsed.UTC(), nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), nil
	}
	if parsed, err := time.Parse("2006-01-02T15:04:05", value); err == nil {
		return parsed.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp %q", value)
}

func loadByFlowHash(ctx context.Context, db *sql.DB, flowHash string) (*LinkState, error) {
	row := db.QueryRowContext(ctx,
		`SELECT state_hash, flow_hash, user_id, session_hash, provider, expires_at, consumed_at, created_at
		 FROM oauth_link_state WHERE flow_hash = ?`, flowHash)
	var (
		state    LinkState
		consumed sql.NullString
	)
	if err := row.Scan(&state.StateHash, &state.FlowHash, &state.UserID, &state.SessionHash,
		&state.Provider, &state.ExpiresAt, &consumed, &state.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if consumed.Valid && strings.TrimSpace(consumed.String) != "" {
		value := consumed.String
		state.ConsumedAt = &value
	}
	return &state, nil
}
