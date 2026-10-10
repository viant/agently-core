package executor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/policy"
	"github.com/viant/authz"
)

type resolvedDatasourceFacts struct{ facts authz.Facts }

func (p *resolvedDatasourceFacts) Resolve(context.Context) (authz.Facts, error) { return p.facts, nil }

func TestResolvedDatasourceAdmissionRetainsHostMappingAndRevocation(t *testing.T) {
	resource := authz.Resource{Kind: "window", ID: "window://example/StudioRecords", Version: "working", Tenant: "tenant"}
	principal := &resolvedDatasourceFacts{authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Minute)}}
	prepared, err := PrepareStaticAuthorization(StaticAuthorizationRegistration{
		ProviderRef: "host", CapabilityMappingRef: "host", PolicyVersion: "1",
		Service: &authz.Service{Provider: principal, Store: staticAuthStore{authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"execute": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}},
		Account: func(context.Context, authz.Facts) (string, error) { return "21", nil },
		AuthorityRevision: func(context.Context, authz.Facts, string) (string, time.Time, error) {
			return "identity-1", principal.facts.ValidUntil, nil
		},
		GateEvaluator: staticGateBridge(func(context.Context, authz.Resource, string, []authz.Entity) (bool, string, time.Time, string, string, string, string, error) {
			return true, "requirements", principal.facts.ValidUntil, "alice", "issuer", "tenant", "21", nil
		}),
		BackendResources: []policy.BackendBinding{{Operation: "datasource.fetch", ID: "records", Resource: authz.Resource{Tenant: "tenant"}, Action: "execute", UseResolvedResource: true}},
	})
	require.NoError(t, err)
	pin := identity.ResolvedResource{ProviderIdentity: "provider", URI: resource.ID, ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("definition"))}, AuthorityBinding: "verified-provider-authority", ValidUntil: time.Now().Add(time.Minute)}
	require.NoError(t, prepared.AuthorizeResolvedDatasource(context.Background(), pin, "records", map[string]interface{}{}))
	require.Error(t, prepared.AuthorizeResolvedDatasource(context.Background(), pin, "unmapped", nil))
	wrong := pin
	wrong.URI = "window://example/Other"
	require.Error(t, prepared.AuthorizeResolvedDatasource(context.Background(), wrong, "records", nil), "a descriptor alias cannot select another window policy")
	wrong = pin
	wrong.ResourceCandidate.Kind, wrong.ResourceCandidate.Revision = identity.StampedCandidate, "2"
	require.Error(t, prepared.AuthorizeResolvedDatasource(context.Background(), wrong, "records", nil), "the host policy must match the held revision")
	wrong = pin
	wrong.ValidUntil = time.Now().Add(-time.Second)
	require.Error(t, prepared.AuthorizeResolvedDatasource(context.Background(), wrong, "records", nil))
	wrong = pin
	wrong.ContentFingerprint = "unbound"
	require.Error(t, prepared.AuthorizeResolvedDatasource(context.Background(), wrong, "records", nil))
	wrong = pin
	wrong.AuthorityBinding = ""
	require.Error(t, prepared.AuthorizeResolvedDatasource(context.Background(), wrong, "records", nil))
	principal.facts.Roles = nil
	require.Error(t, prepared.AuthorizeResolvedDatasource(context.Background(), pin, "records", nil), "provider approval cannot replace host role admission")
	principal.facts.Roles = []string{"reader"}
	principal.facts.ValidUntil = time.Now().Add(-time.Second)
	require.Error(t, prepared.AuthorizeResolvedDatasource(context.Background(), pin, "records", nil), "expired host identity cannot use a valid provider pin")
}
