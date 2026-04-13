package sdk

import (
	"context"
	"net/http/httptest"

	"github.com/viant/agently-core/app/executor"
)

// NewLocalHTTPFromRuntime creates an endpoint-backed SDK client for a local runtime.
// It exposes the runtime through the standard SDK HTTP handler and returns a regular
// HTTPClient, so SDK callers exercise the same endpoint contract as remote clients.
func NewLocalHTTPFromRuntime(ctx context.Context, rt *executor.Runtime, opts ...HandlerOption) (*HTTPClient, func(), error) {
	backend, err := NewBackendFromRuntime(rt)
	if err != nil {
		return nil, nil, err
	}
	handler, err := NewHandlerWithContext(ctx, backend, opts...)
	if err != nil {
		return nil, nil, err
	}
	server := httptest.NewServer(handler)
	client, err := NewHTTP(server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		server.Close()
		return nil, nil, err
	}
	return client, server.Close, nil
}
