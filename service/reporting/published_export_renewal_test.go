package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	publishedidentity "github.com/viant/forge/backend/resource"
)

// Independently authored fixture: a closed published report and token rotation.
type renewedExportReader struct {
	pin              identity.ResolvedResource
	denied           bool
	selectedProvider string
}

func (r *renewedExportReader) Resolve(context.Context, identity.ResourceRef) (*identity.ResolvedResource, error) {
	return nil, errors.New("unscoped discovery forbidden")
}
func (r *renewedExportReader) ResolveForProvider(_ context.Context, ref identity.ResourceRef, provider string) (*identity.ResolvedResource, error) {
	r.selectedProvider = provider
	if r.denied || ref.URI != r.pin.URI {
		return nil, identity.ErrResourceDenied
	}
	p := r.pin
	return &p, nil
}
func (r *renewedExportReader) ReadResolved(_ context.Context, p identity.ResolvedResource) (json.RawMessage, *identity.ResolvedResource, error) {
	if r.denied || !sameResourcePin(&p, &r.pin) {
		return nil, nil, identity.ErrResourceDenied
	}
	return json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Example"}}`), &p, nil
}
func TestCompletedPublishedExportSurvivesRenewedSession(t *testing.T) {
	ctx := authsvc.InjectUser(context.Background(), "owner")
	pin := identity.ResolvedResource{URI: "report://example/summary", ProviderIdentity: "example-provider", ResourceCandidate: identity.ResourceCandidate{Kind: publishedidentity.PublishedCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("synthetic"))}, AuthorityBinding: "old-session"}
	reader := &renewedExportReader{pin: pin}
	reader.pin.AuthorityBinding = "renewed-session"
	store := NewMemoryStore()
	svc := New(Options{Store: store})
	svc.SetResourceReaderFactory(func(context.Context, string) (ResourceReader, error) { return reader, nil })
	completed := time.Now().UTC()
	job := &ExportJob{JobID: "job", OwnerID: "owner", ArtifactRef: pin.URI, ResourcePin: &pin, Status: JobStatusSucceeded, ArtifactID: "artifact", CompletedAt: &completed}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifact(ctx, &Artifact{ArtifactID: "artifact", JobID: "job", OwnerID: "owner", ArtifactRef: pin.URI, Data: []byte("synthetic export"), CreatedAt: completed}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetArtifact(ctx, "artifact")
	if err != nil || string(got.Data) != "synthetic export" {
		t.Fatalf("renewed download: %v", err)
	}
	if reader.selectedProvider != "example-provider" || pin.AuthorityBinding != "old-session" {
		t.Fatal("provider changed or stored authority mutated")
	}
	if _, err = svc.GetArtifact(authsvc.InjectUser(context.Background(), "other"), "artifact"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner: %v", err)
	}
	reader.denied = true
	if _, err = svc.GetArtifact(ctx, "artifact"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked access: %v", err)
	}
	reader.denied = false
	reader.pin.ContentFingerprint = identity.ContentFingerprint([]byte("changed"))
	if _, err = svc.GetArtifact(ctx, "artifact"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("changed report: %v", err)
	}
	reader.pin = pin
	reader.pin.AuthorityBinding = "renewed-session"
	job.Status = JobStatusRunning
	if err = svc.validateExportResourcePin(ctx, job, "report.export"); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("active export authority renewed: %v", err)
	}
}
