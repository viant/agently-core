package intake

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/viant/afs"
	intent "github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

type Reader interface {
	LoadAll(context.Context) ([]*intent.Profile, error)
	Load(context.Context, string) (*intent.Profile, error)
}

type Repository struct {
	reader Reader
	*base.Repository[intent.Profile]
	legacy *base.Repository[intent.Profile]
	fs     afs.Service
	store  workspace.Store
}

// NewWithReader binds definition consumption to a host-owned reader.
func NewWithReader(reader Reader) *Repository { return &Repository{reader: reader} }

func New(fs afs.Service) *Repository {
	return &Repository{
		Repository: base.New[intent.Profile](fs, workspace.KindIntent),
		legacy:     base.New[intent.Profile](fs, workspace.KindPrompt),
		fs:         fs,
	}
}

func NewWithStore(store workspace.Store) *Repository {
	return &Repository{
		Repository: base.NewWithStore[intent.Profile](store, workspace.KindIntent),
		legacy:     base.NewWithStore[intent.Profile](store, workspace.KindPrompt),
		store:      store,
	}
}

// source selects the canonical intents directory first and falls back to the
// legacy prompts directory only when the intent profile is absent.
func (r *Repository) source(ctx context.Context, name string) (*base.Repository[intent.Profile], error) {
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("intake profile repository not configured")
	}
	for _, candidate := range []struct {
		kind string
		repo *base.Repository[intent.Profile]
	}{
		{workspace.KindIntent, r.Repository},
		{workspace.KindPrompt, r.legacy},
	} {
		if candidate.repo == nil {
			continue
		}
		exists, err := r.sourceExists(ctx, candidate.kind, candidate.repo, name)
		if err != nil {
			return nil, err
		}
		if exists {
			return candidate.repo, nil
		}
	}
	return r.Repository, nil
}

func (r *Repository) sourceExists(ctx context.Context, kind string, repo *base.Repository[intent.Profile], name string) (bool, error) {
	if r.store != nil {
		return r.store.Exists(ctx, kind, name)
	}
	filename, err := repo.ResolveFilename(ctx, name)
	if err != nil {
		return false, err
	}
	return r.fs.Exists(ctx, filename)
}

func (r *Repository) Load(ctx context.Context, name string) (*intent.Profile, error) {
	if r != nil && r.reader != nil {
		return r.reader.Load(ctx, name)
	}
	source, err := r.source(ctx, name)
	if err != nil {
		return nil, err
	}
	return source.Load(ctx, name)
}

func (r *Repository) GetRaw(ctx context.Context, name string) ([]byte, error) {
	source, err := r.source(ctx, name)
	if err != nil {
		return nil, err
	}
	return source.GetRaw(ctx, name)
}

// List merges all catalogs. Duplicate names resolve by source precedence.
func (r *Repository) List(ctx context.Context) ([]string, error) {
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("intake profile repository not configured")
	}
	seen := map[string]bool{}
	var names []string
	for _, repo := range []*base.Repository[intent.Profile]{r.Repository, r.legacy} {
		if repo == nil {
			continue
		}
		if r.store == nil {
			exists, err := repo.DirExists(ctx)
			if err != nil {
				return nil, err
			}
			if !exists {
				continue
			}
		}
		items, err := repo.List(ctx)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, name := range items {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func (r *Repository) LoadAll(ctx context.Context) ([]*intent.Profile, error) {
	if r != nil && r.reader != nil {
		return r.reader.LoadAll(ctx)
	}
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("prompt repository not configured")
	}
	names, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)
	out := make([]*intent.Profile, 0, len(names))
	for _, name := range names {
		profile, err := r.Load(ctx, name)
		if err != nil {
			return nil, err
		}
		if profile == nil {
			continue
		}
		out = append(out, profile)
	}
	return out, nil
}

func FilterAllowedProfiles(profiles []*intent.Profile, allow []string) []*intent.Profile {
	if len(profiles) == 0 {
		return nil
	}
	if len(allow) == 0 {
		return profiles
	}
	allowed := map[string]struct{}{}
	for _, id := range allow {
		if trimmed := strings.ToLower(strings.TrimSpace(id)); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	filtered := make([]*intent.Profile, 0, len(profiles))
	for _, profile := range profiles {
		if profile == nil {
			continue
		}
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(profile.ID))]; ok {
			filtered = append(filtered, profile)
		}
	}
	return filtered
}

// AdmitSelected freshly reads delegated definitions; explicit filesystem repositories retain their original behavior.
func (r *Repository) AdmitSelected(ctx context.Context, key string, selected *intent.Profile) (*intent.Profile, error) {
	if r != nil && r.reader != nil {
		return r.reader.Load(ctx, key)
	}
	return selected, nil
}
