package embedder

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/viant/agently-core/genai/embedder/provider"
	"github.com/viant/agently-core/genai/embedder/provider/base"
	"github.com/viant/agently-core/internal/registry"
)

// Finder is a concrete implementation of embedder.Finder that caches
// instantiated embedders and supports hot-swap via Add/Remove/Version.
type Finder struct {
	factory        *provider.Factory
	configRegistry *registry.Registry[*provider.Config]
	configLoader   provider.ConfigLoader
	embedders      map[string]base.Embedder
	mux            sync.RWMutex
	version        int64
}

// Find returns a cached embedder or creates one from configuration.
func (f *Finder) Find(ctx context.Context, id string) (base.Embedder, error) {
	f.mux.RLock()
	if e, ok := f.embedders[id]; ok {
		f.mux.RUnlock()
		return e, nil
	}
	f.mux.RUnlock()

	f.mux.Lock()
	defer f.mux.Unlock()
	// double-check after acquiring write lock
	if e, ok := f.embedders[id]; ok {
		return e, nil
	}

	cfg, err := f.configRegistry.Lookup(ctx, id)
	if err != nil {
		if f.configLoader != nil {
			cfg, err = f.configLoader.Load(ctx, id)
			if err != nil {
				fallback := filepath.ToSlash(filepath.Join("embedders", strings.TrimSpace(id)))
				cfg, err = f.configLoader.Load(ctx, fallback)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if cfg == nil {
		return nil, fmt.Errorf("embedder config not found: %s", id)
	}

	e, err := f.factory.CreateEmbedder(ctx, &cfg.Options)
	if err != nil {
		return nil, err
	}
	f.embedders[id] = e
	return e, nil
}

// Ids returns all registered configuration keys.
func (f *Finder) Ids() []string {
	configs, err := f.configRegistry.List(context.Background())
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(configs))
	for _, cfg := range configs {
		if cfg != nil {
			ids = append(ids, cfg.ID)
		}
	}
	return ids
}

// AddConfig injects or overwrites an embedder configuration and bumps version.
func (f *Finder) AddConfig(name string, cfg *provider.Config) {
	if cfg == nil || name == "" {
		return
	}
	f.configRegistry.Add(name, cfg)
	f.DropEmbedder(name)
	atomic.AddInt64(&f.version, 1)
}

// Remove deletes both configuration and cached instance, bumping version.
func (f *Finder) Remove(name string) {
	f.mux.Lock()
	delete(f.embedders, name)
	f.mux.Unlock()

	f.configRegistry.Remove(name)
	atomic.AddInt64(&f.version, 1)
}

// DropEmbedder removes a cached instance but keeps the config for lazy re-creation.
func (f *Finder) DropEmbedder(name string) {
	f.mux.Lock()
	if _, ok := f.embedders[name]; ok {
		delete(f.embedders, name)
		atomic.AddInt64(&f.version, 1)
	}
	f.mux.Unlock()
}

// Version returns a monotonically increasing counter changed on Add/Remove.
func (f *Finder) Version() int64 {
	return atomic.LoadInt64(&f.version)
}

// New creates a Finder instance.
func New(options ...Option) *Finder {
	ret := &Finder{
		factory:        provider.New(),
		configRegistry: registry.New[*provider.Config](),
		embedders:      map[string]base.Embedder{},
	}
	for _, opt := range options {
		opt(ret)
	}
	return ret
}
