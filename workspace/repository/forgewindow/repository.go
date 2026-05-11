package forgewindow

import (
	"context"

	"github.com/viant/afs"
	viewproto "github.com/viant/agently-core/protocol/ui/view"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

// Repository provides CRUD over workspace dynamic window descriptors.
type Repository struct {
	*base.Repository[viewproto.Spec]
}

func New(fs afs.Service) *Repository {
	return &Repository{base.New[viewproto.Spec](fs, workspace.KindForgeWindow)}
}

func NewWithStore(store workspace.Store) *Repository {
	return &Repository{base.NewWithStore[viewproto.Spec](store, workspace.KindForgeWindow)}
}

func (r *Repository) LoadAll(ctx context.Context) ([]*viewproto.Spec, error) {
	names, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*viewproto.Spec, 0, len(names))
	for _, name := range names {
		item, err := r.Load(ctx, name)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}
