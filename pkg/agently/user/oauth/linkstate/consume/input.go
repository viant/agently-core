package consume

// Consume identifies the single-use state row to transition from pending to
// consumed, bound to its canonical owner and workspace session.
type Consume struct {
	StateHash       string `json:"stateHash,omitempty"`
	CanonicalUserID string `json:"canonicalUserId,omitempty"`
	SessionHash     string `json:"sessionHash,omitempty"`
	// Now anchors the expiry comparison and the consumed_at stamp to one
	// caller-supplied clock value (linkstate.TimeLayout, UTC).
	Now string `json:"now,omitempty"`
}

type Input struct {
	Consume *Consume `parameter:",kind=body,in=data"`
}
