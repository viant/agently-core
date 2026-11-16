package sql

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/tidwall/sjson"
	"testing"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting"
	userprovider "github.com/viant/agently-core/service/resource/provider/user"
)

type personalSQLSourceReader struct {
	pin   identity.ResolvedResource
	raw   json.RawMessage
	reads int
}

func (r *personalSQLSourceReader) Resolve(_ context.Context, ref identity.ResourceRef) (*identity.ResolvedResource, error) {
	if ref.URI != r.pin.URI || ref.Revision != "" && ref.Revision != r.pin.Selector() {
		return nil, identity.ErrResourceDenied
	}
	result := r.pin
	return &result, nil
}
func (r *personalSQLSourceReader) ResolveForProvider(ctx context.Context, ref identity.ResourceRef, provider string) (*identity.ResolvedResource, error) {
	if provider != r.pin.ProviderIdentity {
		return nil, identity.ErrResourceDenied
	}
	return r.Resolve(ctx, ref)
}
func (r *personalSQLSourceReader) ReadResolved(_ context.Context, pin identity.ResolvedResource) (json.RawMessage, *identity.ResolvedResource, error) {
	if pin.URI != r.pin.URI || pin.ContentFingerprint != r.pin.ContentFingerprint {
		return nil, nil, identity.ErrResourceStale
	}
	r.reads++
	result := r.pin
	return append(json.RawMessage(nil), r.raw...), &result, nil
}

func TestNativePersonalReportSourceEnvelopeAndRecordedReset(t *testing.T) {
	ctx, store, _, server := reportingRuntime(t)
	caller := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "alice@example.test"}), "alice-id"), "verified-workspace")
	original := json.RawMessage("{\n  \"schemaVersion\":1,\n  \"reportDocument\":{\"title\":\"Original\", \"integer\":9007199254740993}\n}")
	original = completeSQLPersonalFixture(t, original)
	reader := &personalSQLSourceReader{pin: identity.ResolvedResource{URI: "report://example/original", ProviderIdentity: "example-publisher", ResourceCandidate: identity.ResourceCandidate{Kind: "published", ContentFingerprint: identity.ContentFingerprint(original)}}, raw: original}
	factory := func(context.Context, string) (reporting.ResourceReader, error) { return reader, nil }
	provider, err := userprovider.NewReports(reporting.NewStoreAdapter(store), "user-owner", factory)
	require.NoError(t, err)
	uri := "report://users/alice-id/derived"
	manifest, err := json.Marshal(reporting.PersonalReportSourceManifest{Operation: "copy", Source: &identity.ResourceRef{URI: reader.pin.URI}, Provider: reader.pin.ProviderIdentity, ExpectedSourceFingerprint: reader.pin.ContentFingerprint})
	require.NoError(t, err)
	created, err := provider.CreateResource(caller, primitive.CreateResourceInput{URI: uri, Title: "Derived", FormatVersion: 1, Definition: completeSQLPersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Derived","period":"weekly"}}`)), SourceManifest: manifest, IdempotencyKey: "copy-operation"})
	require.NoError(t, err)
	invalidManifest, err := json.Marshal(reporting.PersonalReportSourceManifest{Operation: "copy", Source: &identity.ResourceRef{URI: "datasource://example/rows"}, ExpectedSourceFingerprint: reader.pin.ContentFingerprint})
	require.NoError(t, err)
	_, err = provider.CreateResource(caller, primitive.CreateResourceInput{URI: "report://users/alice-id/invalid-source", FormatVersion: 1, Definition: completeSQLPersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Invalid source"}}`)), SourceManifest: invalidManifest})
	require.ErrorIs(t, err, identity.ErrResource, "a derivation source must be a report before any source fetch")
	require.Equal(t, 1, reader.reads, "one source body read inside the owning create")
	reopenedStore, err := New(ctx, server, nil, nil)
	require.NoError(t, err)
	// The original is intentionally unavailable to this reopened provider.
	reopened, err := userprovider.NewReports(reporting.NewStoreAdapter(reopenedStore), "user-owner")
	require.NoError(t, err)
	replay, err := reopened.CreateResource(caller, primitive.CreateResourceInput{URI: uri, Title: "Derived", FormatVersion: 1, Definition: completeSQLPersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Derived","period":"weekly"}}`)), SourceManifest: manifest, IdempotencyKey: "copy-operation"})
	require.NoError(t, err, "lost-response retry must succeed after reopen with original reader unavailable")
	require.Equal(t, created, replay)
	require.Equal(t, 1, reader.reads, "replay adds no original source read")
	resourceURI, err := identity.ParseResourceURI(uri)
	require.NoError(t, err)
	read := func() (json.RawMessage, *identity.ResolvedResource) {
		binding, err := reopened.Lookup(caller, identity.VerifiedActor{}, resourceURI)
		require.NoError(t, err)
		resolver, err := binding.Resolver(caller, identity.VerifiedActor{}, "get")
		require.NoError(t, err)
		pin, err := resolver.Resolve(caller, identity.ResourceRef{URI: uri})
		require.NoError(t, err)
		raw, pin, err := resolver.ReadResolved(caller, *pin)
		require.NoError(t, err)
		return raw, pin
	}
	raw, pin := read()
	require.Equal(t, created.ContentFingerprint, pin.ContentFingerprint)
	var envelope struct {
		Metadata struct {
			Source *reporting.PersonalReportSource `json:"personalReport"`
		} `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.NotNil(t, envelope.Metadata.Source)
	require.Equal(t, []byte(original), []byte(envelope.Metadata.Source.Definition), "recorded source bytes survive SQL metadata encoding")
	require.Equal(t, reader.pin.URI, envelope.Metadata.Source.URI)
	// A direct managing writer must respect the same edit precondition as the assistant tool.
	_, err = reopened.UpdateDraft(caller, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: raw})
	require.ErrorIs(t, err, identity.ErrResource)
	_, stillCurrent := read()
	require.Equal(t, pin.ContentFingerprint, stillCurrent.ContentFingerprint)
	unchangedRow, err := reopened.GetResource(caller, uri)
	require.NoError(t, err)
	require.Equal(t, created.RowEtag, unchangedRow.RowEtag)
	_, err = reopened.UpdateDraft(caller, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: completeSQLPersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Edited"}}`)), ExpectedContentFingerprint: pin.ContentFingerprint})
	require.NoError(t, err)
	raw, pin = read()
	_, err = reopened.UpdateDraft(caller, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: raw, ExpectedContentFingerprint: pin.ContentFingerprint, SourceManifest: json.RawMessage(`{"operation":"resetRecorded"}`)})
	require.NoError(t, err, "recorded reset uses the trusted persisted snapshot without fetching the original")
	raw, _ = read()
	require.Contains(t, string(raw), "9007199254740993")
	require.Equal(t, 1, reader.reads)
	// Latest source responses can contain insignificant outer whitespace.
	latest := json.RawMessage(" \n\t" + string(original) + "\n ")
	reader.raw = latest
	reader.pin.ContentFingerprint = identity.ContentFingerprint(latest)
	latestProvider, err := userprovider.NewReports(reporting.NewStoreAdapter(reopenedStore), "user-owner", factory)
	require.NoError(t, err)
	raw, pin = read()
	_, err = latestProvider.UpdateDraft(caller, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: raw, ExpectedContentFingerprint: pin.ContentFingerprint, SourceManifest: json.RawMessage(`{"operation":"resetLatest"}`)})
	require.NoError(t, err)
	_, _ = read()
	_, err = latestProvider.UpdateDraft(caller, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: raw, SourceManifest: json.RawMessage(`{"operation":"resetRecorded"}`)})
	require.ErrorIs(t, err, identity.ErrResource, "direct reset must require a caller expectation")

	for _, title := range []string{"Reset title", ""} {
		raw, pin = read()
		_, err = latestProvider.UpdateDraft(caller, uri, primitive.UpdateDraftInput{Title: &title, FormatVersion: 1, Definition: raw, ExpectedContentFingerprint: pin.ContentFingerprint, SourceManifest: json.RawMessage(`{"operation":"resetRecorded"}`)})
		if title == "" {
			require.Error(t, err, "native compiler rejects an empty report title")
			_, unchanged := read()
			require.Equal(t, pin.ContentFingerprint, unchanged.ContentFingerprint)
			continue
		}

		require.NoError(t, err)
		projected, _ := read()
		var result struct {
			Document struct {
				Title string `json:"title"`
			} `json:"reportDocument"`
		}
		require.NoError(t, json.Unmarshal(projected, &result))
		descriptor, err := latestProvider.GetResource(caller, uri)
		require.NoError(t, err)
		require.Equal(t, title, descriptor.Title)
		require.Equal(t, title, result.Document.Title, "record title and reset definition must agree")
	}
	reader.pin.ProviderIdentity = ""
	invalidPinManifest, err := json.Marshal(reporting.PersonalReportSourceManifest{Operation: "copy", Source: &identity.ResourceRef{URI: reader.pin.URI}, ExpectedSourceFingerprint: reader.pin.ContentFingerprint})
	require.NoError(t, err)
	_, err = latestProvider.CreateResource(caller, primitive.CreateResourceInput{URI: "report://users/alice-id/invalid-provider", FormatVersion: 1, Definition: completeSQLPersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Invalid provider"}}`)), SourceManifest: invalidPinManifest})
	require.ErrorIs(t, err, identity.ErrResource, "invalid provider metadata must fail before persistence")
	rows, err := reporting.NewStoreAdapter(reopenedStore).ListSharedArtifacts(iauth.WithUserInfo(caller, &iauth.UserInfo{Subject: "alice-id"}))
	require.NoError(t, err)
	require.Len(t, rows, 1, "rejected derivations must not leave unreadable rows")
	beforeArchive := rows[0]
	require.Contains(t, string(beforeArchive.Metadata), "copy-operation")
	require.NoError(t, reporting.VerifyUserReportProvenance(beforeArchive, beforeArchive.ResourceDefinition))
	archived, err := latestProvider.ArchiveResource(caller, uri, primitive.ArchiveResourceInput{ExpectedHeadRevision: int64(beforeArchive.Version)})
	require.NoError(t, err)
	require.Equal(t, int64(beforeArchive.Version+1), archived.RowEtag)
	archivedRow, err := reporting.NewStoreAdapter(reopenedStore).GetSharedArtifact(iauth.WithUserInfo(caller, &iauth.UserInfo{Subject: "alice-id"}), beforeArchive.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, "archived", archivedRow.Lifecycle)
	require.Equal(t, beforeArchive.ResourceDefinition, archivedRow.ResourceDefinition)
	require.Equal(t, beforeArchive.Metadata, archivedRow.Metadata)
	require.Equal(t, beforeArchive.Document, archivedRow.Document)
	require.Equal(t, beforeArchive.ReportSpec, archivedRow.ReportSpec)
	require.Equal(t, beforeArchive.OwnerID, archivedRow.OwnerID)
	require.Equal(t, beforeArchive.Kind, archivedRow.Kind)
	require.NoError(t, reporting.VerifyUserReportProvenance(archivedRow, archivedRow.ResourceDefinition))
	delivered, err := reporting.UserReportDefinition(archivedRow, archivedRow.ResourceDefinition)
	require.NoError(t, err)
	require.Equal(t, beforeArchive.ResourceDefinition, delivered)
	_, err = latestProvider.UpdateDraft(caller, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: raw})
	require.Error(t, err, "archived report cannot be edited through the active resource writer")

}

func TestNativeLegacyPersonalReportProjectionIsReadOnlyAndEditable(t *testing.T) {
	for _, source := range []string{`{"uri":"report://example/old","definition":{"schemaVersion":1,"reportDocument":{"title":"Unverified old source"}}}`, `"legacy malformed block"`} {
		t.Run(source, func(t *testing.T) {
			ctx, sqlStore, _, server := reportingRuntime(t)
			caller := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "alice@example.test"}), "alice-id"), "verified-workspace")
			storageCaller := iauth.WithUserInfo(caller, &iauth.UserInfo{Subject: "alice-id"})
			store := reporting.NewStoreAdapter(sqlStore)
			raw := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Saved work","integer":9007199254740993},"metadata":{"note":"preserve","personalReport":` + source + `}}`)
			row := &reporting.SharedArtifact{ArtifactID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("report://users/alice-id/legacy")).String(), ArtifactRef: "report://users/alice-id/legacy", OwnerID: "alice-id", Kind: "reportBuilder.savedReportPayload", Version: 1, ResourceDefinition: raw}
			require.NoError(t, store.CreateSharedArtifact(storageCaller, row))
			reopenedSQL, err := New(ctx, server, nil, nil)
			require.NoError(t, err)
			reopened := reporting.NewStoreAdapter(reopenedSQL)
			provider, err := userprovider.NewReports(reopened, "user-owner")
			require.NoError(t, err)
			uri, err := identity.ParseResourceURI(row.ArtifactRef)
			require.NoError(t, err)
			binding, err := provider.Lookup(caller, identity.VerifiedActor{}, uri)
			require.NoError(t, err)
			resolver, err := binding.Resolver(caller, identity.VerifiedActor{}, "get")
			require.NoError(t, err)
			pin, err := resolver.Resolve(caller, identity.ResourceRef{URI: uri.String()})
			require.NoError(t, err)
			projected, _, err := resolver.ReadResolved(caller, *pin)
			require.NoError(t, err)
			require.NotContains(t, string(projected), "personalReport")
			require.Contains(t, string(projected), "9007199254740993")
			require.Contains(t, string(projected), `"note":"preserve"`)
			stored, err := reopened.GetSharedArtifact(storageCaller, row.ArtifactID)
			require.NoError(t, err)
			require.Equal(t, []byte(raw), []byte(stored.ResourceDefinition), "read projection must not rewrite SQL")
			_, err = provider.UpdateDraft(caller, uri.String(), primitive.UpdateDraftInput{FormatVersion: 1, Definition: projected, ExpectedContentFingerprint: pin.ContentFingerprint, SourceManifest: json.RawMessage(`{"operation":"resetRecorded"}`)})
			require.ErrorIs(t, err, reporting.ErrReportHasNoOriginal)
			stored, err = reopened.GetSharedArtifact(storageCaller, row.ArtifactID)
			require.NoError(t, err)
			require.Equal(t, []byte(raw), []byte(stored.ResourceDefinition), "unsupported reset must not mutate the report")
			saved, err := provider.UpdateDraft(caller, uri.String(), primitive.UpdateDraftInput{FormatVersion: 1, Definition: projected, ExpectedContentFingerprint: pin.ContentFingerprint})
			require.NoError(t, err)
			require.Equal(t, uri.String(), saved.URI)
			finalSQL, err := New(ctx, server, nil, nil)
			require.NoError(t, err)
			final, err := reporting.NewStoreAdapter(finalSQL).GetSharedArtifact(storageCaller, row.ArtifactID)
			require.NoError(t, err)
			require.Equal(t, []byte(projected), []byte(final.ResourceDefinition))
			require.Equal(t, 2, final.Version)
		})
	}
}

func completeSQLPersonalFixture(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	spec := json.RawMessage(`{"version":1,"kind":"reportSpec","source":{"kind":"dashboard.reportBuilder","containerId":"example","stateKey":"example","dataSourceRef":"rows"},"title":"Example","parameters":{"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"single","resultPanePosition":"left","blockOrder":["table"]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"rows","dataSourceRef":"rows","request":{}}],"blocks":[{"id":"table","kind":"tableBlock","datasetRef":"rows","columns":[]}]}`)
	result, err := sjson.SetRawBytes(raw, "reportSpec", spec)
	require.NoError(t, err)
	return result
}

func TestNativeEditedLogicalCopyReceiptAfterSourceChange(t *testing.T) {
	ctx, store, _, server := reportingRuntime(t)
	caller := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "owner@example.test"}), "owner"), "example")
	original := completeSQLPersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Source"}}`))
	candidate := completeSQLPersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Edited"}}`))
	reader := &personalSQLSourceReader{pin: identity.ResolvedResource{URI: "report://example/source", ProviderIdentity: "example", ResourceCandidate: identity.ResourceCandidate{Kind: "published", ContentFingerprint: identity.ContentFingerprint(original)}}, raw: original}
	factory := func(context.Context, string) (reporting.ResourceReader, error) { return reader, nil }
	provider, err := userprovider.NewReports(reporting.NewStoreAdapter(store), "user-owner", factory)
	require.NoError(t, err)
	request := primitive.CreateResourceInput{URI: "report://users/owner/local", Title: "Edited", Definition: candidate, FormatVersion: 1, IdempotencyKey: "original-operation", SourceManifest: json.RawMessage(`{"operation":"copy","source":{"uri":"report://example/source"}}`)}
	first, err := provider.CreateResource(caller, request)
	require.NoError(t, err)
	require.Equal(t, 1, reader.reads)
	reader.raw = completeSQLPersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Updated source"}}`))
	reader.pin.ContentFingerprint = identity.ContentFingerprint(reader.raw)
	retry, err := provider.CreateResource(caller, request)
	require.NoError(t, err)
	require.Equal(t, first, retry)
	require.Equal(t, 1, reader.reads, "replay must perform zero additional source body reads")
	reopenedStore, err := New(ctx, server, nil, nil)
	require.NoError(t, err)
	offline, err := userprovider.NewReports(reporting.NewStoreAdapter(reopenedStore), "user-owner")
	require.NoError(t, err)
	retry, err = offline.CreateResource(caller, request)
	require.NoError(t, err)
	require.Equal(t, first, retry, "recovery uses the durable receipt")
	changed := request
	changed.Definition = reader.raw
	_, err = offline.CreateResource(caller, changed)
	require.ErrorIs(t, err, identity.ErrResourceStale, "a reused key cannot acknowledge a different edit")
	title := "Renamed"
	_, err = offline.UpdateDraft(caller, request.URI, primitive.UpdateDraftInput{FormatVersion: 1, Title: &title, Definition: candidate, ExpectedContentFingerprint: first.ContentFingerprint})
	require.NoError(t, err)
	_, err = offline.CreateResource(caller, request)
	require.ErrorIs(t, err, identity.ErrResourceStale, "a later edit is preserved without consulting the source")
}
