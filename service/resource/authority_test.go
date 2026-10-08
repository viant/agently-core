package resource

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	identity "github.com/viant/agently-core/protocol/resource"
	"testing"
	"time"
)

func TestAuthorityActorsBindFullFactsAndPreserveOriginalLease(t *testing.T) {
	current := gating.Principal{Facts: authz.Facts{Subject: "alice", Issuer: "https://idp.example", Tenant: "example", Roles: []string{"reader"}, Exposures: []string{"internal"}, AuthorityRevision: "business-context-1", ValidUntil: time.Now().Add(time.Minute)}, AccountID: "account", IdentityRevision: "principal-1"}
	resolve, verify := AuthorityActors(func(context.Context) (gating.Principal, error) { return current, nil })
	original, e := resolve(context.Background())
	require.NoError(t, e)
	current.Facts.ValidUntil = current.Facts.ValidUntil.Add(time.Minute)
	require.NoError(t, verify(context.Background(), original))
	current.Facts.Roles = []string{"reader", "writer"}
	require.ErrorIs(t, verify(context.Background(), original), identity.ErrResourceDenied)
	current.Facts.Roles = []string{"reader"}
	current.Facts.AuthorityRevision = "business-context-2"
	require.ErrorIs(t, verify(context.Background(), original), identity.ErrResourceDenied)
	current.Facts.AuthorityRevision = "business-context-1"
	original.ValidUntil = time.Now().Add(-time.Second)
	require.ErrorIs(t, verify(context.Background(), original), identity.ErrResourceDenied)
}
