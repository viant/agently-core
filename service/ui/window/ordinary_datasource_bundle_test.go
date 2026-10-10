package window

import (
	"context"
	"encoding/json"
	"errors"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/forge/backend/types"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOrdinaryDatasourceExternalAndInlineAssociationComposeSameClosedDescriptor(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, workspace.KindForgeWindow, "ordinary")
	mustWriteLoaderFile(t, filepath.Join(base, "main.yaml"), "windowKey: ordinary\nresources:\n  dataSources: [localRows]\nview:\n  content: {id: list, dataSourceRef: localRows}\n")
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, "localRows.yaml"), "id: localRows\ncardinality: collection\nselectors: {data: reply.widgets, dataInfo: nav}\nparameters: [{name: term, default: authored}]\npaging: {enabled: true, mode: offset, size: 2, parameters: {page: skipRows, size: takeRows}}\nbackend: {kind: mcp_tool, service: arbitrary, method: offset}\ncache: {enabled: false}\n")
	binding := ResourceBinding{WindowKey: "ordinary", URI: "window://generic/list"}
	source, err := NewWorkspaceResourceSource(root, []ResourceBinding{binding}, nil)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := identity.ParseResourceURI(binding.URI)
	if err != nil {
		t.Fatal(err)
	}
	external, err := source.LoadBundleWithOptions(context.Background(), uri, LoaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	variant, err := types.SelectWindowResource(external, nil)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor dsproto.DataSource
	if json.Unmarshal(variant.DataSources["localRows"], &descriptor) != nil {
		t.Fatal("external authored descriptor missing")
	}
	embedded := variant.Window
	composed, err := LoadComposedWindowBundleWithOptionsAt(context.Background(), root, binding, func(context.Context, *types.WindowTarget) (*types.Window, error) {
		raw, _ := json.Marshal(embedded)
		var copy types.Window
		json.Unmarshal(raw, &copy)
		return &copy, nil
	}, nil, LoaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	inline, err := types.SelectWindowResource(composed, nil)
	if err != nil {
		t.Fatal(err)
	}
	var inlineDescriptor dsproto.DataSource
	if json.Unmarshal(inline.DataSources["localRows"], &inlineDescriptor) != nil {
		t.Fatal("inline descriptor missing")
	}
	if !reflect.DeepEqual(descriptor, inlineDescriptor) {
		t.Fatalf("delivery changed effective datasource: external=%#v inline=%#v", descriptor, inlineDescriptor)
	}
	if descriptor.Backend.Method != "offset" || descriptor.Selectors.Data != "reply.widgets" || descriptor.Paging.Mode != "offset" || variant.Window.ResourceDependencies["localRows"] == "" {
		t.Fatalf("authored associations lost: %#v", descriptor)
	}
}

func TestOrdinaryDatasourceBundlePreservesLegacyLoadButMissingFetchedBackendFailsClosed(t *testing.T) {
	root := t.TempDir()
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "ordinary", "main.yaml"), "windowKey: ordinary\nresources: {dataSources: [missing]}\nview:\n  content: {id: list, dataSourceRef: missing}\n")
	source, err := NewWorkspaceResourceSource(root, []ResourceBinding{{WindowKey: "ordinary", URI: "window://generic/list"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	uri, _ := identity.ParseResourceURI("window://generic/list")
	raw, err := source.LoadBundleWithOptions(context.Background(), uri, LoaderOptions{})
	if err != nil {
		t.Fatalf("delivery must preserve existing form/service/optional assignment loading: %v", err)
	}
	variant, err := types.SelectWindowResource(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := variant.DataSources["missing"]; present {
		t.Fatal("unprovided declaration must not acquire another datasource descriptor")
	}
	store := dssvc.NewMemoryStore()
	store.Put(&dsproto.DataSource{ID: "missing", Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "other", Method: "must-not-call"}})
	resolved := false
	service := dssvc.New(dssvc.Options{Store: store, DisableCache: true, ResolveDefinition: func(_ context.Context, _ identity.ResolvedResource, target *types.WindowTarget, id string) (*dsproto.DataSource, error) {
		resolved = true
		selected, err := types.SelectWindowResource(raw, target)
		if err != nil {
			return nil, err
		}
		if selected.DataSources[id] == nil {
			return nil, identity.ErrResourceDenied
		}
		return nil, errors.New("unexpected supplied descriptor")
	}})
	pin := identity.ResolvedResource{URI: uri.String()}
	if _, err = service.Fetch(requestctx.WithResolvedResource(context.Background(), pin), "missing", nil, dssvc.FetchOptions{Target: &types.WindowTarget{}}); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("missing fetched producer must fail closed inside its parent bundle: %v", err)
	}
	if !resolved {
		t.Fatal("missing producer was not resolved inside the closed bundle")
	}
}
