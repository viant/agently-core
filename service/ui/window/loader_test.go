package window

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/agently-core/workspace"
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
view:
  content:
    id: reportBuilder
    kind: dashboard.reportBuilder
    reportBuilder:
      actionRefs: [performanceHooks, forecastingHooks]
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
