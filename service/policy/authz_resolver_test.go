package policy

import (
	"context"
	"github.com/viant/authz"
	"testing"
	"time"
)

type authzFacts struct{ facts authz.Facts }

func (f authzFacts) Resolve(context.Context) (authz.Facts, error) { return f.facts, nil }

type authzPolicies map[string]authz.Document

func (s authzPolicies) Get(_ context.Context, r authz.Resource) (authz.Document, error) {
	d, ok := s[r.ID]
	if !ok {
		return authz.Document{}, authz.ErrDenied
	}
	return d, nil
}
func (authzPolicies) Replace(context.Context, authz.Document, int64, string) (authz.Document, error) {
	return authz.Document{}, authz.ErrDenied
}
func TestSharedAuthzResolverDoesNotTrustRequestFactsOrDiscardEntityBounds(t *testing.T) {
	makeResource := func(id string) authz.Resource {
		return authz.Resource{Kind: "window", ID: id, Tenant: "one", Version: "1"}
	}
	policies := authzPolicies{}
	for _, id := range []string{"allowed", "scoped", "denied"} {
		p := authz.Policy{Mode: "protected", Rule: &authz.Rule{Kind: "all", Rules: []authz.Rule{{Kind: "role", Value: "reader"}, {Kind: "exposure", Value: "REPORTING"}}}}
		if id == "scoped" {
			p.EntityType = "publisher"
		}
		if id == "denied" {
			p.Rule = &authz.Rule{Kind: "role", Value: "admin"}
		}
		policies[id] = authz.Document{Resource: makeResource(id), Revision: 1, Policies: map[string]authz.Policy{"view": p}}
	}
	facts := authz.Facts{Subject: "alice", Tenant: "one", Issuer: "trusted", ValidUntil: time.Now().Add(time.Minute), Roles: []string{"reader"}, Exposures: []string{"REPORTING"}, EntityGroups: authz.EntityGroups{"publisher": {"127"}}}
	resolver := &AuthzResolver{Service: &authz.Service{Store: policies, Provider: authzFacts{facts}}, PolicyVersion: "authz-v1", Resource: func(_ context.Context, _ string, c Candidate) (authz.Resource, string, error) {
		return makeResource(c.ID), "view", nil
	}}
	request := &Request{Operation: OperationWindowView, Candidates: []Candidate{{ID: "allowed"}, {ID: "scoped"}, {ID: "denied"}}, Context: map[string]any{"roles": []string{"admin"}, "allowedEntities": map[string]any{"publisher": []string{"999"}}}}
	decision, err := resolver.Resolve(context.Background(), request)
	if err != nil || !decision.Allow || len(decision.AllowedIDs) != 1 || decision.AllowedIDs[0] != "allowed" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if decision.ExpiresAt.After(facts.ValidUntil) {
		t.Fatal("decision lease exceeded verified facts")
	}
	facts.Exposures = nil
	resolver.Service.Provider = authzFacts{facts}
	decision, err = resolver.Resolve(context.Background(), request)
	if err != nil || decision.Allow || len(decision.AllowedIDs) != 0 {
		t.Fatal("missing exposure accepted")
	}
	facts.ValidUntil = time.Now().Add(-time.Second)
	resolver.Service.Provider = authzFacts{facts}
	if _, err = resolver.Resolve(context.Background(), request); err == nil {
		t.Fatal("expired facts accepted")
	}
}
