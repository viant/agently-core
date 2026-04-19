// Package callback is the dispatch runtime for workspace callback definitions.
//
// The dispatcher accepts a DispatchInput (event name, conversation context,
// forge submit payload), renders the matching callback's payload template,
// and invokes the mapped tool via the registry. It is invoked from the
// HTTP adapter at adapter/http/callback.
package callback

// DispatchInput is the envelope the caller (typically the HTTP handler
// wrapping a forge submit event) passes to Service.Dispatch.
type DispatchInput struct {
	// EventName is the callback id the foreground wants routed (e.g.
	// "spo_planner_submit"). Required.
	EventName string `json:"eventName"`

	// ConversationID is the conversation this dispatch is acting on. Required
	// when the callback's payload template references any conversation-scoped
	// placeholder.
	ConversationID string `json:"conversationId,omitempty"`

	// TurnID is optional — the turn that produced the dashboard where the
	// user clicked submit. Surfaced in templates as `{{.turnId}}` for use in
	// recommendation id generation and audit trails.
	TurnID string `json:"turnId,omitempty"`

	// Payload is the raw body forge posts when the user clicks submit.
	// Common shape: `{ "selectedRows": [...] }`. Surfaced in templates as
	// `{{.payload.<key>}}`; the convenience alias `{{.selectedRows}}` is
	// populated when present.
	Payload map[string]interface{} `json:"payload,omitempty"`

	// Context carries arbitrary key/value pairs the foreground wants to
	// expose to the payload template. Typical keys: agencyId, advertiserId,
	// campaignId, adOrderId, audienceId — passed from the current UI view
	// state. Keys here are flattened into the template root and MUST NOT
	// shadow reserved keys (eventName, conversationId, turnId, agentId,
	// payload, selectedRows, now, today).
	Context map[string]interface{} `json:"context,omitempty"`
}

// DispatchOutput is what Service.Dispatch returns. The HTTP adapter
// encodes this as JSON on the wire.
type DispatchOutput struct {
	// EventName echoes the request so clients can multiplex responses.
	EventName string `json:"eventName"`

	// Tool is the tool that was invoked, for audit.
	Tool string `json:"tool"`

	// Result is the tool's textual return value, verbatim. Clients should
	// parse it per the tool's documented schema.
	Result string `json:"result,omitempty"`

	// Error carries any dispatch or tool-invocation failure that should be
	// presented to the user. Network / authz errors are returned via the
	// HTTP status code instead.
	Error string `json:"error,omitempty"`
}

// reservedKeys is the set of template-root fields populated by the
// dispatcher itself. Context keys that collide are silently dropped to
// preserve a predictable template surface.
var reservedKeys = map[string]struct{}{
	"eventName":      {},
	"conversationId": {},
	"turnId":         {},
	"agentId":        {},
	"payload":        {},
	"selectedRows":   {},
	"now":            {},
	"today":          {},
}
