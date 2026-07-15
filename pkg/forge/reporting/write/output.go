package write

import (
	forgereport "github.com/viant/agently-core/pkg/forge/reporting"
	"github.com/viant/xdatly/handler/response"
)

type Output struct {
	response.Status `parameter:",kind=output,in=status" anonymous:"true"`
	Data            *forgereport.SharedArtifact `parameter:",kind=body"`
}

func (o *Output) setError(err error) {
	o.Status.Message = err.Error()
	o.Status.Status = "error"
}
