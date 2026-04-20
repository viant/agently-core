package workspace

import "embed"

// defaultWorkspaceFS contains data-only bootstrap assets. New defaults can be
// added under workspace/defaults without changing bootstrap logic.
//
//go:embed defaults/**
var defaultWorkspaceFS embed.FS
