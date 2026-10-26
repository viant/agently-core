package sdk

import (
	"context"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/sdk/api"
)

// FetchPublishedDatasource uses the existing datasource service's MCP tool
// executor with an exact server-selected descriptor. It bypasses only the
// legacy metadata resolution path; business data authorization stays upstream.
func (c *backendClient) FetchPublishedDatasource(ctx context.Context, descriptor *dsproto.DataSource, inputs map[string]interface{}, windowParameters map[string]any) (*api.FetchDatasourceOutput, error) {
	if c == nil || c.datasourceSvc == nil {
		return nil, ErrDatasourceStackNotConfigured
	}
	result, err := c.datasourceSvc.FetchPublished(ctx, descriptor, inputs, windowParameters)
	if err != nil {
		return nil, err
	}
	return toAPIFetchOutput(result), nil
}
func NewPublishedWindowBindingsForBackend(open PublishedWindowOpen, client Backend) *PublishedWindowBindings {
	executor, ok := client.(interface {
		FetchPublishedDatasource(context.Context, *dsproto.DataSource, map[string]interface{}, map[string]any) (*api.FetchDatasourceOutput, error)
	})
	if !ok {
		return NewPublishedWindowBindings(open, nil, nil)
	}
	return NewPublishedWindowBindings(open, executor.FetchPublishedDatasource, nil)
}
