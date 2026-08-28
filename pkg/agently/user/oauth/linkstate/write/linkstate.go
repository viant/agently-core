package write

// LinkState is the writable oauth_link_state entity. Timestamps travel as
// strings in the shared linkstate.TimeLayout (UTC) so SQLite and MySQL writes
// and comparisons behave identically. The table stores non-secret hashes and
// flow metadata only.
type LinkState struct {
	StateHash   string  `sqlx:"state_hash,primaryKey" json:"stateHash,omitempty"`
	FlowHash    string  `sqlx:"flow_hash" json:"flowHash,omitempty"`
	UserID      string  `sqlx:"user_id" json:"userId,omitempty"`
	SessionHash string  `sqlx:"session_hash" json:"sessionHash,omitempty"`
	Provider    string  `sqlx:"provider" json:"provider,omitempty"`
	ExpiresAt   string  `sqlx:"expires_at" json:"expiresAt,omitempty"`
	ConsumedAt  *string `sqlx:"consumed_at" json:"consumedAt,omitempty"`
	CreatedAt   string  `sqlx:"created_at" json:"createdAt,omitempty"`
	// Now anchors every pending/expired comparison of this request to one
	// caller-supplied clock value (linkstate.TimeLayout, UTC). It is not a
	// column.
	Now string `sqlx:"-" json:"now,omitempty"`

	Has *LinkStateHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type LinkStateHas struct {
	StateHash   bool
	FlowHash    bool
	UserID      bool
	SessionHash bool
	Provider    bool
	ExpiresAt   bool
	ConsumedAt  bool
	CreatedAt   bool
	Now         bool
}

func (s *LinkState) ensureHas() {
	if s.Has == nil {
		s.Has = &LinkStateHas{}
	}
}

func (s *LinkState) SetStateHash(v string) { s.StateHash = v; s.ensureHas(); s.Has.StateHash = true }
func (s *LinkState) SetFlowHash(v string)  { s.FlowHash = v; s.ensureHas(); s.Has.FlowHash = true }
func (s *LinkState) SetUserID(v string)    { s.UserID = v; s.ensureHas(); s.Has.UserID = true }
func (s *LinkState) SetSessionHash(v string) {
	s.SessionHash = v
	s.ensureHas()
	s.Has.SessionHash = true
}
func (s *LinkState) SetProvider(v string)  { s.Provider = v; s.ensureHas(); s.Has.Provider = true }
func (s *LinkState) SetExpiresAt(v string) { s.ExpiresAt = v; s.ensureHas(); s.Has.ExpiresAt = true }
func (s *LinkState) SetConsumedAt(v string) {
	s.ConsumedAt = &v
	s.ensureHas()
	s.Has.ConsumedAt = true
}
func (s *LinkState) SetCreatedAt(v string) { s.CreatedAt = v; s.ensureHas(); s.Has.CreatedAt = true }
func (s *LinkState) SetNow(v string)       { s.Now = v; s.ensureHas(); s.Has.Now = true }
