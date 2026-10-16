package sql

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting"
	producer "github.com/viant/agently-core/service/resource/provider"
	userprovider "github.com/viant/agently-core/service/resource/provider/user"
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
