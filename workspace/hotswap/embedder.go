package hotswap

import (
	"context"
	"path/filepath"

	embedderprovider "github.com/viant/agently-core/genai/embedder/provider"
	embedderfinder "github.com/viant/agently-core/internal/finder/embedder"
	"github.com/viant/agently-core/workspace"
	embedderloader "github.com/viant/agently-core/workspace/loader/embedder"
)

// NewEmbedderAdaptor creates a Reloadable that reloads embedder configs on workspace changes.
func NewEmbedderAdaptor(loader *embedderloader.Service, finder *embedderfinder.Finder) Reloadable {
	return NewAdaptor[*embedderprovider.Config](
		func(ctx context.Context, name string) (*embedderprovider.Config, error) {
			url := filepath.Join(workspace.KindEmbedder, name)
			return loader.Load(ctx, url)
		},
		func(name string, cfg *embedderprovider.Config) {
			id := cfg.ID
			if id == "" {
				id = name
			}
			finder.AddConfig(id, cfg)
		},
		func(name string) { finder.Remove(name) },
	)
}
