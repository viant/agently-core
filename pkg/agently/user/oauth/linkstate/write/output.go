package write

import (
	"github.com/viant/xdatly/handler/response"
	"github.com/viant/xdatly/handler/validator"
)

type Output struct {
	response.Status `parameter:",kind=output,in=status" anonymous:"true"`
	// Data is the stored flow row: the caller's row when it won creation, the
	// concurrent owner's row otherwise.
	Data *LinkState `parameter:",kind=body"`
	// Created reports whether this call created (or replaced a consumed/
	// expired) flow row. The creator owns the flow and may return the
	// authorization URL; every other caller must treat the flow as pending.
	Created    bool                   `parameter:",kind=transient" json:"created"`
	Violations []*validator.Violation `parameter:",kind=transient"`
}

func (o *Output) setError(err error) { o.Status.Message = err.Error(); o.Status.Status = "error" }
