package prompt

import (
	"context"
	"fmt"
	"sort"

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
