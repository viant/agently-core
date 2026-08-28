package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// VerifiedWorkspaceIdentity carries an already-verified workspace identity.
// Both session and bearer authentication paths must build this value only
// after token/session verification and pass it to the shared canonical
// resolver; the resolver never verifies credentials itself.
type VerifiedWorkspaceIdentity struct {
	Provider string
	Issuer   string
	Subject  string
	Email    string
}

// canonicalCacheMaxTTL is the hard upper bound for cached subject mappings.
// Provider reload and user-status changes must invalidate entries immediately;
// deployments without a cross-pod invalidation bus converge within this TTL.
const canonicalCacheMaxTTL = 30 * time.Second

type canonicalCacheEntry struct {
	id        string
	expiresAt time.Time
}

// CanonicalUserResolver maps verified workspace identities to the canonical
// Agently users.id. It is shared by the session and bearer entry paths so both
// apply identical provider/subject checks; the bearer path must not implement
// a second, weaker subject-to-user mapping.
type CanonicalUserResolver struct {
	users UserService
	ttl   time.Duration
	mu    sync.Mutex
	cache map[string]canonicalCacheEntry
}

// NewCanonicalUserResolver creates a resolver over the given user service.
func NewCanonicalUserResolver(users UserService) *CanonicalUserResolver {
	return &CanonicalUserResolver{
		users: users,
		ttl:   canonicalCacheMaxTTL,
		cache: map[string]canonicalCacheEntry{},
	}
}

// ResolveCanonicalWorkspaceUser returns the canonical users.id for a verified
// workspace identity. It accepts only already-verified identities, never
// creates or upserts a user, and fails closed (returns an error) when the
// canonical owner cannot be determined reliably.
func (r *CanonicalUserResolver) ResolveCanonicalWorkspaceUser(ctx context.Context, identity VerifiedWorkspaceIdentity) (string, error) {
	if r == nil || r.users == nil {
		return "", fmt.Errorf("canonical resolver: user service is not configured")
	}
	provider := strings.TrimSpace(identity.Provider)
	subject := strings.TrimSpace(identity.Subject)
	if provider == "" || subject == "" {
		return "", fmt.Errorf("canonical resolver: verified identity requires provider and subject")
	}
	key := provider + "|" + subject
	now := time.Now()
	r.mu.Lock()
	if entry, ok := r.cache[key]; ok && now.Before(entry.expiresAt) {
		r.mu.Unlock()
		return entry.id, nil
	}
	r.mu.Unlock()

	user, err := r.users.GetBySubjectAndProvider(ctx, subject, provider)
	if err != nil {
		return "", fmt.Errorf("canonical resolver: subject lookup failed: %w", err)
	}
	if user == nil || strings.TrimSpace(user.ID) == "" {
		return "", fmt.Errorf("canonical resolver: no canonical user for verified workspace identity")
	}
	if user.Disabled {
		// Disabled canonical users fail closed on both entry paths and are
		// never cached; re-enabling takes effect on the next request.
		return "", fmt.Errorf("canonical resolver: canonical user is disabled")
	}
	id := strings.TrimSpace(user.ID)
	r.mu.Lock()
	ttl := r.ttl
	if ttl <= 0 || ttl > canonicalCacheMaxTTL {
		ttl = canonicalCacheMaxTTL
	}
	r.cache[key] = canonicalCacheEntry{id: id, expiresAt: now.Add(ttl)}
	r.mu.Unlock()
	return id, nil
}

// Invalidate drops all cached subject mappings. Call on provider reload and
// on local user-status changes (disable/delete).
func (r *CanonicalUserResolver) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.cache = map[string]canonicalCacheEntry{}
	r.mu.Unlock()
}
