// Package forgelookup loads extension/forge/lookups/*.yaml overlay files.
package forgelookup

import (
	"github.com/viant/afs"
	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

// Repository provides CRUD over YAML overlay configs.
type Repository struct {
	*base.Repository[loproto.Overlay]
}

func New(fs afs.Service) *Repository {
	return &Repository{base.New[loproto.Overlay](fs, workspace.KindForgeLookup)}
}

func NewWithStore(store workspace.Store) *Repository {
	return &Repository{base.NewWithStore[loproto.Overlay](store, workspace.KindForgeLookup)}
}
