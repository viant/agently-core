package view

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/viant/afs"
	corepolicy "github.com/viant/agently-core/service/policy"
	repo "github.com/viant/agently-core/workspace/repository/forgewindow"
	"github.com/viant/authz"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/service/meta"
)

type metadataFacts struct{ facts authz.Facts }

func (p *metadataFacts) Resolve(context.Context) (authz.Facts, error) {
	if p.facts.Subject == "" {
		return authz.Facts{}, authz.ErrIdentityDenied
	}
	return p.facts, nil
}

type metadataSource map[string]json.RawMessage

func (s metadataSource) Candidates(_ context.Context, uri identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	raw, ok := s[uri.String()]
	if !ok {
		return nil, identity.ErrResourceDenied
	}
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(raw)}}, nil
}
func (s metadataSource) ReadCandidate(_ context.Context, uri identity.ResourceURI, _ identity.ResourceCandidate) (json.RawMessage, error) {
	return s[uri.String()], nil
}
func TestAuthenticatedCatalogMetadataIsDefaultVisibleWithDeclaredRequirements(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		var entries []forgeuisvc.SavedWindow
		var documents []authz.Document
		var selections []authz.SelectionDocument
		var bindings []corepolicy.ResourceRevisionBinding
		source := metadataSource{}
		uris := map[string]string{}
		for _, id := range []string{"baseline", "role", "feature"} {
			uri := "window://steward/" + id
			uris[id] = uri
			mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", id+".yaml"), "id: "+id+"\nwindowKey: "+id+"\ntitle: "+id+"\n")
			entries = append(entries, forgeuisvc.SavedWindow{WindowDefinitionSummary: forgeuisvc.WindowDefinitionSummary{WindowID: id, Title: id, ResourceURI: uri}, Key: id})
			source[uri] = json.RawMessage(`{"windowKey":"` + id + `","view":{"content":{"id":"` + id + `"}}}`)
			family := authz.ResourceFamily{Kind: "window", ID: uri, Tenant: "tenant"}
			requirement := authz.Policy{Mode: "public"}
			if id == "role" {
				requirement = authz.Policy{Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "ops"}}
			}
			if id == "feature" {
				requirement = authz.Policy{Mode: "protected", Rule: &authz.Rule{Kind: "exposure", Value: "REPORTS"}}
			}
			documents = append(documents, authz.Document{Resource: authz.Resource{Kind: family.Kind, ID: family.ID, Tenant: family.Tenant, Version: identity.WorkingCandidate}, Revision: 1, Policies: map[string]authz.Policy{"describe": requirement}})
			selections = append(selections, authz.SelectionDocument{Resource: family, Revision: 1, DefaultVersion: identity.WorkingCandidate})
			bindings = append(bindings, corepolicy.ResourceRevisionBinding{Operation: corepolicy.OperationWindowView, URI: uri, Resource: family, Action: "describe"})
		}
		policyStore, err := authz.NewStaticStore(documents)
		if err != nil {
			t.Fatal(err)
		}
		selectionStore, err := authz.NewStaticSelectionStore(selections)
		if err != nil {
			t.Fatal(err)
		}
		facts := &metadataFacts{facts: authz.Facts{Subject: "alice", Issuer: "fixture", Tenant: "tenant", ValidUntil: time.Now().Add(time.Minute)}}
		checker := &corepolicy.ActionAuthorizer{Service: &authz.Service{Provider: facts, Store: policyStore}, Account: func(context.Context, authz.Facts) (string, error) { return "account", nil }, Gate: func(context.Context, authz.Facts, string, authz.Resource, string, *authz.Entity) (corepolicy.GateResult, error) {
			return corepolicy.GateResult{Allow: true, Revision: "metadata-only", ValidUntil: facts.facts.ValidUntil}, nil
		}}
		authority, err := corepolicy.NewResourceRevisionPolicy(checker, corepolicy.OperationWindowView, bindings, selectionStore, func(context.Context, authz.Facts, string) (string, time.Time, error) {
			return "fixture-identity-revision", facts.facts.ValidUntil, nil
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		resolver := &identity.ResourceResolver{Source: source, Policy: authority}
		catalog, err := forgeuisvc.NewMetadataWindowCatalog(meta.New(afs.New(), root), root, entries, forgeuisvc.WithWindowResourceResolver(func(_ context.Context, key string) (*identity.ResourceResolver, identity.ResourceRef, error) {
			uri := uris[key]
			if uri == "" {
				return nil, identity.ResourceRef{}, identity.ErrResourceDenied
			}
			return resolver, identity.ResourceRef{URI: uri}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		bridge := forgeuisvc.NewService(&forgeuisvc.Config{WindowDefinitions: catalog, DynamicWindowAuthorizer: func(context.Context, string) (bool, error) { return true, nil }})
		// Canonical metadata must not depend on a second legacy ID mapping.
		views := New(repo.New(afs.New()), bridge, WithViewAuthorizer(func(context.Context, string) (bool, error) { return false, nil }), WithWindowAuthorizer(func(context.Context, string) (bool, error) { return false, nil }))
		output := &ListOutput{}
		if err := views.list(context.Background(), &ListInput{}, output); err != nil || len(output.Items) != 1 || output.Items[0].ID != "baseline" {
			t.Fatalf("default visibility / unmet requirements=%+v %v", output, err)
		}
		facts.facts.Roles = []string{"ops"}
		facts.facts.Exposures = []string{"REPORTS"}
		output = &ListOutput{}
		if err := views.list(context.Background(), &ListInput{}, output); err != nil || len(output.Items) != 3 {
			t.Fatalf("declared requirements satisfied=%+v %v", output, err)
		}
		// Describing a resource does not grant execution or any datasource action.
		if err := checker.AuthorizeMany(context.Background(), documents[0].Resource, "execute", nil, ""); err == nil {
			t.Fatal("metadata visibility granted execution")
		}
		facts.facts.Subject = ""
		if err := views.list(context.Background(), &ListInput{}, &ListOutput{}); err == nil {
			t.Fatal("unauthenticated metadata listed")
		}
	})
}

func TestCanonicalCatalogIDsKeepSharedRendererResourcesIndependent(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		for _, id := range []string{"forecastingCubeBuilder", "metricReportBuilder", "reportBuilder"} {
			mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", id+".yaml"), "id: "+id+"\nwindowKey: reportBuilder\ntitle: "+id+"\n")
		}
		entries := []forgeuisvc.SavedWindow{}
		source := metadataSource{}
		for _, id := range []string{"forecastingCubeBuilder", "metricReportBuilder", "reportBuilder"} {
			uri := "window://steward/" + id
			entries = append(entries, forgeuisvc.SavedWindow{WindowDefinitionSummary: forgeuisvc.WindowDefinitionSummary{WindowID: id, Title: id, ResourceURI: uri}, Key: id})
			source[uri] = json.RawMessage(`{"windowKey":"reportBuilder","view":{"content":{"id":"` + id + `"}}}`)
		}
		policy := catalogAliasPolicy{denied: "metricReportBuilder"}
		resolver := &identity.ResourceResolver{Source: source, Policy: policy}
		catalog, err := forgeuisvc.NewMetadataWindowCatalog(meta.New(afs.New(), root), root, entries, forgeuisvc.WithWindowResourceResolver(func(_ context.Context, id string) (*identity.ResourceResolver, identity.ResourceRef, error) {
			return resolver, identity.ResourceRef{URI: "window://steward/" + id}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		service := New(repo.New(afs.New()), forgeuisvc.NewService(&forgeuisvc.Config{WindowDefinitions: catalog}))
		items, err := service.loadAll(context.Background())
		if err != nil || len(items) != 2 {
			t.Fatalf("distinct canonical catalog=%+v %v", items, err)
		}
		for _, item := range items {
			if item.ID == "metricReportBuilder" || item.WindowKey != item.ID || item.Resource.URI != "window://steward/"+item.ID {
				t.Fatalf("renderer alias replaced identity: %+v", item)
			}
		}
		if _, err := service.loadRequested(context.Background(), "metricReportBuilder", nil); err == nil {
			t.Fatal("denied exact ID fell through allowed reportBuilder renderer")
		}
		item, err := service.loadRequested(context.Background(), "forecastingCubeBuilder", &identity.ResourceRef{URI: "window://steward/forecastingCubeBuilder", Revision: identity.WorkingCandidate})
		if err != nil || item.WindowKey != "forecastingCubeBuilder" {
			t.Fatalf("trusted exact catalog mapping=%+v %v", item, err)
		}
	})
}

type catalogAliasPolicy struct{ denied string }

func (p catalogAliasPolicy) SelectRevision(_ context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if ref.URI == "window://steward/"+p.denied {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "alice/account/revision", ValidUntil: time.Now().Add(time.Minute)}, nil
}
