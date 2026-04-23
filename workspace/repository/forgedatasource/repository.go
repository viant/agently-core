// Package forgedatasource loads extension/forge/datasources/*.yaml workspace
// resources.
package forgedatasource

import (
	"github.com/viant/afs"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

// Repository provides CRUD over YAML datasource configs.
type Repository struct {
	*base.Repository[dsproto.DataSource]
}

func New(fs afs.Service) *Repository {
	return &Repository{base.New[dsproto.DataSource](fs, workspace.KindForgeDataSource)}
}

func NewWithStore(store workspace.Store) *Repository {
	return &Repository{base.NewWithStore[dsproto.DataSource](store, workspace.KindForgeDataSource)}
}
