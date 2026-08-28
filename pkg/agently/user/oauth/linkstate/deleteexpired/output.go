package deleteexpired

import (
	"github.com/viant/xdatly/handler/response"
	"github.com/viant/xdatly/handler/validator"
)

type Output struct {
	response.Status `parameter:",kind=output,in=status" anonymous:"true"`
	// Deleted is the number of expired rows removed.
	Deleted int64 `parameter:",kind=transient" json:"deleted"`
	// OldestExpiresAt is the smallest expires_at among the deleted rows
	// (linkstate.TimeLayout); empty when nothing was removed. The maintenance
	// loop derives its oldest-expired age metric from it.
	OldestExpiresAt string                 `parameter:",kind=transient" json:"oldestExpiresAt,omitempty"`
	Violations      []*validator.Violation `parameter:",kind=transient"`
}

func (o *Output) setError(err error) { o.Status.Message = err.Error(); o.Status.Status = "error" }
