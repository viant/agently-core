package requestctx

import "strings"

// IsInternalHelperAgentID identifies control-plane agents that must not become
// the owning agent of an existing main turn or its approval continuation.
func IsInternalHelperAgentID(agentID string) bool {
	switch strings.ToLower(strings.TrimSpace(agentID)) {
	case "agent_selector", "agent-selector", "intake_sidecar", "tool_router", "planner_pass":
		return true
	}
	return false
}
