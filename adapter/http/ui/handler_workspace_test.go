package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/agently-core/workspace"
)

func TestWindowHandler_MergesWorkspaceForgeAssets(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	prevRoot := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	mustWriteWorkspaceUIFile(t, filepath.Join(metaRoot, "window", "chat", "new", "main.yaml"), `
namespace: Chat
dialogs:
  - id: settings
    title: Settings
dataSource:
  meta: {}
view:
  content:
    containers: []
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "dialogs", "workItemPicker.yaml"), `
id: workItemPicker
title: Select Ad Order
dataSourceRef: work_item_lookup
content:
  id: workItemPickerContent
  dataSourceRef: work_item_lookup
  containers: []
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "datasources", "work_item_lookup.yaml"), `
cardinality: collection
parameters: []
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/chat/new")
	if err != nil {
		t.Fatalf("window request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Dialogs []struct {
				Id string `json:"id"`
			} `json:"dialogs"`
			DataSource map[string]map[string]any `json:"dataSource"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.Status != "ok" {
		t.Fatalf("expected ok status, got %q", payload.Status)
	}

	dialogs := map[string]bool{}
	for _, dialog := range payload.Data.Dialogs {
		dialogs[dialog.Id] = true
	}
	if !dialogs["settings"] || !dialogs["workItemPicker"] {
		t.Fatalf("expected built-in and workspace dialogs, got %#v", payload.Data.Dialogs)
	}
	if _, ok := payload.Data.DataSource["meta"]; !ok {
		t.Fatalf("expected built-in meta datasource")
	}
	if _, ok := payload.Data.DataSource["work_item_lookup"]; !ok {
		t.Fatalf("expected merged workspace datasource")
	}
}

func TestWindowHandler_LoadsWorkspaceOwnedForgeWindowWhenStaticWindowIsAbsent(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	prevRoot := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "order.yaml"), `
id: order
title: Details
windowKey: order
namespace: Details
view:
  content:
    id: orderRoot
    containers:
      - id: summary
        title: Summary
        items:
          - id: selectedSpend
            label: Spend
            type: label
            scope: metrics
            dataSourceRef: order_performance_period_today
            dataField: periodSummary.spend
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "datasources", "order_performance_period_today.yaml"), `
id: order_performance_period_today
cardinality: collection
parameters:
  - name: order_id
    in: windowForm
    location: RecordId.0
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/order")
	if err != nil {
		t.Fatalf("window request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Namespace string `json:"namespace"`
			View      struct {
				Content struct {
					ID string `json:"id"`
				} `json:"content"`
			} `json:"view"`
			DataSource map[string]map[string]interface{} `json:"dataSource"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.Status != "ok" {
		t.Fatalf("expected ok status, got %q", payload.Status)
	}
	if payload.Data.Namespace != "Details" {
		t.Fatalf("expected workspace namespace, got %q", payload.Data.Namespace)
	}
	if payload.Data.View.Content.ID != "orderRoot" {
		t.Fatalf("expected workspace content id, got %#v", payload.Data.View.Content.ID)
	}
	if _, ok := payload.Data.DataSource["order_performance_period_today"]; !ok {
		t.Fatalf("expected merged workspace datasource")
	}
}

func TestWindowHandler_WorkspaceForgeWindowOverridesStaticWindow(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	prevRoot := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	mustWriteWorkspaceUIFile(t, filepath.Join(metaRoot, "window", "order", "main.yaml"), `
namespace: Static Order
view:
  content:
    id: staticRoot
    containers: []
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "order", "web", "main.yaml"), `
$import(shared/web/main.yaml)
`)
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "order", "shared", "web", "main.yaml"), `
namespace: Workspace Order
view:
  content:
    id: workspaceRoot
    containers:
      - id: summaryRail
        layout:
          kind: grid
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/order?platform=web&formFactor=desktop")
	if err != nil {
		t.Fatalf("window request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Namespace string `json:"namespace"`
			View      struct {
				Content struct {
					ID         string `json:"id"`
					Containers []struct {
						ID     string `json:"id"`
						Layout struct {
							Kind string `json:"kind"`
						} `json:"layout"`
					} `json:"containers"`
				} `json:"content"`
			} `json:"view"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.Data.Namespace != "Workspace Order" {
		t.Fatalf("expected workspace namespace, got %q", payload.Data.Namespace)
	}
	if payload.Data.View.Content.ID != "workspaceRoot" {
		t.Fatalf("expected workspace content id, got %q", payload.Data.View.Content.ID)
	}
	if len(payload.Data.View.Content.Containers) != 1 || payload.Data.View.Content.Containers[0].Layout.Kind != "grid" {
		t.Fatalf("expected workspace grid summary container, got %#v", payload.Data.View.Content.Containers)
	}
}

func TestWindowHandler_WorkspaceForgeWindowBranchErrorsDoNotFallBack(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	prevRoot := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	mustWriteWorkspaceUIFile(t, filepath.Join(metaRoot, "window", "order", "main.yaml"), `
namespace: Static Order
view:
  content:
    id: staticRoot
    containers: []
`)
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "order.yaml"), `
namespace: Legacy Workspace Order
view:
  content:
    id: legacyRoot
    containers: []
`)
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "order", "web", "main.yaml"), `
$import(shared/web/missing.yaml)
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/order?platform=web&formFactor=desktop")
	if err != nil {
		t.Fatalf("window request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected branch load failure, got %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "missing.yaml") {
		t.Fatalf("expected missing import in response, got %s", string(body))
	}
}

func TestWindowHandler_WorkspaceForgeWindowNoTargetUsesSharedDefault(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	prevRoot := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	mustWriteWorkspaceUIFile(t, filepath.Join(metaRoot, "window", "order", "main.yaml"), `
namespace: Static Order
view:
  content:
    id: staticRoot
    containers: []
`)
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "order", "shared", "main.yaml"), `
$import(shared/web/main.yaml)
`)
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "order", "shared", "web", "main.yaml"), `
namespace: Workspace Web Default Order
view:
  content:
    id: webDefaultRoot
    containers:
      - id: summaryRail
        layout:
          kind: grid
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/order")
	if err != nil {
		t.Fatalf("window request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Data struct {
			Namespace string `json:"namespace"`
			View      struct {
				Content struct {
					ID         string `json:"id"`
					Containers []struct {
						Layout struct {
							Kind string `json:"kind"`
						} `json:"layout"`
					} `json:"containers"`
				} `json:"content"`
			} `json:"view"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Data.Namespace != "Workspace Web Default Order" {
		t.Fatalf("expected shared web default namespace, got %q", payload.Data.Namespace)
	}
	if payload.Data.View.Content.ID != "webDefaultRoot" {
		t.Fatalf("expected shared web default content, got %q", payload.Data.View.Content.ID)
	}
	if len(payload.Data.View.Content.Containers) != 1 || payload.Data.View.Content.Containers[0].Layout.Kind != "grid" {
		t.Fatalf("expected shared web grid default, got %#v", payload.Data.View.Content.Containers)
	}
}

func TestWindowHandler_LoadsWorkspaceOwnedForgeWindowWithImportedSharedContent(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	prevRoot := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "reportBuilder.yaml"), `
id: reportBuilder
title: Metric Report Builder
windowKey: reportBuilder
namespace: Metric Report Builder
view:
  content:
    $import('../../../shared/metric_report_builder.yaml')
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "shared", "metric_report_builder.yaml"), `
kind: dashboard.reportBuilder
id: metricsCubeBuilder
title: Metric Report Builder
dataSourceRef: metrics_ad_cube_report
reportBuilder:
  measures:
    - id: totalSpend
      key: totalSpend
      label: Spend
      paramPath: measures.totalSpend
  dimensions:
    - id: eventDate
      key: eventDate
      label: Date
      chartAxis: true
      default: true
      paramPath: dimensions.eventDate
  result:
    defaultMode: chart
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "datasources", "metrics_ad_cube_report.yaml"), `
id: metrics_ad_cube_report
cardinality: collection
autoFetch: false
backend:
  kind: mcp_tool
  service: workspace
  method: MetricsCube
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/reportBuilder")
	if err != nil {
		t.Fatalf("window request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Namespace string `json:"namespace"`
			View      struct {
				Content struct {
					ID            string `json:"id"`
					Kind          string `json:"kind"`
					DataSourceRef string `json:"dataSourceRef"`
					Dashboard     struct {
						ReportBuilder map[string]interface{} `json:"reportBuilder"`
					} `json:"dashboard"`
				} `json:"content"`
			} `json:"view"`
			DataSource map[string]map[string]interface{} `json:"dataSource"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.Status != "ok" {
		t.Fatalf("expected ok status, got %q", payload.Status)
	}
	if payload.Data.Namespace != "Metric Report Builder" {
		t.Fatalf("expected workspace namespace, got %q", payload.Data.Namespace)
	}
	if payload.Data.View.Content.ID != "metricsCubeBuilder" {
		t.Fatalf("expected imported content id, got %q", payload.Data.View.Content.ID)
	}
	if payload.Data.View.Content.Kind != "dashboard.reportBuilder" {
		t.Fatalf("expected imported report builder kind, got %q", payload.Data.View.Content.Kind)
	}
	if payload.Data.View.Content.DataSourceRef != "metrics_ad_cube_report" {
		t.Fatalf("expected imported datasource ref, got %q", payload.Data.View.Content.DataSourceRef)
	}
	if payload.Data.View.Content.Dashboard.ReportBuilder == nil {
		t.Fatalf("expected imported reportBuilder config")
	}
	measures, ok := payload.Data.View.Content.Dashboard.ReportBuilder["measures"].([]interface{})
	if !ok || len(measures) == 0 {
		t.Fatalf("expected reportBuilder measures, got %#v", payload.Data.View.Content.Dashboard.ReportBuilder["measures"])
	}
	if _, ok := payload.Data.DataSource["metrics_ad_cube_report"]; !ok {
		t.Fatalf("expected merged workspace datasource")
	}
	if payload.Data.DataSource["metrics_ad_cube_report"]["autoFetch"] != false {
		t.Fatalf("expected datasource autoFetch=false, got %#v", payload.Data.DataSource["metrics_ad_cube_report"]["autoFetch"])
	}
}

func TestWindowHandler_LoadsWorkspaceOwnedForgeForecastingBuilderWithImportedSharedContent(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	prevRoot := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "forecastingCubeBuilder.yaml"), `
id: forecastingCubeBuilder
title: Forecasting Cube
windowKey: forecastingCubeBuilder
namespace: Forecasting Cube
view:
  content:
    $import('../../../shared/forecasting_report_builder.yaml')
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "shared", "forecasting_report_builder.yaml"), `
kind: dashboard.reportBuilder
id: forecastingCubeBuilder
title: Forecasting Cube
dataSourceRef: forecasting_cube_report
reportBuilder:
  drillMetadata:
    hierarchies:
      - id: forecast_inventory
        levels:
          - field: channelV2
            label: Channel
          - field: publisherId
            label: Publisher
          - field: siteType
            label: Site Type
      - id: forecast_location
        levels:
          - field: country
            label: Country
          - field: region
            label: Region
          - field: metrocode
            label: Metro Code
  hooks:
    initializeState: Forecasting Cube.workspaceForecastingBuilder.initializeState
    buildRequest: Forecasting Cube.workspaceForecastingBuilder.buildRequest
  measures:
    - id: avails
      key: avails
      label: Avails
  dimensions:
    - id: eventDate
      key: eventDate
      label: Date
      chartAxis: true
      default: true
  result:
    defaultMode: chart
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "datasources", "forecasting_cube_report.yaml"), `
id: forecasting_cube_report
cardinality: collection
autoFetch: false
backend:
  kind: mcp_tool
  service: workspace
  method: ForecastCube
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/forecastingCubeBuilder")
	if err != nil {
		t.Fatalf("window request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Namespace string `json:"namespace"`
			View      struct {
				Content struct {
					ID            string `json:"id"`
					Kind          string `json:"kind"`
					DataSourceRef string `json:"dataSourceRef"`
					Dashboard     struct {
						ReportBuilder map[string]interface{} `json:"reportBuilder"`
					} `json:"dashboard"`
				} `json:"content"`
			} `json:"view"`
			DataSource map[string]map[string]interface{} `json:"dataSource"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.Status != "ok" {
		t.Fatalf("expected ok status, got %q", payload.Status)
	}
	if payload.Data.Namespace != "Forecasting Cube" {
		t.Fatalf("expected workspace namespace, got %q", payload.Data.Namespace)
	}
	if payload.Data.View.Content.ID != "forecastingCubeBuilder" {
		t.Fatalf("expected imported content id, got %q", payload.Data.View.Content.ID)
	}
	if payload.Data.View.Content.Kind != "dashboard.reportBuilder" {
		t.Fatalf("expected imported report builder kind, got %q", payload.Data.View.Content.Kind)
	}
	if payload.Data.View.Content.DataSourceRef != "forecasting_cube_report" {
		t.Fatalf("expected imported datasource ref, got %q", payload.Data.View.Content.DataSourceRef)
	}
	if payload.Data.View.Content.Dashboard.ReportBuilder == nil {
		t.Fatalf("expected imported reportBuilder config")
	}
	drillMetadata, ok := payload.Data.View.Content.Dashboard.ReportBuilder["drillMetadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected reportBuilder drillMetadata, got %#v", payload.Data.View.Content.Dashboard.ReportBuilder["drillMetadata"])
	}
	hierarchies, ok := drillMetadata["hierarchies"].([]interface{})
	if !ok || len(hierarchies) != 2 {
		t.Fatalf("expected 2 drill hierarchies, got %#v", drillMetadata["hierarchies"])
	}
	inventoryHierarchy, ok := hierarchies[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected inventory hierarchy object, got %#v", hierarchies[0])
	}
	if inventoryHierarchy["id"] != "forecast_inventory" {
		t.Fatalf("expected forecast_inventory hierarchy id, got %#v", inventoryHierarchy["id"])
	}
	inventoryLevels, ok := inventoryHierarchy["levels"].([]interface{})
	if !ok || len(inventoryLevels) != 3 {
		t.Fatalf("expected 3 inventory drill levels, got %#v", inventoryHierarchy["levels"])
	}
	inventoryFields := make([]string, 0, len(inventoryLevels))
	for _, level := range inventoryLevels {
		levelMap, ok := level.(map[string]interface{})
		if !ok {
			t.Fatalf("expected inventory level object, got %#v", level)
		}
		field, ok := levelMap["field"].(string)
		if !ok {
			t.Fatalf("expected inventory level field string, got %#v", levelMap["field"])
		}
		inventoryFields = append(inventoryFields, field)
	}
	expectedInventoryFields := []string{"channelV2", "publisherId", "siteType"}
	if len(inventoryFields) != len(expectedInventoryFields) {
		t.Fatalf("expected inventory drill fields %v, got %v", expectedInventoryFields, inventoryFields)
	}
	for index, expected := range expectedInventoryFields {
		if inventoryFields[index] != expected {
			t.Fatalf("expected inventory drill field %d to be %q, got %q", index, expected, inventoryFields[index])
		}
	}
	locationHierarchy, ok := hierarchies[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected location hierarchy object, got %#v", hierarchies[1])
	}
	if locationHierarchy["id"] != "forecast_location" {
		t.Fatalf("expected forecast_location hierarchy id, got %#v", locationHierarchy["id"])
	}
	locationLevels, ok := locationHierarchy["levels"].([]interface{})
	if !ok || len(locationLevels) != 3 {
		t.Fatalf("expected 3 location drill levels, got %#v", locationHierarchy["levels"])
	}
	locationFields := make([]string, 0, len(locationLevels))
	for _, level := range locationLevels {
		levelMap, ok := level.(map[string]interface{})
		if !ok {
			t.Fatalf("expected location level object, got %#v", level)
		}
		field, ok := levelMap["field"].(string)
		if !ok {
			t.Fatalf("expected location level field string, got %#v", levelMap["field"])
		}
		locationFields = append(locationFields, field)
	}
	expectedLocationFields := []string{"country", "region", "metrocode"}
	if len(locationFields) != len(expectedLocationFields) {
		t.Fatalf("expected location drill fields %v, got %v", expectedLocationFields, locationFields)
	}
	for index, expected := range expectedLocationFields {
		if locationFields[index] != expected {
			t.Fatalf("expected location drill field %d to be %q, got %q", index, expected, locationFields[index])
		}
	}
	if _, ok := payload.Data.DataSource["forecasting_cube_report"]; !ok {
		t.Fatalf("expected merged forecasting datasource")
	}
	if payload.Data.DataSource["forecasting_cube_report"]["autoFetch"] != false {
		t.Fatalf("expected forecasting datasource autoFetch=false, got %#v", payload.Data.DataSource["forecasting_cube_report"]["autoFetch"])
	}
}

func TestWindowHandler_LoadsWorkspaceOwnedForgeWindowCompanionJS(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	prevRoot := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "reportBuilder", "main.yaml"), `
id: reportBuilder
title: Metric Report Builder
windowKey: reportBuilder
namespace: Metric Report Builder
view:
  content:
    id: reportBuilderRoot
    kind: dashboard.reportBuilder
    dataSourceRef: metrics_ad_cube_report
    dashboard:
      reportBuilder:
        hooks:
          buildRequest: workspaceReportBuilder.buildRequest
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "reportBuilder", "main.js"), `
(() => ({
  workspaceReportBuilder: {
    buildRequest({ request }) {
      return {
        ...request,
        filters: {
          ...(request.filters || {}),
          hello: "world",
        },
      };
    },
  },
}))()
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/reportBuilder")
	if err != nil {
		t.Fatalf("window request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Namespace string `json:"namespace"`
			View      struct {
				Content struct {
					ID string `json:"id"`
				} `json:"content"`
			} `json:"view"`
			Actions *struct {
				Code string `json:"code"`
			} `json:"actions"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.Status != "ok" {
		t.Fatalf("expected ok status, got %q", payload.Status)
	}
	if payload.Data.Namespace != "Metric Report Builder" {
		t.Fatalf("expected workspace namespace, got %q", payload.Data.Namespace)
	}
	if payload.Data.View.Content.ID != "reportBuilderRoot" {
		t.Fatalf("expected workspace content id, got %q", payload.Data.View.Content.ID)
	}
	if payload.Data.Actions == nil || payload.Data.Actions.Code == "" {
		t.Fatalf("expected companion js code to load, got %#v", payload.Data.Actions)
	}
	if !strings.Contains(payload.Data.Actions.Code, "hello: \"world\"") {
		t.Fatalf("expected companion js code in actions, got %q", payload.Data.Actions.Code)
	}
}

func mustWriteWorkspaceUIFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
}
