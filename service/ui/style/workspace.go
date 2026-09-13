package style

import "github.com/viant/agently-core/workspace"

// Workspace shares the effective style snapshot between metadata, HTTP assets,
// and MCP publication. Root changes invalidate the previous workspace cache.
var workspaceService = New(workspace.Root)

func Workspace() *Service { return workspaceService }
