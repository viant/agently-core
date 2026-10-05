package sdk

import (
	"context"
	"fmt"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
)

// Handler spies use an explicit native test backend. The outward HTTP client
// deliberately no longer implements the server's native streaming contract.
type httpTestBackend struct{ *HTTPClient }

func (httpTestBackend) StreamEvents(context.Context, *StreamEventsInput) (streaming.Subscription, error) {
	return nil, fmt.Errorf("unexpected native stream in handler spy")
}
func (httpTestBackend) Query(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
	return nil, fmt.Errorf("unexpected native query in handler spy")
}
