package window

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/afs"
	"github.com/viant/agently-core/workspace"
	wsmeta "github.com/viant/agently-core/workspace/service/meta"
	metaSvc "github.com/viant/forge/backend/service/meta"
	forgeTypes "github.com/viant/forge/backend/types"
)

func TestLoadWorkspaceWindowAppliesRegisteredEnricher(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "reportBuilder.yaml"), `
namespace: Reports
view:
  content:
    id: reportBuilder
    kind: dashboard.reportBuilder
`)
		calls := 0
		cleanup := SetWorkspaceWindowEnricher(func(_ context.Context, window *forgeTypes.Window) error {
			calls++
			window.View.Content.Title = "Discovered report"
			return nil
		})
		defer cleanup()

		got, err := LoadWorkspaceWindow(context.Background(), "reportBuilder", nil)
		if err != nil {
			t.Fatalf("load workspace window: %v", err)
		}
		if calls != 1 || got == nil || got.View.Content == nil || got.View.Content.Title != "Discovered report" {
			t.Fatalf("expected registered enricher to update window once, calls=%d window=%#v", calls, got)
		}
	})
}

func TestLoadWorkspaceWindowLoadsDatasourceFromDomainSubfolderByExplicitID(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "advertiser.yaml"), `
namespace: Advertiser
resources:
  dataSources: [advertiser_lookup]
view:
  content:
    id: advertiser
    kind: dashboard
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, "advertiser", "lookup.yaml"), `
id: advertiser_lookup
title: Advertiser Lookup
cardinality: collection
backend:
  kind: inline
  rows: []
`)
		datasourcePaths, err := wsmeta.New(afs.New(), root).ListRecursive(context.Background(), workspace.KindForgeDataSource)
		if err != nil || len(datasourcePaths) != 1 {
			t.Fatalf("recursive datasource discovery: paths=%v err=%v", datasourcePaths, err)
		}

		got, err := LoadWorkspaceWindow(context.Background(), "advertiser", nil)
		if err != nil {
			t.Fatalf("load workspace window: %v", err)
		}
		dataSource, ok := got.DataSource["advertiser_lookup"]
		if !ok {
			t.Fatalf("expected explicit datasource id, got keys %#v", got.DataSource)
		}
		if dataSource.Service == nil || dataSource.Service.URI != "/v1/api/datasources/advertiser_lookup/fetch" {
			t.Fatalf("unexpected datasource service: %#v", dataSource.Service)
		}
	})
}

func TestLoadWorkspaceWindowValidatesResourceModelsAfterDatasourceMerge(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "record.yaml"), `
schemas:
  record: {type: object, identity: [id], properties: {id: {type: integer}, name: {type: string}}}
resourceModels:
  record:
    schemaRef: record
    read: {dataSourceRef: record_read}
    write: {dataSourceRef: record_patch, inputPath: Records, collection: true}
    fields: {id: {write: Id}, name: {write: Name}}
dataSource:
  inline_status: {cardinality: object}
view:
  content:
    id: record
    mutationCommand:
      dataSourceRef: record_patch
      payload: {modelRef: record, source: {scope: form, dataSourceRef: record_read}}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, "record", "read.yaml"), "id: record_read\ncardinality: collection\nbackend: {kind: inline, rows: []}\n")
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, "record", "patch.yaml"), "id: record_patch\ncardinality: object\nbackend: {kind: inline, rows: []}\n")
		got, err := LoadWorkspaceWindow(context.Background(), "record", nil)
		if err != nil {
			t.Fatalf("load resource-model window: %v", err)
		}
		if got.ResourceModels["record"].Read.DataSourceRef != "record_read" || got.ResourceModels["record"].Write.DataSourceRef != "record_patch" || got.DataSource["record_read"].ResourceModelRef != "" {
			t.Fatalf("resource model or datasources were truncated: %#v", got.ResourceModels["record"])
		}
	})
}

func TestLoadWorkspaceWindowMergesGlobalModelsForWorkspaceDialogs(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "line.yaml"), `
namespace: Line
resources:
  dialogs: [campaignFlightDelete]
view:
  content: {id: line}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeModel, "campaign.yaml"), `
schemas:
  flightDelete: {type: object, required: [campaignId], properties: {campaignId: {type: integer}}}
resourceModels:
  flightDelete:
    schemaRef: flightDelete
    write: {dataSourceRef: flight_delete, inputPath: Data, mode: full}
    fields: {campaignId: {write: CampaignId}}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "campaignFlightDelete.yaml"), `
id: campaignFlightDelete
content:
  id: deleteCommand
  mutationCommand:
    dataSourceRef: flight_delete
    payload: {modelRef: flightDelete, source: {scope: extras, selector: data}, mode: full}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, "campaign", "flight_delete.yaml"), "id: flight_delete\ncardinality: object\nbackend: {kind: inline, rows: []}\n")

		got, err := LoadWorkspaceWindow(context.Background(), "line", nil)
		if err != nil {
			t.Fatalf("load line window with global Campaign dialog model: %v", err)
		}
		if got.ResourceModels["flightDelete"].Write.DataSourceRef != "flight_delete" || got.Schemas["flightDelete"].Type != "object" {
			t.Fatalf("global model registry was not merged: %#v", got.ResourceModels)
		}
	})
}

func TestLoadWorkspaceWindowSkipsInvalidUnrelatedGlobalAssets(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "advertiser.yaml"), `
namespace: Advertiser
view:
  content: {id: advertiser}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "brokenOrder.yaml"), `
id: brokenOrder
content:
  id: broken
  items:
    - {id: warning, value: Is this broken? yes}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "unknownModel.yaml"), `
id: unknownModel
content:
  id: command
  mutationCommand:
    dataSourceRef: missing_patch
    payload: {modelRef: missingModel, source: {scope: extras, selector: data}}
`)

		got, err := LoadWorkspaceWindow(context.Background(), "advertiser", nil)
		if err != nil {
			t.Fatalf("unrelated global asset must not fail requested window: %v", err)
		}
		if got == nil || got.View.Content == nil || got.View.Content.ID != "advertiser" {
			t.Fatalf("requested window did not survive unrelated asset failures: %#v", got)
		}
		for _, dialog := range got.Dialogs {
			if dialog.Id == "brokenOrder" || dialog.Id == "unknownModel" {
				t.Fatalf("invalid global dialog was not quarantined: %s", dialog.Id)
			}
		}
	})
}

func TestLoadWorkspaceWindowValidatesGlobalModelsLazilyByAffectedWindow(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "order.yaml"), `
namespace: Order
view:
  content: {id: order}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "advertiser.yaml"), `
namespace: Advertiser
resources:
  dataSources: [advertiser_patch]
  resourceModels: [advertiserMutation]
view:
  content:
    id: advertiser
    mutationCommand:
      dataSourceRef: advertiser_patch
      payload: {modelRef: advertiserMutation, source: {scope: form}}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeModel, "advertiser.yaml"), `
schemas:
  advertiserMutation:
    type: object
    properties:
      flights: {type: array}
  advertiserFlight:
    type: object
    properties: {startDate: {type: string}}
resourceModels:
  advertiserMutation:
    schemaRef: advertiserMutation
    write: {dataSourceRef: advertiser_patch, inputPath: Data, mode: full}
    fields:
      flights: {write: Flights, collection: {modelRef: advertiserFlight, mode: replace}}
  advertiserFlight:
    schemaRef: advertiserFlight
    fields: {startDate: {write: StartDate}}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, "advertiser", "patch.yaml"), "id: advertiser_patch\ncardinality: object\nbackend: {kind: inline, rows: []}\n")

		order, err := LoadWorkspaceWindow(context.Background(), "order", nil)
		if err != nil {
			t.Fatalf("invalid Advertiser registry must not block Order: %v", err)
		}
		if _, ok := order.ResourceModels["advertiserMutation"]; ok {
			t.Fatal("invalid unrelated registry must be quarantined from Order")
		}

		_, err = LoadWorkspaceWindow(context.Background(), "advertiser", nil)
		if err == nil || !strings.Contains(err.Error(), "unknown resource model advertiserMutation") {
			t.Fatalf("Advertiser must fail closed when it references its quarantined model, got %v", err)
		}
	})
}

func TestLoadWorkspaceWindowQuarantinesUnrelatedGlobalIdentityConflicts(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "advertiser.yaml"), `
namespace: Advertiser
view:
  content: {id: advertiser}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, "order", "first.yaml"), "id: shared_order_lookup\ncardinality: collection\nbackend: {kind: inline, rows: []}\n")
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, "line", "second.yaml"), "id: shared_order_lookup\ncardinality: collection\nbackend: {kind: inline, rows: []}\n")
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeModel, "order.yaml"), `
schemas:
  sharedOrder: {type: object, properties: {id: {type: integer}}}
resourceModels:
  sharedOrder: {schemaRef: sharedOrder, read: {dataSourceRef: shared_order_lookup}}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeModel, "line.yaml"), `
schemas:
  sharedOrder: {type: object, properties: {id: {type: string}}}
resourceModels:
  sharedOrder: {schemaRef: sharedOrder, read: {dataSourceRef: another_lookup}}
`)

		got, err := LoadWorkspaceWindow(context.Background(), "advertiser", nil)
		if err != nil {
			t.Fatalf("unrelated duplicate global identities must not fail requested window: %v", err)
		}
		if _, ok := got.DataSource["shared_order_lookup"]; ok {
			t.Fatal("ambiguous datasource identity must be quarantined")
		}
		if _, ok := got.Schemas["sharedOrder"]; ok {
			t.Fatal("ambiguous schema identity must be quarantined")
		}
		if _, ok := got.ResourceModels["sharedOrder"]; ok {
			t.Fatal("ambiguous resource model identity must be quarantined")
		}
	})
}

func TestLoadWorkspaceWindowPreservesImportedTargetOverrides(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "reportBuilder.yaml"), `
$import(reportBuilder/shared/main.yaml)
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "reportBuilder", "shared", "main.yaml"), `
namespace: Performance Metrics
presentation: hosted
region: chat.top
view:
  content:
    $import(content.yaml)
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "reportBuilder", "shared", "content.yaml"), `
kind: dashboard.reportBuilder
id: reportBuilderContent
reportBuilder:
  $import('../../shared/report_builder.yaml:reportBuilder')
targetOverrides:
  mobile:
    reportBuilder:
      filterPresentation: drawer-left
  tablet:
    reportBuilder:
      filterPresentation: rail-left
  android:
    reportBuilder:
      touchDensity: roomy
  phone:
    reportBuilder:
      filterPresentation: drawer-left
      unifiedFamilyRows: true
  android:phone:
    reportBuilder:
      bottomSheetFilters: true
  iosTablet:
    reportBuilder:
      filterSummaryMode: pinned
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "shared", "report_builder.yaml"), `
reportBuilder:
  filterPresentation: rail-left
  resultMode: table
  export:
    enabled: true
ignored:
  resultMode: chart
`)

		got, err := LoadWorkspaceWindow(context.Background(), "reportBuilder", nil)
		if err != nil {
			t.Fatalf("load workspace window: %v", err)
		}
		if got == nil || got.View.Content == nil {
			t.Fatalf("expected imported workspace window content, got %#v", got)
		}
		if got.Namespace != "Performance Metrics" || got.Presentation != "hosted" || got.Region != "chat.top" {
			t.Fatalf("unexpected loaded window shell: %#v", got)
		}
		content := got.View.Content
		if content.Kind != "dashboard.reportBuilder" {
			t.Fatalf("expected imported report-builder content, got %q", content.Kind)
		}
		if content.Dashboard == nil || content.Dashboard.ReportBuilder == nil {
			t.Fatalf("expected reportBuilder compact alias to decode, got %#v", content)
		}
		if content.Dashboard.ReportBuilder["filterPresentation"] != "rail-left" {
			t.Fatalf("expected keyed reportBuilder import, got %#v", content.Dashboard.ReportBuilder)
		}
		if content.Dashboard.ReportBuilder["resultMode"] != "table" {
			t.Fatalf("expected keyed reportBuilder import to ignore sibling keys, got %#v", content.Dashboard.ReportBuilder)
		}
		export, ok := content.Dashboard.ReportBuilder["export"].(map[string]interface{})
		if !ok || export["enabled"] != true {
			t.Fatalf("expected nested reportBuilder object import, got %#v", content.Dashboard.ReportBuilder["export"])
		}
		if content.TargetOverrides["mobile"]["reportBuilder"].(map[string]interface{})["filterPresentation"] != "drawer-left" {
			t.Fatalf("expected mobile target override to survive load, got %#v", content.TargetOverrides)
		}
		if content.TargetOverrides["tablet"]["reportBuilder"].(map[string]interface{})["filterPresentation"] != "rail-left" {
			t.Fatalf("expected tablet target override to survive load, got %#v", content.TargetOverrides)
		}
		if content.TargetOverrides["phone"]["reportBuilder"].(map[string]interface{})["unifiedFamilyRows"] != true {
			t.Fatalf("expected phone target override to survive load, got %#v", content.TargetOverrides)
		}
		if content.TargetOverrides["android"]["reportBuilder"].(map[string]interface{})["touchDensity"] != "roomy" {
			t.Fatalf("expected android target override to survive load, got %#v", content.TargetOverrides)
		}
		if content.TargetOverrides["android:phone"]["reportBuilder"].(map[string]interface{})["bottomSheetFilters"] != true {
			t.Fatalf("expected android:phone target override to survive load, got %#v", content.TargetOverrides)
		}
		if content.TargetOverrides["iosTablet"]["reportBuilder"].(map[string]interface{})["filterSummaryMode"] != "pinned" {
			t.Fatalf("expected iosTablet target override to survive load, got %#v", content.TargetOverrides)
		}
	})
}

func TestLoadWorkspaceWindowAppliesTargetContextToFolderizedImports(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		base := filepath.Join(root, workspace.KindForgeWindow, "order")
		mustWriteLoaderFile(t, filepath.Join(base, "shared", "main.yaml"), `
namespace: Order
presentation: hosted
region: chat.top
view:
  content:
    $import(content.yaml)
`)
		mustWriteLoaderFile(t, filepath.Join(base, "shared", "content.yaml"), `
kind: dashboard.reportBuilder
id: shared-default
title: Shared default
`)
		mustWriteLoaderFile(t, filepath.Join(base, "mobile", "phone", "main.yaml"), `
$import('../../shared/main.yaml')
`)
		mustWriteLoaderFile(t, filepath.Join(base, "mobile", "phone", "content.yaml"), `
kind: dashboard.reportBuilder
id: mobile-phone
title: Mobile phone
`)
		mustWriteLoaderFile(t, filepath.Join(base, "content.yaml"), `
kind: dashboard.reportBuilder
id: legacy-root
title: Legacy root
`)

		got, err := LoadWorkspaceWindow(context.Background(), "order", &metaSvc.TargetContext{
			Platform:   "ios",
			FormFactor: "phone",
			Surface:    "app",
		})
		if err != nil {
			t.Fatalf("load workspace window: %v", err)
		}
		if got == nil || got.View.Content == nil {
			t.Fatalf("expected target-aware workspace window content, got %#v", got)
		}
		if got.Namespace != "Order" || got.Presentation != "hosted" || got.Region != "chat.top" {
			t.Fatalf("unexpected loaded window shell: %#v", got)
		}
		if got.View.Content.ID != "mobile-phone" {
			t.Fatalf("expected phone target content import to win, got %#v", got.View.Content)
		}
		if got.View.Content.Title != "Mobile phone" {
			t.Fatalf("expected phone target content title, got %#v", got.View.Content)
		}
	})
}

func TestLoadWorkspaceWindowPrefersExactPlatformFormFactorBranch(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		base := filepath.Join(root, workspace.KindForgeWindow, "metricReportBuilder")
		mustWriteLoaderFile(t, filepath.Join(base, "shared", "main.yaml"), `
namespace: Performance Metrics
presentation: hosted
region: chat.top
view:
  content:
    $import(content.yaml)
`)
		mustWriteLoaderFile(t, filepath.Join(base, "shared", "content.yaml"), `
kind: dashboard.reportBuilder
id: shared-default
title: Shared report builder
`)
		mustWriteLoaderFile(t, filepath.Join(base, "mobile", "phone", "main.yaml"), `
$import('../../shared/main.yaml')
`)
		mustWriteLoaderFile(t, filepath.Join(base, "mobile", "phone", "content.yaml"), `
kind: dashboard.reportBuilder
id: mobile-phone
title: Mobile phone fallback
`)
		mustWriteLoaderFile(t, filepath.Join(base, "android", "phone", "main.yaml"), `
$import('../../shared/main.yaml')
`)
		mustWriteLoaderFile(t, filepath.Join(base, "android", "phone", "content.yaml"), `
kind: dashboard.reportBuilder
id: android-phone
title: Android phone
`)
		mustWriteLoaderFile(t, filepath.Join(base, "android", "main.yaml"), `
$import('../shared/main.yaml')
`)
		mustWriteLoaderFile(t, filepath.Join(base, "android", "content.yaml"), `
kind: dashboard.reportBuilder
id: android-platform
title: Android platform
`)

		got, err := LoadWorkspaceWindow(context.Background(), "metricReportBuilder", &metaSvc.TargetContext{
			Platform:   "android",
			FormFactor: "phone",
			Surface:    "app",
		})
		if err != nil {
			t.Fatalf("load workspace window: %v", err)
		}
		if got == nil || got.View.Content == nil {
			t.Fatalf("expected target-aware workspace window content, got %#v", got)
		}
		if got.View.Content.ID != "android-phone" {
			t.Fatalf("expected exact android/phone content to win over mobile/phone and android fallbacks, got %#v", got.View.Content)
		}
		if got.View.Content.Title != "Android phone" {
			t.Fatalf("expected exact android/phone target title, got %#v", got.View.Content)
		}
	})
}

func TestLoadWorkspaceWindowLoadsRootSiblingActionCodeForFolderizedWindow(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		base := filepath.Join(root, workspace.KindForgeWindow, "metricReportBuilder")
		mustWriteLoaderFile(t, filepath.Join(base, "shared", "main.yaml"), `
namespace: Performance Metrics
presentation: hosted
region: chat.top
view:
  content:
    id: metricsCubeBuilder
    kind: dashboard.reportBuilder
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "metricReportBuilder.js"), `
(() => ({
  stewardReportBuilder: {
    buildRequest() {
      return {};
    },
  },
}))()
`)

		got, err := LoadWorkspaceWindow(context.Background(), "metricReportBuilder", &metaSvc.TargetContext{
			Platform:   "web",
			FormFactor: "desktop",
			Surface:    "app",
		})
		if err != nil {
			t.Fatalf("load workspace window: %v", err)
		}
		if got == nil {
			t.Fatalf("expected workspace window")
		}
		if strings.TrimSpace(got.Actions.Code) == "" {
			t.Fatalf("expected root sibling action code to load for folderized workspace window")
		}
	})
}

func TestLoadWorkspaceWindowLoadsRootSiblingActionCodeForFolderizedWindowInBrowserSurface(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		base := filepath.Join(root, workspace.KindForgeWindow, "metricReportBuilder")
		mustWriteLoaderFile(t, filepath.Join(base, "shared", "main.yaml"), `
namespace: Performance Metrics
presentation: hosted
region: chat.top
view:
  content:
    id: metricsCubeBuilder
    kind: dashboard.reportBuilder
`)
		mustWriteLoaderFile(t, filepath.Join(base, "web", "main.yaml"), `
$import('../shared/main.yaml')
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "metricReportBuilder.js"), `
(() => ({
  stewardReportBuilder: {
    buildRequest() {
      return {};
    },
  },
}))()
`)

		got, err := LoadWorkspaceWindow(context.Background(), "metricReportBuilder", &metaSvc.TargetContext{
			Platform:   "web",
			FormFactor: "desktop",
			Surface:    "browser",
		})
		if err != nil {
			t.Fatalf("load workspace window: %v", err)
		}
		if got == nil {
			t.Fatalf("expected workspace window")
		}
		if strings.TrimSpace(got.Actions.Code) == "" {
			t.Fatalf("expected root sibling action code to load for browser-targeted folderized workspace window")
		}
	})
}

func TestLoadWorkspaceWindowMergesDeclaredActionRefs(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		base := filepath.Join(root, workspace.KindForgeWindow, "reportBuilder")
		mustWriteLoaderFile(t, filepath.Join(base, "shared", "main.yaml"), `
namespace: Reports
presentation: hosted
region: chat.top
actionRefs: [performanceHooks, forecastingHooks]
view:
  content:
    id: reportBuilder
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "performanceHooks.js"), `(() => ({ performance: { buildRequest() { return {}; } } }))()`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "forecastingHooks.js"), `(() => ({ forecasting: { buildRequest() { return {}; } } }))()`)

		got, err := LoadWorkspaceWindow(context.Background(), "reportBuilder", &metaSvc.TargetContext{
			Platform:   "web",
			FormFactor: "desktop",
			Surface:    "browser",
		})
		if err != nil {
			t.Fatalf("load workspace window: %v", err)
		}
		if got == nil || got.Actions == nil {
			t.Fatalf("expected merged action code, got %#v", got)
		}
		if !strings.Contains(got.Actions.Code, "performance") || !strings.Contains(got.Actions.Code, "forecasting") {
			t.Fatalf("expected both action refs in merged code, got %q", got.Actions.Code)
		}
		if got.View.Content.Dashboard != nil {
			t.Fatalf("ordinary window action refs must not introduce dashboard metadata: %#v", got.View.Content.Dashboard)
		}
	})
}

func withLoaderWorkspaceRoot(t *testing.T, body func(root string)) {
	t.Helper()
	prev := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	t.Cleanup(func() {
		workspace.SetRoot(prev)
	})
	body(root)
}

func mustWriteLoaderFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
