package forecastbinding

import (
	"context"
	"strings"
	"time"

	"github.com/viant/agently-core/protocol/mcpname"
)

// ToolBindings is supplied by the host. The core evidence adapter does not
// bake application MCP names into its public contracts.
type ToolBindings struct {
	Profile    string
	Conversion string
	Data       string
}

func (b ToolBindings) Validate() error {
	names := []string{b.Profile, b.Conversion, b.Data}
	for _, name := range names {
		normalized := strings.ToLower(strings.TrimSpace(mcpname.Display(name)))
		if normalized == "" {
			return reject("invalid tool bindings")
		}
	}
	return nil
}

func (b ToolBindings) isProfile(tool string) bool    { return toolMatches(tool, b.Profile) }
func (b ToolBindings) isConversion(tool string) bool { return toolMatches(tool, b.Conversion) }
func (b ToolBindings) isData(tool string) bool       { return toolMatches(tool, b.Data) }

func toolMatches(actual, configured string) bool {
	return configured != "" && strings.EqualFold(mcpname.Display(actual), mcpname.Display(configured))
}

// SelectionOrigin describes the evidence for entity selection; it is assigned
// by the authenticated host, never copied from client or model-supplied JSON.
type SelectionOrigin string

const (
	SelectionUser         SelectionOrigin = "user-selection"
	SelectionToolEvidence SelectionOrigin = "tool-evidence"
)

// DateOrigin distinguishes verified structured intent from executed evidence.
type DateOrigin string

const (
	DateUserWindow       DateOrigin = "user-window"
	DateWorkspaceDefault DateOrigin = "workspace-default"
	DateToolEvidence     DateOrigin = "tool-evidence"
)

// Admission is a frozen server-side turn record. Owner/scope/time are supplied
// from authentication and turn state. Optional typed user selection is captured
// before intake; model-generated intake hints cannot populate this record.
type Admission struct {
	Scope
	StarterMessageID string          `json:"starterMessageId"`
	ReceivedAt       time.Time       `json:"receivedAt"`
	TimeZone         string          `json:"timeZone"`
	DateOrigin       DateOrigin      `json:"dateOrigin"`
	Dates            []string        `json:"dates"`
	SelectionOrigin  SelectionOrigin `json:"selectionOrigin"`
	EntityIDs        []int64         `json:"entityIds,omitempty"`
}

// PlanSources must be loaded through the host's authorized native store adapter.
// A client or model may name an op ID but cannot provide these trusted bodies.
type PlanSources struct {
	Profile    Call
	Conversion Call
	Tools      ToolBindings `json:"-"`
}

// ProducedPolicy includes source identity because target correctness must be
// checked before a valid request matrix can become an admitted plan.
type ProducedPolicy struct {
	Policy                 Policy
	EntityIDs              []int64
	ProducerVersion        string
	ProfileRequestHash     string
	ProfileResponseHash    string
	ConversionRequestHash  string
	ConversionResponseHash string
}

// PolicyProducer is injected by the host at composition time. It adapts the
// host's configured producer contract to the evidence plan without embedding
// application tool names or schema in this package.
// A nil/unregistered producer means unsupported policy, never unchecked fallback.
type PolicyProducer interface {
	Produce(context.Context, Admission, PlanSources) (*ProducedPolicy, error)
}

// SourceStore is the bounded read boundary for future writer integration.
// Implementations must validate owner+conversation+turn on every loaded call,
// including restart/resume; op IDs alone are not authorization.
type SourceStore interface {
	LoadCompletedCall(context.Context, Scope, string) (*Call, error)
	LoadAdmission(context.Context, Scope) (*Admission, error)
}

// Dependencies are startup-injected, per-host capabilities, not model tool input.
// This contract does not install writers or enable enforcement by itself.
type Dependencies struct {
	Producer PolicyProducer
	Store    SourceStore
	Tools    ToolBindings
}
