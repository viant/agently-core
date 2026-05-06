package intake

type ClassificationContext struct {
	Title      string  `json:"title,omitempty"`
	Intent     string  `json:"intent,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type ScopeContext struct {
	Values map[string]string `json:"values,omitempty"`
}

type PromptingContext struct {
	SuggestedProfileID string   `json:"suggestedProfileId,omitempty"`
	AppendToolBundles  []string `json:"appendToolBundles,omitempty"`
	TemplateID         string   `json:"templateId,omitempty"`
}

type RoutingContext struct {
	SelectedAgentID string `json:"selectedAgentId,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Source          string `json:"source,omitempty"`
}

type PlannerContext struct {
	Trigger string `json:"trigger,omitempty"`
	AgentID string `json:"agentId,omitempty"`
}

// Context is the structured output of intake. It is grouped by feature
// area so routing, scope extraction, prompting hints, and planner state do not
// bleed together as unrelated top-level keys.
type Context struct {
	Classification ClassificationContext `json:"classification,omitempty"`
	Scope          ScopeContext          `json:"scope,omitempty"`
	Prompting      PromptingContext      `json:"prompting,omitempty"`
	Routing        RoutingContext        `json:"routing,omitempty"`
	Planner        PlannerContext        `json:"planner,omitempty"`
}

// ContextKey is the key used to store Context in QueryInput.Context.
const ContextKey = "intake.turnContext"

// Source values for Context.Source. Centralized so the runtime, intake
// services, and observability subscribers all reference the same constants.
const (
	SourceWorkspace      = "workspace"
	SourceAgent          = "agent"
	SourceReused         = "reused"
	SourceCallerProvided = "caller-provided"
	SourceFallback       = "fallback"
)

// Mode values for Context.Mode. Workspace intake's classification result.
const (
	ModeRoute   = "route"
	ModeClarify = "clarify"
	ModePlanner = "planner"
)

// StoreCallerProvided records a caller-supplied Context into the QueryInput
// context map under the well-known ContextKey. The stored value is a copy
// (so later mutation of the caller's struct does not race with the runtime),
// annotated with Source = SourceCallerProvided. The runtime's intake skip
// rule then sees a non-nil Context under the key and bypasses the
// intake-sidecar LLM call (see service/agent/intake_query.go).
//
// ctxMap is the QueryInput.Context map (lazy-initialized when nil). Returns
// the resulting (possibly newly-allocated) map and a pointer to the stored
// copy.
func StoreCallerProvided(ctxMap map[string]any, override *Context) (map[string]any, *Context) {
	if override == nil {
		return ctxMap, nil
	}
	if ctxMap == nil {
		ctxMap = make(map[string]any)
	}
	tc := *override
	tc.Routing.Source = SourceCallerProvided
	ctxMap[ContextKey] = &tc
	return ctxMap, &tc
}

// FromContext retrieves a Context previously stored under ContextKey.
// Returns nil when missing or the stored value has the wrong type.
func FromContext(ctxMap map[string]any) *Context {
	if len(ctxMap) == 0 {
		return nil
	}
	v, ok := ctxMap[ContextKey]
	if !ok || v == nil {
		return nil
	}
	switch tc := v.(type) {
	case *Context:
		return tc
	case Context:
		out := tc
		return &out
	}
	return nil
}
