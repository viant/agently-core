package auth

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/internal/authlog"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

const sessionStoreLoadTimeout = 5 * time.Second

// Session represents an authenticated user session.
//
// Identity model:
//   - UserID   = canonical agently users.id when available
//   - Subject  = raw oauth/jwt subject
//   - Username = jwt.preferred_username or jwt.name — display name only
//   - Email    = jwt.email — display / contact only
//
// Identity split:
//   - sess.UserID is the canonical users.id UUID for internal persistence,
//     token storage, token refresh, and session bookkeeping.
//   - request EffectiveUserID is the provider subject/email/username used by
//     ownership and visibility filters, including created_by_user_id.
//
// Do not feed sess.EffectiveUserID() into request context unless
// created_by_user_id has been migrated to canonical users.id. Token persistence
// can use the canonical UserID, but request-scoped ownership must remain
// subject-compatible.
type Session struct {
	ID       string         `json:"id"`
	UserID   string         `json:"userId,omitempty"`
	Username string         `json:"username"`
	Email    string         `json:"email,omitempty"`
	Subject  string         `json:"subject,omitempty"`
	Provider string         `json:"provider,omitempty"`
	Scopes   []string       `json:"scopes,omitempty"`
	Tokens   *scyauth.Token `json:"-"`
	// TransientRefreshRetryAt suppresses repeated refresh attempts/log spam
	// after a temporary token-endpoint failure. Runtime auth code persists the
	// cooldown through session metadata when a durable store is configured.
	TransientRefreshRetryAt time.Time `json:"-"`
	CreatedAt               time.Time `json:"createdAt"`
	ExpiresAt               time.Time `json:"expiresAt"`
}

// EffectiveUserID returns the canonical session identity when available.
// This is intended for session/token persistence. For request-scoped
// ownership or visibility checks, use Subject/Email/Username directly; those
// filters still compare against subject-based created_by_user_id values.
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
	ID             string    `json:"id"`
	UserID         string    `json:"userId,omitempty"`
	Username       string    `json:"username"`
	Email          string    `json:"email,omitempty"`
	Subject        string    `json:"subject,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	Scopes         []string  `json:"scopes,omitempty"`
	AccessToken    string    `json:"accessToken,omitempty"`
	IDToken        string    `json:"idToken,omitempty"`
	RefreshToken   string    `json:"refreshToken,omitempty"`
	TokenExpiresAt time.Time `json:"tokenExpiresAt,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

// SessionStore is a pluggable backend for persistent session storage.
type SessionStore interface {
	Get(ctx context.Context, id string) (*SessionRecord, error)
	Upsert(ctx context.Context, rec *SessionRecord) error
	Delete(ctx context.Context, id string) error
}

// Manager manages user sessions with an in-memory cache and optional persistent store.
type Manager struct {
	mu     sync.RWMutex
	mem    map[string]*Session
	ttl    time.Duration
	store  SessionStore // optional persistent backing store
	loadMu sync.Mutex
	loads  map[string]*sessionLoad
}

type sessionLoad struct {
	done chan struct{}
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
		loads: make(map[string]*sessionLoad),
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
	load, leader := m.beginSessionLoad(id)
	if !leader {
		<-load.done
		m.mu.RLock()
		s, ok = m.mem[id]
		m.mu.RUnlock()
		if ok {
			if s.IsExpired() {
				m.Delete(ctx, id)
				return nil
			}
			return s
		}
		return nil
	}
	defer m.finishSessionLoad(id, load)
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionStoreLoadTimeout)
	defer cancel()
	rec, err := m.store.Get(loadCtx, id)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "session_get",
			Classification: "persistence",
			Action:         "preserve_no_inject",
		})
		return nil
	}
	if rec == nil {
		return nil
	}
	sess := recordToSession(rec)
	if sess.IsExpired() {
		storeCtx, cancel := authStoreContext(ctx)
		if err := m.store.Delete(storeCtx, id); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "session_delete",
				UserID:         strings.TrimSpace(rec.UserID),
				Provider:       strings.TrimSpace(rec.Provider),
				Classification: "persistence",
				Action:         "preserve",
			})
		}
		cancel()
		return nil
	}
	m.mu.Lock()
	m.mem[id] = sess
	m.mu.Unlock()
	return sess
}

func (m *Manager) beginSessionLoad(id string) (*sessionLoad, bool) {
	m.loadMu.Lock()
	defer m.loadMu.Unlock()
	if existing, ok := m.loads[id]; ok {
		return existing, false
	}
	load := &sessionLoad{done: make(chan struct{})}
	m.loads[id] = load
	return load, true
}

func (m *Manager) finishSessionLoad(id string, load *sessionLoad) {
	m.loadMu.Lock()
	defer m.loadMu.Unlock()
	current, ok := m.loads[id]
	if ok && current == load {
		delete(m.loads, id)
		close(load.done)
	}
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
		storeCtx, cancel := authStoreContext(ctx)
		if err := m.store.Upsert(storeCtx, sessionToRecord(s)); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "session_put",
				UserID:         strings.TrimSpace(s.UserID),
				Provider:       strings.TrimSpace(s.Provider),
				Classification: "persistence",
				Action:         "preserve",
			})
		}
		cancel()
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
			authlog.Log(ctx, authlog.Event{
				Op:             "session_persist",
				UserID:         strings.TrimSpace(rec.UserID),
				Provider:       strings.TrimSpace(rec.Provider),
				Classification: "persistence",
				Action:         "preserve",
			})
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
		storeCtx, cancel := authStoreContext(ctx)
		if err := m.store.Delete(storeCtx, id); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "session_delete",
				Classification: "persistence",
				Action:         "preserve",
			})
		}
		cancel()
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
		Scopes:    append([]string(nil), r.Scopes...),
		CreatedAt: r.CreatedAt,
		ExpiresAt: r.ExpiresAt,
	}
	if r.AccessToken != "" || r.IDToken != "" || r.RefreshToken != "" {
		s.Tokens = &scyauth.Token{
			Token: oauth2.Token{
				AccessToken:  r.AccessToken,
				RefreshToken: r.RefreshToken,
				Expiry:       r.TokenExpiresAt,
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
		Scopes:    append([]string(nil), s.Scopes...),
		CreatedAt: s.CreatedAt,
		ExpiresAt: s.ExpiresAt,
	}
	if s.Tokens != nil {
		r.AccessToken = s.Tokens.AccessToken
		r.IDToken = s.Tokens.IDToken
		r.RefreshToken = s.Tokens.RefreshToken
		r.TokenExpiresAt = s.Tokens.Expiry
	}
	return r
}
