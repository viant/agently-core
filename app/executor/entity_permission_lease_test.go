package executor

import (
	"context"
	"testing"
	"time"

	"github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/authz"
)

func TestStaticAuthorizationForwardsLeasedOnlyEntityProvider(t *testing.T) {
	resource := authz.Resource{Kind: "dataSource", ID: "rows", Version: "1", Tenant: "tenant"}
	facts := authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}
	service := &authz.Service{Provider: staticAuthFacts{facts}, Store: staticAuthStore{authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"retrieve": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}}
	lease := time.Now().Add(30 * time.Second)
	calls := 0
	prepared, err := PrepareStaticAuthorization(StaticAuthorizationRegistration{ProviderRef: "shared", CapabilityMappingRef: "resources", PolicyVersion: "mapping", Service: service, Account: func(context.Context, authz.Facts) (string, error) { return "21", nil }, AuthorityRevision: func(context.Context, authz.Facts, string) (string, time.Time, error) {
		return "identity", facts.ValidUntil, nil
	}, GateEvaluator: staticGateBridge(func(context.Context, authz.Resource, string, []authz.Entity) (bool, string, time.Time, string, string, string, string, error) {
		return true, "gate", facts.ValidUntil, "alice", "issuer", "tenant", "21", nil
	}), EntityPermissionWithLease: func(context.Context, authz.Facts, authz.Entity, string) (bool, time.Time, error) {
		calls++
		return true, lease, nil
	}, Capabilities: []permittedview.CapabilityBinding{{ResourceType: "advertiser", Capability: "read", EntityType: "advertiser", Resource: resource, Action: "retrieve"}}, BackendResources: []policy.BackendBinding{{Operation: "datasource.fetch", ID: "rows", Resource: resource, Action: "retrieve", EntityType: "advertiser", Permission: "read", SelectionParameter: "advertiserId", SelectionMode: "single"}}})
	if err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder()
	if err = prepared.Register(builder); err != nil {
		t.Fatal(err)
	}
	provider := builder.authorizationProviders["shared"]
	if provider.EntityPermissionWithLease == nil {
		t.Fatal("registration dropped leased provider")
	}
	allow, returned, err := provider.EntityPermissionWithLease(context.Background(), facts, authz.Entity{Type: "advertiser", ID: "1"}, "read")
	if err != nil || !allow || !returned.Equal(lease) {
		t.Fatalf("permission lease not forwarded %v %v %v", allow, returned, err)
	}
	if err = provider.DatasourceAuthorize(context.Background(), "rows", map[string]interface{}{"advertiserId": "1"}); err != nil || calls != 2 {
		t.Fatalf("backend did not use leased provider: calls=%d err=%v", calls, err)
	}
}
