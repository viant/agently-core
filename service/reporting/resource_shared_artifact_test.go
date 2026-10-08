package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/viant/agently-core/service/reporting/catalog"
	"testing"
	"time"

	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
)

type sharedReportPolicy struct {
	actor *identity.VerifiedActor
	allow bool
	uri   identity.ResourceURI
}

func (p *sharedReportPolicy) SelectRevision(_ context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if !p.allow || ref.URI != p.uri.String() || len(candidates) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	candidate := candidates[0]
	if ref.Revision != "" && ref.Revision != candidate.Selector() {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{
		Candidate: candidate,
		// Simulate refresh-token rotation. Bookmark reads must bind to current
		// authority after matching the stable actor/account scope.
		AuthorityBinding: p.actor.IdentityRevision + "/" + p.actor.AccountID,
		ValidUntil:       p.actor.ValidUntil,
	}, nil
}

func TestCanonicalSharedReportRoundTripReauthorizesStableActorAndExactPin(t *testing.T) {
	ctx := authsvc.InjectUser(context.Background(), "alice")
	uri, _ := identity.ParseResourceURI("report://tenant/Sales")
	actor := &identity.VerifiedActor{Subject: "alice", Issuer: "issuer", TenantID: "tenant", AccountID: "account-a", IdentityRevision: "login-1", ValidUntil: time.Now().Add(time.Hour)}
	raw := json.RawMessage(`{"schemaVersion":1,"format":"forge.authoredReport","reportDocument":{"title":"Sales"},"reportSpec":{"kind":"reportSpec","version":1}}`)
	source := &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return append(json.RawMessage(nil), raw...), nil }}
	policy := &sharedReportPolicy{actor: actor, allow: true, uri: uri}
	resolver := &identity.ResourceResolver{Source: source, Policy: policy}
	catalog := &catalog.ReportCatalogService{Identity: func(context.Context) (identity.VerifiedActor, error) { return *actor, nil }}
	client := reportmemory.New()
	service := New(Options{
		Store:         NewStoreAdapter(client),
		ReportCatalog: catalog,
		ResourceResolver: func(_ context.Context, action string) (*identity.ResourceResolver, error) {
			if action != "report.retrieve" {
				t.Fatalf("unexpected shared-report action: %s", action)
			}
			return resolver, nil
		},
		NewID: func() string { return "shared-sales" },
	})
	shared, err := service.ShareArtifact(ctx, &ShareArtifactRequest{
		Resource:         &identity.ResourceRef{URI: uri.String(), Revision: "working"},
		SavedViewOverlay: json.RawMessage(`{"title":"Sales view"}`),
		Metadata:         json.RawMessage(`{"note":"user metadata"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if shared.Resource == nil || shared.Resource.URI != uri.String() || shared.ResourceDefinition != nil || len(shared.Document) != 0 || len(shared.ReportSpec) != 0 || len(shared.ReportFill) != 0 || len(shared.ReportPrint) != 0 {
		t.Fatalf("canonical share duplicated the report definition: %+v", shared)
	}
	if string(shared.Metadata) != `{"note":"user metadata"}` {
		t.Fatalf("public metadata changed: %s", shared.Metadata)
	}
	storedRecord, err := client.GetSharedArtifact(ctx, shared.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	if storedRecord == nil || len(storedRecord.Metadata) == 0 || len(storedRecord.Document) != 0 || len(storedRecord.ReportSpec) != 0 {
		t.Fatalf("canonical pin was not stored in the adapter metadata envelope: %+v", storedRecord)
	}
	var storedMetadata map[string]json.RawMessage
	if json.Unmarshal(storedRecord.Metadata, &storedMetadata) != nil || len(storedMetadata[canonicalSharedResourceEnvelopeKey]) == 0 {
		t.Fatalf("missing reserved server metadata envelope: %s", storedRecord.Metadata)
	}

	// A new identity revision and changed authority binding are allowed after
	// refresh when the stable actor/account and exact content pin still match.
	actor.IdentityRevision = "login-2"
	got, err := service.GetSharedArtifact(ctx, shared.ArtifactID)
	if err != nil || got.Resource == nil || got.Resource.AuthorityBinding != "login-2/account-a" {
		t.Fatalf("refreshed exact shared report=%+v err=%v", got, err)
	}
	transitioned, err := service.TransitionArtifact(ctx, &TransitionArtifactRequest{ArtifactRef: shared.ArtifactRef, To: "published"})
	if err != nil || transitioned.Resource == nil || transitioned.Lifecycle != "published" || len(transitioned.ResourceDefinition) != 0 {
		t.Fatalf("canonical transition materialized another report: %+v %v", transitioned, err)
	}

	actor.AccountID = "account-b"
	if got, err := service.GetSharedArtifact(ctx, shared.ArtifactID); got != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("different account retrieved canonical share: %+v %v", got, err)
	}
	actor.AccountID = "account-a"
	actor.ValidUntil = time.Now().Add(time.Hour)
	policy.allow = false // fresh policy resolution reflects revoked access
	if got, err := service.GetSharedArtifact(ctx, shared.ArtifactID); got != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked canonical share remained visible: %+v %v", got, err)
	}
	policy.allow = true
	raw = json.RawMessage(`{"schemaVersion":1,"format":"forge.authoredReport","reportDocument":{"title":"Changed"},"reportSpec":{"kind":"reportSpec","version":1}}`)
	if got, err := service.GetSharedArtifact(ctx, shared.ArtifactID); got != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("working report drift was silently followed: %+v %v", got, err)
	}
	if _, err := service.ShareArtifact(ctx, &ShareArtifactRequest{Resource: &identity.ResourceRef{URI: "report://other/Sales", Revision: "working"}}); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("wrong namespace report was shared: %v", err)
	}
	if got, err := service.GetSharedArtifact(authsvc.InjectUser(context.Background(), "bob"), shared.ArtifactID); got != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("different owner retrieved canonical share: %+v %v", got, err)
	}
}

func TestCanonicalSharedReportRejectsClientReportPayloads(t *testing.T) {
	uri := "report://tenant/Sales"
	for _, request := range []*ShareArtifactRequest{
		{Resource: &identity.ResourceRef{URI: uri}, ReportDocument: json.RawMessage(`{"title":"forged"}`)},
		{Resource: &identity.ResourceRef{URI: uri}, ReportExportRequest: &ReportExportRequest{ReportSpec: json.RawMessage(`{"forged":true}`)}},
	} {
		if _, err := New(Options{Store: NewMemoryStore()}).ShareArtifact(context.Background(), request); !errors.Is(err, identity.ErrResourceDenied) {
			t.Fatalf("client report payload accepted for canonical share: %v", err)
		}
	}
}

func TestUnifiedSharedReportsRejectUnpinnedLegacyKindsAndSnapshots(t *testing.T) {
	ctx := authsvc.InjectUser(context.Background(), "alice")
	service := New(Options{Store: NewMemoryStore(), ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) {
		return nil, identity.ErrResourceDenied
	}})
	if _, err := service.ShareArtifact(ctx, &ShareArtifactRequest{ArtifactRef: "legacy-report", ReportExportRequest: &ReportExportRequest{}}); err == nil {
		t.Fatal("canonical mode created unpinned legacy report model")
	}
	if _, err := service.TransitionArtifact(ctx, &TransitionArtifactRequest{ArtifactRef: "legacy-report", To: "published", ReportExportRequest: &ReportExportRequest{}}); err == nil {
		t.Fatal("canonical mode published unpinned legacy snapshot")
	}
	for _, kind := range []string{savedReportArtifactKind, savedViewArtifactKind, "reportBuilder.publishedSnapshot"} {
		artifact := &SharedArtifact{ArtifactID: "legacy-" + kind, ArtifactRef: "legacy-ref-" + kind, OwnerID: "alice", Kind: kind, ReportID: "old-bare-id"}
		if err := service.store.CreateSharedArtifact(ctx, artifact); err != nil {
			t.Fatal(err)
		}
		if _, err := service.GetSharedArtifact(ctx, artifact.ArtifactID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unbound reportkind visible: %s %v", kind, err)
		}
	}
}
