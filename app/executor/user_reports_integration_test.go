package executor_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/primitive"
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
	_, err = runtime.Registry.Execute(ctx, "reporting:update_report", map[string]any{"resource": map[string]any{"uri": saved.ArtifactRef}, "title": "Updated report"})
	require.NoError(t, err, "ordinary updates must not invent unsupported concurrency constraints")
	updated, err := runtime.Reporting.GetReport(ctx, &reporting.GetReportInput{ArtifactID: saved.ArtifactID})
	require.NoError(t, err)
	require.Equal(t, "Updated report", updated.Title)
	bob := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "bob@example.test"}), "canonical-bob"), "verified-workspace")
	_, err = runtime.Registry.Execute(bob, "reporting:get_report", map[string]any{"artifactId": saved.ArtifactID})
	require.Error(t, err, "the managing user MCP must deny another owner")
	_, err = runtime.Registry.Execute(ctx, "reporting:save_report", map[string]any{"resource": map[string]any{"uri": "report://platform/forbidden"}, "definition": map[string]any{"schemaVersion": 1, "reportDocument": map[string]any{"title": "Forbidden"}}})
	require.Error(t, err, "the managing user MCP must reject a non-user write target")
}
