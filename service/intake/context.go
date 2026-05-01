package intake

// TurnContext is the structured output of the intake sidecar.
// Class A fields are always safe; Class B fields are only populated when
// the agent's intake scope includes the corresponding scope constant.
type TurnContext struct {
	// Class A — metadata, always safe.

	// Title is a short, human-readable label for the current task (≤ 80 chars).
	Title string `json:"title,omitempty"`
	// Intent classifies the user's goal (e.g. "diagnosis", "comparison", "summary").
	Intent string `json:"intent,omitempty"`
	// Context holds lightweight orchestration context extracted from the request
	// (e.g. scope ids, timeframe hints, issue labels). It is not an
	// authoritative domain-object record.
	Context map[string]string `json:"context,omitempty"`
	// ClarificationNeeded is true when the request is too ambiguous to act on.
	ClarificationNeeded bool `json:"clarificationNeeded,omitempty"`
	// ClarificationQuestion is the question to ask the user when ClarificationNeeded.
	ClarificationQuestion string `json:"clarificationQuestion,omitempty"`

	// Class B — delegation hints, only populated when in scope.

	// SuggestedProfileId is the id of the most relevant prompt profile.
	SuggestedProfileId string `json:"suggestedProfileId,omitempty"`
	// AppendToolBundles lists additional tool bundle ids detected from the task.
	AppendToolBundles []string `json:"appendToolBundles,omitempty"`
	// TemplateId is the suggested output template for this turn.
	TemplateId string `json:"templateId,omitempty"`
	// Confidence is the sidecar's self-reported confidence (0–1) in its profile
	// and routing suggestions.
	Confidence float64 `json:"confidence,omitempty"`

	// Workspace-intake fields (additive). Populated by workspace-level intake
	// (when implemented) or by callers via RunInput.WorkspaceIntake. Agent intake
	// MUST NOT write SelectedAgentID or Mode (the runtime drops those if it
	// detects a violation) — agent intake is field-refinement only.

	// SelectedAgentID is the agent chosen for this turn. Written by workspace
	// intake or by a caller-provided override; never by agent intake.
	SelectedAgentID string `json:"selectedAgentId,omitempty"`

	// Mode classifies the turn outcome of workspace intake:
	//   "route"   — normal turn; route to SelectedAgentID and run.
	//   "clarify" — ambiguous; resolve chosen agent and pass clarification hint.
	// Empty when workspace intake has not run (legacy agent-only intake).
	Mode string `json:"mode,omitempty"`

	// Source records who produced this TurnContext for telemetry / debugging:
	//   "workspace"        — workspace intake LLM call
	//   "agent"            — agent intake refinement
	//   "reused"           — cross-turn reuse of a prior TurnContext
	//   "caller-provided"  — supplied via RunInput.WorkspaceIntake
	//   "fallback"         — produced by the deterministic fallback chain
	// Empty for legacy agent-only intake outputs.
	Source string `json:"source,omitempty"`

	// ActivateSkills lists skill names workspace intake suggests activating for
	// this turn. Validated against the chosen agent's visible skills before use.
	ActivateSkills []string `json:"activateSkills,omitempty"`
}

// ContextKey is the key used to store TurnContext in QueryInput.Context.
const ContextKey = "intake.turnContext"

// Source values for TurnContext.Source. Centralized so the runtime, intake
// services, and observability subscribers all reference the same constants.
const (
	SourceWorkspace      = "workspace"
	SourceAgent          = "agent"
	SourceReused         = "reused"
	SourceCallerProvided = "caller-provided"
	SourceFallback       = "fallback"
)

// Mode values for TurnContext.Mode. Workspace intake's classification result.
const (
	ModeRoute   = "route"
	ModeClarify = "clarify"
)

// SanitizeAgentRefinement enforces the invariant that agent intake never writes
// SelectedAgentID or Mode. Call this on the agent-intake output before
// merging it into the running TurnContext. Returns the list of fields the
// agent intake attempted to write but had stripped, so the runtime can emit
// a diagnostic.
//
// This is the code-enforced version of the doc rule "agent decides how, not
// who" (intake-impt.md §3).
func SanitizeAgentRefinement(tc *TurnContext) []string {
	if tc == nil {
		return nil
	}
	var stripped []string
	if tc.SelectedAgentID != "" {
		stripped = append(stripped, "selectedAgentId")
		tc.SelectedAgentID = ""
	}
	if tc.Mode != "" {
		stripped = append(stripped, "mode")
		tc.Mode = ""
	}
	return stripped
}

// StoreCallerProvided records a caller-supplied TurnContext into the QueryInput
// context map under the well-known ContextKey. The stored value is a copy
// (so later mutation of the caller's struct does not race with the runtime),
// annotated with Source = SourceCallerProvided. The runtime's intake skip
// rule then sees a non-nil TurnContext under the key and bypasses the
// intake-sidecar LLM call (see service/agent/intake_query.go).
//
// ctxMap is the QueryInput.Context map (lazy-initialized when nil). Returns
// the resulting (possibly newly-allocated) map and a pointer to the stored
// copy.
func StoreCallerProvided(ctxMap map[string]any, override *TurnContext) (map[string]any, *TurnContext) {
	if override == nil {
		return ctxMap, nil
	}
	if ctxMap == nil {
		ctxMap = make(map[string]any)
	}
	tc := *override
	tc.Source = SourceCallerProvided
	ctxMap[ContextKey] = &tc
	return ctxMap, &tc
}

// FromContext retrieves a TurnContext previously stored under ContextKey.
// Returns nil when missing or the stored value has the wrong type.
func FromContext(ctxMap map[string]any) *TurnContext {
	if len(ctxMap) == 0 {
		return nil
	}
	v, ok := ctxMap[ContextKey]
	if !ok || v == nil {
		return nil
	}
	switch tc := v.(type) {
	case *TurnContext:
		return tc
	case TurnContext:
		out := tc
		return &out
	}
	return nil
}
