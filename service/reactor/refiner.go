package reactor

import "github.com/viant/agently-core/protocol/agent/plan"

// ToolKey uniquely identifies a tool call by its name and canonicalised
// arguments JSON.  Hashable so it can be used as a map key.
type ToolKey struct {
	Name string
	Args string
}

func RefinePlan(p *plan.Plan) {
	// No-op refinement: keep plan steps as-is.
	// We intentionally do not de-duplicate here to allow providers
	// (e.g. OpenAI /v1/responses) to receive all tool calls as issued.
	if p == nil {
		return
	}
}
