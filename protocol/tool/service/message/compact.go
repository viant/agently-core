package message

// This compaction is intended only to free space for the Token‑Limit Presentation
// message during a context-limit recovery flow. It should not be exposed to the LLM
// as a general-purpose cleanup tool. Prefer LLM-driven removals via
// message:listCandidates + message:remove for normal operation.

type CompactInput struct {
	MaxTokens      int    `json:"maxTokens" description:"Target total token budget for retained messages."`
	Strategy       string `json:"strategy,omitempty" description:"Removal order: oldest-first (default), text-first, tool-first" choices:"oldest-first,text-first,tool-first"`
	PreservePinned bool   `json:"preservePinned,omitempty" description:"Reserved for future use (no effect in v1)."`
}

type CompactOutput struct {
	RemovedCount int `json:"removedCount"`
	FreedTokens  int `json:"freedTokens"`
	KeptTokens   int `json:"keptTokens"`
}
