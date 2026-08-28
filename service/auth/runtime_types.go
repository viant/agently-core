package auth

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
	vcfg "github.com/viant/scy/auth/jwt/verifier"
)

type Runtime struct {
	cfg             *Config
	sessions        *Manager
	jwtMintKey      string
	jwtVerifier     *vcfg.Service
	jwtService      *JWTService
	handlerOpts     []HandlerOption
	ext             *authExtension
	stopRefresh     func()
	retryMu         sync.Mutex
	retryAtByID     map[string]time.Time
	loggedRetryByID map[string]time.Time

	delegatedRefresher DelegatedTokenRefresher
}

type runtimeAuthUser struct {
	EffectiveUserID string
	// CanonicalUserID is the Agently users.id resolved for the verified
	// workspace identity; it is a persistence identity only and never
	// replaces EffectiveUserID.
	CanonicalUserID string
	Subject         string
	Email           string
	Provider        string
	Tokens          *scyauth.Token
}

type runtimeAuthContextKey struct{}

func WithAuthExtensions(base http.Handler, runtime *Runtime) http.Handler {
	if runtime == nil || runtime.ext == nil {
		return base
	}
	mux := http.NewServeMux()
	runtime.ext.Register(mux)
	mux.Handle("/", base)
	return runtime.protect(mux)
}

func WithAuthProtection(base http.Handler, runtime *Runtime) http.Handler {
	if runtime == nil {
		return base
	}
	return runtime.protectAll(base)
}

func withRuntimeAuthUser(ctx context.Context, user *runtimeAuthUser) context.Context {
	if user == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, runtimeAuthContextKey{}, *user)
	ctx = InjectUser(ctx, user.effectiveUserID())
	if strings.TrimSpace(user.CanonicalUserID) != "" {
		ctx = iauth.WithCanonicalUserID(ctx, user.CanonicalUserID)
	}
	if strings.TrimSpace(user.Provider) != "" {
		ctx = iauth.WithProvider(ctx, user.Provider)
	}
	if user.Tokens != nil {
		ctx = InjectTokens(ctx, user.Tokens)
	}
	return ctx
}

// canonicalResolver returns the shared canonical workspace-user resolver used
// by both session and verified-bearer identity paths. It is owned by the auth
// extension so both entry paths share one cache and one set of checks.
func (r *Runtime) canonicalResolver() *CanonicalUserResolver {
	if r == nil || r.ext == nil {
		return nil
	}
	return r.ext.canonical
}

// SetDelegatedTokenRefresher installs the delegated (per-provider) refresher
// used by the background watcher for rows stored under delegated provider
// keys. Until it is installed, delegated rows are skipped without mutation.
func (r *Runtime) SetDelegatedTokenRefresher(refresher DelegatedTokenRefresher) {
	if r == nil {
		return
	}
	r.delegatedRefresher = refresher
}

func (u *runtimeAuthUser) effectiveUserID() string {
	if u == nil {
		return ""
	}
	if v := strings.TrimSpace(u.EffectiveUserID); v != "" {
		return v
	}
	if v := strings.TrimSpace(u.Subject); v != "" {
		return v
	}
	return strings.TrimSpace(u.Email)
}

func RuntimeUserFromContext(ctx context.Context) *UserInfo {
	if ctx == nil {
		return nil
	}
	if raw, ok := ctx.Value(runtimeAuthContextKey{}).(runtimeAuthUser); ok {
		return &UserInfo{
			Subject: raw.Subject,
			Email:   raw.Email,
		}
	}
	return nil
}

func (r *Runtime) refreshRetryKey(sess *Session) string {
	if r == nil || sess == nil {
		return ""
	}
	subject := strings.TrimSpace(sess.EffectiveUserID())
	provider := strings.TrimSpace(sess.Provider)
	if subject == "" {
		subject = strings.TrimSpace(sess.ID)
	}
	if subject == "" {
		return ""
	}
	return provider + "|" + subject
}

func (r *Runtime) loadRefreshRetryAt(sess *Session) time.Time {
	if r == nil || sess == nil {
		return time.Time{}
	}
	if !sess.TransientRefreshRetryAt.IsZero() {
		return sess.TransientRefreshRetryAt
	}
	key := r.refreshRetryKey(sess)
	if key == "" {
		return time.Time{}
	}
	r.retryMu.Lock()
	defer r.retryMu.Unlock()
	if until, ok := r.retryAtByID[key]; ok {
		if time.Now().Before(until) {
			sess.TransientRefreshRetryAt = until
			return until
		}
		delete(r.retryAtByID, key)
	}
	return time.Time{}
}

func (r *Runtime) storeRefreshRetryAt(sess *Session, until time.Time) {
	if r == nil || sess == nil || until.IsZero() {
		return
	}
	sess.TransientRefreshRetryAt = until
	key := r.refreshRetryKey(sess)
	if key == "" {
		return
	}
	r.retryMu.Lock()
	if r.retryAtByID == nil {
		r.retryAtByID = map[string]time.Time{}
	}
	r.retryAtByID[key] = until
	r.retryMu.Unlock()
}

func (r *Runtime) clearRefreshRetryAt(sess *Session) {
	if r == nil || sess == nil {
		return
	}
	sess.TransientRefreshRetryAt = time.Time{}
	key := r.refreshRetryKey(sess)
	if key == "" {
		return
	}
	r.retryMu.Lock()
	delete(r.retryAtByID, key)
	delete(r.loggedRetryByID, key)
	r.retryMu.Unlock()
}

func (r *Runtime) shouldLogRefreshRetry(sess *Session, until time.Time) bool {
	if r == nil || sess == nil || until.IsZero() {
		return true
	}
	key := r.refreshRetryKey(sess)
	if key == "" {
		return true
	}
	r.retryMu.Lock()
	defer r.retryMu.Unlock()
	if r.loggedRetryByID == nil {
		r.loggedRetryByID = map[string]time.Time{}
	}
	if prev, ok := r.loggedRetryByID[key]; ok && prev.Equal(until) {
		return false
	}
	r.loggedRetryByID[key] = until
	return true
}
