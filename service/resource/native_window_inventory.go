package resource

import (
	"context"
	"encoding/json"
	view "github.com/viant/agently-core/protocol/ui/view"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	"gopkg.in/yaml.v3"
)

// NativeWorkspaceWindowBindings inventories original window entry points only.
// Target branches and imported fragments remain inside the owning window bundle.
// Namespace is trusted startup configuration, never an authored display label.
func NativeWorkspaceWindowBindings(ctx context.Context, root, namespace string) ([]workspacewindow.ResourceBinding, error) {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	reader := NewExtensionReader(opened)
	defer reader.Close()
	const directory = "extension/forge/windows"
	if err := reader.CheckPath(directory); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	err = fs.WalkDir(opened.FS(), directory, func(file string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = reader.CheckPath(file); err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		if !strings.HasSuffix(file, ".yaml") && !strings.HasSuffix(file, ".yml") {
			return nil
		}
		relative := strings.TrimPrefix(file, directory+"/")
		leaf := path.Base(relative)
		candidate := strings.TrimSuffix(relative, path.Ext(relative))
		if leaf == "main.yaml" || leaf == "main.yml" {
			parts := strings.Split(path.Dir(relative), "/")
			for i, p := range parts {
				switch p {
				case "shared", "web", "mobile", "android", "ios", "desktop", "phone", "tablet":
					parts = parts[:i]
					goto found
				}
			}
		found:
			candidate = strings.Join(parts, "/")
		} else if strings.Contains(relative, "/") {
			return nil
		}
		if candidate == "" {
			return nil
		}
		uri, err := identity.ParseResourceURI("window://" + namespace + "/" + candidate)
		if err != nil {
			return err
		}
		raw, err := reader.ReadFile(ctx, file)
		if err != nil {
			return err
		}
		var node yaml.Node
		if err = yaml.Unmarshal(raw, &node); err != nil {
			return err
		}
		top := &node
		if top.Kind == yaml.DocumentNode && len(top.Content) > 0 {
			top = top.Content[0]
		}
		entry := top.Kind == yaml.ScalarNode && strings.HasPrefix(strings.TrimSpace(top.Value), "$import(")
		if top.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(top.Content); i += 2 {
				switch top.Content[i].Value {
				case "view", "windowKey":
					entry = true
				}
			}
		}
		if entry {
			keys[uri.Name] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(keys))
	for key := range keys {
		names = append(names, key)
	}
	sort.Strings(names)
	bindings := make([]workspacewindow.ResourceBinding, 0, len(names))
	for _, key := range names {
		bindings = append(bindings, workspacewindow.ResourceBinding{WindowKey: key, URI: "window://" + namespace + "/" + key})
	}
	return bindings, nil
}

// attachNativeWindowViewMetadata captures the original model-facing descriptor
// inside the admitted owning window during native producer startup.
func attachNativeWindowViewMetadata(ctx context.Context, r *ExtensionReader, file string, raw json.RawMessage) (json.RawMessage, error) {
	source, err := r.ReadFile(ctx, file)
	if err != nil {
		return nil, err
	}
	var spec view.Spec
	var node yaml.Node
	if err = yaml.Unmarshal(source, &node); err != nil {
		return nil, err
	}
	if err = resolveNativeAuthoredImports(ctx, r, file, &node); err != nil {
		return nil, err
	}
	if err = node.Decode(&spec); err != nil {
		return nil, err
	}
	if spec.ID == "" || spec.WindowKey == "" {
		return raw, nil
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	envelope["viewMetadata"], err = json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}
