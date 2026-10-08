package executor

import (
	"context"
	"testing"
	"time"

	"github.com/viant/agently-core/service/policy"
	"github.com/viant/authz"
	identity "github.com/viant/agently-core/protocol/resource"
)

type forbiddenResourceSelections struct{ t *testing.T }

func (s forbiddenResourceSelections) GetSelection(context.Context, authz.ResourceFamily) (authz.SelectionDocument, error) {
	s.t.Fatal("explicit lifecycle authority recursively selected a default")
	return authz.SelectionDocument{}, authz.ErrDenied
}
func TestPreparedResourceAuthorityUsesExplicitPolicyWithoutSelectionRecursion(t *testing.T) {
	uri, _ := identity.ParseResourceURI("report://shared/Sales")
	family := authz.ResourceFamily{Kind: uri.Kind, ID: uri.String(), Tenant: "tenant"}
	resource := authz.Resource{Kind: uri.Kind, ID: uri.String(), Tenant: "tenant", Version: "logical"}
	facts := authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}
	account := "account"
	change := false
	lease := time.Now().Add(time.Minute)
	service := &authz.Service{Provider: staticAuthFacts{facts}, Store: staticAuthStore{authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"read": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}}
	prepared, err := PrepareStaticAuthorization(StaticAuthorizationRegistration{ProviderRef: "shared", CapabilityMappingRef: "mapping", PolicyVersion: "one", Service: service, Account: func(context.Context, authz.Facts) (string, error) { return account, nil }, AuthorityRevision: func(context.Context, authz.Facts, string) (string, time.Time, error) {
		return "identity-one", lease, nil
	}, GateEvaluator: staticGateBridge(func(context.Context, authz.Resource, string, []authz.Entity) (bool, string, time.Time, string, string, string, string, error) {
		old := account
		if change {
			account = "different"
		}
		return true, "gate-one", lease, "alice", "issuer", "tenant", old, nil
	}), ResourceRevisionMappings: forbiddenResourceSelections{t}, ResourceRevisionBindings: []policy.ResourceRevisionBinding{{Operation: "resource.selection.read", URI: uri.String(), Resource: family, Action: "read"}}})
	if err != nil {
		t.Fatal(err)
	}
	authority := prepared.ResourceAuthority()
	actor, err := authority.AuthorizeResource(context.Background(), uri, "logical", "selection.read")
	if err != nil || actor.Subject != "alice" || actor.AccountID != "account" || actor.ValidUntil.After(lease) {
		t.Fatalf("direct actor=%+v %v", actor, err)
	}
	if _, err := authority.AuthorizeResource(context.Background(), uri, "logical", "describe"); err == nil {
		t.Fatal("unmapped header action received implicit metadata grant")
	}
	if _, err := authority.AuthorizeResource(context.Background(), uri, "latest", "selection.read"); err == nil {
		t.Fatal("latest lifecycle selector accepted")
	}
	change = true
	if _, err := authority.AuthorizeResource(context.Background(), uri, "logical", "selection.read"); err == nil {
		t.Fatal("account changed across protected action")
	}
	if err := prepared.SetResourceRevisionMappings(nil); err == nil {
		t.Fatal("nil final selection store accepted")
	}
	empty, _ := authz.NewStaticSelectionStore(nil)
	if err := prepared.SetResourceRevisionMappings(empty); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Register(NewBuilder()); err != nil {
		t.Fatal(err)
	}
	if err := prepared.SetResourceRevisionMappings(empty); err == nil {
		t.Fatal("live selection binding mutated after registration")
	}
}

type resourceDecisionMarker struct{}
type resourceDecisionFixture struct {
	active           bool
	begins, finishes int
	deny             bool
}

func (s *resourceDecisionFixture) BeginDecision(ctx context.Context) (context.Context, func() error, error) {
	s.active = true
	s.begins++
	return context.WithValue(ctx, resourceDecisionMarker{}, true), func() error {
		s.active = false
		s.finishes++
		if s.deny {
			return policy.ErrIdentityRejected
		}
		return nil
	}, nil
}
func (*resourceDecisionFixture) WithoutDecision(ctx context.Context) context.Context {
	return context.WithValue(ctx, resourceDecisionMarker{}, false)
}
func TestResourceDecisionScopeClosesBeforeMutationAndSuppressesChangedAuthority(t *testing.T) {
	ctx := context.Background()
	uri, _ := identity.ParseResourceURI("report://shared/Sales")
	resource := authz.Resource{Kind: uri.Kind, ID: uri.String(), Tenant: "tenant", Version: "working"}
	facts := authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"writer"}, ValidUntil: time.Now().Add(time.Minute)}
	scope := &resourceDecisionFixture{}
	prepared, err := PrepareStaticAuthorization(StaticAuthorizationRegistration{ProviderRef: "shared", CapabilityMappingRef: "mapping", PolicyVersion: "one", DecisionScope: scope, Service: &authz.Service{Provider: staticAuthFacts{facts}, Store: staticAuthStore{authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"import": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "writer"}}}}}}, Account: func(context.Context, authz.Facts) (string, error) { return "account", nil }, AuthorityRevision: func(context.Context, authz.Facts, string) (string, time.Time, error) {
		return "identity-one", facts.ValidUntil, nil
	}, GateEvaluator: staticGateBridge(func(context.Context, authz.Resource, string, []authz.Entity) (bool, string, time.Time, string, string, string, string, error) {
		if !scope.active {
			t.Fatal("pure check outside decision scope")
		}
		return true, "gate-one", facts.ValidUntil, "alice", "issuer", "tenant", "account", nil
	}), ResourceRevisionMappings: forbiddenResourceSelections{t}, ResourceRevisionBindings: []policy.ResourceRevisionBinding{{Operation: "resource.import", URI: uri.String(), Resource: authz.ResourceFamily{Kind: uri.Kind, ID: uri.String(), Tenant: "tenant"}, Action: "import"}}})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := prepared.ResourceAuthority().AuthorizeResource(ctx, uri, "working", "import")
	if err != nil || !actor.Valid(time.Now()) || scope.active || scope.begins != 1 || scope.finishes != 1 {
		t.Fatalf("scope actor=%+v active=%v %v", actor, scope.active, err)
	}
	// A facade invokes its storage mutation only after authorization returns;
	// neither the decision marker nor the cached profile reaches that callback.
	mutation := func(writeCtx context.Context) {
		if scope.active || writeCtx.Value(resourceDecisionMarker{}) != nil {
			t.Fatal("decision context leaked into DB mutation")
		}
	}
	mutation(ctx)
	scope.deny = true
	actor, err = prepared.ResourceAuthority().AuthorizeResource(ctx, uri, "working", "import")
	if err == nil || actor.Subject != "" || scope.active || scope.begins != 2 || scope.finishes != 2 {
		t.Fatal("final role/account change released actor", actor, err)
	}
}

func (*resourceDecisionFixture) MetadataReadActive(context.Context) bool { return false }
