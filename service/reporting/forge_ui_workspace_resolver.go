package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/agently-core/workspace"
	forgehandlers "github.com/viant/forge/backend/handlers"
	forgemeta "github.com/viant/forge/backend/service/meta"
)

type workspaceForgeUIViewResolver struct {
	loader *forgemeta.Service
}

func newWorkspaceForgeUIViewResolver() ForgeUIViewResolver {
	return &workspaceForgeUIViewResolver{loader: forgemeta.New(afs.New(), "")}
}

func (r *workspaceForgeUIViewResolver) ResolveForgeUIView(
	ctx context.Context,
	viewRef string,
	target ForgeTargetContext,
	_ []string,
) (*ResolvedForgeUIView, error) {
	viewRef = strings.TrimSpace(viewRef)
	forgeTarget := &forgemeta.TargetContext{
		Platform: target.Platform, FormFactor: target.FormFactor,
		Surface: target.Surface, Capabilities: append([]string(nil), target.Capabilities...),
	}
	switch {
	case strings.HasPrefix(viewRef, "feed://"):
		id := strings.Trim(strings.TrimPrefix(viewRef, "feed://"), "/")
		if id == "" {
			return nil, fmt.Errorf("feed view reference is missing an id")
		}
		var spec map[string]any
		path := filepath.Join(workspace.Root(), "feeds", id+".yaml")
		if err := r.loader.LoadWithURLAndTarget(ctx, path, &spec, forgeTarget); err != nil {
			return nil, err
		}
		ui, ok := spec["ui"]
		if !ok {
			return nil, fmt.Errorf("feed %s has no ui", id)
		}
		data, err := json.Marshal(ui)
		if err != nil {
			return nil, err
		}
		return &ResolvedForgeUIView{UI: data, DataSources: map[string]json.RawMessage{}}, nil
	case strings.HasPrefix(viewRef, "forge://window/"):
		key := strings.Trim(strings.TrimPrefix(viewRef, "forge://window/"), "/")
		if key == "" {
			return nil, fmt.Errorf("forge window reference is missing a key")
		}
		window, err := forgehandlers.LoadWindow(
			ctx, r.loader, filepath.Join(workspace.Root(), "extension", "forge", "windows"), key, "", forgeTarget,
		)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(window)
		if err != nil {
			return nil, err
		}
		return &ResolvedForgeUIView{UI: data, DataSources: map[string]json.RawMessage{}}, nil
	default:
		return nil, fmt.Errorf("unsupported forge view reference %q", viewRef)
	}
}
