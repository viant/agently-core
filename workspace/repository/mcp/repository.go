package mcp

import (
	"github.com/viant/afs"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

// Repository manages MCP client option configs stored in $AGENTLY_WORKSPACE/mcp.
type Repository struct {
	*base.Repository[mcpcfg.MCPClient]
}

func New(fs afs.Service) *Repository {
	return &Repository{base.New[mcpcfg.MCPClient](fs, workspace.KindMCP)}
}

// NewWithStore constructs a Repository backed by a workspace.Store.
func NewWithStore(store workspace.Store) *Repository {
	return &Repository{base.NewWithStore[mcpcfg.MCPClient](store, workspace.KindMCP)}
}
