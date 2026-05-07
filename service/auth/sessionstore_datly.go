package auth

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"

	sessionwrite "github.com/viant/agently-core/pkg/agently/user/session/write"
)

// NewSessionStoreDAO constructs a Datly-backed session store.
func NewSessionStoreDAO(dao *datly.Service) *SessionStoreDAO {
	return &SessionStoreDAO{dao: dao}
}

// SessionStoreDAO uses Datly to persist sessions.
type SessionStoreDAO struct {
	dao *datly.Service
}

// Get loads a session by id.
func (s *SessionStoreDAO) Get(ctx context.Context, id string) (*SessionRecord, error) {
	if s == nil || s.dao == nil || strings.TrimSpace(id) == "" {
		return nil, nil
	}
	conn, err := s.dao.Resource().Connector("agently")
	if err != nil {
		return nil, err
	}
	db, err := conn.DB()
	if err != nil {
		return nil, err
	}
	const query = `SELECT id, user_id, provider, created_at, updated_at, expires_at
FROM session
WHERE id = ?
LIMIT 1`
	row := db.QueryRowContext(ctx, query, strings.TrimSpace(id))
	var (
		rec       SessionRecord
		updatedAt sql.NullTime
	)
	if err := row.Scan(&rec.ID, &rec.UserID, &rec.Provider, &rec.CreatedAt, &updatedAt, &rec.ExpiresAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.Subject = rec.UserID
	rec.Username = rec.UserID
	return &rec, nil
}

// Upsert inserts or updates a session record.
func (s *SessionStoreDAO) Upsert(ctx context.Context, rec *SessionRecord) error {
	if s == nil || s.dao == nil || rec == nil {
		return nil
	}
	// user_id stores the canonical agently users.id when available.
	userID := strings.TrimSpace(firstNonEmpty(rec.UserID, rec.Subject, rec.Email))
	if strings.TrimSpace(rec.ID) == "" || strings.TrimSpace(userID) == "" {
		return nil
	}
	provider := strings.TrimSpace(rec.Provider)
	if provider == "" {
		provider = "local"
	}
	in := &sessionwrite.Input{Session: &sessionwrite.Session{}}
	in.Session.SetID(rec.ID)
	in.Session.SetUserID(userID)
	in.Session.SetProvider(provider)
	if !rec.ExpiresAt.IsZero() {
		in.Session.SetExpiresAt(rec.ExpiresAt)
	} else {
		in.Session.SetExpiresAt(time.Now().Add(168 * time.Hour))
	}
	out := &sessionwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("PATCH", sessionwrite.PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	)
	return err
}

// Delete removes a session by id.
func (s *SessionStoreDAO) Delete(ctx context.Context, id string) error {
	if s == nil || s.dao == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	conn, err := s.dao.Resource().Connector("agently")
	if err != nil {
		return err
	}
	db, err := conn.DB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `DELETE FROM session WHERE id = ?`, strings.TrimSpace(id))
	return err
}
