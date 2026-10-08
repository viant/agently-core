package resources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aug "github.com/viant/agently-core/service/augmenter"
	"github.com/viant/agently-core/workspace"
)

func newRawBoundaryFixture(t *testing.T) (*Service, string) {
	t.Helper()
	previous := workspace.Root()
	root, err := os.MkdirTemp("", "resources-canonical-boundary-")
	if err != nil {
		t.Fatal(err)
	}
	workspace.SetRoot(root)
	t.Cleanup(func() {
		workspace.SetRoot(previous)
		_ = os.RemoveAll(root)
	})
	files := map[string]string{
		"README.md": "ROOT_DESCRIPTION_SECRET_CANONICAL\n",
		"extension/forge/reporting/reports/sales.json": `{"secret":"CANONICAL_REPORT"}`,
		"extension/forge/windows/orders.yaml":          "secret: CANONICAL_WINDOW\n",
		"intent/forecast.yaml":                         "secret: CANONICAL_INTENT\n",
		"skills/finance/SKILL.md":                      "# CANONICAL_SKILL\n",
		"extension/forge/lookups/countries.yaml":       "kind: lookup\n",
		"agents/alice/skills/external/SKILL.md":        "# Agent-owned skill\n",
		"docs/readme.md":                               "ordinary workspace document\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "extension/forge/reporting"), filepath.Join(root, "docs/reporting-link")); err != nil {
		t.Fatal(err)
	}
	service := New(dummyAugmenter(t), WithRawResourceBoundary(workspace.NewRawResourceBoundary(root)))
	return service, root
}

func TestRootsSkipProtectedRootsAndNeverDescribeBroadProtectedParents(t *testing.T) {
	service, _ := newRawBoundaryFixture(t)
	service.defaults.Locations = []string{"workspace://localhost/"}
	var broad RootsOutput
	if err := service.roots(context.Background(), &RootsInput{}, &broad); err != nil {
		t.Fatal(err)
	}
	if len(broad.Roots) != 1 || strings.Contains(broad.Roots[0].Description, "ROOT_DESCRIPTION_SECRET_CANONICAL") {
		t.Fatalf("broad workspace root exposed a summary read from workspace files: %+v", broad.Roots)
	}
	service.defaults.Locations = []string{"workspace://localhost/extension/forge/reporting"}
	var protected RootsOutput
	if err := service.roots(context.Background(), &RootsInput{}, &protected); err != nil {
		t.Fatal(err)
	}
	if len(protected.Roots) != 0 {
		t.Fatalf("protected raw root was discoverable: %+v", protected.Roots)
	}
}

func TestRawCanonicalResourceBoundaryBlocksEveryFileReadSurface(t *testing.T) {
	service, root := newRawBoundaryFixture(t)
	ctx := context.Background()
	protected := "workspace://localhost/extension/forge/reporting/reports/sales.json"
	for _, request := range []*ReadInput{
		{URI: protected},
		{RootURI: "workspace://localhost/", Path: "extension/forge/reporting/reports/sales.json"},
		{RootID: "workspace://localhost/", Path: "extension/forge/reporting/reports/sales.json"},
		{URI: "workspace://localhost/docs/reporting-link/reports/sales.json"},
		{URI: filepath.Join(root, "extension/forge/reporting/reports/sales.json")},
		{RootURI: "workspace://localhost/", Path: "extension/forge/windows/orders.yaml"},
		{URI: "workspace://localhost/intent/forecast.yaml"},
		{URI: "workspace://localhost/skills/finance/SKILL.md"},
	} {
		if err := service.read(ctx, request, &ReadOutput{}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
			t.Fatalf("raw read bypassed canonical boundary for %+v: %v", request, err)
		}
	}
	if err := service.read(ctx, &ReadInput{URI: protected, Representation: "text"}, &ReadOutput{}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
		t.Fatalf("structured read bypassed canonical boundary: %v", err)
	}
	if err := service.inspect(ctx, &InspectInput{URI: protected}, &InspectOutput{}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
		t.Fatalf("inspect bypassed canonical boundary: %v", err)
	}
	if err := service.exportAsset(ctx, &ExportInput{URI: protected, Operation: "convert", Output: ExportFormat{Format: "json"}}, &ExportOutput{}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
		t.Fatalf("export bypassed canonical boundary: %v", err)
	}
	if err := service.readImage(ctx, &ReadImageInput{URI: protected}, &ReadImageOutput{}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
		t.Fatalf("image read bypassed canonical boundary: %v", err)
	}
	// Existing raw resources outside protected canonical directories remain readable.
	var allowed ReadOutput
	if err := service.read(ctx, &ReadInput{URI: "workspace://localhost/docs/readme.md"}, &allowed); err != nil || !strings.Contains(allowed.Content, "ordinary workspace") {
		t.Fatalf("ordinary resource was blocked: output=%+v err=%v root=%s", allowed, err, root)
	}
	// External skill repositories are not inside this workspace's canonical root.
	external := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(external, []byte("# External skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var skill ReadOutput
	if err := service.read(ctx, &ReadInput{URI: external}, &skill); err != nil || !strings.Contains(skill.Content, "External skill") {
		t.Fatalf("external skill resource was blocked: output=%+v err=%v", skill, err)
	}
}

func TestRawCanonicalBoundaryFiltersShallowListsAndRejectsRecursiveParentScans(t *testing.T) {
	service, _ := newRawBoundaryFixture(t)
	ctx := context.Background()
	var shallow ListOutput
	if err := service.list(ctx, &ListInput{RootURI: "workspace://localhost/extension/forge"}, &shallow); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, item := range shallow.Items {
		names = append(names, item.Name)
		if strings.EqualFold(item.Name, "windows") || strings.EqualFold(item.Name, "reporting") {
			t.Fatalf("shallow list exposed protected directory: %+v", shallow.Items)
		}
	}
	if !containsPath(names, "lookups") {
		t.Fatalf("allowed sibling disappeared from shallow list: %+v", shallow.Items)
	}
	for _, request := range []*ListInput{
		{RootURI: "workspace://localhost/extension/forge/reporting"},
		{RootID: "workspace://localhost/extension/forge/reporting"},
		{RootURI: "workspace://localhost/extension/forge", Recursive: true},
		{RootID: "workspace://localhost/extension/forge", Recursive: true},
		{URI: "workspace://localhost/extension/forge/windows/orders.yaml", Recursive: true},
	} {
		if err := service.list(ctx, request, &ListOutput{}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
			t.Fatalf("list scan bypassed canonical boundary for %+v: %v", request, err)
		}
	}
	if err := service.grepFiles(ctx, &GrepInput{RootURI: "workspace://localhost/extension/forge", Path: ".", Pattern: "CANONICAL", Recursive: true}, &GrepOutput{}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
		t.Fatalf("recursive grep bypassed canonical boundary: %v", err)
	}
	if err := service.grepFiles(ctx, &GrepInput{RootURI: "workspace://localhost/", Path: "extension/forge/reporting/reports/sales.json", Pattern: "CANONICAL"}, &GrepOutput{}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
		t.Fatalf("direct grep bypassed canonical boundary: %v", err)
	}
}

func TestRawCanonicalBoundaryStopsSemanticIndexingButLeavesAgentRootsAvailable(t *testing.T) {
	root := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	for _, path := range []string{"extension/forge/reporting/reports", "agents/alice"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	service := New(dummyAugmenter(t), WithRawResourceBoundary(workspace.NewRawResourceBoundary(root)), WithDefaultEmbedder("fixture"))
	calls := 0
	service.augmentDocsOverride = func(_ context.Context, _ *aug.AugmentDocsInput, output *aug.AugmentDocsOutput) error {
		calls++
		output.Documents = nil
		return nil
	}
	service.defaults.Locations = []string{"workspace://localhost/"}
	if err := service.match(context.Background(), &MatchInput{Query: "report", Model: "fixture"}, &MatchOutput{}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) || calls != 0 {
		t.Fatalf("semantic index read a canonical parent: calls=%d err=%v", calls, err)
	}
	service.defaults.Locations = []string{"workspace://localhost/agents/alice"}
	if err := service.matchDocuments(context.Background(), &MatchDocumentsInput{Query: "agent skill", RootIDs: []string{"workspace://localhost/agents/alice"}, Model: "fixture"}, &MatchDocumentsOutput{}); err != nil || calls != 1 {
		t.Fatalf("explicit agent root was blocked: calls=%d err=%v", calls, err)
	}
}

func containsPath(paths []string, expected string) bool {
	for _, path := range paths {
		if path == expected {
			return true
		}
	}
	return false
}
