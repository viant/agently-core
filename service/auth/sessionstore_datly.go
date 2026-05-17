package auth

import (
	"context"
	"database/sql"
	"strings"
	"sync"
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
	mu  sync.RWMutex
	db  *sql.DB
}

// Get loads a session by id.
func (s *SessionStoreDAO) Get(ctx context.Context, id string) (*SessionRecord, error) {
	if s == nil || s.dao == nil || strings.TrimSpace(id) == "" {
		return nil, nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp("session", "get", strings.TrimSpace(id), started, opErr)
	}()
	db, err := s.dbHandle()
	if err != nil {
		opErr = err
		return nil, err
	}
	const query = `SELECT s.id, s.user_id, s.provider, s.created_at, s.updated_at, s.expires_at,
       u.username, u.display_name, u.email, u.subject
FROM session s
LEFT JOIN users u ON u.id = s.user_id
WHERE s.id = ?
LIMIT 1`
	row := db.QueryRowContext(ctx, query, strings.TrimSpace(id))
	var (
		rec         SessionRecord
		updatedAt   sql.NullTime
		username    sql.NullString
		displayName sql.NullString
		email       sql.NullString
		subject     sql.NullString
	)
	if err := row.Scan(
		&rec.ID,
		&rec.UserID,
		&rec.Provider,
		&rec.CreatedAt,
		&updatedAt,
		&rec.ExpiresAt,
		&username,
		&displayName,
		&email,
		&subject,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		opErr = err
		return nil, err
	}
	rec.Subject = strings.TrimSpace(firstNonEmpty(subject.String, rec.UserID))
	rec.Username = strings.TrimSpace(firstNonEmpty(displayName.String, username.String, email.String, rec.UserID))
	rec.Email = strings.TrimSpace(email.String)
	return &rec, nil
}

func (s *SessionStoreDAO) dbHandle() (*sql.DB, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	s.mu.RLock()
	if s.db != nil {
		db := s.db
		s.mu.RUnlock()
		return db, nil
	}
	s.mu.RUnlock()
	conn, err := s.dao.Resource().Connector("agently")
	if err != nil {
		return nil, err
	}
	db, err := conn.DB()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.db == nil {
		s.db = db
	}
	cached := s.db
	s.mu.Unlock()
	return cached, nil
}

// Upsert inserts or updates a session record.
func (s *SessionStoreDAO) Upsert(ctx context.Context, rec *SessionRecord) error {
	if s == nil || s.dao == nil || rec == nil {
		return nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp("session", "upsert", strings.TrimSpace(rec.ID), started, opErr)
	}()
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
	opErr = err
	return err
}

// Delete removes a session by id.
func (s *SessionStoreDAO) Delete(ctx context.Context, id string) error {
	if s == nil || s.dao == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp("session", "delete", strings.TrimSpace(id), started, opErr)
	}()
	db, err := s.dbHandle()
	if err != nil {
		opErr = err
		return err
	}
	_, err = db.ExecContext(ctx, `DELETE FROM session WHERE id = ?`, strings.TrimSpace(id))
	opErr = err
	return err
}
