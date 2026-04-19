// Package callback defines the workspace resource that routes foreground UI
// submit events to tool invocations with payload templating.
//
// A Callback is loaded from a YAML file under `<workspace>/callbacks/`. The
// filename stem serves as the event id; the `id` field in the file (if present)
// must match.
//
// See service/callback for the dispatch runtime and
// adapter/http/callback for the HTTP endpoint.
package callback

// Callback binds one foreground UI event (e.g. `spo_planner_submit`) to a
// tool invocation. The incoming submit payload is rendered into JSON via the
// PayloadMapping.Body Go-template and forwarded to the target tool.
type Callback struct {
	// ID is the event name (e.g. "spo_planner_submit"). Matches the filename
	// stem; required.
	ID string `yaml:"id" json:"id"`

	// Description is free text used by catalogue/registry listings.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Tool is the name the registry's Execute expects (e.g.
	// "steward-SaveRecommendation"). Required.
	Tool string `yaml:"tool" json:"tool"`

	// AllowedRoles optionally narrows who can dispatch this callback. When
	// empty, any authenticated caller is permitted.
	AllowedRoles []string `yaml:"allowedRoles,omitempty" json:"allowedRoles,omitempty"`

	// Payload declares how to render the tool's input args from the dispatch
	// context.
	Payload PayloadMapping `yaml:"payload" json:"payload"`
}

// PayloadMapping declares the Go-template that produces the tool's input
// args. The rendered output MUST be valid JSON; it is parsed into a
// map[string]interface{} and handed to Registry.Execute.
//
// Placeholders available to the template (see service/callback/render.go):
//
//	{{.eventName}}         callback event id
//	{{.conversationId}}    from the dispatch input
//	{{.turnId}}            from the dispatch input (optional)
//	{{.agentId}}           from the current conversation, when known
//	{{.agencyId}}          from conversation scope, when resolvable
//	{{.advertiserId}}      "
//	{{.campaignId}}        "
//	{{.adOrderId}}         "
//	{{.audienceId}}        "
//	{{.payload.*}}         raw forge submit body (nested access supported)
//	{{.selectedRows}}      convenience alias for .payload.selectedRows
//	{{.now}}               current timestamp (RFC3339)
//	{{.today}}             current date (YYYY-MM-DD)
//
// Template funcs: json, lower, upper, trim, default.
type PayloadMapping struct {
	// Body is the text/template source. When it does not begin with a
	// JSON-object opener `{`, the rendered output is wrapped as
	// `{"input": <rendered>}` before parsing — this supports bare scalar
	// tool inputs.
	Body string `yaml:"body" json:"body"`
}

// Validate reports structural problems with c (empty id, missing tool,
// missing payload body).
func (c *Callback) Validate() error {
	if c == nil {
		return errNilCallback
	}
	if c.ID == "" {
		return errEmptyID
	}
	if c.Tool == "" {
		return errEmptyTool
	}
	if c.Payload.Body == "" {
		return errEmptyPayload
	}
	return nil
}
