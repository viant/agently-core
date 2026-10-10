package executor_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/service/policy"
	forgesvc "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
)

type authzCacheFacts struct{ value authz.Facts }

func (p authzCacheFacts) Resolve(context.Context) (authz.Facts, error) { return p.value, nil }

type authzCacheStore struct{ doc authz.Document }

func (s authzCacheStore) Get(context.Context, authz.Resource) (authz.Document, error) {
	return s.doc, nil
}
func (authzCacheStore) Replace(context.Context, authz.Document, int64, string) (authz.Document, error) {
	return authz.Document{}, authz.ErrDenied
}

type authzReadyCatalog struct{}

func (authzReadyCatalog) AuthzReady() bool { return true }
func (authzReadyCatalog) List(context.Context, *forgesvc.WindowDefinitionListInput) (*forgesvc.WindowDefinitionListOutput, error) {
	return &forgesvc.WindowDefinitionListOutput{}, nil
}
func (authzReadyCatalog) Get(context.Context, *forgesvc.WindowDefinitionGetInput) (*forgesvc.WindowDefinitionGetOutput, error) {
	return nil, authz.ErrDenied
}

func TestWholeWindowAuthzWithLegacyCapabilityUIStillDisablesResultCache(t *testing.T) {
	config := &forgesvc.Config{WindowDefinitions: authzReadyCatalog{}}
	field := reflect.ValueOf(config).Elem().FieldByName("DynamicWindowAuthorizer")
	if !field.IsValid() {
		t.Skip("pinned Forge predates protected dynamic windows")
	}
	field.Set(reflect.ValueOf(func(context.Context, string) (bool, error) { return true, nil }))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("policy:\n  authorization:\n    mode: authz\n    providerRef: shared\n    ui: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	resource := authz.Resource{Kind: "window", ID: "orders", Version: "1", Tenant: "tenant"}
	facts := authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}
	service := &authz.Service{Store: authzCacheStore{authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"execute": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}, Provider: authzCacheFacts{facts}}
	provider := executor.AuthorizationProvider{Service: service, Account: func(context.Context, authz.Facts) (string, error) { return "account", nil }, PolicyVersion: "v1", PolicyResource: func(context.Context, string, policy.Candidate) (authz.Resource, string, error) {
		return resource, "execute", nil
	}, Gate: func(context.Context, authz.Facts, string, authz.Resource, string, *authz.Entity) (policy.GateResult, error) {
		return policy.GateResult{Allow: true, Revision: "r1", ValidUntil: time.Now().Add(time.Minute)}, nil
	}, WindowAuthorize: func(context.Context, string) (bool, error) { return true, nil }}
	rt, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithUIBridge(forgesvc.NewService(config)).WithAuthorizationProvider("shared", provider).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(context.Background())
	if !rt.DatasourceDisableCache || rt.WindowAuthorizer == nil || rt.PermittedResolver != nil {
		t.Fatalf("mixed authz/legacy runtime: disableCache=%v window=%v permitted=%T", rt.DatasourceDisableCache, rt.WindowAuthorizer != nil, rt.PermittedResolver)
	}
}

func TestPrincipalOnlyUIAuthzBuildsWithoutEntityPermissionProvider(t *testing.T) {
	config := &forgesvc.Config{WindowDefinitions: authzReadyCatalog{}}
	field := reflect.ValueOf(config).Elem().FieldByName("DynamicWindowAuthorizer")
	if !field.IsValid() {
		t.Skip("pinned Forge predates protected dynamic windows")
	}
	field.Set(reflect.ValueOf(func(context.Context, string) (bool, error) { return true, nil }))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("ui:\n  authorization:\n    mode: authz\n    providerRef: shared\n    capabilityMappingRef: global\n"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	resource := authz.Resource{Kind: "window", ID: "dashboard", Version: "1", Tenant: "tenant"}
	facts := authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}
	service := &authz.Service{Store: authzCacheStore{authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"execute": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}, Provider: authzCacheFacts{facts}}
	provider := executor.AuthorizationProvider{Service: service, Account: func(context.Context, authz.Facts) (string, error) { return "21", nil },
		AuthorityRevision: func(context.Context, authz.Facts, string) (string, time.Time, error) {
			return "revision", time.Now().Add(time.Minute), nil
		},
		AccountProjection: permittedview.NumericAccountProjection, PolicyVersion: "v1",
		Gate: func(context.Context, authz.Facts, string, authz.Resource, string, *authz.Entity) (policy.GateResult, error) {
			return policy.GateResult{Allow: true, Revision: "r1", ValidUntil: time.Now().Add(time.Minute)}, nil
		},
		WindowAuthorize:     func(context.Context, string) (bool, error) { return true, nil },
		DatasourceAuthorize: func(context.Context, string, map[string]interface{}) error { return nil },
	}
	builder := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithUIBridge(forgesvc.NewService(config)).WithAuthorizationProvider("shared", provider)
	builder.WithCapabilityMapping("global", func(context.Context, string, int, string, bool) (authz.Resource, string, *authz.Entity, error) {
		return resource, "execute", nil, nil
	})
	if rejected, err := builder.Build(context.Background()); err == nil {
		if rejected != nil {
			_ = rejected.Close(context.Background())
		}
		t.Fatal("principal-only authz accepted a missing trusted authority snapshot")
	}
	provider.AuthoritySnapshot = func(ctx context.Context) (gating.Principal, error) {
		current, err := service.Provider.Resolve(ctx)
		if err != nil {
			return gating.Principal{}, err
		}
		account, err := provider.Account(ctx, current)
		if err != nil {
			return gating.Principal{}, err
		}
		revision, _, err := provider.AuthorityRevision(ctx, current, account)
		return gating.Principal{Facts: current, AccountID: account, IdentityRevision: revision}, err
	}
	builder.WithAuthorizationProvider("shared", provider)
	rt, err := builder.Build(context.Background())
	if err != nil {
		t.Fatalf("principal-only authz build: %v", err)
	}
	defer rt.Close(context.Background())
	if rt.PermittedResolver == nil || rt.DatasourceAuthorizer == nil {
		t.Fatal("principal-only authz services were not installed")
	}
	snapshot, err := rt.PermittedResolver.Resolve(context.Background(), &permittedview.Request{ResourceType: "window", RequestedGlobalCapabilities: []string{"open"}, IncludePrincipal: true})
	if err != nil || snapshot == nil || !snapshot.GlobalCapabilities["open"] || snapshot.Principal["subject"] != "alice" {
		t.Fatalf("principal-only snapshot=%+v err=%v", snapshot, err)
	}
	service.Provider = authzCacheFacts{authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"unprivileged"}, ValidUntil: time.Now().Add(time.Hour)}}
	denied, err := rt.PermittedResolver.Resolve(context.Background(), &permittedview.Request{ResourceType: "window", RequestedGlobalCapabilities: []string{"open"}, IncludePrincipal: true})
	if err != nil || denied == nil || denied.GlobalCapabilities["open"] {
		t.Fatalf("principal-only ACL denial was not retained: snapshot=%+v err=%v", denied, err)
	}
}
