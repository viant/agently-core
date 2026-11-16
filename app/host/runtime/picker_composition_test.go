package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/viant/afs"
	"github.com/viant/agently-core/app/host/runtime/internal/testmetadata"
	"github.com/viant/agently-core/internal/testutil/catalogfixture"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	windowloader "github.com/viant/agently-core/service/ui/window"
	wsmeta "github.com/viant/agently-core/workspace/service/meta"
	"github.com/viant/forge/backend/types"
	"os"
	"path/filepath"
	"testing"
)

// The linked shell and authored lookup compose through the ordinary native host.
func TestChatComposesDeclaredLookupWithoutChangingItsProvider(t *testing.T) {
	root := catalogfixture.New(t)
	binding := windowloader.ResourceBinding{WindowKey: "chat/new", CatalogID: "chat/new", URI: "window://agently/chat-new"}
	ctx := context.Background()
	uri, _ := identity.ParseResourceURI(binding.URI)
	linked, err := testEmbeddedWindowSource([]windowloader.ResourceBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	profile := &types.WindowTarget{Platform: "web", FormFactor: "desktop"}
	raw, err := linked.loadTarget(ctx, uri, profile)
	if err != nil {
		t.Fatal(err)
	}
	var expected types.Window
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	for _, d := range expected.Dialogs {
		if d.Id == "chooseDocument" {
			t.Fatal("linked fixture unexpectedly supplies private picker")
		}
	}
	if err = windowloader.MergeWorkspaceForgeAssetsAt(ctx, root, &expected, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := newInternalWindowSnapshot(ctx, root, "", &hostAuthorizationFile{linkedMetadata: &testmetadata.FS, FrameworkWindowResources: []windowloader.ResourceBinding{binding}})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.workspace == nil || len(snapshot.framework) != 0 {
		t.Fatal("workspace-composed shell escaped native source guard")
	}
	candidates, err := snapshot.Candidates(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	composed, err := snapshot.ReadCandidate(ctx, uri, candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	selected, err := types.SelectWindowResource(composed, profile)
	if err != nil {
		t.Fatal(err)
	}
	expectedDialogs, _ := json.Marshal(expected.Dialogs)
	actualDialogs, _ := json.Marshal(selected.Window.Dialogs)
	if !bytes.Equal(expectedDialogs, actualDialogs) {
		t.Fatal("canonical chat changed authored picker dialog definitions")
	}

	for _, id := range []string{"document_lookup"} {
		a, _ := json.Marshal(expected.DataSource[id])
		b, _ := json.Marshal(selected.Window.DataSource[id])
		if !bytes.Equal(a, b) {
			t.Fatalf("original datasource changed: %s", id)
		}
		if len(selected.DataSources[id]) == 0 || selected.Window.ResourceDependencies[id] != identity.ContentFingerprint(selected.DataSources[id]) {
			t.Fatalf("missing exact datasource closure/pin: %s", id)
		}
	}
	for id, path := range map[string]string{"document_lookup": "extension/forge/datasources/document_lookup.yaml"} {
		var original, canonical dsproto.DataSource
		if err := wsmeta.New(afs.New(), root).Load(ctx, path, &original); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(selected.DataSources[id], &canonical); err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(original.Backend)
		b, _ := json.Marshal(canonical.Backend)
		if original.Backend == nil || !bytes.Equal(a, b) {
			t.Fatalf("original protected backend changed: %s", id)
		}
	}
	// The ordinary native configuration uses the same original composed closure.
	native, err := newNativeWindowHostSnapshot(ctx, root, NativeWindowHostConfig{linkedMetadata: &testmetadata.FS, Windows: []windowloader.ResourceBinding{{WindowKey: "documents", URI: "window://tenant/documents"}}, FrameworkWindows: []windowloader.ResourceBinding{binding}, ExecutionBindings: []NativeWindowExecutionBinding{{URI: binding.URI, DataSource: "ad_order_lookup", IdentitySelectors: []string{"Objects.*.Id"}}}})
	if err != nil {
		t.Fatal(err)
	}
	nativeCandidates, err := native.Candidates(ctx, uri)
	if err != nil || len(nativeCandidates) != 1 {
		t.Fatal("native framework candidate unavailable", err)
	}
	nativeRaw, err := native.ReadCandidate(ctx, uri, nativeCandidates[0])
	if err != nil {
		t.Fatal(err)
	}
	nativeVariant, err := types.SelectWindowResource(nativeRaw, profile)
	if err != nil {
		t.Fatal(err)
	}
	nativeDialogs, _ := json.Marshal(nativeVariant.Window.Dialogs)
	if !bytes.Equal(expectedDialogs, nativeDialogs) {
		t.Fatal("native configuration changed authored picker dialogs")
	}
	for _, id := range []string{"document_lookup"} {
		if !bytes.Equal(nativeVariant.DataSources[id], selected.DataSources[id]) {
			t.Fatalf("native configuration changed original descriptor: %s", id)
		}
	}
	seen := map[string]bool{}
	for _, d := range selected.Window.Dialogs {
		seen[d.Id] = true
	}
	if !seen["chooseDocument"] {
		t.Fatal("authored pickers absent")
	}
	// Modify only this test-owned copy after capture. Original candidate and all
	// its dependencies become stale; the host does not rematerialize at request time.
	dialog := filepath.Join(root, "extension/forge/dialogs/chooseDocument.yaml")
	body, err := os.ReadFile(dialog)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(dialog, bytes.Replace(body, []byte("Choose a document"), []byte("Browse catalog"), 1), 0600); err != nil {
		t.Fatal(err)
	}
	if err = snapshot.workspace.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	prepared := snapshot.CompileCount()
	denied, err := snapshot.ReadCandidate(ctx, uri, candidates[0])
	if !errors.Is(err, identity.ErrResourceStale) || denied != nil {
		t.Fatalf("changed authored picker must deny: %v", err)
	}
	if snapshot.CompileCount() != prepared {
		t.Fatal("snapshot recompiled changed source")
	}
}
