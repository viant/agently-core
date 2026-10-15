package resource

import (
	"context"

	service "github.com/viant/forge/backend/provider"
	identity "github.com/viant/forge/backend/resource"

	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type denyingWindowProvider struct {
	*fixtureProvider
	gets int
}

func (p *denyingWindowProvider) CallTool(ctx context.Context, r *schema.CallToolRequestParams, options ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	if r.Name == "windows/get" {
		p.gets++
		if p.gets == 2 {
			return nil, identity.ErrResourceDenied
		}
	}
	return p.fixtureProvider.CallTool(ctx, r, options...)
}

type localCatalogStub struct {
	summary service.WindowDefinitionSummary
}

func (localCatalogStub) AuthzReady() bool { return true }
func (s localCatalogStub) List(context.Context, *service.WindowDefinitionListInput) (*service.WindowDefinitionListOutput, error) {
	return &service.WindowDefinitionListOutput{Windows: []service.WindowDefinitionSummary{s.summary}}, nil
}
func (localCatalogStub) Get(context.Context, *service.WindowDefinitionGetInput) (*service.WindowDefinitionGetOutput, error) {
	return &service.WindowDefinitionGetOutput{WindowID: "local"}, nil
}
