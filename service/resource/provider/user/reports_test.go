package user

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting"
	producer "github.com/viant/agently-core/service/resource/provider"
	"github.com/viant/forge/backend/reporting/registry"
)

func userContext(canonical, effective string) context.Context {
	return iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: effective}), canonical), "verified-workspace")
}
func userActor() identity.VerifiedActor {
	return identity.VerifiedActor{Subject: "alice@example.test", Issuer: "trusted", IdentityRevision: "original", ValidUntil: time.Now().Add(time.Hour)}
}
func userURI(t *testing.T, value string) identity.ResourceURI {
	t.Helper()
	uri, err := identity.ParseResourceURI(value)
	require.NoError(t, err)
	return uri
}
func draftDefinition(title string) json.RawMessage {
	return json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"` + title + `"}}`)
}

type countedUserStore struct {
	*reporting.MemoryStore
	gets    []string
	lists   int
	failure error
}

func (s *countedUserStore) GetSharedArtifact(ctx context.Context, id string) (*reporting.SharedArtifact, error) {
	s.gets = append(s.gets, id)
	if s.failure != nil {
		return nil, s.failure
	}
	return s.MemoryStore.GetSharedArtifact(ctx, id)
}
func (s *countedUserStore) ListSharedArtifacts(ctx context.Context) ([]*reporting.SharedArtifact, error) {
	s.lists++
	return s.MemoryStore.ListSharedArtifacts(ctx)
}

func TestUserReportsStoreOwnershipDraftsAndFreshWorkingBytes(t *testing.T) {
	store := &countedUserStore{MemoryStore: reporting.NewMemoryStore()}
	source, err := NewReports(store, "user-owner")
	require.NoError(t, err)
	alice := userContext("alice-id", "alice@example.test")
	bob := userContext("bob-id", "bob@example.test")
	uri := userURI(t, "report://users/alice-id/custom/monthly")
	input := primitive.CreateResourceInput{URI: uri.String(), Title: "Original", FormatVersion: 1, Definition: draftDefinition("Original")}
	original := append([]byte(nil), input.Definition...)
	result, err := source.CreateResource(alice, input)
	require.NoError(t, err)
	require.Equal(t, original, []byte(input.Definition))
	require.Equal(t, uri.String(), result.URI)
	require.Zero(t, result.LatestStamp)
	row, err := store.MemoryStore.GetSharedArtifact(alice, userReportStorageID(uri))
	require.NoError(t, err)
	require.Equal(t, "alice-id", row.OwnerID)
	require.Equal(t, uri.String(), row.ArtifactRef)
	require.Empty(t, row.ReportSpec, "editable draft must not fabricate runnable spec")
	listed, err := source.List(alice, userActor(), "report", "users")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "alice-id", listed[0].OwnerID)
	require.Equal(t, uri.String(), listed[0].URI)
	store.lists = 0
	binding, err := source.Lookup(alice, userActor(), uri)
	require.NoError(t, err)
	require.Zero(t, store.lists, "Get must use direct identities, not enumerate all reports")
	resolver, err := binding.Resolver(alice, userActor(), "get")
	require.NoError(t, err)
	before, err := resolver.Resolve(alice, identity.ResourceRef{URI: uri.String()})
	require.NoError(t, err)
	raw, _, err := resolver.ReadResolved(alice, *before)
	require.NoError(t, err)
	var envelope registry.ReportEnvelope
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.Empty(t, envelope.ReportSpec)
	update := primitive.UpdateDraftInput{FormatVersion: 1, Definition: draftDefinition("Updated")}
	unchanged := append([]byte(nil), update.Definition...)
	after, err := source.UpdateDraft(alice, uri.String(), update)
	require.NoError(t, err)
	require.Equal(t, unchanged, []byte(update.Definition))
	require.NotEqual(t, before.ContentFingerprint, after.ContentFingerprint)
	for _, unsupported := range []primitive.UpdateDraftInput{{FormatVersion: 1, Definition: update.Definition, ExpectedRowEtag: 1}, {FormatVersion: 1, Definition: update.Definition, ExpectedDraftRevision: 1}, {FormatVersion: 1, Definition: update.Definition, ExpectedContentFingerprint: "not-atomic"}} {
		_, err = source.UpdateDraft(alice, uri.String(), unsupported)
		require.ErrorIs(t, err, ErrUserReportOperationUnsupported)
	}
	_, _, err = resolver.ReadResolved(alice, *before)
	require.ErrorIs(t, err, identity.ErrResourceStale)
	_, err = source.Lookup(bob, userActor(), uri)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	_, err = source.UpdateDraft(bob, uri.String(), update)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	_, err = source.ArchiveResource(bob, uri.String(), primitive.ArchiveResourceInput{})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	bobURI := userURI(t, "report://users/bob-id/custom/monthly")
	_, err = source.CreateResource(bob, primitive.CreateResourceInput{URI: bobURI.String(), FormatVersion: 1, Definition: draftDefinition("Bob")})
	require.NoError(t, err)
	require.NotEqual(t, userReportStorageID(uri), userReportStorageID(bobURI))
	_, err = source.ArchiveResource(alice, uri.String(), primitive.ArchiveResourceInput{})
	require.NoError(t, err)
	_, err = source.Lookup(alice, userActor(), uri)
	require.ErrorIs(t, err, producer.ErrCatalogNotFound)
}
func TestUserReportsLegacyOwnerAliasAndInvalidAuthority(t *testing.T) {
	store := &countedUserStore{MemoryStore: reporting.NewMemoryStore()}
	source, err := NewReports(store, "user-owner")
	require.NoError(t, err)
	ctx := userContext("alice-id", "alice@example.test")
	legacy := &reporting.SharedArtifact{ArtifactID: "legacy", OwnerID: "alice@example.test", Kind: "reportBuilder.savedReportPayload", Document: json.RawMessage(`{"title":"legacy"}`)}
	require.NoError(t, store.CreateSharedArtifact(ctx, legacy))
	require.NoError(t, store.CreateSharedArtifact(ctx, &reporting.SharedArtifact{ArtifactID: "bob-record", OwnerID: "bob-id", Kind: legacy.Kind, Document: legacy.Document}))
	uri := userURI(t, "report://users/alice-id/legacy")
	binding, err := source.Lookup(ctx, userActor(), uri)
	require.NoError(t, err)
	require.Equal(t, "alice-id", binding.OwnerID)
	require.Len(t, store.gets, 2, "legacy probes only after namespaced PK not-found")
	require.Zero(t, store.lists)
	_, err = source.UpdateDraft(ctx, uri.String(), primitive.UpdateDraftInput{FormatVersion: 1, Definition: draftDefinition("changed")})
	require.NoError(t, err)
	persisted, err := store.MemoryStore.GetSharedArtifact(ctx, "legacy")
	require.NoError(t, err)
	require.Equal(t, legacy.OwnerID, persisted.OwnerID, "update must not migrate legacy ownership")
	_, err = source.Lookup(ctx, userActor(), userURI(t, "report://users/alice-id/bob-record"))
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	for _, bad := range []context.Context{userContext("", "alice@example.test"), userContext("bad/name", "alice@example.test"), userContext("bad%2fname", "alice@example.test"), userContext("alice-id", ""), iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "alice@example.test"}), "alice-id")} {
		_, err = source.Lookup(bad, userActor(), uri)
		require.ErrorIs(t, err, identity.ErrResourceDenied)
	}
	_, err = source.Lookup(ctx, identity.VerifiedActor{PublicRead: true, ValidUntil: time.Now().Add(time.Minute)}, uri)
	require.NoError(t, err, "verified workspace user does not need fabricated corporate claims")
	_, err = source.Lookup(ctx, userActor(), userURI(t, "report://users/bob-id/legacy"))
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	store.gets = nil
	store.failure = errors.New("storage unavailable")
	_, err = source.Lookup(ctx, userActor(), uri)
	require.EqualError(t, err, "storage unavailable")
	require.Len(t, store.gets, 1, "storage failure must not probe alternate legacy identity")
}
func TestUserReportsUnsupportedConcurrencyAndCollisions(t *testing.T) {
	store := reporting.NewMemoryStore()
	source, err := NewReports(store, "user-owner")
	require.NoError(t, err)
	ctx := userContext("alice-id", "alice@example.test")
	uri := userURI(t, "report://users/alice-id/report")
	require.NoError(t, store.CreateSharedArtifact(ctx, &reporting.SharedArtifact{ArtifactID: userReportStorageID(uri), OwnerID: "alice-id", Kind: "reportBuilder.savedReportPayload", ArtifactRef: "report://users/alice-id/different", Document: json.RawMessage(`{}`)}))
	_, err = source.Lookup(ctx, userActor(), uri)
	require.ErrorIs(t, err, producer.ErrCatalogCollision)
	_, err = source.StampResource(ctx, uri.String(), primitive.StampResourceInput{})
	require.ErrorIs(t, err, ErrUserReportOperationUnsupported)
	_, err = source.CloneResource(ctx, primitive.CloneResourceInput{})
	require.ErrorIs(t, err, ErrUserReportOperationUnsupported)
	_, err = source.CreateResource(ctx, primitive.CreateResourceInput{URI: "report://users/alice-id/report2", FormatVersion: 1, Definition: draftDefinition("A"), IdempotencyKey: "unsupported"})
	require.ErrorIs(t, err, ErrUserReportOperationUnsupported)
}

func TestUserNamespaceWorkspaceWitnessAndWorkingAuthority(t *testing.T) {
	ctx := userContext("alice-id", "alice@example.test")
	source, err := NewReports(reporting.NewMemoryStore(), "user-owner")
	require.NoError(t, err)
	uri := userURI(t, "report://users/alice-id/source")
	_, err = source.CreateResource(ctx, primitive.CreateResourceInput{URI: uri.String(), FormatVersion: 1, Definition: draftDefinition("A")})
	require.NoError(t, err)
	public := identity.VerifiedActor{PublicRead: true, ValidUntil: time.Now().Add(time.Minute)}
	require.NoError(t, Authorize(ctx, public, identity.ResourceURI{Kind: "resource", Namespace: "users", Name: "catalog"}, "namespace.get"))
	require.NoError(t, Authorize(ctx, public, uri, "resource.get"))
	require.ErrorIs(t, Authorize(ctx, public, userURI(t, "report://users/bob-id/source"), "resource.get"), identity.ErrResourceDenied)
	binding, err := source.Lookup(ctx, public, uri)
	require.NoError(t, err)
	resolver, err := binding.Resolver(ctx, public, "get")
	require.NoError(t, err)
	pin, err := resolver.Resolve(ctx, identity.ResourceRef{URI: uri.String()})
	require.NoError(t, err)
	require.NotEmpty(t, pin.AuthorityBinding)
	require.Equal(t, public.ValidUntil, pin.ValidUntil)
	_, _, err = resolver.ReadResolved(iauth.WithProvider(ctx, "different-verified-provider"), *pin)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	_, _, err = resolver.ReadResolved(userContext("bob-id", "bob@example.test"), *pin)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	for _, missing := range []context.Context{context.Background(), iauth.WithProvider(iauth.WithCanonicalUserID(context.Background(), "alice-id"), "verified"), iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "alice@example.test"}), "alice-id")} {
		require.ErrorIs(t, Authorize(missing, public, uri, "resource.get"), identity.ErrResourceDenied)
	}
}
