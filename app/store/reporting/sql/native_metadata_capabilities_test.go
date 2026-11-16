package sql

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	artifactread "github.com/viant/agently-core/internal/datly/reporting/sharedartifact/read"
	artifactwrite "github.com/viant/agently-core/internal/datly/reporting/sharedartifact/write"
	reportmodel "github.com/viant/agently-core/model/reportshareartifact"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	xhandler "github.com/viant/xdatly/handler"
)

func TestNativeSharedArtifactMetadataProjectionKeepsOwnerScopeAndOmitsBlobs(t *testing.T) {
	ctx, store, _, server := reportingRuntime(t)
	alice := iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "projection-alice"})
	bob := iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "projection-bob"})
	blob := bytes.Repeat([]byte("opaque synthetic payload "), 4096)
	for _, entry := range []struct {
		ctx       context.Context
		id, owner string
	}{{alice, "alice-report", "projection-alice"}, {bob, "bob-report", "projection-bob"}} {
		require.NoError(t, store.CreateSharedArtifact(entry.ctx, &reportmodel.Record{ArtifactID: entry.id, ArtifactRef: "report://users/" + entry.owner + "/example", OwnerID: entry.owner, Kind: "reportBuilder.savedReportPayload", Lifecycle: "draft", Version: 1, Title: "Example", Document: blob, Metadata: blob, CreatedAt: time.Now()}))
	}
	owner := "projection-alice"
	input := &artifactread.Input{}
	input.SetInternal(false)
	input.SetReadMode("rows")
	input.SetOwnerSubject(&owner)
	input.SetFields([]string{"artifact_id", "artifact_ref", "owner_id", "kind", "lifecycle", "version", "title"})
	target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[artifactread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/shared-artifact"}}
	value, err := server.InvokeComponent(alice, dexec.ComponentRequest{Target: target, Input: input, Providers: nativeArtifactCapabilityProviders(owner)})
	require.NoError(t, err)
	output, ok := value.(*artifactread.Output)
	require.True(t, ok)
	require.Len(t, output.Data, 1)
	row := output.Data[0]
	require.Equal(t, "alice-report", row.ArtifactId)
	require.Equal(t, owner, row.OwnerId)
	require.Equal(t, "Example", row.Title)
	require.Nil(t, row.ReportDocumentJson)
	require.Nil(t, row.MetadataJson)
	require.Nil(t, row.ReportFillJson)
	prepared, err := server.InvokeComponent(alice, dexec.ComponentRequest{Target: target, Input: input, Providers: nativeArtifactCapabilityProviders(owner), PrepareQuery: true})
	require.NoError(t, err)
	query, ok := prepared.(*dexec.PreparedQuery)
	require.True(t, ok)
	require.NotEmpty(t, query.SQL)
	require.Contains(t, query.Args, &owner)
}

func TestNativeSharedArtifactSparseArchiveUsesExistingWriterCAS(t *testing.T) {
	ctx, store, _, server := reportingRuntime(t)
	owner := "archive-alice"
	caller := iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: owner})
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	original := &reportmodel.Record{ArtifactID: "archive-report", ArtifactRef: "report://users/archive-alice/example", OwnerID: owner, Kind: "reportBuilder.savedReportPayload", Lifecycle: "draft", Version: 1, Title: "Keep title", Document: []byte(`{"title":"Keep document"}`), Metadata: []byte(`{"keep":"metadata"}`), CreatedAt: created}
	require.NoError(t, store.CreateSharedArtifact(caller, original))
	entity := &artifactwrite.SharedArtifact{}
	entity.SetArtifactId(original.ArtifactID)
	entity.SetOwnerId(owner)
	entity.SetLifecycle("archived")
	entity.SetVersion(2)
	input := &artifactwrite.Input{}
	input.SetInternal(false)
	input.SetOwnerSubject(&owner)
	input.SetWriteIntent("update")
	input.SetExpectedVersion(1)
	input.SetArtifact(entity)
	target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[artifactwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/forge/reporting/shared-artifact"}}
	_, err := server.InvokeComponent(caller, dexec.ComponentRequest{Target: target, Input: input, Providers: nativeArtifactCapabilityProviders(owner)})
	require.NoError(t, err)
	stored, err := store.GetSharedArtifact(caller, original.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, "archived", stored.Lifecycle)
	require.Equal(t, 2, stored.Version)
	require.Equal(t, original.Title, stored.Title)
	require.Equal(t, original.Document, stored.Document)
	require.Equal(t, original.Metadata, stored.Metadata)
	require.True(t, original.CreatedAt.Equal(stored.CreatedAt))
	_, err = server.InvokeComponent(caller, dexec.ComponentRequest{Target: target, Input: input, Providers: nativeArtifactCapabilityProviders(owner)})
	var conflict *xhandler.Conflict
	require.ErrorAs(t, err, &conflict)
}

// Reuse the private artifact-access/visibility binding shape used by the store;
// those trusted providers remain authoritative even with a typed reader input.
func nativeArtifactCapabilityProviders(owner string) []locator.Provider {
	return []locator.Provider{
		provider.Named("artifactaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "mode" {
				return "rows", true, nil
			}
			return false, true, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
}
