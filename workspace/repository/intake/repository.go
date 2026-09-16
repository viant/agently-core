package intake

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/viant/afs"
	intake "github.com/viant/agently-core/protocol/intake"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

type Repository struct {
	*base.Repository[intake.Profile]
	legacy *base.Repository[intake.Profile]
	fs     afs.Service
	store  workspace.Store
}

func New(fs afs.Service) *Repository {
	return &Repository{
		Repository: base.New[intake.Profile](fs, workspace.KindIntent),
		legacy:     base.New[intake.Profile](fs, workspace.KindPrompt),
		fs:         fs,
	}
}

func NewWithStore(store workspace.Store) *Repository {
	return &Repository{
		Repository: base.NewWithStore[intake.Profile](store, workspace.KindIntent),
		legacy:     base.NewWithStore[intake.Profile](store, workspace.KindPrompt),
		store:      store,
	}
}

// source selects the canonical intents directory first and falls back to the
// legacy prompts directory only when the intent profile is absent.
func (r *Repository) source(ctx context.Context, name string) (*base.Repository[intake.Profile], error) {
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("intake profile repository not configured")
	}
	for _, candidate := range []struct {
		kind string
		repo *base.Repository[intake.Profile]
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

func (r *Repository) sourceExists(ctx context.Context, kind string, repo *base.Repository[intake.Profile], name string) (bool, error) {
	if r.store != nil {
		return r.store.Exists(ctx, kind, name)
	}
	filename, err := repo.ResolveFilename(ctx, name)
	if err != nil {
		return false, err
	}
	return r.fs.Exists(ctx, filename)
}

func (r *Repository) Load(ctx context.Context, name string) (*intake.Profile, error) {
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
	for _, repo := range []*base.Repository[intake.Profile]{r.Repository, r.legacy} {
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

func (r *Repository) LoadAll(ctx context.Context) ([]*intake.Profile, error) {
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
	out := make([]*intake.Profile, 0, len(names))
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

func FilterAllowedProfiles(profiles []*intake.Profile, allow []string) []*intake.Profile {
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
	filtered := make([]*intake.Profile, 0, len(profiles))
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
