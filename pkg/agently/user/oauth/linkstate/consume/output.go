package consume

import (
	"github.com/viant/xdatly/handler/response"
	"github.com/viant/xdatly/handler/validator"
)

// Outcome values reported by the atomic consume transition. Only
// OutcomeConsumed grants the callback; the service adapter collapses every
// other outcome into one non-enumerable error and keeps the classification
// for audit records only.
const (
	OutcomeConsumed        = "consumed"
	OutcomeAbsent          = "absent"
	OutcomeExpired         = "expired"
	OutcomeAlreadyConsumed = "already_consumed"
	OutcomeUserMismatch    = "user_mismatch"
	OutcomeSessionMismatch = "session_mismatch"
)

type Output struct {
	response.Status `parameter:",kind=output,in=status" anonymous:"true"`
	Outcome         string                 `parameter:",kind=transient" json:"outcome,omitempty"`
	Violations      []*validator.Violation `parameter:",kind=transient"`
}

func (o *Output) setError(err error) { o.Status.Message = err.Error(); o.Status.Status = "error" }
