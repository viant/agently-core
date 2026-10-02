package reportrun

import (
	"encoding/json"
	"time"
)

const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"

	MaterializerLegacyBrowser = "legacy_browser"
)

// Record is the durable browser materialization of an exact reporting
// snapshot. Completed snapshot fields are immutable at the service boundary.
type Record struct {
	ReportRunID      string          `json:"reportRunId"`
	OwnerID          string          `json:"ownerId"`
	ConversationID   string          `json:"conversationId,omitempty"`
	Materializer     string          `json:"materializer"`
	Origin           string          `json:"origin,omitempty"`
	BuilderRef       string          `json:"builderRef,omitempty"`
	PresetID         string          `json:"presetId,omitempty"`
	SourceKind       string          `json:"sourceKind,omitempty"`
	SourceID         string          `json:"sourceId,omitempty"`
	RequestedParams  json.RawMessage `json:"requestedParams,omitempty"`
	EffectiveParams  json.RawMessage `json:"effectiveParams,omitempty"`
	Status           string          `json:"status"`
	FailureCode      string          `json:"failureCode,omitempty"`
	FailureText      string          `json:"failureText,omitempty"`
	StartedAt        time.Time       `json:"startedAt"`
	CompletedAt      *time.Time      `json:"completedAt,omitempty"`
	Revision         int64           `json:"revision"`
	UIRunRequestID   string          `json:"uiRunRequestId"`
	ReportSpec       json.RawMessage `json:"reportSpec,omitempty"`
	ReportFill       json.RawMessage `json:"reportFill,omitempty"`
	ReportPrint      json.RawMessage `json:"reportPrint,omitempty"`
	ActivationSource string          `json:"activationSource,omitempty"`
	AdoptionSource   string          `json:"adoptionSource,omitempty"`
	ActorID          string          `json:"actorId,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}
