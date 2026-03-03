package embedder

import "github.com/viant/agently-core/genai/embedder/provider"

// Option defines a functional option for Finder.
type Option func(*Finder)

// WithConfigLoader sets a custom configuration loader.
func WithConfigLoader(loader provider.ConfigLoader) Option {
	return func(f *Finder) {
		f.configLoader = loader
	}
}

// WithInitial adds embedder configurations at construction time.
func WithInitial(configs ...*provider.Config) Option {
	return func(f *Finder) {
		for _, cfg := range configs {
			if cfg != nil && cfg.ID != "" {
				f.configRegistry.Add(cfg.ID, cfg)
			}
		}
	}
}
