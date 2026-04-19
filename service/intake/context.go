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
}

// ContextKey is the key used to store TurnContext in QueryInput.Context.
const ContextKey = "intake.turnContext"
