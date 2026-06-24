package meta

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/viant/afs"
	"gopkg.in/yaml.v3"
)

func TestResolveImports_ResolvesNestedTopLevelScalarImports(t *testing.T) {
	root := t.TempDir()
	mustWriteImportFile(t, filepath.Join(root, "main.yaml"), "$import(shared/main.yaml)\n")
	mustWriteImportFile(t, filepath.Join(root, "shared", "main.yaml"), "$import(web/main.yaml)\n")
	mustWriteImportFile(t, filepath.Join(root, "shared", "web", "main.yaml"), "id: order\nwindowKey: order\n")

	data, err := os.ReadFile(filepath.Join(root, "main.yaml"))
	if err != nil {
		t.Fatalf("read root yaml: %v", err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		t.Fatalf("unmarshal root yaml: %v", err)
	}
	if err := ResolveImports(context.Background(), afs.New(), &node, root); err != nil {
		t.Fatalf("resolve imports: %v", err)
	}
	var actual struct {
		ID        string `yaml:"id"`
		WindowKey string `yaml:"windowKey"`
	}
	if err := node.Decode(&actual); err != nil {
		t.Fatalf("decode resolved yaml: %v", err)
	}
	if actual.ID != "order" || actual.WindowKey != "order" {
		t.Fatalf("unexpected resolved content: %#v", actual)
	}
}

func TestResolveImports_SelectsYAMLKeyAndResolvesNestedImports(t *testing.T) {
	root := t.TempDir()
	mustWriteImportFile(t, filepath.Join(root, "main.yaml"), `
reportBuilder:
  $import(shared/report_builder.yaml:reportBuilder)
`)
	mustWriteImportFile(t, filepath.Join(root, "shared", "report_builder.yaml"), `
reportBuilder:
  filterPresentation: rail-left
  title: $import(title.txt)
other:
  filterPresentation: ignored
`)
	mustWriteImportFile(t, filepath.Join(root, "shared", "title.txt"), "Performance Metrics")

	data, err := os.ReadFile(filepath.Join(root, "main.yaml"))
	if err != nil {
		t.Fatalf("read root yaml: %v", err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		t.Fatalf("unmarshal root yaml: %v", err)
	}
	if err := ResolveImports(context.Background(), afs.New(), &node, root); err != nil {
		t.Fatalf("resolve imports: %v", err)
	}
	var actual struct {
		ReportBuilder map[string]string `yaml:"reportBuilder"`
	}
	if err := node.Decode(&actual); err != nil {
		t.Fatalf("decode resolved yaml: %v", err)
	}
	if actual.ReportBuilder["filterPresentation"] != "rail-left" {
		t.Fatalf("expected keyed reportBuilder import, got %#v", actual.ReportBuilder)
	}
	if actual.ReportBuilder["title"] != "Performance Metrics" {
		t.Fatalf("expected nested import inside keyed node, got %#v", actual.ReportBuilder)
	}
}

func TestResolveImports_PreservesMobileTargetOverridesWithImportedBuilderContent(t *testing.T) {
	root := t.TempDir()
	mustWriteImportFile(t, filepath.Join(root, "windows", "reportBuilder.yaml"), "$import(reportBuilder/shared/main.yaml)\n")
	mustWriteImportFile(t, filepath.Join(root, "windows", "reportBuilder", "shared", "main.yaml"), `
id: reportBuilder
windowKey: reportBuilder
view:
  content:
    $import(content.yaml)
`)
	mustWriteImportFile(t, filepath.Join(root, "windows", "reportBuilder", "shared", "content.yaml"), `
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
	mustWriteImportFile(t, filepath.Join(root, "windows", "shared", "report_builder.yaml"), `
reportBuilder:
  filterPresentation: rail-left
  resultMode: table
  export:
    enabled: true
ignored:
  resultMode: chart
`)

	data, err := os.ReadFile(filepath.Join(root, "windows", "reportBuilder.yaml"))
	if err != nil {
		t.Fatalf("read root yaml: %v", err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		t.Fatalf("unmarshal root yaml: %v", err)
	}
	if err := ResolveImports(context.Background(), afs.New(), &node, filepath.Join(root, "windows")); err != nil {
		t.Fatalf("resolve imports: %v", err)
	}
	var actual struct {
		ID        string `yaml:"id"`
		WindowKey string `yaml:"windowKey"`
		View      struct {
			Content struct {
				Kind            string                 `yaml:"kind"`
				ReportBuilder   map[string]interface{} `yaml:"reportBuilder"`
				TargetOverrides map[string]struct {
					ReportBuilder map[string]interface{} `yaml:"reportBuilder"`
				} `yaml:"targetOverrides"`
			} `yaml:"content"`
		} `yaml:"view"`
	}
	if err := node.Decode(&actual); err != nil {
		t.Fatalf("decode resolved yaml: %v", err)
	}
	if actual.ID != "reportBuilder" || actual.WindowKey != "reportBuilder" {
		t.Fatalf("unexpected resolved window identity: %#v", actual)
	}
	if actual.View.Content.Kind != "dashboard.reportBuilder" {
		t.Fatalf("expected imported report-builder content, got %q", actual.View.Content.Kind)
	}
	if actual.View.Content.ReportBuilder["filterPresentation"] != "rail-left" {
		t.Fatalf("expected keyed reportBuilder import, got %#v", actual.View.Content.ReportBuilder)
	}
	if actual.View.Content.ReportBuilder["resultMode"] != "table" {
		t.Fatalf("expected selected reportBuilder key to ignore sibling keys, got %#v", actual.View.Content.ReportBuilder)
	}
	if export, ok := actual.View.Content.ReportBuilder["export"].(map[string]interface{}); !ok || export["enabled"] != true {
		t.Fatalf("expected nested reportBuilder object import, got %#v", actual.View.Content.ReportBuilder["export"])
	}
	if actual.View.Content.TargetOverrides["mobile"].ReportBuilder["filterPresentation"] != "drawer-left" {
		t.Fatalf("expected mobile target override to survive import, got %#v", actual.View.Content.TargetOverrides)
	}
	if actual.View.Content.TargetOverrides["tablet"].ReportBuilder["filterPresentation"] != "rail-left" {
		t.Fatalf("expected tablet target override to survive import, got %#v", actual.View.Content.TargetOverrides)
	}
	if actual.View.Content.TargetOverrides["phone"].ReportBuilder["unifiedFamilyRows"] != true {
		t.Fatalf("expected phone target override to survive import, got %#v", actual.View.Content.TargetOverrides)
	}
	if actual.View.Content.TargetOverrides["android"].ReportBuilder["touchDensity"] != "roomy" {
		t.Fatalf("expected android target override to survive import, got %#v", actual.View.Content.TargetOverrides)
	}
	if actual.View.Content.TargetOverrides["android:phone"].ReportBuilder["bottomSheetFilters"] != true {
		t.Fatalf("expected android:phone target override to survive import, got %#v", actual.View.Content.TargetOverrides)
	}
	if actual.View.Content.TargetOverrides["iosTablet"].ReportBuilder["filterSummaryMode"] != "pinned" {
		t.Fatalf("expected iosTablet target override to survive import, got %#v", actual.View.Content.TargetOverrides)
	}
}

func mustWriteImportFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
