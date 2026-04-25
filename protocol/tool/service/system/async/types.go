package async

import (
	asynccfg "github.com/viant/agently-core/protocol/async"
)

// ListInput is the LLM-facing input schema for system/async:list.
//
// Intentionally minimal: no conversation id, no turn id, no state filter.
// The conversation scope is resolved from the request context; terminal
// ops are excluded by construction (they are pruned by GC and are not
// actionable). Both fields are optional narrowing filters.
type ListInput struct {
	// Tool narrows results to ops launched by this start-tool name,
	// e.g. "llm/agents:start" or "system/exec:execute". Empty = any.
	Tool string `json:"tool,omitempty"`
	// Mode narrows results by execution mode: "wait" or "detach".
	// Empty = any.
	Mode string `json:"mode,omitempty"`
}

// ListOutput is the LLM-facing output schema. Each entry contains the
// information needed to construct a status call for the op without
// guessing (tool name, op id, and the arg name under which to pass the
// id — or, for same-tool-recall patterns, a pre-built args map).
type ListOutput struct {
	Ops []asynccfg.PendingOp `json:"ops"`
}
