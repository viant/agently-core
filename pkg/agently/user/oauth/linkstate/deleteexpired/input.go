package deleteexpired

// Cleanup deletes every oauth_link_state row whose expiry is at or before the
// caller-supplied horizon (linkstate.TimeLayout, UTC). Consumed rows expire on
// the same horizon: their expires_at never moves.
type Cleanup struct {
	Before string `json:"before,omitempty"`
}

type Input struct {
	Cleanup *Cleanup `parameter:",kind=body,in=data"`
}
