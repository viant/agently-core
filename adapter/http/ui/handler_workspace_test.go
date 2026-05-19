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
title: Order Summary
windowKey: order
namespace: Order Summary
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
    location: AdOrderId.0
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
	if payload.Data.Namespace != "Order Summary" {
		t.Fatalf("expected workspace namespace, got %q", payload.Data.Namespace)
	}
	if payload.Data.View.Content.ID != "orderRoot" {
		t.Fatalf("expected workspace content id, got %#v", payload.Data.View.Content.ID)
	}
	if _, ok := payload.Data.DataSource["order_performance_period_today"]; !ok {
		t.Fatalf("expected merged workspace datasource")
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

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "metricReportBuilder.yaml"), `
id: metricReportBuilder
title: Metric Report Builder
windowKey: metricReportBuilder
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
  service: steward
  method: MetricsAdCube
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/metricReportBuilder")
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
  hooks:
    initializeState: Forecasting Cube.stewardForecastingBuilder.initializeState
    buildRequest: Forecasting Cube.stewardForecastingBuilder.buildRequest
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
  service: steward
  method: ForecastingCube
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
	if _, ok := payload.Data.DataSource["forecasting_cube_report"]; !ok {
		t.Fatalf("expected merged forecasting datasource")
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

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "metricReportBuilder", "main.yaml"), `
id: metricReportBuilder
title: Metric Report Builder
windowKey: metricReportBuilder
namespace: Metric Report Builder
view:
  content:
    id: metricReportBuilderRoot
    kind: dashboard.reportBuilder
    dataSourceRef: metrics_ad_cube_report
    dashboard:
      reportBuilder:
        hooks:
          buildRequest: stewardReportBuilder.buildRequest
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "metricReportBuilder", "main.js"), `
(() => ({
  stewardReportBuilder: {
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

	resp, err := http.Get(server.URL + "/window/metricReportBuilder")
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
	if payload.Data.View.Content.ID != "metricReportBuilderRoot" {
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
