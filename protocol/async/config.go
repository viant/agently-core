package async

import shared "github.com/viant/mcp/client/config/async"

// Runtime operation state stays in Core; authored descriptors are shared by MCP hosts.
type (
	ExecutionMode = shared.ExecutionMode
	Config        = shared.Config
	RunConfig     = shared.RunConfig
	StatusConfig  = shared.StatusConfig
	CancelConfig  = shared.CancelConfig
	Selector      = shared.Selector
)

const (
	ExecutionModeWait   = shared.ExecutionModeWait
	ExecutionModeDetach = shared.ExecutionModeDetach
)

func NormalizeExecutionMode(value, fallback string) string {
	return shared.NormalizeExecutionMode(value, fallback)
}
func ExecutionModeWaits(value string) bool { return shared.ExecutionModeWaits(value) }
