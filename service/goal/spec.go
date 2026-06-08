package goal

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ContinueMode selects how an active goal is allowed to continue.
type ContinueMode string

const (
	// ContinueModeIdleOnly permits the controller to enqueue a continuation
	// only when the conversation is idle.
	ContinueModeIdleOnly ContinueMode = "idle_only"
	// ContinueModeManualOnly disables autonomous continuation; the goal still
	// exists and accounts usage, but only user/runtime action advances it.
	ContinueModeManualOnly ContinueMode = "manual_only"
)

// TurnPolicy controls what happens after a turn finishes for an active goal.
type TurnPolicy string

const (
	TurnPolicyEvaluate TurnPolicy = "evaluate"
	TurnPolicyWait     TurnPolicy = "wait"
)

// AsyncPolicy controls what happens after an async operation completes.
type AsyncPolicy string

const (
	AsyncPolicyEvaluate AsyncPolicy = "evaluate"
	AsyncPolicyWait     AsyncPolicy = "wait"
)

// ControllerSpec is the persisted, runtime-owned policy block for a goal. It is
// stored as JSON in the goal.controller_spec column. A nil spec means the goal
// is not autonomous.
type ControllerSpec struct {
	ContinueMode             ContinueMode `json:"continueMode"`
	OnTurnFinished           TurnPolicy   `json:"onTurnFinished"`
	OnAsyncCompleted         AsyncPolicy  `json:"onAsyncCompleted"`
	MaxAutonomousTurns       *int         `json:"maxAutonomousTurns,omitempty"`
	MaxConsecutiveNoProgress *int         `json:"maxConsecutiveNoProgress,omitempty"`
}

// Validate rejects unknown enum values. Empty enum values are not permitted:
// a spec exists only when the goal is explicitly configured.
func (s *ControllerSpec) Validate() error {
	switch s.ContinueMode {
	case ContinueModeIdleOnly, ContinueModeManualOnly:
	default:
		return fmt.Errorf("unknown continueMode %q", s.ContinueMode)
	}
	switch s.OnTurnFinished {
	case TurnPolicyEvaluate, TurnPolicyWait:
	default:
		return fmt.Errorf("unknown onTurnFinished %q", s.OnTurnFinished)
	}
	switch s.OnAsyncCompleted {
	case AsyncPolicyEvaluate, AsyncPolicyWait:
	default:
		return fmt.Errorf("unknown onAsyncCompleted %q", s.OnAsyncCompleted)
	}
	if s.MaxAutonomousTurns != nil && *s.MaxAutonomousTurns < 0 {
		return fmt.Errorf("maxAutonomousTurns must not be negative")
	}
	if s.MaxConsecutiveNoProgress != nil && *s.MaxConsecutiveNoProgress < 0 {
		return fmt.Errorf("maxConsecutiveNoProgress must not be negative")
	}
	return nil
}

// Encode serialises the spec to the JSON form stored in controller_spec.
func (s *ControllerSpec) Encode() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("encode controller spec: %w", err)
	}
	return string(data), nil
}

// DecodeControllerSpec parses the controller_spec column value. An empty value
// yields a nil spec (the goal is not autonomous). A malformed or invalid value
// is an error; the runtime does not fall back to a default policy.
func DecodeControllerSpec(raw string) (*ControllerSpec, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	spec := &ControllerSpec{}
	if err := json.Unmarshal([]byte(raw), spec); err != nil {
		return nil, fmt.Errorf("decode controller spec: %w", err)
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return spec, nil
}
