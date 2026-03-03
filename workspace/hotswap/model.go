package hotswap

import (
	"context"
	"path/filepath"

	modelfinder "github.com/viant/agently-core/internal/finder/model"
	modelprovider "github.com/viant/agently-core/genai/llm/provider"
	"github.com/viant/agently-core/workspace"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
)

// NewModelAdaptor creates a Reloadable that reloads model configs on workspace changes.
func NewModelAdaptor(loader *modelloader.Service, finder *modelfinder.Finder) Reloadable {
	return NewAdaptor[*modelprovider.Config](
		func(ctx context.Context, name string) (*modelprovider.Config, error) {
			url := filepath.Join(workspace.KindModel, name)
			return loader.Load(ctx, url)
		},
		func(name string, cfg *modelprovider.Config) {
			id := cfg.ID
			if id == "" {
				id = name
			}
			finder.AddConfig(id, cfg)
		},
		func(name string) { finder.Remove(name) },
	)
}
