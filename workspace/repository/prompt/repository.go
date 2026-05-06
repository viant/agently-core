package prompt

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/viant/afs"
	promptdef "github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

type Repository struct {
	*base.Repository[promptdef.Profile]
}

func New(fs afs.Service) *Repository {
	return &Repository{Repository: base.New[promptdef.Profile](fs, workspace.KindPrompt)}
}

func NewWithStore(store workspace.Store) *Repository {
	return &Repository{Repository: base.NewWithStore[promptdef.Profile](store, workspace.KindPrompt)}
}

func (r *Repository) LoadAll(ctx context.Context) ([]*promptdef.Profile, error) {
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
	out := make([]*promptdef.Profile, 0, len(names))
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

func FilterAllowedProfiles(profiles []*promptdef.Profile, allow []string) []*promptdef.Profile {
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
	filtered := make([]*promptdef.Profile, 0, len(profiles))
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
