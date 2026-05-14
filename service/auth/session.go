package auth

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// Session represents an authenticated user session.
//
// Identity model:
//   - UserID   = canonical agently users.id when available
//   - Subject  = raw oauth/jwt subject
//   - Username = jwt.preferred_username or jwt.name — display name only
//   - Email    = jwt.email — display / contact only
//
// UserID is the preferred stable identity for session-owned auth, persistence,
// foreign-key joins, and token ownership. Subject is preserved as the raw IdP
// identity for provider lookups and diagnostics.
type Session struct {
	ID       string         `json:"id"`
	UserID   string         `json:"userId,omitempty"`
	Username string         `json:"username"`
	Email    string         `json:"email,omitempty"`
	Subject  string         `json:"subject,omitempty"`
	Provider string         `json:"provider,omitempty"`
	Tokens   *scyauth.Token `json:"-"`
	// TransientRefreshRetryAt suppresses repeated refresh attempts/log spam
	// after a temporary token-endpoint failure. In-memory only.
	TransientRefreshRetryAt time.Time `json:"-"`
	CreatedAt               time.Time `json:"createdAt"`
	ExpiresAt               time.Time `json:"expiresAt"`
}

// EffectiveUserID returns jwt.sub as the stable user identity.
// Falls back to Subject, then Email, then Username for sessions without a
// canonical stored user id (e.g. local/anonymous mode).
func (s *Session) EffectiveUserID() string {
	if s == nil {
		return ""
	}
	if v := strings.TrimSpace(s.UserID); v != "" {
		return v
	}
	if v := strings.TrimSpace(s.Subject); v != "" {
		return v
	}
	if v := strings.TrimSpace(s.Email); v != "" {
		return v
	}
	return strings.TrimSpace(s.Username)
}

// IsExpired returns true when the session has passed its expiry time.
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// SessionRecord is the persistent form of a session for external stores.
type SessionRecord struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userId,omitempty"`
	Username     string    `json:"username"`
	Email        string    `json:"email,omitempty"`
	Subject      string    `json:"subject,omitempty"`
	Provider     string    `json:"provider,omitempty"`
	AccessToken  string    `json:"accessToken,omitempty"`
	IDToken      string    `json:"idToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// SessionStore is a pluggable backend for persistent session storage.
type SessionStore interface {
	Get(ctx context.Context, id string) (*SessionRecord, error)
	Upsert(ctx context.Context, rec *SessionRecord) error
	Delete(ctx context.Context, id string) error
}

// Manager manages user sessions with an in-memory cache and optional persistent store.
type Manager struct {
	mu    sync.RWMutex
	mem   map[string]*Session
	ttl   time.Duration
	store SessionStore // optional persistent backing store
}

// NewManager creates a session manager with the given TTL.
// If store is nil, sessions are stored only in memory.
func NewManager(ttl time.Duration, store SessionStore) *Manager {
	if ttl <= 0 {
		ttl = 168 * time.Hour // 7 days default
	}
	return &Manager{
		mem:   make(map[string]*Session),
		ttl:   ttl,
		store: store,
	}
}

// Get retrieves a session by ID. Returns nil if not found or expired.
func (m *Manager) Get(ctx context.Context, id string) *Session {
	m.mu.RLock()
	s, ok := m.mem[id]
	m.mu.RUnlock()
	if ok {
		if s.IsExpired() {
			m.Delete(ctx, id)
			return nil
		}
		return s
	}
	if m.store == nil {
		return nil
	}
	rec, err := m.store.Get(ctx, id)
	if err != nil || rec == nil {
		return nil
	}
	sess := recordToSession(rec)
	if sess.IsExpired() {
		_ = m.store.Delete(ctx, id)
		return nil
	}
	m.mu.Lock()
	m.mem[id] = sess
	m.mu.Unlock()
	return sess
}

// Put stores a session.
func (m *Manager) Put(ctx context.Context, s *Session) {
	if s == nil {
		return
	}
	if s.ExpiresAt.IsZero() {
		s.ExpiresAt = time.Now().Add(m.ttl)
	}
	m.mu.Lock()
	m.mem[s.ID] = s
	m.mu.Unlock()
	if m.store != nil {
		_ = m.store.Upsert(ctx, sessionToRecord(s))
	}
}

// PutAsync stores the session in memory immediately and persists it to the
// backing store out-of-band. Use this on latency-sensitive HTTP auth paths
// where the request should not block on durable session persistence.
func (m *Manager) PutAsync(ctx context.Context, s *Session) {
	if s == nil {
		return
	}
	if s.ExpiresAt.IsZero() {
		s.ExpiresAt = time.Now().Add(m.ttl)
	}
	m.mu.Lock()
	m.mem[s.ID] = s
	m.mu.Unlock()
	if m.store == nil {
		return
	}
	rec := sessionToRecord(s)
	go func() {
		authPersistMu.Lock()
		defer authPersistMu.Unlock()
		persistCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := m.store.Upsert(persistCtx, rec); err != nil {
			log.Printf("[auth-session] async persist failed session=%q err=%v", strings.TrimSpace(rec.ID), err)
		}
	}()
}

// ActiveSessions returns a snapshot of all non-expired sessions in memory.
func (m *Manager) ActiveSessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := time.Now()
	var result []*Session
	for _, s := range m.mem {
		if s != nil && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			result = append(result, s)
		}
	}
	return result
}

// Delete removes a session.
func (m *Manager) Delete(ctx context.Context, id string) {
	m.mu.Lock()
	delete(m.mem, id)
	m.mu.Unlock()
	if m.store != nil {
		_ = m.store.Delete(ctx, id)
	}
}

func recordToSession(r *SessionRecord) *Session {
	s := &Session{
		ID:        r.ID,
		UserID:    r.UserID,
		Username:  r.Username,
		Email:     r.Email,
		Subject:   r.Subject,
		Provider:  r.Provider,
		CreatedAt: r.CreatedAt,
		ExpiresAt: r.ExpiresAt,
	}
	if r.AccessToken != "" || r.IDToken != "" || r.RefreshToken != "" {
		s.Tokens = &scyauth.Token{
			Token: oauth2.Token{
				AccessToken:  r.AccessToken,
				RefreshToken: r.RefreshToken,
			},
			IDToken: r.IDToken,
		}
	}
	return s
}

func sessionToRecord(s *Session) *SessionRecord {
	r := &SessionRecord{
		ID:        s.ID,
		UserID:    s.UserID,
		Username:  s.Username,
		Email:     s.Email,
		Subject:   s.Subject,
		Provider:  s.Provider,
		CreatedAt: s.CreatedAt,
		ExpiresAt: s.ExpiresAt,
	}
	if s.Tokens != nil {
		r.AccessToken = s.Tokens.AccessToken
		r.IDToken = s.Tokens.IDToken
		r.RefreshToken = s.Tokens.RefreshToken
	}
	return r
}
