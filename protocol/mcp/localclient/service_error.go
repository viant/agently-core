package localclient

import (
	"context"
	"sync"

	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client"
)

// In-process services retain their original Go error without changing MCP's
// ordinary IsError/text response. Only this invocation's trusted handler can
// fill the carrier; remote response text never establishes an error identity.
type serviceErrorKey struct{}
type serviceError struct {
	owner *serviceHandler
	mu    sync.Mutex
	err   error
}
type serviceClient struct {
	client.Interface
	handler *serviceHandler
}

func (c *serviceClient) CallTool(ctx context.Context, params *schema.CallToolRequestParams, options ...client.RequestOption) (*schema.CallToolResult, error) {
	state := &serviceError{owner: c.handler}
	result, err := c.Interface.CallTool(context.WithValue(ctx, serviceErrorKey{}, state), params, options...)
	state.mu.Lock()
	original := state.err
	state.mu.Unlock()
	if original != nil {
		return nil, original
	}
	return result, err
}
func (h *serviceHandler) preserveError(ctx context.Context, err error) {
	state, _ := ctx.Value(serviceErrorKey{}).(*serviceError)
	if state == nil || state.owner != h {
		return
	}
	state.mu.Lock()
	state.err = err
	state.mu.Unlock()
}
