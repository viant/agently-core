package window

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/viant/afs"
	"github.com/viant/agently-core/workspace"
	forgeHandlers "github.com/viant/forge/backend/handlers"
	metaSvc "github.com/viant/forge/backend/service/meta"
)

// Regression coverage against the real Steward deployment metadata and the
// real Agently Agent window. Both checkouts are optional: set
// AGENTLY_STEWARD_WORKSPACE (…/steward_ai/deployment/steward) and
// AGENTLY_METADATA_ROOT (…/agently/metadata) or keep the default sibling
// layout under $GOPATH/src/github.com; the tests skip when either is missing.
func stewardWorkspaceRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("AGENTLY_STEWARD_WORKSPACE"); root != "" {
		return root
	}
	root := filepath.Join(goPathSrc(), "github.com", "viant-internal", "steward_ai", "deployment", "steward")
	if _, err := os.Stat(filepath.Join(root, workspace.KindForgeWindow)); err != nil {
		t.Skipf("steward workspace not available at %s", root)
	}
	return root
}

func agentlyMetadataRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("AGENTLY_METADATA_ROOT"); root != "" {
		return root
	}
	root := filepath.Join(goPathSrc(), "github.com", "viant", "agently", "metadata")
	if _, err := os.Stat(filepath.Join(root, "window", "agent")); err != nil {
		t.Skipf("agently metadata not available at %s", root)
	}
	return root
}

func goPathSrc() string {
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return filepath.Join(gopath, "src")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "go", "src")
}

func withStewardWorkspace(t *testing.T) {
	t.Helper()
	root := stewardWorkspaceRoot(t)
	prev := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(prev) })
}

var stewardTargets = map[string]*metaSvc.TargetContext{
	"default": nil,
	"web":     {Platform: "web", Surface: "browser"},
	"phone":   {Platform: "ios", FormFactor: "phone", Surface: "app"},
	"tablet":  {Platform: "ios", FormFactor: "tablet", Surface: "app"},
	"android": {Platform: "android", FormFactor: "phone", Surface: "app"},
}

func TestStewardAgentWindowExcludesStewardAssets(t *testing.T) {
	withStewardWorkspace(t)
	metadataRoot := agentlyMetadataRoot(t)
	windowRoot := "file://" + filepath.ToSlash(filepath.Join(metadataRoot, "window"))
	loader := metaSvc.New(afs.New(), windowRoot)
	for name, target := range stewardTargets {
		t.Run(name, func(t *testing.T) {
			agent, err := forgeHandlers.LoadWindow(context.Background(), loader, windowRoot, "agent", "", target)
			if err != nil {
				t.Skipf("agent window unavailable for target %s: %v", name, err)
			}
			ownDataSources := sortedDataSourceKeys(agent)
			assignment, err := LoadResourceAssignment(context.Background(), loader, windowRoot, "agent", "", target)
			if err != nil {
				t.Fatalf("load agent assignment: %v", err)
			}
			if err := MergeWorkspaceForgeAssets(context.Background(), agent, assignment); err != nil {
				t.Fatalf("merge agent window against steward workspace: %v", err)
			}
			if got := sortedDataSourceKeys(agent); len(got) != len(ownDataSources) {
				t.Fatalf("agent window gained workspace datasources: before=%v after=%v", ownDataSources, got)
			}
			for _, id := range []string{"advertiser_properties", "campaign_properties", "order_properties", "recommendation_list", "resource_authorization"} {
				if _, leaked := agent.DataSource[id]; leaked {
					t.Fatalf("steward datasource %s leaked into agent window", id)
				}
			}
			for _, dialog := range agent.Dialogs {
				for _, steward := range []string{"advertiserCampaignCreate", "adOrderPicker", "campaignFlightDraft", "orderLineDraft", "targetingTreePicker"} {
					if dialog.Id == steward {
						t.Fatalf("steward dialog %s leaked into agent window", steward)
					}
				}
			}
			if len(agent.Schemas) != 0 || len(agent.ResourceModels) != 0 {
				t.Fatalf("steward resource models leaked into agent window: schemas=%d models=%d", len(agent.Schemas), len(agent.ResourceModels))
			}
		})
	}
}

func TestStewardWindowsLoadWithExplicitAssignments(t *testing.T) {
	withStewardWorkspace(t)
	expectations := map[string]struct {
		dataSources []string
		dialogs     []string
		excluded    []string // dialogs that belong to other windows
	}{
		"spoReportBuilder": {dataSources: []string{"spo_parent_org_report", "spo_client_deal_publisher_split_report", "spo_iris_coverage_report"}, excluded: []string{"advertiserCreateDraft", "orderLineDraft"}},
		"advertiser":       {dataSources: []string{"advertiser_properties", "resource_authorization", "advertiser_custom_rates"}, dialogs: []string{"advertiserCampaignCreate", "advertiserLegacyPixelArchive", "advertiserRetargetingAudienceSource", "advertiserConversionPixelDraft"}, excluded: []string{"lineHistoryFilters", "campaignArchiveConfirm"}},
		"advertiserList":   {dataSources: []string{"advertiser_list_performance", "advertiser_starred_list", "advertiser_watch_patch"}, dialogs: []string{"advertiserCreateDraft"}, excluded: []string{"advertiserCampaignCreate", "orderLineDraft"}},
		"campaign":         {dataSources: []string{"campaign_properties", "campaign_watch_add", "campaign_bid_browser_values"}, dialogs: []string{"campaignCreativeDraft", "campaignBidMultiplierDraft", "campaignBidMultiplierRemoveDraft", "associatedOrders"}, excluded: []string{"advertiserCreateDraft", "lineHistoryFilters"}},
		"campaignList":     {dataSources: []string{"campaign_list_watch_add"}, dialogs: []string{"advertiserCampaignCreate"}, excluded: []string{"orderLineDraft", "lineHistoryFilters"}},
		"order":            {dataSources: []string{"order_line_draft", "order_owned_flight_draft", "order_advanced_third_party_fee_draft"}, dialogs: []string{"orderLineDraft", "orderBidMultiplierDraft", "orderBidMultiplierRemoveConfirm", "associatedOrders"}, excluded: []string{"advertiserCreateDraft", "lineHistoryFilters"}},
		"line":             {dataSources: []string{"line_performance_period_today", "line_forecast_detail_overview", "line_history_user_values"}, dialogs: []string{"lineHistoryFilters", "lineCreativeDetail", "lineRecommendationDetail"}, excluded: []string{"advertiserCreateDraft", "campaignArchiveConfirm"}},
		"recommendation":   {dataSources: []string{"recommendation_list", "recommendation_status"}, excluded: []string{"advertiserCreateDraft", "orderLineDraft", "targetingTreePicker"}},
		"reportBuilder":    {dataSources: []string{"forecasting_cube_report", "metrics_ad_cube_report"}, dialogs: []string{"targetingTreePicker"}, excluded: []string{"advertiserCreateDraft", "orderLineDraft"}},
	}
	windows, err := filepath.Glob(filepath.Join(workspace.Root(), workspace.KindForgeWindow, "*.yaml"))
	if err != nil || len(windows) == 0 {
		t.Fatalf("no steward windows found: %v", err)
	}
	for _, path := range windows {
		key := filepath.Base(path[:len(path)-len(".yaml")])
		for name, target := range stewardTargets {
			t.Run(key+"/"+name, func(t *testing.T) {
				got, err := LoadWorkspaceWindow(context.Background(), key, target)
				if err != nil {
					t.Fatalf("steward window %s must load with its explicit assignment: %v", key, err)
				}
				if got == nil {
					t.Fatalf("steward window %s resolved to nil", key)
				}
				for id := range CollectWindowReferences(got).YAML.DataSources {
					if _, attached := got.DataSource[id]; !attached {
						t.Errorf("window %s references unattached datasource %s", key, id)
					}
				}
				if key == "advertiserList" {
					t.Logf("advertiserList/%s has %d assigned datasources", name, len(got.DataSource))
					if len(got.DataSource) > 12 {
						t.Fatalf("advertiser list received unrelated workspace datasources: %d", len(got.DataSource))
					}
				}
				expected, ok := expectations[key]
				if !ok {
					return
				}
				for _, id := range expected.dataSources {
					if _, ok := got.DataSource[id]; !ok {
						t.Fatalf("window %s lost required datasource %s", key, id)
					}
				}
				for _, id := range expected.dialogs {
					if !containsDialog(got, id) {
						t.Fatalf("window %s lost required dialog %s; has %v", key, id, dialogIDs(got))
					}
				}
				for _, id := range expected.excluded {
					if containsDialog(got, id) {
						t.Fatalf("window %s must not receive unrelated dialog %s", key, id)
					}
				}
			})
		}
	}
}
