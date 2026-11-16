package executor_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/tidwall/sjson"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	iauth "github.com/viant/agently-core/internal/auth"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/reporting"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
)

func TestDefaultReportingUsesUserMCPAndSharedGateway(t *testing.T) {
	previous := workspace.Root()
	workspace.SetRoot(t.TempDir())
	t.Cleanup(func() { workspace.SetRoot(previous) })
	ctx := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "alice@example.test"}), "canonical-alice"), "verified-workspace")
	runtime, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithDefaults(&execconfig.Defaults{Reporting: execconfig.ReportingDefaults{Enabled: true, QueueIntervalMs: 60000}}).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	require.True(t, runtime.MCPManager.IsLocalClient("workspace-users"))
	query := primitive.CatalogQuery{Namespace: "users"}
	before, err := resources.ListPage[json.RawMessage](ctx, runtime.PrimitiveProviders, "report", query)
	require.NoError(t, err)
	require.Empty(t, before.Items)
	input := map[string]any{"reportId": "my-report", "title": "My report", "reportDocument": map[string]any{"kind": "reportDocument", "id": "my-report", "title": "My report"}, "reportSpec": validReportingIntegrationSpecPayload()}
	original, _ := json.Marshal(input)
	raw, err := runtime.Registry.Execute(ctx, "reporting:save_report", input)
	require.NoError(t, err)
	unchanged, _ := json.Marshal(input)
	require.Equal(t, original, unchanged)
	var saved reporting.SharedArtifact
	require.NoError(t, json.Unmarshal([]byte(raw), &saved))
	require.True(t, strings.HasPrefix(saved.ArtifactRef, "report://users/canonical-alice/"), saved.ArtifactRef)
	require.Equal(t, saved.ArtifactRef, saved.ArtifactID)
	require.Equal(t, "canonical-alice", saved.OwnerID)
	after, err := resources.ListPage[json.RawMessage](ctx, runtime.PrimitiveProviders, "report", query)
	require.NoError(t, err)
	require.Len(t, after.Items, 1, "save must invalidate the same owner's cached list")
	require.Equal(t, "workspace-users", after.Items[0].Connection.ProviderIdentity)
	raw, err = runtime.Registry.Execute(ctx, "reporting:get_report", map[string]any{"artifactId": saved.ArtifactID})
	require.NoError(t, err)
	var reopened reporting.SharedArtifact
	require.NoError(t, json.Unmarshal([]byte(raw), &reopened))
	require.Equal(t, saved.ArtifactRef, reopened.ArtifactRef)
	require.JSONEq(t, string(saved.Document), string(reopened.Document))
	byName, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ReportID: "my-report"})
	require.NoError(t, err)
	require.Equal(t, saved.ArtifactRef, byName.ArtifactRef)
	listed, err := runtime.Reporting.ListReports(ctx, &reporting.ListReportsInput{CurrentUserOnly: true})
	require.NoError(t, err)
	require.Len(t, listed.Reports, 1)
	require.True(t, listed.Reports[0].OwnedByCurrentUser)
	require.NotNil(t, listed.Reports[0].Capabilities)
	require.True(t, listed.Reports[0].Capabilities.Edit)
	require.True(t, listed.Reports[0].Capabilities.Duplicate)
	require.True(t, listed.Reports[0].Capabilities.Rename)
	require.True(t, listed.Reports[0].Capabilities.Delete)
	_, err = runtime.Registry.Execute(ctx, "reporting:update_report", map[string]any{"resource": map[string]any{"uri": saved.ArtifactRef}, "title": "Updated report"})
	require.NoError(t, err, "ordinary updates must not invent unsupported concurrency constraints")
	updated, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactID: saved.ArtifactID})
	require.NoError(t, err)
	require.Equal(t, "Updated report", updated.Title)
	bob := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "bob@example.test"}), "canonical-bob"), "verified-workspace")
	_, err = runtime.Registry.Execute(bob, "reporting:get_report", map[string]any{"artifactId": saved.ArtifactID})
	require.Error(t, err, "the managing user MCP must deny another owner")
	bobReports, err := runtime.Reporting.ListReports(bob, &reporting.ListReportsInput{CurrentUserOnly: true})
	require.NoError(t, err)
	require.Empty(t, bobReports.Reports, "capability metadata cannot reveal another owner's private report")
	_, err = runtime.Registry.Execute(ctx, "reporting:save_report", map[string]any{"resource": map[string]any{"uri": "report://platform/forbidden"}, "definition": map[string]any{"schemaVersion": 1, "reportDocument": map[string]any{"title": "Forbidden"}}})
	require.Error(t, err, "the managing user MCP must reject a non-user write target")
}

func TestPersonalReportCopyResetUsesExistingUserProvider(t *testing.T) {
	previous := workspace.Root()
	workspace.SetRoot(t.TempDir())
	t.Cleanup(func() { workspace.SetRoot(previous) })
	ctx := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "alice@example.test"}), "canonical-alice"), "verified-workspace")
	runtime, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithDefaults(&execconfig.Defaults{Reporting: execconfig.ReportingDefaults{Enabled: true, QueueIntervalMs: 60000}}).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	original, err := runtime.Reporting.SaveReport(ctx, &reporting.SaveReportRequest{ReportID: "original", Title: "Original", Definition: completePersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Original","period":"daily"}}`))})
	require.NoError(t, err)
	before, err := runtime.Reporting.ListReports(ctx, &reporting.ListReportsInput{CurrentUserOnly: true})
	require.NoError(t, err)
	require.Len(t, before.Reports, 1)
	// Exact requests remain usable if the preference store is unavailable.
	preferenceReads := 0
	failure := errors.New("preference reader unavailable")
	runtime.Reporting.SetPersonalWorkspacePreferences(func(context.Context) (*authsvc.WorkspacePreferences, error) { preferenceReads++; return nil, failure })
	_, err = runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{Resource: &identity.ResourceRef{URI: original.ArtifactRef}})
	require.NoError(t, err)
	_, err = runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ResolvedResource: original.Resource, PreferPersonal: true})
	require.NoError(t, err)
	require.Zero(t, preferenceReads)
	_, err = runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{Resource: &identity.ResourceRef{URI: original.ArtifactRef}, PreferPersonal: true})
	require.ErrorIs(t, err, failure)
	require.Equal(t, 1, preferenceReads)
	runtime.Reporting.SetPersonalWorkspacePreferences(nil)
	// Reading an original is not an authoring operation.
	_, err = runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactRef: original.ArtifactRef})
	require.NoError(t, err)
	gatewayCatalog := &resources.ReportGatewayCatalog{Gateway: runtime.PrimitiveProviders}
	runtime.Reporting.SetResourceReaderFactory(func(ctx context.Context, operation string) (reporting.ResourceReader, error) {
		if operation == "report.duplicate" {
			return nil, identity.ErrResourceDenied
		}
		return gatewayCatalog.Reader(ctx, operation)
	})
	copyRequest := &reporting.DuplicateReportRequest{ReportID: original.ArtifactRef, Title: "Weekly review", IdempotencyKey: "turn-copy-1", Definition: completePersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Weekly review","period":"weekly"}}`))}
	saved, err := runtime.Reporting.DuplicateReport(ctx, copyRequest)
	require.NoError(t, err)
	replay, err := runtime.Reporting.DuplicateReport(ctx, copyRequest)
	require.NoError(t, err)
	require.Equal(t, saved.ArtifactRef, replay.ArtifactRef)
	after, err := runtime.Reporting.ListReports(ctx, &reporting.ListReportsInput{CurrentUserOnly: true})
	require.NoError(t, err)
	require.Len(t, after.Reports, 2)
	invalid := *copyRequest
	invalid.IdempotencyKey = "invalid-copy"
	invalid.Definition = json.RawMessage(`{"schemaVersion":1}`)
	_, err = runtime.Reporting.DuplicateReport(ctx, &invalid)
	require.Error(t, err)
	unchangedCount, err := runtime.Reporting.ListReports(ctx, &reporting.ListReportsInput{CurrentUserOnly: true})
	require.NoError(t, err)
	require.Len(t, unchangedCount.Reports, 2)
	var definition map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(saved.ResourceDefinition, &definition))
	require.Contains(t, string(definition["metadata"]), "personalReport")
	updated, err := runtime.Reporting.UpdateReport(ctx, &reporting.UpdateReportRequest{ReportID: saved.ArtifactRef, Title: "Changed", ExpectedContentFingerprint: saved.Resource.ContentFingerprint})
	require.NoError(t, err)
	reset, err := runtime.Reporting.ResetReport(ctx, &reporting.ResetReportRequest{Resource: &identity.ResourceRef{URI: saved.ArtifactRef}, ExpectedContentFingerprint: updated.Resource.ContentFingerprint})
	require.NoError(t, err)
	require.Equal(t, saved.ArtifactRef, reset.ArtifactRef)
	require.Contains(t, string(reset.Document), "daily")
	require.Contains(t, string(reset.Document), "Changed")
	entry := authsvc.WorkspaceReportDefault{SourceProvider: original.Resource.ProviderIdentity, SourceURI: original.ArtifactRef, TargetURI: saved.ArtifactRef}
	require.NoError(t, runtime.Reporting.ValidatePersonalDefault(ctx, entry))
	preferences := &authsvc.WorkspacePreferences{Defaults: []authsvc.WorkspaceReportDefault{entry}}
	runtime.Reporting.SetPersonalWorkspacePreferences(func(context.Context) (*authsvc.WorkspacePreferences, error) { return preferences, nil })
	preferred, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactRef: original.ArtifactRef, PreferPersonal: true})
	require.NoError(t, err)
	require.Equal(t, saved.ArtifactRef, preferred.ArtifactRef)
	exact, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactRef: original.ArtifactRef})
	require.NoError(t, err)
	require.Equal(t, original.ArtifactRef, exact.ArtifactRef)
	pinned, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ResolvedResource: original.Resource, PreferPersonal: true})
	require.NoError(t, err)
	require.Equal(t, original.ArtifactRef, pinned.ArtifactRef, "an exact pin takes precedence over defaults")

	preferences.Defaults[0].TargetURI = original.ArtifactRef
	_, err = runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactRef: original.ArtifactRef, PreferPersonal: true})
	require.ErrorContains(t, err, "no longer matches")
	preferences.Defaults = nil
	fallback, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactRef: original.ArtifactRef, PreferPersonal: true})
	require.NoError(t, err)
	require.Equal(t, original.ArtifactRef, fallback.ArtifactRef)
	untouched, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactRef: original.ArtifactRef})
	require.NoError(t, err)
	require.JSONEq(t, string(original.Document), string(untouched.Document))
	lineage := func(report *reporting.SharedArtifact) *reporting.PersonalReportSource {
		var metadata struct {
			Source *reporting.PersonalReportSource `json:"personalReport"`
		}
		require.NoError(t, json.Unmarshal(report.Metadata, &metadata))
		require.NotNil(t, metadata.Source)
		return metadata.Source
	}
	require.Equal(t, original.ArtifactRef, lineage(reset).ParentURI, "snapshot reset preserves the original parent")
	nested, err := runtime.Reporting.DuplicateReport(ctx, &reporting.DuplicateReportRequest{ReportID: saved.ArtifactRef, Title: "Nested review", IdempotencyKey: "nested-copy"})
	require.NoError(t, err)
	require.Equal(t, saved.ArtifactRef, lineage(nested).ParentURI)
	nestedReset, err := runtime.Reporting.ResetReport(ctx, &reporting.ResetReportRequest{Resource: &identity.ResourceRef{URI: nested.ArtifactRef}, ExpectedContentFingerprint: nested.Resource.ContentFingerprint})
	require.NoError(t, err)
	require.Equal(t, saved.ArtifactRef, lineage(nestedReset).ParentURI, "reset must not make a copy its own parent")
	require.Equal(t, original.ArtifactRef, lineage(nestedReset).URI)
	latestOriginal, err := runtime.Reporting.UpdateReport(ctx, &reporting.UpdateReportRequest{ReportID: original.ArtifactRef, ReportDocument: json.RawMessage(`{"title":"Original","period":"monthly"}`)})
	require.NoError(t, err)
	latestReset, err := runtime.Reporting.ResetReport(ctx, &reporting.ResetReportRequest{Resource: &identity.ResourceRef{URI: nested.ArtifactRef}, ExpectedContentFingerprint: nestedReset.Resource.ContentFingerprint, Latest: true})
	require.NoError(t, err)
	require.Equal(t, nested.ArtifactRef, latestReset.ArtifactRef)
	require.Equal(t, "Nested review", latestReset.Title)
	require.Contains(t, string(latestReset.Document), `"title":"Nested review"`)
	require.Contains(t, string(latestReset.Document), "monthly")
	require.Equal(t, saved.ArtifactRef, lineage(latestReset).ParentURI)
	require.Equal(t, original.ArtifactRef, lineage(latestReset).URI)
	require.Equal(t, latestOriginal.Resource.ContentFingerprint, lineage(latestReset).Fingerprint)
	require.Contains(t, string(lineage(latestReset).Definition), "monthly")
	_, err = runtime.Reporting.DeleteReport(ctx, &reporting.DeleteReportRequest{ReportID: original.ArtifactRef})
	require.NoError(t, err)
	_, err = runtime.Reporting.ResetReport(ctx, &reporting.ResetReportRequest{Resource: &identity.ResourceRef{URI: saved.ArtifactRef}, ExpectedContentFingerprint: reset.Resource.ContentFingerprint})
	require.NoError(t, err, "recorded snapshot reset must not require the original to remain available")
}

func TestPersonalReportExecutionRetainsDatasourceAndCurrentCaller(t *testing.T) {
	previous := workspace.Root()
	workspace.SetRoot(t.TempDir())
	t.Cleanup(func() { workspace.SetRoot(previous) })
	ctx := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "alice@example.test"}), "canonical-alice"), "verified-workspace")
	runtime, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithDefaults(&execconfig.Defaults{Reporting: execconfig.ReportingDefaults{Enabled: true, QueueIntervalMs: 60000}}).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	spec := validReportingIntegrationSpecPayload()
	definition, err := json.Marshal(map[string]any{"schemaVersion": 1, "reportDocument": map[string]any{"title": "Original"}, "reportSpec": spec, "dataSources": map[string]any{"demo": map[string]any{"id": "demo", "cardinality": "collection", "backend": map[string]any{"kind": "mcp_tool", "service": "example", "method": "read"}}}})
	require.NoError(t, err)
	original, err := runtime.Reporting.SaveReport(ctx, &reporting.SaveReportRequest{ReportID: "executable-original", Title: "Original", Definition: definition})
	require.NoError(t, err)
	calls := 0
	denied := false
	runtime.Reporting.SetResourceDatasetExecutor(func(callCtx context.Context, pin identity.ResolvedResource, descriptor *dsproto.DataSource, parameters map[string]any) (*dsproto.FetchResult, error) {
		calls++
		require.Equal(t, "alice@example.test", iauth.EffectiveUserID(callCtx))
		require.Equal(t, "demo", descriptor.ID)
		if denied {
			return nil, identity.ErrResourceDenied
		}
		return &dsproto.FetchResult{Rows: []map[string]any{{"category": "Example", "amount": 7}}}, nil
	})
	copied, err := runtime.Reporting.DuplicateReport(ctx, &reporting.DuplicateReportRequest{ReportID: original.ArtifactRef, Title: "Personal", IdempotencyKey: "execution-copy"})
	require.NoError(t, err)
	require.Contains(t, string(copied.Document), `"title":"Personal"`)
	require.Contains(t, string(copied.ReportSpec), `"title":"Personal"`)
	require.Zero(t, calls, "copying must not fetch datasource rows")
	renamed, err := runtime.Reporting.UpdateReport(ctx, &reporting.UpdateReportRequest{ReportID: copied.ArtifactRef, Title: "Weekly review", ExpectedContentFingerprint: copied.Resource.ContentFingerprint})
	require.NoError(t, err)
	require.Equal(t, "Weekly review", renamed.Title)
	require.Contains(t, string(renamed.Document), `"title":"Weekly review"`)
	require.Contains(t, string(renamed.ReportSpec), `"title":"Weekly review"`)
	final, err := runtime.Reporting.UpdateReport(ctx, &reporting.UpdateReportRequest{ReportID: copied.ArtifactRef, Title: "Final review", Definition: renamed.ResourceDefinition, ExpectedContentFingerprint: renamed.Resource.ContentFingerprint})
	require.NoError(t, err)
	require.Contains(t, string(final.Document), `"title":"Final review"`)
	require.Contains(t, string(final.ReportSpec), `"title":"Final review"`)
	reset, err := runtime.Reporting.ResetReport(ctx, &reporting.ResetReportRequest{Resource: &identity.ResourceRef{URI: copied.ArtifactRef}, ExpectedContentFingerprint: final.Resource.ContentFingerprint})
	require.NoError(t, err)
	require.Equal(t, copied.ArtifactRef, reset.ArtifactRef)
	require.Equal(t, "Final review", reset.Title)
	require.Contains(t, string(reset.Document), `"title":"Final review"`)
	require.Contains(t, string(reset.ReportSpec), `"title":"Final review"`)
	require.Zero(t, calls, "rename and reset must not execute data")
	unchanged, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactRef: original.ArtifactRef})
	require.NoError(t, err)
	require.Equal(t, original.Resource.ContentFingerprint, unchanged.Resource.ContentFingerprint)
	require.Equal(t, string(original.ResourceDefinition), string(unchanged.ResourceDefinition), "personal renames and reset cannot rewrite the source")
	originalRun, err := runtime.Reporting.ExecuteResource(ctx, &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: original.ArtifactRef}, FillOnly: true})
	require.NoError(t, err)
	require.NotEmpty(t, originalRun.ReportFill)
	personalRun, err := runtime.Reporting.ExecuteResource(ctx, &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: copied.ArtifactRef}, FillOnly: true})
	require.NoError(t, err)
	require.NotEmpty(t, personalRun.ReportFill)
	require.Equal(t, 2, calls)
	denied = true
	_, err = runtime.Reporting.ExecuteResource(ctx, &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: copied.ArtifactRef}, FillOnly: true})
	require.Error(t, err)
}

func TestPersonalReportProviderOwnsProvenanceThroughNativeMCP(t *testing.T) {
	previous := workspace.Root()
	workspace.SetRoot(t.TempDir())
	t.Cleanup(func() { workspace.SetRoot(previous) })
	ctx := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "alice@example.test"}), "canonical-alice"), "verified-workspace")
	runtime, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithDefaults(&execconfig.Defaults{Reporting: execconfig.ReportingDefaults{Enabled: true, QueueIntervalMs: 60000}}).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	original, err := runtime.Reporting.SaveReport(ctx, &reporting.SaveReportRequest{ReportID: "original", Title: "Original", Definition: completePersonalFixture(t, json.RawMessage("{\n \"schemaVersion\":1,\n \"reportDocument\":{\"title\":\"Original\",\"integer\":9007199254740993}\n}"))})
	require.NoError(t, err)
	writer, err := resources.NewAuthoring(runtime.PrimitiveProviders, "workspace-users")
	require.NoError(t, err)
	_, err = writer.UpdateDraft(ctx, original.ArtifactRef, primitive.UpdateDraftInput{FormatVersion: 1, Definition: original.ResourceDefinition, ExpectedContentFingerprint: original.Resource.ContentFingerprint, SourceManifest: json.RawMessage(`{"operation":"resetRecorded"}`)})
	require.ErrorIs(t, err, errors.ErrUnsupported, "native MCP must retain the unsupported reset outcome")
	forged := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Unverified"},"metadata":{"personalReport":{"uri":"report://example/unverified","revision":"working","fingerprint":"unverified","definition":{"schemaVersion":1,"reportDocument":{"title":"Unverified"}}}}}`)
	forged = completePersonalFixture(t, forged)
	_, err = writer.CreateResource(ctx, primitive.CreateResourceInput{URI: "report://users/canonical-alice/forged", FormatVersion: 1, Definition: forged})
	require.Error(t, err)
	require.ErrorIs(t, err, identity.ErrResource)
	listed, err := runtime.Reporting.ListReports(ctx, &reporting.ListReportsInput{CurrentUserOnly: true})
	require.NoError(t, err)
	require.Len(t, listed.Reports, 1, "unverified provenance must not create a row")
	manifest, err := json.Marshal(reporting.PersonalReportSourceManifest{Operation: "copy", Source: &identity.ResourceRef{URI: original.ArtifactRef, Revision: original.Resource.Selector()}, Provider: original.Resource.ProviderIdentity, ExpectedSourceFingerprint: original.Resource.ContentFingerprint})
	require.NoError(t, err)
	uri := "report://users/canonical-alice/derived"
	// Even a direct caller cannot choose the stored original: the manifest
	// is resolved by the same verified provider reader as ordinary reporting.
	created, err := writer.CreateResource(ctx, primitive.CreateResourceInput{URI: uri, Title: "Derived", FormatVersion: 1, Definition: forged, SourceManifest: manifest})
	require.NoError(t, err)
	readback := func() *reporting.SharedArtifact {
		got, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactRef: uri})
		require.NoError(t, err)
		return got
	}
	lineage := func(got *reporting.SharedArtifact) *reporting.PersonalReportSource {
		var definition struct {
			Metadata struct {
				Source *reporting.PersonalReportSource `json:"personalReport"`
			} `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal(got.ResourceDefinition, &definition))
		require.NotNil(t, definition.Metadata.Source)
		return definition.Metadata.Source
	}
	first := readback()
	require.Equal(t, created.ContentFingerprint, first.Resource.ContentFingerprint)
	require.Equal(t, original.ArtifactRef, lineage(first).URI)
	require.Equal(t, original.Resource.ContentFingerprint, lineage(first).Fingerprint)
	require.Equal(t, []byte(original.ResourceDefinition), []byte(lineage(first).Definition))
	_, err = writer.UpdateDraft(ctx, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: forged, ExpectedContentFingerprint: first.Resource.ContentFingerprint})
	require.ErrorIs(t, err, identity.ErrResource)
	require.Equal(t, first.Resource.ContentFingerprint, readback().Resource.ContentFingerprint)
	plain := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Edited","period":"weekly"}}`)
	plain = completePersonalFixture(t, plain)
	updated, err := writer.UpdateDraft(ctx, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: plain, ExpectedContentFingerprint: first.Resource.ContentFingerprint})
	require.NoError(t, err, "ordinary editing can omit provider-owned provenance")
	fresh := readback()
	require.Equal(t, updated.ContentFingerprint, fresh.Resource.ContentFingerprint)
	require.Equal(t, lineage(first), lineage(fresh))
	reset, err := writer.UpdateDraft(ctx, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: plain, ExpectedContentFingerprint: fresh.Resource.ContentFingerprint, SourceManifest: json.RawMessage(`{"operation":"resetRecorded"}`)})
	require.NoError(t, err)
	require.Equal(t, reset.ContentFingerprint, readback().Resource.ContentFingerprint)
	require.Contains(t, string(readback().Document), "9007199254740993")
	_, err = writer.UpdateDraft(ctx, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: plain, ExpectedContentFingerprint: fresh.Resource.ContentFingerprint, SourceManifest: json.RawMessage(`{"operation":"resetRecorded"}`)})
	require.ErrorIs(t, err, identity.ErrResourceStale, "stale mutation cannot overwrite the reset")
	listed, err = runtime.Reporting.ListReports(ctx, &reporting.ListReportsInput{CurrentUserOnly: true})
	require.NoError(t, err)
	require.Len(t, listed.Reports, 2)
}

func TestPersonalReportSpecOnlyUsesNativeCompilerAndRejectsNullDocument(t *testing.T) {
	previous := workspace.Root()
	workspace.SetRoot(t.TempDir())
	t.Cleanup(func() { workspace.SetRoot(previous) })
	ctx := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "alice@example.test"}), "canonical-alice"), "verified-workspace")
	runtime, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithDefaults(&execconfig.Defaults{Reporting: execconfig.ReportingDefaults{Enabled: true, QueueIntervalMs: 60000}}).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	fields := map[string]any{"schemaVersion": 1, "reportSpec": validReportingIntegrationSpecPayload(), "reportDocument": nil,
		"dataSources": map[string]any{"demo": map[string]any{"id": "demo", "cardinality": "collection", "backend": map[string]any{"kind": "mcp_tool", "service": "example", "method": "read"}}}}
	invalid, err := json.Marshal(fields)
	require.NoError(t, err)
	_, err = runtime.Reporting.SaveReport(ctx, &reporting.SaveReportRequest{ReportID: "null-document", Definition: invalid})
	require.ErrorIs(t, err, identity.ErrResource, "native envelope validation rejects explicit null; the host must not invent another interpretation")
	listed, err := runtime.Reporting.ListReports(ctx, &reporting.ListReportsInput{CurrentUserOnly: true})
	require.NoError(t, err)
	require.Empty(t, listed.Reports)
	delete(fields, "reportDocument")
	definition, err := json.Marshal(fields)
	require.NoError(t, err)
	original, err := runtime.Reporting.SaveReport(ctx, &reporting.SaveReportRequest{ReportID: "spec-only", Definition: definition})
	require.NoError(t, err)
	dataCalls := 0
	runtime.Reporting.SetResourceDatasetExecutor(func(callCtx context.Context, pin identity.ResolvedResource, descriptor *dsproto.DataSource, _ map[string]any) (*dsproto.FetchResult, error) {
		dataCalls++
		require.Equal(t, "alice@example.test", iauth.EffectiveUserID(callCtx))
		require.Equal(t, "demo", descriptor.ID)
		return &dsproto.FetchResult{Rows: []map[string]any{{"category": "Example", "amount": 7}}}, nil
	})
	copy, err := runtime.Reporting.DuplicateReport(ctx, &reporting.DuplicateReportRequest{ReportID: original.ArtifactRef, Title: "Personal spec", IdempotencyKey: "spec-copy"})
	require.NoError(t, err)
	reset, err := runtime.Reporting.ResetReport(ctx, &reporting.ResetReportRequest{Resource: &identity.ResourceRef{URI: copy.ArtifactRef}, ExpectedContentFingerprint: copy.Resource.ContentFingerprint})
	require.NoError(t, err)
	require.Equal(t, copy.ArtifactRef, reset.ArtifactRef)
	require.Contains(t, string(reset.ReportSpec), `"title":"Personal spec"`)
	require.Zero(t, dataCalls, "copy and reset only author the definition")
	run, err := runtime.Reporting.ExecuteResource(ctx, &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: reset.ArtifactRef}, FillOnly: true})
	require.NoError(t, err)
	require.NotEmpty(t, run.ReportFill)
	require.Equal(t, 1, dataCalls, "the native spec-only execution fetches data once")
	unchanged, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactRef: original.ArtifactRef})
	require.NoError(t, err)
	require.Equal(t, original.Resource.ContentFingerprint, unchanged.Resource.ContentFingerprint)
}

func TestPersonalReportCompilerRejectsInvalidCandidatesBeforeNativeWrite(t *testing.T) {
	previous := workspace.Root()
	workspace.SetRoot(t.TempDir())
	t.Cleanup(func() { workspace.SetRoot(previous) })
	ctx := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "alice@example.test"}), "canonical-alice"), "verified-workspace")
	runtime, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithDefaults(&execconfig.Defaults{Reporting: execconfig.ReportingDefaults{Enabled: true, QueueIntervalMs: 60000}}).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	spec, err := json.Marshal(validReportingIntegrationSpecPayload())
	require.NoError(t, err)
	original, err := runtime.Reporting.SaveReport(ctx, &reporting.SaveReportRequest{ReportID: "original", ReportSpec: spec})
	require.NoError(t, err)
	invalid := json.RawMessage(`{"schemaVersion":1,"reportSpec":{}}`)
	_, err = runtime.Reporting.DuplicateReport(ctx, &reporting.DuplicateReportRequest{ReportID: original.ArtifactRef, Title: "Invalid", IdempotencyKey: "invalid-compile", Definition: invalid})
	require.Error(t, err)
	list, err := runtime.Reporting.ListReports(ctx, &reporting.ListReportsInput{CurrentUserOnly: true})
	require.NoError(t, err)
	require.Len(t, list.Reports, 1, "invalid customization must create no row")
	copy, err := runtime.Reporting.DuplicateReport(ctx, &reporting.DuplicateReportRequest{ReportID: original.ArtifactRef, Title: "Valid", IdempotencyKey: "valid-compile"})
	require.NoError(t, err)
	_, err = runtime.Reporting.UpdateReport(ctx, &reporting.UpdateReportRequest{ReportID: copy.ArtifactRef, ExpectedContentFingerprint: copy.Resource.ContentFingerprint, Definition: invalid})
	require.Error(t, err)
	readback, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactRef: copy.ArtifactRef})
	require.NoError(t, err)
	require.Equal(t, copy.Resource.ContentFingerprint, readback.Resource.ContentFingerprint, "failed edit must preserve saved report")
}

func completePersonalFixture(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	spec, err := json.Marshal(validReportingIntegrationSpecPayload())
	require.NoError(t, err)
	result, err := sjson.SetRawBytes(raw, "reportSpec", spec)
	require.NoError(t, err)
	return result
}

func TestPersonalCopyReplayDoesNotRequireOriginalAfterSave(t *testing.T) {
	previous := workspace.Root()
	workspace.SetRoot(t.TempDir())
	t.Cleanup(func() { workspace.SetRoot(previous) })
	ctx := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "alice@example.test"}), "canonical-alice"), "verified-workspace")
	runtime, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithDefaults(&execconfig.Defaults{Reporting: execconfig.ReportingDefaults{Enabled: true, QueueIntervalMs: 60000}}).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	original, err := runtime.Reporting.SaveReport(ctx, &reporting.SaveReportRequest{ReportID: "source", Title: "Source", Definition: completePersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Source"}}`))})
	require.NoError(t, err)
	edited := &reporting.DuplicateReportRequest{ResolvedResource: original.Resource, Title: "Personal", IdempotencyKey: "stable-edit", Definition: completePersonalFixture(t, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Personal"}}`))}
	logical := &reporting.DuplicateReportRequest{Resource: &identity.ResourceRef{URI: original.ArtifactRef}, Title: "Logical", IdempotencyKey: "stable-logical"}
	exact := &reporting.DuplicateReportRequest{ResolvedResource: original.Resource, Title: "Exact", IdempotencyKey: "stable-exact"}
	editedLogical := *logical
	editedLogical.Definition = edited.Definition
	editedLogical.IdempotencyKey = "stable-logical-edit"
	requests := []*reporting.DuplicateReportRequest{edited, logical, exact, &editedLogical}
	saved := make([]*reporting.SharedArtifact, len(requests))
	for i, request := range requests {
		saved[i], err = runtime.Reporting.DuplicateReport(ctx, request)
		require.NoError(t, err)
	}
	legacy := *exact
	legacy.Title = "Saved through create"
	legacy.IdempotencyKey = "legacy-create"
	legacy.Definition = original.ResourceDefinition
	first, err := runtime.Reporting.DuplicateReport(ctx, &legacy)
	require.NoError(t, err)
	legacy.Definition = nil
	recovered, err := runtime.Reporting.DuplicateReport(ctx, &legacy)
	require.NoError(t, err, "recognize a prior unchanged copy saved through create")
	require.Equal(t, first.ArtifactRef, recovered.ArtifactRef)
	for _, mismatch := range []string{"source", "fingerprint"} {
		bad := *exact
		pin := *original.Resource
		if mismatch == "source" {
			pin.ProviderIdentity = "other-provider"
		} else {
			pin.ContentFingerprint = strings.Repeat("a", 64)
		}
		bad.ResolvedResource = &pin
		_, err = runtime.Reporting.DuplicateReport(ctx, &bad)
		require.Error(t, err, "a different %s cannot claim this receipt", mismatch)
		bad.IdempotencyKey = "bad-" + mismatch
		_, err = runtime.Reporting.DuplicateReport(ctx, &bad)
		require.Error(t, err, "first write must verify %s", mismatch)
	}
	// Both selectors recover the previously admitted copy if the mutable
	// source has changed. Their result is independent of its new contents.
	updatedTitle := "Updated original"
	updated, err := runtime.Reporting.UpdateReport(ctx, &reporting.UpdateReportRequest{Resource: &identity.ResourceRef{URI: original.ArtifactRef}, Title: updatedTitle, ExpectedContentFingerprint: original.Resource.ContentFingerprint})
	require.NoError(t, err)
	require.NotEqual(t, original.Resource.ContentFingerprint, updated.Resource.ContentFingerprint)
	for i, request := range requests {
		replay, err := runtime.Reporting.DuplicateReport(ctx, request)
		require.NoError(t, err)
		require.Equal(t, saved[i].Resource.ContentFingerprint, replay.Resource.ContentFingerprint)
	}
	_, err = runtime.Reporting.DeleteReport(ctx, &reporting.DeleteReportRequest{ReportID: original.ArtifactRef})
	require.NoError(t, err)
	for i, request := range requests {
		replay, err := runtime.Reporting.DuplicateReport(ctx, request)
		require.NoError(t, err, "recover the saved copy after its original is removed")
		require.Equal(t, saved[i].ArtifactRef, replay.ArtifactRef)
		require.Equal(t, saved[i].Resource.ContentFingerprint, replay.Resource.ContentFingerprint)
		changed := *request
		changed.Title = "Different request"
		_, err = runtime.Reporting.DuplicateReport(ctx, &changed)
		require.ErrorIs(t, err, identity.ErrResourceStale, "reusing the operation key must not accept a different request")
		changed = *request
		changed.IdempotencyKey += "-new"
		_, err = runtime.Reporting.DuplicateReport(ctx, &changed)
		require.Error(t, err, "new writes still require the authorized original")
	}
}
