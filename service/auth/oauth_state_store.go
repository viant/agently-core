package auth

import (
	"context"
	"time"

	"github.com/viant/agently-core/app/store/native"
	oauthlinkstate "github.com/viant/agently-core/internal/store/oauthlinkstate"
	dexec "github.com/viant/datly/exec"
)

// ErrOAuthStateInvalid deliberately gives every rejected consume the same
// public result: absent, expired, replayed, cross-user and cross-session states
// are indistinguishable to callers.
var ErrOAuthStateInvalid = oauthlinkstate.ErrInvalidState

// OAuthStateRecord contains non-secret link-state metadata. The authorization
// code, PKCE verifier and encrypted browser state remain outside this row.
type OAuthStateRecord = oauthlinkstate.Record

// OAuthStateStore is the distributed, single-use state contract used by MCP
// link endpoints. Datly v1 owns CAS and winner replay in generated components.
type OAuthStateStore interface {
	CreateOrGetPending(context.Context, *OAuthStateRecord) (*OAuthStateRecord, bool, error)
	Consume(context.Context, string, string, string) error
	DeleteExpired(context.Context, time.Time) (int64, time.Time, error)
}

// OAuthStateStoreNative attaches the trusted internal access scope to the
// generated v1 link-state reader and writer. HTTP values cannot set this scope.
type OAuthStateStoreNative struct {
	store *oauthlinkstate.Store
}

func NewOAuthStateStoreNative(invoker dexec.ComponentInvoker) *OAuthStateStoreNative {
	if invoker == nil {
		return nil
	}
	return &OAuthStateStoreNative{store: &oauthlinkstate.Store{Invoker: invoker}}
}

func oauthStateContext(ctx context.Context) context.Context {
	return native.WithAccess(ctx, native.Access{Internal: true})
}

func (s *OAuthStateStoreNative) CreateOrGetPending(ctx context.Context, record *OAuthStateRecord) (*OAuthStateRecord, bool, error) {
	return s.store.CreateOrGetPending(oauthStateContext(ctx), record)
}

func (s *OAuthStateStoreNative) GetPending(ctx context.Context, flowHash string) (*OAuthStateRecord, error) {
	return s.store.GetPending(oauthStateContext(ctx), flowHash)
}

func (s *OAuthStateStoreNative) Consume(ctx context.Context, stateHash, canonicalUserID, sessionHash string) error {
	return s.store.Consume(oauthStateContext(ctx), stateHash, canonicalUserID, sessionHash)
}

func (s *OAuthStateStoreNative) DeleteExpired(ctx context.Context, before time.Time) (int64, time.Time, error) {
	return s.store.DeleteExpired(oauthStateContext(ctx), before)
}
