package reporting

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	publishedidentity "github.com/viant/forge/backend/resource"
	"testing"
)

type publishedCatalogFixture struct {
	raw     json.RawMessage
	revoked bool
	checks  int
}

func (r *publishedCatalogFixture) Resolve(context.Context, identity.ResourceRef) (*identity.ResolvedResource, error) {
	return &identity.ResolvedResource{URI: "report://example/summary", ProviderIdentity: "producer", ResourceCandidate: identity.ResourceCandidate{Kind: publishedidentity.PublishedCandidate, ContentFingerprint: identity.ContentFingerprint(r.raw)}, AuthorityBinding: "synthetic-local-admission"}, nil
}
func (r *publishedCatalogFixture) ReadResolved(ctx context.Context, pin identity.ResolvedResource) (json.RawMessage, *identity.ResolvedResource, error) {
	return append(json.RawMessage(nil), r.raw...), &pin, nil
}
func (r *publishedCatalogFixture) ReadDefinition(ctx context.Context, ref identity.ResourceRef, _ *identity.ResolvedResource) (*primitive.GetResult, error) {
	pin, err := r.Resolve(ctx, ref)
	return &primitive.GetResult{ResolvedResource: pin, Resource: &primitive.ResourceState{DefinitionBytes: append([]byte(nil), r.raw...)}}, err
}
func (r *publishedCatalogFixture) VerifyPublishedResource(context.Context, identity.ResolvedResource) error {
	r.checks++
	if r.revoked {
		return identity.ErrResourceDenied
	}
	return nil
}
func TestGetPublishedReportPreservesClosedReferenceEnvelope(t *testing.T) {
	reader := &publishedCatalogFixture{raw: json.RawMessage(`{"schemaVersion":2,"format":"forge.reportReferences","reportSpec":{"title":"Synthetic report"},"dataSources":{"rows":{"kind":"mcp"}},"dataSourceResources":{}}`)}
	service := New(Options{Store: NewMemoryStore()})
	service.SetResourceReaderFactory(func(context.Context, string) (ResourceReader, error) { return reader, nil })
	input := &GetReportInput{Resource: &identity.ResourceRef{URI: "report://example/summary"}}
	got, err := service.getResourceReportUnscoped(context.Background(), input)
	require.NoError(t, err)
	require.JSONEq(t, string(reader.raw), string(got.ResourceDefinition))
	require.Empty(t, got.Resource.Selector())
	require.Equal(t, 1, reader.checks)
	reader.revoked = true
	_, err = service.getResourceReportUnscoped(context.Background(), input)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
}
