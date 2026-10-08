package window

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/workspace"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
)

func TestWindowBundlePinsVariantScriptsAndInlineDatasourceContracts(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, workspace.KindForgeWindow, "sales")
	definition := func(id, defaultValue string) string {
		return "windowKey: sales\ndataSource:\n  items:\n    cardinality: collection\n    selectors: {data: data}\n    parameters:\n      - {from: windowForm, to: metrics, name: region, default: " + defaultValue + "}\nview:\n  content:\n    id: " + id + "\n    dataSourceRef: items\n    targetOverrides:\n      phone: {className: phone-only}\n"
	}
	mustWriteLoaderFile(t, filepath.Join(base, "main.yaml"), definition("desktopRoot", "desktop"))
	mustWriteLoaderFile(t, filepath.Join(base, "main.js"), "desktopAction()")
	mustWriteLoaderFile(t, filepath.Join(base, "mobile", "phone", "main.yaml"), definition("phoneRoot", "phone"))
	mustWriteLoaderFile(t, filepath.Join(base, "mobile", "phone", "main.js"), "phoneAction()")
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, "items.yaml"), "id: items\ncardinality: collection\nparameters:\n  - {name: region, from: windowForm, to: metrics, default: global}\nselectors: {data: data}\nbackend: {kind: inline, rows: [{value: trusted}]}\ncache: {enabled: false}\n")
	source, err := NewWorkspaceResourceSource(root, []ResourceBinding{{WindowKey: "sales", URI: "window://example/sales"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolver := identity.ResourceResolver{Source: source, Policy: workspaceRevisionPolicyFunc(func(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "verified-parent", ValidUntil: time.Now().Add(time.Minute)}, nil
	})}
	pin, err := resolver.Resolve(context.Background(), identity.ResourceRef{URI: "window://example/sales"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := resolver.ReadResolved(context.Background(), *pin)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		target                   *types.WindowTarget
		id, script, defaultValue string
	}{{nil, "desktopRoot", "desktopAction()", "desktop"}, {&types.WindowTarget{Platform: "ios", FormFactor: "phone", Surface: "app"}, "phoneRoot", "phoneAction()", "phone"}} {
		variant, err := types.SelectWindowResource(raw, item.target)
		if err != nil {
			t.Fatal(err)
		}
		var ds dsproto.DataSource
		if json.Unmarshal(variant.DataSources["items"], &ds) != nil {
			t.Fatal("typed descriptor missing")
		}
		if variant.Window.View.Content.ID != item.id || variant.Window.Actions == nil || variant.Window.Actions.Code != item.script || ds.Parameters[0].Default != item.defaultValue || ds.Backend == nil || ds.Backend.Kind != dsproto.BackendInline || variant.Window.View.Content.TargetOverrides["phone"] == nil {
			t.Fatalf("selected target contract lost: window=%s ds=%+v", variant.Window.View.Content.ID, ds)
		}
	}
	// A change to a phone branch invalidates the whole parent candidate, even
	// when a caller asks to retain its unchanged default presentation.
	mustWriteLoaderFile(t, filepath.Join(base, "mobile", "phone", "main.js"), "changedPhoneAction()")
	if _, _, err := resolver.ReadResolved(context.Background(), *pin); !errors.Is(err, identity.ErrResourceStale) {
		t.Fatalf("target branch drift did not invalidate parent: %v", err)
	}
}
