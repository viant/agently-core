package auth

import (
	"context"
	"strings"
	"time"

	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"

	sessionread "github.com/viant/agently-core/pkg/agently/user/session"
	sessiondelete "github.com/viant/agently-core/pkg/agently/user/session/delete"
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
	out := &sessionread.SessionOutput{}
	in := sessionread.SessionInput{Id: id, Has: &sessionread.SessionInputHas{Id: true}}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("GET", sessionread.SessionPathURI)),
		datly.WithInput(&in),
		datly.WithOutput(out),
	); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 || out.Data[0] == nil {
		return nil, nil
	}
	row := out.Data[0]
	return &SessionRecord{
		ID:        row.Id,
		Subject:   row.UserId, // user_id column stores jwt.sub — the stable identity
		Username:  row.UserId, // display fallback until username is stored separately
		Provider:  row.Provider,
		CreatedAt: row.CreatedAt,
		ExpiresAt: row.ExpiresAt,
	}, nil
}

// Upsert inserts or updates a session record.
func (s *SessionStoreDAO) Upsert(ctx context.Context, rec *SessionRecord) error {
	if s == nil || s.dao == nil || rec == nil {
		return nil
	}
	// user_id stores jwt.sub — the stable identity from the IDP.
	userID := strings.TrimSpace(firstNonEmpty(rec.Subject, rec.Email))
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
	in := &sessiondelete.Input{Ids: []string{id}}
	out := &sessiondelete.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("DELETE", sessiondelete.PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	)
	return err
}
