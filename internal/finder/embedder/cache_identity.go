package embedder

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// EmbeddingCacheIdentity identifies the configured embedding space across
// process restarts. Credential references and tokens do not define that space.
func (f *Finder) EmbeddingCacheIdentity(ctx context.Context, id string) (string, error) {
	cfg, err := f.configRegistry.Lookup(ctx, id)
	if err != nil && f.configLoader != nil {
		cfg, err = f.configLoader.Load(ctx, id)
		if err != nil {
			cfg, err = f.configLoader.Load(ctx, filepath.ToSlash(filepath.Join("embedders", strings.TrimSpace(id))))
		}
	}
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", fmt.Errorf("embedder config not found")
	}
	options := cfg.Options
	raw, err := json.Marshal([]any{id, options.Provider, options.Model, options.URL, options.ProjectID, options.Location, options.Scopes})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
