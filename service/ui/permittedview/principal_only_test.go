package permittedview

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/authz"
)

func TestPrincipalProjectionRequiresNoAccountResourcePolicyOrCapabilityMapping(t *testing.T) {
	facts := authz.Facts{Subject: "alice", Issuer: "verified", Roles: []string{"operator"}, Exposures: []string{"REPORTS_UI"}, ValidUntil: time.Now().Add(time.Minute)}
	resolver := &AuthzResolver{Service: &authz.Service{Provider: authzTestProvider{facts: facts}}}
	result, err := resolver.Resolve(context.Background(), &Request{IncludePrincipal: true})
	require.NoError(t, err)
	require.Equal(t, []string{"operator"}, result.Principal["roles"])
	require.Equal(t, []string{"REPORTS_UI"}, result.Principal["features"])
	require.Nil(t, result.Account)
	require.Empty(t, result.Resources)
	require.Empty(t, result.GlobalCapabilities)
	require.NotEmpty(t, result.AuthorizationVersion)
	result.Principal["roles"].([]string)[0] = "caller mutation"
	again, err := resolver.Resolve(context.Background(), &Request{IncludePrincipal: true})
	require.NoError(t, err)
	require.Equal(t, []string{"operator"}, again.Principal["roles"])
	changed := facts
	changed.Exposures = nil
	resolver.Service.Provider = authzTestProvider{facts: changed}
	revoked, err := resolver.Resolve(context.Background(), &Request{IncludePrincipal: true})
	require.NoError(t, err)
	require.Empty(t, revoked.Principal["features"])
	require.NotEqual(t, again.AuthorizationVersion, revoked.AuthorizationVersion)
	expired := facts
	expired.ValidUntil = time.Now().Add(-time.Second)
	resolver.Service.Provider = authzTestProvider{facts: expired}
	_, err = resolver.Resolve(context.Background(), &Request{IncludePrincipal: true})
	require.Error(t, err)
	resolver.Service.Provider = authzErrorProvider{err: authz.ErrDenied}
	_, err = resolver.Resolve(context.Background(), &Request{IncludePrincipal: true})
	require.ErrorIs(t, err, authz.ErrDenied)
}

func TestPrincipalProjectionPreservesDependencyAndCancellationCauses(t *testing.T) {
	cause := errors.New("identity dependency unavailable")
	resolver := &AuthzResolver{Service: &authz.Service{Provider: authzErrorProvider{err: cause}}}
	_, err := resolver.Resolve(context.Background(), &Request{IncludePrincipal: true})
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorIs(t, err, cause)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = resolver.Resolve(ctx, &Request{IncludePrincipal: true})
	require.ErrorIs(t, err, context.Canceled)
}
