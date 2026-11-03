package cohort

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/forge/backend/artifactcohort"
	"sync/atomic"
	"testing"
	"time"
)

type presentationPrincipalKey struct{}
type presentationPrincipal struct{}

func (presentationPrincipal) ResolvePrincipal(ctx context.Context) (gating.Principal, error) {
	value, ok := ctx.Value(presentationPrincipalKey{}).(gating.Principal)
	if !ok {
		return gating.Principal{}, authz.ErrDenied
	}
	return value, nil
}

type presentationCatalog struct {
	publisher *artifactcohort.Publisher
	calls     atomic.Int32
}

func (c *presentationCatalog) Discover(context.Context) (artifactcohort.Manifest, error) {
	c.calls.Add(1)
	return c.publisher.Snapshot()
}
func (c *presentationCatalog) Fetch(_ context.Context, input artifactcohort.FetchInput) (*artifactcohort.Artifact, error) {
	c.calls.Add(1)
	return c.publisher.Get(input.Root)
}

func TestCohortPresentationRoleWorksAcrossUserAccountsWithoutRemoteCalls(t *testing.T) {
	ctx := context.Background()
	publisher := artifactcohort.NewPublisher()
	root := artifactcohort.RootRef{URI: "navigation://sample/main", Kind: "navigation", Version: 1}
	_, _, err := publisher.Publish(ctx, root, json.RawMessage(`{"applications":[{"id":"sample","title":"Sample","menus":[]}]}`), func(context.Context, json.RawMessage) error { return nil })
	require.NoError(t, err)
	policy := authz.Resource{Tenant: "tenant", Kind: "cohort", ID: "readers", Version: "logical"}
	_, err = publisher.ReplaceCatalog(ctx, []authz.Document{{Resource: policy, Revision: 1, Policies: map[string]authz.Policy{artifactcohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}, []artifactcohort.Cohort{{Tenant: "tenant", Name: "readers", PolicyResource: policy, Roots: []artifactcohort.RootRef{root}}}, publisher.Generation())
	require.NoError(t, err)
	caller := &presentationCatalog{publisher: publisher}
	facts := verifiedCohortFacts{source: presentationPrincipal{}, tenant: "tenant"}
	replica, err := artifactcohort.NewReplica(artifactcohort.Config{Enabled: true, Source: "catalog", Tenant: "tenant", Account: "publisher-account", Caller: caller, Facts: facts, SessionKey: func(ctx context.Context) (string, error) {
		principal, err := (presentationPrincipal{}).ResolvePrincipal(ctx)
		return principal.Facts.Subject, err
	}})
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	before := caller.calls.Load()
	for _, user := range []struct{ subject, account, role string }{{"first", "account-one", "reader"}, {"second", "account-two", "reader"}, {"third", "publisher-account", "reader"}} {
		t.Run(user.subject, func(t *testing.T) {
			principal := gating.Principal{AccountID: user.account, Facts: authz.Facts{Subject: user.subject, Issuer: "verified", Tenant: "tenant", Roles: []string{user.role}, ValidUntil: time.Now().Add(time.Hour)}}
			current := context.WithValue(ctx, presentationPrincipalKey{}, principal)
			require.NoError(t, replica.Admit(current))
			roots, err := replica.PermittedRoots(current)
			require.NoError(t, err)
			require.Len(t, roots, 1)
			require.Equal(t, root.URI, roots[0].URI)
		})
	}
	withoutRole := context.WithValue(ctx, presentationPrincipalKey{}, gating.Principal{AccountID: "account-one", Facts: authz.Facts{Subject: "without-role", Issuer: "verified", Tenant: "tenant", ValidUntil: time.Now().Add(time.Hour)}})
	require.ErrorIs(t, replica.Admit(withoutRole), artifactcohort.ErrDenied, "producer-authored role policy still controls presentation")
	require.Equal(t, before, caller.calls.Load(), "user presentation must use the preloaded policy and metadata")
}
