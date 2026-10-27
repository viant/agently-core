package reporting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
)

type reportMetadataReader struct {
	ResourceReader
	ordinary, fresh int
	previous        *identity.ResolvedResource
	reply           *primitive.GetResult
}

func (r *reportMetadataReader) ReadDefinition(context.Context, identity.ResourceRef, *identity.ResolvedResource) (*primitive.GetResult, error) {
	r.ordinary++
	return r.reply, nil
}
func (r *reportMetadataReader) ReadDefinitionFresh(_ context.Context, ref identity.ResourceRef, previous *identity.ResolvedResource) (*primitive.GetResult, error) {
	r.fresh++
	r.previous = cloneResolvedResource(previous)
	if ref.URI != previous.URI || ref.Revision != previous.Selector() {
		return nil, identity.ErrResourceDenied
	}
	return r.reply, nil
}

type reportMetadataUnsupportedReader struct {
	ResourceReader
	ordinary int
}

func (r *reportMetadataUnsupportedReader) ReadDefinition(context.Context, identity.ResourceRef, *identity.ResolvedResource) (*primitive.GetResult, error) {
	r.ordinary++
	return nil, identity.ErrResourceDenied
}

func TestGetReportMetadataRefreshUsesFreshSelectionAndPreservesExpiredBookmark(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Sample"}}`)
	previous := &identity.ResolvedResource{ProviderIdentity: "sample-owner", URI: "report://example/sample", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(raw)}, AuthorityBinding: "sample-binding", ValidUntil: time.Now().Add(-time.Minute)}
	fresh := cloneResolvedResource(previous)
	fresh.ValidUntil = time.Now().Add(5 * time.Minute)
	reader := &reportMetadataReader{reply: &primitive.GetResult{Resource: &primitive.ResourceState{URI: fresh.URI, DefinitionBytes: raw, ContentFingerprint: fresh.ContentFingerprint}, ResolvedResource: fresh}}
	service := New(Options{Store: NewMemoryStore()})
	service.SetResourceReaderFactory(func(context.Context, string) (ResourceReader, error) { return reader, nil })
	request := &GetReportInput{RefreshMetadata: true, Resource: &identity.ResourceRef{URI: previous.URI, Revision: previous.Selector()}, ResolvedResource: previous}
	result, err := service.GetReport(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 1, reader.fresh)
	require.Zero(t, reader.ordinary)
	require.Equal(t, previous, reader.previous)
	require.True(t, previous.ValidUntil.Before(time.Now()), "old bookmark must remain unchanged")
	require.True(t, result.Resource.ValidUntil.Equal(fresh.ValidUntil))
	request.RefreshMetadata = false
	request.ResolvedResource = fresh
	_, err = service.GetReport(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 1, reader.ordinary)
	require.Equal(t, 1, reader.fresh)
}
func TestGetReportMetadataRefreshDoesNotFallBackToUnknownCachedReader(t *testing.T) {
	reader := &reportMetadataUnsupportedReader{}
	service := New(Options{Store: NewMemoryStore()})
	service.SetResourceReaderFactory(func(context.Context, string) (ResourceReader, error) { return reader, nil })
	pin := &identity.ResolvedResource{ProviderIdentity: "sample-owner", URI: "report://example/sample", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("sample"))}, AuthorityBinding: "sample-binding", ValidUntil: time.Now().Add(-time.Minute)}
	_, err := service.GetReport(context.Background(), &GetReportInput{RefreshMetadata: true, Resource: &identity.ResourceRef{URI: pin.URI, Revision: pin.Selector()}, ResolvedResource: pin})
	require.Error(t, err)
	require.Zero(t, reader.ordinary, "cache-unknown reader cannot silently reuse the expired grant")
	_, err = service.GetReport(context.Background(), &GetReportInput{RefreshMetadata: true, Resource: &identity.ResourceRef{URI: pin.URI, Revision: pin.Selector()}})
	require.Error(t, err)
	require.Zero(t, reader.ordinary)
}
