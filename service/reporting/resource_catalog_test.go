package reporting

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/service/reporting/catalog"
	"testing"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
)

type catalogSource map[string]json.RawMessage

func (s catalogSource) Candidates(_ context.Context, uri identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	raw, ok := s[uri.String()]
	if !ok {
		return nil, identity.ErrResourceDenied
	}
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(raw)}}, nil
}
func (s catalogSource) ReadCandidate(_ context.Context, uri identity.ResourceURI, _ identity.ResourceCandidate) (json.RawMessage, error) {
	return s[uri.String()], nil
}

func TestExistingReportToolsShareNamespaceCatalogAndCurrentUserFilter(t *testing.T) {
	ctx := context.Background()
	source := catalogSource{"report://shared/sales": json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Sales"},"reportSpec":{"title":"Sales"}}`), "report://shared/forecast": json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Forecast"},"reportSpec":{"title":"Forecast"}}`)}
	resolver := &identity.ResourceResolver{Source: source, Policy: reportPolicyFunc(func(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "alice/account", ValidUntil: time.Now().Add(time.Minute)}, nil
	})}
	catalog := &catalog.ReportCatalogService{Identity: func(context.Context) (identity.VerifiedActor, error) {
		return identity.VerifiedActor{Subject: "alice", Issuer: "issuer", TenantID: "tenant", AccountID: "account", IdentityRevision: "revision", ValidUntil: time.Now().Add(time.Minute)}, nil
	}, Resolver: func(context.Context) (*identity.ResourceResolver, error) { return resolver, nil }, Inventories: []catalog.ReportInventory{catalog.ReportInventoryFunc(func(context.Context, identity.VerifiedActor) ([]catalog.ReportCatalogCandidate, error) {
		return []catalog.ReportCatalogCandidate{{URI: "report://shared/sales", Title: "Sales", OwnerID: "alice"}, {URI: "report://shared/forecast", Title: "Forecast", OwnerID: "bob"}}, nil
	})}}
	service := New(Options{Store: NewMemoryStore(), ReportCatalog: catalog, ResourceResolver: func(_ context.Context, operation string) (*identity.ResourceResolver, error) {
		if operation != "report.retrieve" {
			t.Fatal(operation)
		}
		return resolver, nil
	}})
	list, err := service.Method("list_reports")
	if err != nil {
		t.Fatal(err)
	}
	var all ListReportsResult
	if err = list(ctx, &ListReportsInput{}, &all); err != nil || len(all.Reports) != 2 {
		t.Fatalf("unified report listing: %+v %v", all, err)
	}
	var current ListReportsResult
	if err = list(ctx, &ListReportsInput{CurrentUserOnly: true}, &current); err != nil || len(current.Reports) != 1 || !current.Reports[0].OwnedByCurrentUser || current.Reports[0].OwnerID != "alice" {
		t.Fatalf("ownership filter: %+v %v", current, err)
	}
	if current.Reports[0].Namespace != "shared" || current.Reports[0].Resource == nil {
		t.Fatal("report tool lost canonical resource")
	}
	get, err := service.Method("get_report")
	if err != nil {
		t.Fatal(err)
	}
	var report SharedArtifact
	if err = get(ctx, &GetReportInput{ResolvedResource: current.Reports[0].Resource}, &report); err != nil || report.Resource == nil || len(report.ResourceDefinition) == 0 {
		t.Fatalf("get lost exact report envelope/pin: %+v %v", report, err)
	}
	source[report.Resource.URI] = json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Changed"},"reportSpec":{}}`)
	if err = get(ctx, &GetReportInput{ResolvedResource: report.Resource}, &SharedArtifact{}); err == nil {
		t.Fatal("get followed newer working report after list")
	}
}
