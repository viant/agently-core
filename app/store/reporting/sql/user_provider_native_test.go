package sql

import (
	"context"
	"encoding/json"
	xhandler "github.com/viant/xdatly/handler"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	iauth "github.com/viant/agently-core/internal/auth"
	reportmodel "github.com/viant/agently-core/model/reportshareartifact"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting"
	producer "github.com/viant/agently-core/service/resource/provider"
	userprovider "github.com/viant/agently-core/service/resource/provider/user"
	dexec "github.com/viant/datly/exec"
	resourcehost "github.com/viant/forge/backend/resourcehost"
)

func TestNativeSQLUserReportProviderPersistsCanonicalAndLegacyOwners(t *testing.T) {
	ctx, sqlStore, db, server := reportingRuntime(t)
	caller := iauth.WithBearer(iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "alice@example.test"}), "alice-id"), "verified-workspace"), "original-credential")
	other := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "bob@example.test"}), "bob-id"), "verified-workspace")
	storage := reporting.NewStoreAdapter(sqlStore)
	require.NoError(t, storage.CreateSharedArtifact(caller, &reporting.SharedArtifact{ArtifactID: "legacy-row", ArtifactRef: "legacy-reference", OwnerID: "alice@example.test", Kind: "reportBuilder.savedReportPayload", Document: json.RawMessage(`{"title":"Legacy"}`), CreatedAt: time.Now()}))
	source, err := userprovider.NewReports(storage, "user-owner")
	require.NoError(t, err)
	definition := json.RawMessage(`{
  "schemaVersion":1,
  "reportDocument":{"title":"SQL saved","entityId":9007199254740993},
  "builderRef":"original-builder",
  "dataSources":{"rows":{"ID":"rows","backend":{"kind":"mcp_tool","service":"managing","method":"search","parameters":{"original":"unchanged"}}}},
  "state":{"metadata":{"original":true}}
}`)
	uri, err := identity.ParseResourceURI("report://users/alice-id/custom/saved")
	require.NoError(t, err)
	input := primitive.CreateResourceInput{URI: uri.String(), Title: "SQL saved", FormatVersion: 1, Definition: definition}
	original := append([]byte(nil), definition...)
	_, err = source.CreateResource(caller, input)
	require.NoError(t, err)
	require.Equal(t, original, []byte(input.Definition))
	var persistedOwner string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT owner_id FROM report_shared_artifact WHERE artifact_ref=?", uri.String()).Scan(&persistedOwner))
	require.Equal(t, "alice-id", persistedOwner)
	require.Equal(t, "alice@example.test", iauth.EffectiveUserID(caller))
	require.Equal(t, "alice-id", iauth.CanonicalUserID(caller))
	require.Equal(t, "verified-workspace", iauth.Provider(caller))
	require.Equal(t, "original-credential", iauth.MCPAuthToken(caller, false))
	reopenedSQL, err := New(ctx, server, nil, nil)
	require.NoError(t, err)
	reopened, err := userprovider.NewReports(reporting.NewStoreAdapter(reopenedSQL), "user-owner")
	require.NoError(t, err)
	public := identity.VerifiedActor{PublicRead: true, ValidUntil: time.Now().Add(time.Minute)}
	listed, err := reopened.List(caller, public, "report", "users")
	require.NoError(t, err)
	require.Len(t, listed, 2)
	for _, row := range listed {
		require.Equal(t, "alice-id", row.OwnerID)
	}
	binding, err := reopened.Lookup(caller, public, uri)
	require.NoError(t, err)
	resolver, err := binding.Resolver(caller, public, "get")
	require.NoError(t, err)
	before, err := resolver.Resolve(caller, identity.ResourceRef{URI: uri.String()})
	require.NoError(t, err)
	raw, _, err := resolver.ReadResolved(caller, *before)
	require.NoError(t, err)
	require.Equal(t, original, []byte(raw), "SQL metadata serialization must retain exact complete envelope bytes")
	_, err = reopened.Lookup(other, public, uri)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	_, err = reopened.UpdateDraft(other, uri.String(), primitive.UpdateDraftInput{FormatVersion: 1, Definition: definition})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	updated := json.RawMessage(`{ "schemaVersion":1,"reportDocument":{"title":"Updated","entityId":9007199254740993},"builderRef":"retained","dataSources":{"rows":{"ID":"rows","backend":{"kind":"mcp_tool","service":"managing","method":"search"}}} }`)
	_, err = reopened.UpdateDraft(caller, uri.String(), primitive.UpdateDraftInput{FormatVersion: 1, Definition: updated})
	require.NoError(t, err)
	legacy, err := identity.ParseResourceURI("report://users/alice-id/legacy-row")
	require.NoError(t, err)
	_, err = reopened.UpdateDraft(caller, legacy.String(), primitive.UpdateDraftInput{FormatVersion: 1, Definition: json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Legacy updated"}}`)})
	require.NoError(t, err)
	var legacyOwner string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT owner_id FROM report_shared_artifact WHERE artifact_id='legacy-row'").Scan(&legacyOwner))
	require.Equal(t, "alice@example.test", legacyOwner)
	thirdSQL, err := New(ctx, server, nil, nil)
	require.NoError(t, err)
	third, err := userprovider.NewReports(reporting.NewStoreAdapter(thirdSQL), "user-owner")
	require.NoError(t, err)
	binding, err = third.Lookup(caller, public, uri)
	require.NoError(t, err)
	resolver, err = binding.Resolver(caller, public, "get")
	require.NoError(t, err)
	after, err := resolver.Resolve(caller, identity.ResourceRef{URI: uri.String()})
	require.NoError(t, err)
	require.NotEqual(t, before.ContentFingerprint, after.ContentFingerprint)
	raw, _, err = resolver.ReadResolved(caller, *after)
	require.NoError(t, err)
	require.Equal(t, []byte(updated), []byte(raw))
	for _, ref := range []string{uri.String(), legacy.String()} {
		_, err = third.ArchiveResource(caller, ref, primitive.ArchiveResourceInput{})
		require.NoError(t, err)
	}
	_, err = third.Lookup(caller, public, uri)
	require.ErrorIs(t, err, producer.ErrCatalogNotFound)
	require.Equal(t, "alice@example.test", iauth.EffectiveUserID(caller))
	require.Equal(t, "original-credential", iauth.MCPAuthToken(caller, false))
}

func TestNativePersonalReportCASPreservesWinningDefinition(t *testing.T) {
	ctx, store, _, _ := reportingRuntime(t)
	caller := iauth.WithCanonicalUserID(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "alice@example.test"}), "alice-id")
	adapter := reporting.NewStoreAdapter(store)
	original := &reporting.SharedArtifact{ArtifactID: "cas-personal", OwnerID: "alice@example.test", ArtifactRef: "report://users/alice-id/custom/cas", Kind: "reportBuilder.savedReportPayload", Lifecycle: "draft", Version: 0, Document: json.RawMessage(`{"title":"Original"}`), CreatedAt: time.Now()}
	require.NoError(t, adapter.CreateSharedArtifact(caller, original))
	a, err := adapter.GetSharedArtifact(caller, original.ArtifactID)
	require.NoError(t, err)
	b, err := adapter.GetSharedArtifact(caller, original.ArtifactID)
	require.NoError(t, err)
	writer := adapter.(interface {
		UpdateSharedArtifactCAS(context.Context, *reporting.SharedArtifact, int) error
	})
	a.Version = 1
	a.Document = json.RawMessage(`{"title":"Winner"}`)
	require.NoError(t, writer.UpdateSharedArtifactCAS(caller, a, 0))
	b.Version = 1
	b.Document = json.RawMessage(`{"title":"Stale"}`)
	var conflict *xhandler.Conflict
	require.ErrorAs(t, writer.UpdateSharedArtifactCAS(caller, b, 0), &conflict)
	saved, err := adapter.GetSharedArtifact(caller, original.ArtifactID)
	require.NoError(t, err)
	require.JSONEq(t, `{"title":"Winner"}`, string(saved.Document))
}

type countedArtifactInvoker struct {
	dexec.ComponentInvoker
	calls int
}

func (c *countedArtifactInvoker) InvokeComponent(ctx context.Context, request dexec.ComponentRequest) (any, error) {
	c.calls++
	return c.ComponentInvoker.InvokeComponent(ctx, request)
}

func TestSharedArtifactCreateUsesOneNativeInvocationAndNeverOverwrites(t *testing.T) {
	ctx, _, _, server := reportingRuntime(t)
	counted := &countedArtifactInvoker{ComponentInvoker: server}
	store, err := New(ctx, counted, nil, nil)
	require.NoError(t, err)
	owner := iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "create-owner"})
	original := &reportmodel.Record{ArtifactID: "create-only", ArtifactRef: "report://users/example/original", OwnerID: "create-owner", Kind: "reportBuilder.savedReportPayload", Lifecycle: "draft", Version: 1, Title: "Original", CreatedAt: time.Now().UTC()}
	require.NoError(t, store.CreateSharedArtifact(owner, original))
	require.Equal(t, 1, counted.calls, "create delegates once to the native create intent instead of adding a separate pre-read")
	duplicate := *original
	duplicate.Title = "Must not replace original"
	require.ErrorIs(t, store.CreateSharedArtifact(owner, &duplicate), reportstore.ErrAlreadyExists)
	require.Equal(t, 2, counted.calls, "duplicate detection belongs to the same native writer")
	saved, err := store.GetSharedArtifact(owner, original.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, "Original", saved.Title)
	wrong := *original
	wrong.ArtifactID = "wrong-owner"
	wrong.OwnerID = "another-owner"
	before := counted.calls
	require.ErrorIs(t, store.CreateSharedArtifact(owner, &wrong), errNotFound)
	require.Equal(t, before, counted.calls, "native store ownership denies a mismatched owner before database invocation")
}

func TestNativeUserArchivePreservesReportAndRejectsStaleVersion(t *testing.T) {
	ctx, sqlStore, _, _ := reportingRuntime(t)
	caller := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "archive-alice"}), "archive-alice"), "verified-workspace")
	other := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "archive-bob"}), "archive-bob"), "verified-workspace")
	adapter := reporting.NewStoreAdapter(sqlStore)
	source, err := userprovider.NewReports(adapter, "user-owner")
	require.NoError(t, err)
	uri := "report://users/archive-alice/saved"
	raw := json.RawMessage(`{ "schemaVersion":1,"reportDocument":{"title":"Keep definition","integer":9007199254740993},"metadata":{"note":"Keep metadata"} }`)
	created, err := source.CreateResource(caller, primitive.CreateResourceInput{URI: uri, Title: "Saved", FormatVersion: 1, Definition: raw})
	require.NoError(t, err)
	rows, err := adapter.ListSharedArtifacts(caller)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	before := *rows[0]
	_, err = source.ArchiveResource(other, uri, primitive.ArchiveResourceInput{ExpectedHeadRevision: created.RowEtag})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	_, err = source.ArchiveResource(caller, uri, primitive.ArchiveResourceInput{ExpectedHeadRevision: created.RowEtag + 1})
	require.ErrorIs(t, err, identity.ErrResourceStale)
	unchanged, err := adapter.GetSharedArtifact(caller, before.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, before.Version, unchanged.Version)
	require.Equal(t, before.Lifecycle, unchanged.Lifecycle)
	archived, err := source.ArchiveResource(caller, uri, primitive.ArchiveResourceInput{ExpectedHeadRevision: created.RowEtag})
	require.NoError(t, err)
	require.Equal(t, created.RowEtag+1, archived.RowEtag)
	reopened, err := userprovider.NewReports(reporting.NewStoreAdapter(sqlStore), "user-owner")
	require.NoError(t, err)
	replay, err := reopened.ArchiveResource(caller, uri, primitive.ArchiveResourceInput{ExpectedHeadRevision: created.RowEtag})
	require.NoError(t, err)
	require.Equal(t, archived.RowEtag, replay.RowEtag)
	_, err = reopened.GetResource(caller, uri)
	require.ErrorIs(t, err, producer.ErrCatalogNotFound)
	active, err := reopened.List(caller, identity.VerifiedActor{}, "report", "users")
	require.NoError(t, err)
	require.Empty(t, active)
	stored, err := adapter.GetSharedArtifact(caller, before.ArtifactID)
	require.NoError(t, err, "archive retains the existing database row")
	require.Equal(t, "archived", stored.Lifecycle)
	require.Equal(t, before.ResourceDefinition, stored.ResourceDefinition)
	require.Equal(t, before.Metadata, stored.Metadata)
	require.Equal(t, before.Title, stored.Title)
}

func TestNativeReportEnvelopeEncodingFailurePreservesLegacyRow(t *testing.T) {
	ctx, store, _, server := reportingRuntime(t)
	caller := iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "example"})
	original := &reportmodel.Record{OwnerID: "example", ArtifactID: "legacy", ArtifactRef: "old", Kind: "reportBuilder.savedReportPayload", Version: 1, Lifecycle: "draft", Title: "Original", Document: []byte(`{"title":"Original"}`), Metadata: []byte("old opaque metadata"), CreatedAt: time.Now().UTC()}
	require.NoError(t, store.CreateSharedArtifact(caller, original))
	adapter := reporting.NewStoreAdapter(store)
	row, err := adapter.GetSharedArtifact(caller, "legacy")
	require.NoError(t, err)
	row.ResourceDefinition = json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Changed"}}`)
	row.Version++
	row.Title = "Changed"
	err = adapter.(interface {
		UpdateSharedArtifactCAS(context.Context, *reporting.SharedArtifact, int) error
	}).UpdateSharedArtifactCAS(caller, row, 1)
	var encodeErr *json.MarshalerError
	require.ErrorAs(t, err, &encodeErr)
	reopened, err := New(ctx, server, nil, nil)
	require.NoError(t, err)
	saved, err := reopened.GetSharedArtifact(caller, "legacy")
	require.NoError(t, err)
	require.Equal(t, original.Version, saved.Version)
	require.Equal(t, original.Title, saved.Title)
	require.Equal(t, original.Document, saved.Document)
	require.Equal(t, original.Metadata, saved.Metadata)
}

func TestPersonalCatalogBatchOwnsItsRowsAndVerifiesContentBeforeDelivery(t *testing.T) {
	ctx, sqlStore, db, _ := reportingRuntime(t)
	caller := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "alice@example.test"}), "alice-id"), "fixture")
	store := reporting.NewStoreAdapter(sqlStore)
	source, err := userprovider.NewReports(store, "personal")
	require.NoError(t, err)
	const uri = "report://users/alice-id/summary"
	_, err = source.CreateResource(caller, primitive.CreateResourceInput{URI: uri, Title: "Summary", FormatVersion: 1, Definition: json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Summary"}}`)})
	require.NoError(t, err)
	rows := []resourcehost.LocatedResource{{Connection: resourcehost.Connection{ProviderIdentity: "personal"}, Resource: primitive.ResourceState{Namespace: "users", URI: uri}}}
	scoped, finish, err := source.PrepareCatalogPage(caller, rows)
	require.NoError(t, err)
	require.NotNil(t, finish)
	before, err := source.GetResource(scoped, uri)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE report_shared_artifact SET title='Concurrent title' WHERE artifact_ref=?", uri)
	require.NoError(t, err)
	held, err := source.GetResource(scoped, uri)
	require.NoError(t, err)
	require.Equal(t, before.Title, held.Title)
	require.ErrorIs(t, finish(), identity.ErrResourceStale)
	_, err = source.GetResource(scoped, uri)
	require.ErrorIs(t, err, context.Canceled)
	fresh, err := source.GetResource(caller, uri)
	require.NoError(t, err)
	require.Equal(t, "Concurrent title", fresh.Title)
	other := iauth.WithCanonicalUserID(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "bob@example.test"}), "bob-id")
	_, _, err = source.PrepareCatalogPage(other, rows)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
}
