package skill

import mcpname2 "github.com/viant/agently-core/protocol/mcpname"

const (
	ServiceName      = "llm/skills"
	ListToolName     = ServiceName + ":list"
	GetToolName      = ServiceName + ":get"
	ActivateToolName = ServiceName + ":activate"
)

var (
	ListToolNameCanonical     = mcpname2.Canonical(ListToolName)
	ActivateToolNameCanonical = mcpname2.Canonical(ActivateToolName)
)
