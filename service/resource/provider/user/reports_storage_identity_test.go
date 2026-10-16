package user

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	reportfs "github.com/viant/agently-core/app/store/reporting/fs"
	iauth "github.com/viant/agently-core/internal/auth"
	primitive "github.com/viant/agently-core/protocol/primitive"
	"github.com/viant/agently-core/service/reporting"
)

type enforcingOwnerStore struct{ *reporting.MemoryStore }

func (s enforcingOwnerStore) CreateSharedArtifact(ctx context.Context, row *reporting.SharedArtifact) error {
	if row.OwnerID != iauth.EffectiveUserID(ctx) {
		return reporting.ErrNotFound
	}
	return s.MemoryStore.CreateSharedArtifact(ctx, row)
}
func (s enforcingOwnerStore) GetSharedArtifact(ctx context.Context, id string) (*reporting.SharedArtifact, error) {
	row, err := s.MemoryStore.GetSharedArtifact(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.OwnerID != iauth.EffectiveUserID(ctx) {
		return nil, reporting.ErrNotFound
	}
	return row, nil
}
func (s enforcingOwnerStore) ListSharedArtifacts(ctx context.Context) ([]*reporting.SharedArtifact, error) {
	rows, err := s.MemoryStore.ListSharedArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	var out []*reporting.SharedArtifact
	for _, row := range rows {
		if row.OwnerID == iauth.EffectiveUserID(ctx) {
			out = append(out, row)
		}
	}
	return out, nil
}
func (s enforcingOwnerStore) UpdateSharedArtifact(ctx context.Context, row *reporting.SharedArtifact) error {
	if _, err := s.GetSharedArtifact(ctx, row.ArtifactID); err != nil {
		return err
	}
	if row.OwnerID != iauth.EffectiveUserID(ctx) {
		return reporting.ErrNotFound
	}
	return s.MemoryStore.UpdateSharedArtifact(ctx, row)
}
func (s enforcingOwnerStore) DeleteSharedArtifact(ctx context.Context, id string) error {
	if _, err := s.GetSharedArtifact(ctx, id); err != nil {
		return err
	}
	return s.MemoryStore.DeleteSharedArtifact(ctx, id)
}

func TestUserReportsPrivateStorageIdentityAndLegacyOwner(t *testing.T) {
	store := enforcingOwnerStore{reporting.NewMemoryStore()}
	ctx := iauth.WithBearer(userContext("alice-id", "alice@example.test"), "unchanged-credential")
	require.NoError(t, store.CreateSharedArtifact(ctx, &reporting.SharedArtifact{ArtifactID: "old", OwnerID: "alice@example.test", Kind: "reportBuilder.savedReportPayload", Document: draftDoc()}))
	source, err := NewReports(store, "owner")
	require.NoError(t, err)
	uri := "report://users/alice-id/new"
	_, err = source.CreateResource(ctx, primitive.CreateResourceInput{URI: uri, FormatVersion: 1, Definition: draftDefinition("new")})
	require.NoError(t, err)
	require.Equal(t, "alice@example.test", iauth.EffectiveUserID(ctx))
	require.Equal(t, "unchanged-credential", iauth.MCPAuthToken(ctx, false))
	require.Equal(t, "verified-workspace", iauth.Provider(ctx))
	_, err = source.Lookup(ctx, userActor(), userURI(t, uri))
	require.NoError(t, err)
	listed, err := source.List(ctx, userActor(), "report", "users")
	require.NoError(t, err)
	require.Len(t, listed, 2)
	for _, ref := range []string{uri, "report://users/alice-id/old"} {
		_, err = source.UpdateDraft(ctx, ref, primitive.UpdateDraftInput{FormatVersion: 1, Definition: draftDefinition("updated")})
		require.NoError(t, err)
		_, err = source.ArchiveResource(ctx, ref, primitive.ArchiveResourceInput{})
		require.NoError(t, err)
	}
}

// The real filesystem store resolves its state root under the supplied
// persistence identity, matching principal-scoped state configurations.
type ownerDirectoryState struct{ root string }

func (s ownerDirectoryState) StateRoot(ctx context.Context) (string, error) {
	return filepath.Join(s.root, iauth.EffectiveUserID(ctx)), nil
}
func (s ownerDirectoryState) StatePath(ctx context.Context, scope string) (string, error) {
	root, err := s.StateRoot(ctx)
	return filepath.Join(root, scope), err
}
func TestUserReportsFilesystemCanonicalAndLegacyDirectories(t *testing.T) {
	state := ownerDirectoryState{t.TempDir()}
	ctx := userContext("alice-id", "alice@example.test")
	store := reporting.NewStoreAdapter(reportfs.New(state))
	require.NoError(t, store.CreateSharedArtifact(ctx, &reporting.SharedArtifact{ArtifactID: "old", OwnerID: "alice@example.test", Kind: "reportBuilder.savedReportPayload", Document: draftDoc()}))
	bob := userContext("bob-id", "bob@example.test")
	require.NoError(t, store.CreateSharedArtifact(bob, &reporting.SharedArtifact{ArtifactID: "bob", OwnerID: "bob@example.test", Kind: "reportBuilder.savedReportPayload", Document: draftDoc()}))
	source, err := NewReports(store, "owner")
	require.NoError(t, err)
	uri := "report://users/alice-id/new"
	_, err = source.CreateResource(ctx, primitive.CreateResourceInput{URI: uri, FormatVersion: 1, Definition: draftDefinition("new")})
	require.NoError(t, err)
	_, err = store.GetSharedArtifact(ctx, userReportStorageID(userURI(t, uri)))
	require.ErrorIs(t, err, reporting.ErrNotFound, "new records must not leak into legacy identity's state directory")
	reopened, err := NewReports(reporting.NewStoreAdapter(reportfs.New(state)), "owner")
	require.NoError(t, err)
	listed, err := reopened.List(ctx, userActor(), "report", "users")
	require.NoError(t, err)
	require.Len(t, listed, 2)
	for _, ref := range []string{uri, "report://users/alice-id/old"} {
		_, err = reopened.Lookup(ctx, userActor(), userURI(t, ref))
		require.NoError(t, err)
		_, err = reopened.UpdateDraft(ctx, ref, primitive.UpdateDraftInput{FormatVersion: 1, Definition: draftDefinition("reopened")})
		require.NoError(t, err)
		_, err = reopened.ArchiveResource(ctx, ref, primitive.ArchiveResourceInput{})
		require.NoError(t, err)
	}
}

func draftDoc() json.RawMessage { return json.RawMessage(`{"title":"legacy"}`) }
