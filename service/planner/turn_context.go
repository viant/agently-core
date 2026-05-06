package planner

const ContextKey = "planner.context"

type Trigger string

const (
	TriggerLowConfidence    Trigger = "low_confidence"
	TriggerCreativePhrase   Trigger = "creative_phrase"
	TriggerValidatorFailure Trigger = "validator_failure"
)

type PlannerContext struct {
	Trigger             Trigger        `json:"trigger"`
	Attempt             int            `json:"attempt"`
	StrategyFamily      string         `json:"strategyFamily,omitempty"`
	BaseProfiles        []string       `json:"baseProfiles,omitempty"`
	ToolBundles         []string       `json:"toolBundles,omitempty"`
	TemplateID          string         `json:"templateId,omitempty"`
	ExecutionOrder      []string       `json:"executionOrder,omitempty"`
	RequiredEvidence    []string       `json:"requiredEvidence,omitempty"`
	Guards              []string       `json:"finalizationGuards,omitempty"`
	NarrationPolicy     map[string]any `json:"narrationPolicy,omitempty"`
	WorkspaceExtensions map[string]any `json:"workspaceExtensions,omitempty"`
	ParallelToolCalls   *bool          `json:"parallelToolCalls,omitempty"`
}

type queryInputWithContext interface {
	GetContext() map[string]any
}

// FromQueryInput returns the typed planner context for the current turn, or nil
// when planner mode did not run. It is intentionally tiny so downstream code
// reads one canonical helper instead of reaching into Context with a raw key.
func FromQueryInput(input queryInputWithContext) *PlannerContext {
	if input == nil {
		return nil
	}
	ctx := input.GetContext()
	if len(ctx) == 0 {
		return nil
	}
	pc, _ := ctx[ContextKey].(*PlannerContext)
	return pc
}
