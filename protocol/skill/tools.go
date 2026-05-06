package skill

import mcpname "github.com/viant/agently-core/pkg/mcpname"

const (
	ServiceName      = "llm/skills"
	ListToolName     = ServiceName + ":list"
	ActivateToolName = ServiceName + ":activate"
)

var (
	ListToolNameCanonical     = mcpname.Canonical(ListToolName)
	ActivateToolNameCanonical = mcpname.Canonical(ActivateToolName)
)
