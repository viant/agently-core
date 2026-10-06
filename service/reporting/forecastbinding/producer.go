package forecastbinding

import (
	"context"
	"time"
)

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
	AudienceIDs      []int64         `json:"audienceIds,omitempty"`
}

// PlanSources must be loaded through the host's authorized native store adapter.
// A client or model may name an op ID but cannot provide these trusted bodies.
type PlanSources struct {
	Profile    Call
	Conversion Call
}

// ProducedPolicy includes source identity because target correctness must be
// checked before a valid request matrix can become an admitted plan.
type ProducedPolicy struct {
	Policy                 Policy
	AudienceIDs            []int64
	ProducerVersion        string
	ProfileRequestHash     string
	ProfileResponseHash    string
	ConversionRequestHash  string
	ConversionResponseHash string
}

// PolicyProducer is injected by the host at composition time. Implementations
// use the authoritative targeting converter/profile projection. Core deliberately
// does not import a private Steward package or infer mappings from cube calls.
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
}
