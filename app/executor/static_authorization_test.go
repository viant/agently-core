package executor

import (
	"context"
	"testing"
	"time"

	"github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/authz"
)

type staticAuthFacts struct{ value authz.Facts }

func (p staticAuthFacts) Resolve(context.Context) (authz.Facts, error) { return p.value, nil }

type staticAuthStore struct{ doc authz.Document }

func (s staticAuthStore) Get(context.Context, authz.Resource) (authz.Document, error) {
	return s.doc, nil
}
func (s staticAuthStore) Replace(context.Context, authz.Document, int64, string) (authz.Document, error) {
	return authz.Document{}, authz.ErrDenied
}

type staticGateBridge func(context.Context, authz.Resource, string, []authz.Entity) (bool, string, time.Time, string, string, string, string, error)

func (f staticGateBridge) Check(ctx context.Context, resource authz.Resource, action string, selected []authz.Entity) (bool, string, time.Time, string, string, string, string, error) {
	return f(ctx, resource, action, selected)
}

func TestPreparedAuthorizationRegistersExactHostMappings(t *testing.T) {
	resource := authz.Resource{Kind: "window", ID: "orders", Version: "1", Tenant: "tenant"}
	facts := authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}
	service := &authz.Service{Provider: staticAuthFacts{facts}, Store: staticAuthStore{authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"execute": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}}
	prepared, err := PrepareStaticAuthorization(StaticAuthorizationRegistration{
		ProviderRef: "shared", CapabilityMappingRef: "windows", PolicyVersion: "mapping-1", Service: service,
		Account: func(context.Context, authz.Facts) (string, error) { return "21", nil },
		AuthorityRevision: func(context.Context, authz.Facts, string) (string, time.Time, error) {
			return "identity-1", time.Now().Add(time.Minute), nil
		},
		GateEvaluator: staticGateBridge(func(context.Context, authz.Resource, string, []authz.Entity) (bool, string, time.Time, string, string, string, string, error) {
			return true, "requirements:policy", time.Now().Add(time.Minute), "alice", "issuer", "tenant", "21", nil
		}),
		EntityPermission: func(context.Context, authz.Facts, authz.Entity, string) (bool, error) { return true, nil },
		EntityRoles: func(context.Context, authz.Facts, authz.Entity) ([]string, error) {
			return []string{"entityReader"}, nil
		},
		WholeResources: []policy.ResourceBinding{{Operation: policy.OperationWindowView, CandidateID: "orders", Resource: resource, Action: "execute"}},
		Capabilities:   []permittedview.CapabilityBinding{{ResourceType: "window", Capability: "open", Global: true, Resource: resource, Action: "execute"}},
		BackendMapper: policy.BackendMapper(func(_ context.Context, operation, id string, _ map[string]interface{}) (authz.Resource, string, []authz.Entity, string, error) {
			if operation != "report.retrieve" || id != "report-run://created" {
				return authz.Resource{}, "", nil, "", policy.ErrDenied
			}
			return resource, "execute", nil, "", nil
		}),
		ProtectTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := prepared.WindowAuthorizer(context.Background(), "orders"); err != nil || !allowed {
		t.Fatalf("window callback=%v %v", allowed, err)
	}
	if allowed, err := prepared.WindowAuthorizer(context.Background(), "unknown"); err != nil || allowed {
		t.Fatalf("unknown window=%v %v", allowed, err)
	}
	builder := NewBuilder()
	if err := prepared.Register(builder); err != nil {
		t.Fatal(err)
	}
	if builder.authorizationProviders["shared"].Gate == nil || builder.authorizationProviders["shared"].ToolAuthorize == nil || builder.authorizationProviders["shared"].WindowAuthorize == nil || builder.authorizationProviders["shared"].EntityRoles == nil || builder.authorizationProviders["shared"].AuthorityRevision == nil || builder.capabilityMappings["windows"] == nil || builder.capabilityMappingsV2["windows"] == nil {
		t.Fatal("trusted registration was incomplete")
	}
	if err := builder.authorizationProviders["shared"].ReportAuthorize(context.Background(), "report.retrieve", "report-run://created"); err != nil {
		t.Fatalf("host dynamic report mapper was not registered: %v", err)
	}
}

func TestPreparedAuthorizationAllowsPrincipalOnlyMappingsWithoutEntityProvider(t *testing.T) {
	resource := authz.Resource{Kind: "window", ID: "dashboard", Version: "1", Tenant: "tenant"}
	facts := authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}
	service := &authz.Service{Provider: staticAuthFacts{facts}, Store: staticAuthStore{authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"execute": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}}
	registration := StaticAuthorizationRegistration{
		ProviderRef: "shared", CapabilityMappingRef: "global", PolicyVersion: "mapping-1", Service: service,
		Account: func(context.Context, authz.Facts) (string, error) { return "21", nil },
		AuthorityRevision: func(context.Context, authz.Facts, string) (string, time.Time, error) {
			return "identity-1", time.Now().Add(time.Minute), nil
		},
		GateEvaluator: staticGateBridge(func(context.Context, authz.Resource, string, []authz.Entity) (bool, string, time.Time, string, string, string, string, error) {
			return true, "requirements:policy", time.Now().Add(time.Minute), "alice", "issuer", "tenant", "21", nil
		}),
		WholeResources: []policy.ResourceBinding{{Operation: policy.OperationWindowView, CandidateID: "dashboard", Resource: resource, Action: "execute"}},
		Capabilities:   []permittedview.CapabilityBinding{{ResourceType: "window", Capability: "open", Global: true, Resource: resource, Action: "execute"}},
	}
	prepared, err := PrepareStaticAuthorization(registration)
	if err != nil {
		t.Fatalf("principal-only host required an entity provider: %v", err)
	}
	if allowed, err := prepared.WindowAuthorizer(context.Background(), "dashboard"); err != nil || !allowed {
		t.Fatalf("principal-only window admission=%v err=%v", allowed, err)
	}
	registration.Capabilities = append(registration.Capabilities, permittedview.CapabilityBinding{ResourceType: "customer", Capability: "read", EntityType: "customer", Resource: resource, Action: "execute"})
	if _, err := PrepareStaticAuthorization(registration); err == nil {
		t.Fatal("resource capability accepted without entity provider")
	}
	registration.Capabilities = registration.Capabilities[:1]
	registration.BackendResources = []policy.BackendBinding{{Operation: "datasource.fetch", ID: "rows", Resource: resource, Action: "retrieve", EntityType: "customer", Permission: "read", SelectionParameter: "customerId", SelectionMode: "single"}}
	if _, err := PrepareStaticAuthorization(registration); err == nil {
		t.Fatal("entity-scoped backend accepted without entity provider")
	}
}
