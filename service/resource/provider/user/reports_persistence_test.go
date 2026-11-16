package user

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	reportfs "github.com/viant/agently-core/app/store/reporting/fs"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting"
	fsstate "github.com/viant/agently-core/workspace/store/fs"
)

func TestUserReportsCompleteEnvelopeSurvivesFilesystemStoreReopen(t *testing.T) {
	root := t.TempDir()
	ctx := userContext("alice-id", "alice@example.test")
	first, err := NewReports(reporting.NewStoreAdapter(reportfs.New(fsstate.NewStateStore(root))), "user-owner")
	require.NoError(t, err)
	definition := json.RawMessage(`{
  "schemaVersion": 1,
  "reportDocument": {"title":"Stored","entityId":9007199254740993},
  "builderRef": "original-builder",
  "dataSources": {"rows":{"ID":"rows","backend":{"kind":"mcp_tool","service":"platform","method":"search"}}},
  "state": {"original":{"filter":"unchanged","metadata":{"mode":"original"}}}
}`)
	input := primitive.CreateResourceInput{URI: "report://users/alice-id/saved/custom", Title: "Stored", FormatVersion: 1, Definition: definition}
	_, err = first.CreateResource(ctx, input)
	require.NoError(t, err)
	reopened, err := NewReports(reporting.NewStoreAdapter(reportfs.New(fsstate.NewStateStore(root))), "user-owner")
	require.NoError(t, err)
	uri := userURI(t, input.URI)
	binding, err := reopened.Lookup(ctx, userActor(), uri)
	require.NoError(t, err)
	resolver, err := binding.Resolver(ctx, userActor(), "get")
	require.NoError(t, err)
	pin, err := resolver.Resolve(ctx, identity.ResourceRef{URI: input.URI})
	require.NoError(t, err)
	raw, _, err := resolver.ReadResolved(ctx, *pin)
	require.NoError(t, err)
	require.JSONEq(t, string(definition), string(raw), "complete envelope must survive actual durable StoreAdapter, not memory-only ResourceDefinition")
	require.Equal(t, []byte(definition), []byte(raw), "formatting and exact large integer must preserve original fingerprint")
	updated := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Updated"},"builderRef":"same-builder","dataSources":{"rows":{"ID":"rows","backend":{"kind":"mcp_tool","service":"platform","method":"search","parameters":{"original":"preserved"}}}}}`)
	_, err = reopened.UpdateDraft(ctx, input.URI, primitive.UpdateDraftInput{FormatVersion: 1, Definition: updated})
	require.NoError(t, err)
	third, err := NewReports(reporting.NewStoreAdapter(reportfs.New(fsstate.NewStateStore(root))), "user-owner")
	require.NoError(t, err)
	binding, err = third.Lookup(ctx, userActor(), uri)
	require.NoError(t, err)
	resolver, err = binding.Resolver(ctx, userActor(), "get")
	require.NoError(t, err)
	fresh, err := resolver.Resolve(ctx, identity.ResourceRef{URI: input.URI})
	require.NoError(t, err)
	require.NotEqual(t, pin.ContentFingerprint, fresh.ContentFingerprint)
	raw, _, err = resolver.ReadResolved(ctx, *fresh)
	require.NoError(t, err)
	require.JSONEq(t, string(updated), string(raw))
	require.Equal(t, []byte(updated), []byte(raw))
	_, err = third.ArchiveResource(ctx, input.URI, primitive.ArchiveResourceInput{})
	require.NoError(t, err)
}

func TestUserReportCreateReplaySurvivesStoreReopen(t *testing.T) {
	root := t.TempDir()
	ctx := userContext("alice-id", "alice@example.test")
	create := primitive.CreateResourceInput{URI: "report://users/alice-id/copies/operation-one", Title: "Personal", FormatVersion: 1, Definition: json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Personal"}}`), IdempotencyKey: "request-one"}
	source, err := NewReports(reporting.NewStoreAdapter(reportfs.New(fsstate.NewStateStore(root))), "user-owner")
	require.NoError(t, err)
	first, err := source.CreateResource(ctx, create)
	require.NoError(t, err)
	reopened, err := NewReports(reporting.NewStoreAdapter(reportfs.New(fsstate.NewStateStore(root))), "user-owner")
	require.NoError(t, err)
	replay, err := reopened.CreateResource(ctx, create)
	require.NoError(t, err)
	require.Equal(t, first.URI, replay.URI)
	listed, err := reopened.List(ctx, userActor(), "report", "users")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	changed := create
	changed.Definition = json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Different"}}`)
	_, err = reopened.CreateResource(ctx, changed)
	require.ErrorIs(t, err, identity.ErrResourceStale)
}

func TestUserReportRetainsVersionTwoDatasourceReferencesAfterReopen(t *testing.T) {
	root := t.TempDir()
	ctx := userContext("alice-id", "alice@example.test")
	definition := json.RawMessage(`{"schemaVersion":2,"format":"forge.reportReferences","reportDocument":{"title":"Personal"},"reportSpec":{"kind":"reportSpec"},"dataSourceResources":{"rows":{"resource":{"uri":"datasource://example/rows","revision":"working"},"providerIdentity":"example","contentFingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}`)
	source, err := NewReports(reporting.NewStoreAdapter(reportfs.New(fsstate.NewStateStore(root))), "user-owner")
	require.NoError(t, err)
	input := primitive.CreateResourceInput{URI: "report://users/alice-id/personal/v2", Title: "Personal", FormatVersion: 2, Definition: definition}
	_, err = source.CreateResource(ctx, input)
	require.NoError(t, err)
	reopened, err := NewReports(reporting.NewStoreAdapter(reportfs.New(fsstate.NewStateStore(root))), "user-owner")
	require.NoError(t, err)
	uri := userURI(t, input.URI)
	binding, err := reopened.Lookup(ctx, userActor(), uri)
	require.NoError(t, err)
	resolver, err := binding.Resolver(ctx, userActor(), "get")
	require.NoError(t, err)
	pin, err := resolver.Resolve(ctx, identity.ResourceRef{URI: input.URI})
	require.NoError(t, err)
	raw, _, err := resolver.ReadResolved(ctx, *pin)
	require.NoError(t, err)
	require.Equal(t, []byte(definition), []byte(raw))
}
