package template

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/viant/afs"
	templ "github.com/viant/agently-core/protocol/template"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

type Repository struct {
	*base.Repository[templ.Template]
}

func New(fs afs.Service) *Repository {
	return &Repository{Repository: base.New[templ.Template](fs, workspace.KindTemplate)}
}

func NewWithStore(store workspace.Store) *Repository {
	return &Repository{Repository: base.NewWithStore[templ.Template](store, workspace.KindTemplate)}
}

func (r *Repository) LoadAll(ctx context.Context) ([]*templ.Template, error) {
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("template repository not configured")
	}
	names, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)
	out := make([]*templ.Template, 0, len(names))
	for _, name := range names {
		tpl, err := r.Load(ctx, name)
		if err != nil {
			return nil, err
		}
		if tpl == nil {
			continue
		}
		if err := tpl.Validate(); err != nil {
			return nil, fmt.Errorf("invalid template %q: %w", strings.TrimSpace(name), err)
		}
		out = append(out, tpl)
	}
	return out, nil
}
