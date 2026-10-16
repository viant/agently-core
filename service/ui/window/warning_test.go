package window

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	forgeTypes "github.com/viant/forge/backend/types"
	forgeSource "github.com/viant/forge/backend/window/source"
)

func TestMissingDataSourceWarningsAreOncePerWindowAndWorkspace(t *testing.T) {
	var output bytes.Buffer
	original := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(original)
	roots := []string{t.TempDir(), t.TempDir()}
	for _, root := range roots {
		for _, identity := range []string{"orders", "inventory"} {
			for i := 0; i < 3; i++ {
				window := &forgeTypes.Window{WindowKey: identity, View: forgeTypes.View{Content: &forgeTypes.Container{ID: identity, Binding: forgeTypes.Binding{DataSourceRef: "unavailable"}}}}
				if err := MergeWorkspaceForgeAssetsAt(context.Background(), root, window, &ResourceAssignment{DataSources: []string{"unavailable"}}); err != nil {
					t.Fatalf("missing datasource must still load: %v", err)
				}
				notes, err := validateWindowReferences(window, newWorkspaceCatalog(), root)
				if err != nil || len(notes) != 1 {
					t.Fatalf("diagnostic arrays must remain available: %v %v", notes, err)
				}

				resolver := newAssignmentResolver(window, newWorkspaceCatalog())
				resolver.workspaceRoot = root
				resolver.applyAssignment((&ResourceAssignment{DataSources: []string{"unavailable"}}).Normalized())
				if len(resolver.unresolved) != 1 {
					t.Fatalf("unresolved assignment diagnostics suppressed: %v", resolver.unresolved)
				}
			}
		}
	}
	if count := strings.Count(output.String(), "workspace forge: window"); count != 4 {
		t.Fatalf("want one warning per window/root, got %d: %s", count, output.String())
	}
}

func TestDynamicDialogAdvisoryIsOncePerWindow(t *testing.T) {
	var output bytes.Buffer
	original := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(original)
	root := t.TempDir()
	window := &forgeTypes.Window{WindowKey: "dynamic-dialogs"}
	for _, code := range []string{
		`({open:({context})=>context.handlers.window.openDialog({dialogId:context.kind})})`,
		`({open:({context})=>context.handlers.window.openDialog({dialogId:context.other}),second:({context})=>context.handlers.window.openDialog({dialogId:context.third})})`,
	} {
		window.SetCode([]byte(code))
		if _, err := validateWindowReferences(window, newWorkspaceCatalog(), root); err != nil {
			t.Fatal(err)
		}
	}
	if count := strings.Count(output.String(), "computes dialog ids dynamically"); count != 1 {
		t.Fatalf("want one dynamic advisory across code variants, got %d: %s", count, output.String())
	}
}

func TestLegacyAndForgeLoadersShareWarningHistory(t *testing.T) {
	var output bytes.Buffer
	original := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(original)
	root := t.TempDir()
	makeWindow := func() *forgeTypes.Window {
		return &forgeTypes.Window{WindowKey: "shared-loader", View: forgeTypes.View{Content: &forgeTypes.Container{ID: "shared", Binding: forgeTypes.Binding{DataSourceRef: "unavailable"}}}}
	}
	if err := forgeSource.MergeWorkspaceForgeAssetsAt(context.Background(), root, makeWindow(), nil); err != nil {
		t.Fatal(err)
	}
	if err := MergeWorkspaceForgeAssetsAt(context.Background(), root, makeWindow(), nil); err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(output.String(), "workspace forge: window"); count != 1 {
		t.Fatalf("shared loaders emitted %d warnings: %s", count, output.String())
	}
}
