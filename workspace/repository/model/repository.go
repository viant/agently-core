package model

import (
	"github.com/viant/afs"
	llmprovider "github.com/viant/agently-core/genai/llm/provider"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

// Repository provides CRUD over YAML model configs.
type Repository struct {
	*base.Repository[llmprovider.Config]
}

func New(fs afs.Service) *Repository {
	return &Repository{base.New[llmprovider.Config](fs, workspace.KindModel)}
}

// NewWithStore constructs a Repository backed by a workspace.Store.
func NewWithStore(store workspace.Store) *Repository {
	return &Repository{base.NewWithStore[llmprovider.Config](store, workspace.KindModel)}
}
