package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/forge/backend/types"
	"gopkg.in/yaml.v3"
)

// NativeLookupPickerDefinitions captures only dialogs referenced by authored
// lookup resources. Each picker is an ordinary window bundle containing the
// intact Dialog; its dependency closure uses the existing native window loader.
func NativeLookupPickerDefinitions(ctx context.Context, root string, lookups, windows []NativeSnapshotDefinition, enrich StaticWorkspaceWindowEnricher) ([]NativeSnapshotDefinition, error) {
	handle, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	reader := &ExtensionReader{root: handle}
	defer reader.close()
	needed := map[string]string{}
	for _, definition := range lookups {
		uri, e := identity.ParseResourceURI(definition.URI)
		if e != nil || uri.Kind != "lookup" {
			continue
		}
		raw, e := definition.Load(ctx, reader, definition.File)
		if e != nil {
			return nil, e
		}
		overlay, e := DecodeLookupDefinition(1, raw)
		if e != nil {
			return nil, e
		}
		for _, binding := range overlay.Bindings {
			if id := binding.Lookup.DialogId; id != "" && !strings.Contains(id, "://") {
				if original := needed[id]; original != "" && original != uri.Namespace {
					return nil, fmt.Errorf("%w: lookup picker namespace", ErrCollision)
				}
				needed[id] = uri.Namespace
			}
		}
	}
	if len(needed) == 0 {
		return nil, nil
	}
	files := map[string]string{}
	err = fs.WalkDir(handle.FS(), "extension/forge/dialogs", func(file string, entry fs.DirEntry, e error) error {
		if e != nil {
			if os.IsNotExist(e) {
				return nil
			}
			return e
		}
		if entry.IsDir() || !(strings.HasSuffix(file, ".yaml") || strings.HasSuffix(file, ".yml")) {
			return nil
		}
		raw, e := reader.ReadFile(ctx, file)
		if e != nil {
			return e
		}
		var dialog types.Dialog
		if e = yaml.Unmarshal(raw, &dialog); e != nil {
			return e
		}
		if needed[dialog.Id] == "" {
			return nil
		}
		if files[dialog.Id] != "" {
			return fmt.Errorf("%w: duplicate lookup picker id", ErrCollision)
		}
		files[dialog.Id] = file
		return nil
	})
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, definition := range windows {
		known[definition.URI] = true
	}
	ids := make([]string, 0, len(needed))
	for id := range needed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var definitions []NativeSnapshotDefinition
	for _, id := range ids {
		file := files[id]
		if file == "" {
			return nil, fmt.Errorf("lookup picker %q is missing", id)
		}
		uri := identity.ResourceURI{Kind: "window", Namespace: needed[id], Name: id}
		if !uri.Valid() {
			return nil, identity.ErrResource
		}
		if known[uri.String()] {
			return nil, fmt.Errorf("%w: lookup picker window identity", ErrCollision)
		}
		binding := workspacewindow.ResourceBinding{WindowKey: id, URI: uri.String()}
		definitions = append(definitions, NativeSnapshotDefinition{URI: uri.String(), Title: id, File: file, FormatVersion: 2, Load: nativeLookupPickerLoader(binding, file, enrich)})
	}
	return definitions, nil
}

func nativeLookupPickerLoader(binding workspacewindow.ResourceBinding, file string, enrich StaticWorkspaceWindowEnricher) ExtensionLoader {
	return func(ctx context.Context, reader *ExtensionReader, _ string) (json.RawMessage, error) {
		raw, err := reader.ReadFile(ctx, file)
		if err != nil {
			return nil, err
		}
		var dialog types.Dialog
		if err = yaml.Unmarshal(raw, &dialog); err != nil {
			return nil, err
		}
		if dialog.Id != binding.WindowKey {
			return nil, identity.ErrResource
		}
		base := func(context.Context, *types.WindowTarget) (*types.Window, error) {
			return &types.Window{Title: dialog.Title, WindowKey: binding.WindowKey, View: types.View{Content: &types.Container{ID: binding.WindowKey + "Host"}}, Dialogs: []types.Dialog{dialog}}, nil
		}
		return NativeComposedWindowLoader(binding, base, enrich)(ctx, reader, path.Clean(file))
	}
}
