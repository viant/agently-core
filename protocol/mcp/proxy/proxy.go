package proxy

import (
	"context"
	"strings"

	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// Proxy wraps an MCP client to normalize tool names and provide simple helpers.
type Proxy struct {
	cli    mcpclient.Interface
	server string
}

// NewProxy constructs a proxy bound to a specific server name.
func NewProxy(_ context.Context, server string, cli mcpclient.Interface) (*Proxy, error) {
	if cli == nil {
		return nil, nil
	}
	return &Proxy{cli: cli, server: strings.TrimSpace(server)}, nil
}

// CallTool normalizes name and dispatches to the underlying client.
func (p *Proxy) CallTool(ctx context.Context, name string, args map[string]interface{}, opts ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	call := normalizeToolName(p.server, strings.TrimSpace(name))
	res, err := p.cli.CallTool(ctx, &mcpschema.CallToolRequestParams{Name: call, Arguments: args}, opts...)
	return res, err
}

// ResumeTool resubmits a July input-required tool call with the opaque request
// state and collected responses returned by the server.
func (p *Proxy) ResumeTool(ctx context.Context, name string, args, inputResponses map[string]interface{}, requestState string, opts ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	call := normalizeToolName(p.server, strings.TrimSpace(name))
	return p.cli.CallTool(ctx, &mcpschema.CallToolRequestParams{
		Name:           call,
		Arguments:      args,
		InputResponses: inputResponses,
		RequestState:   &requestState,
	}, opts...)
}

// ListAllTools returns all tools for the server by paging through cursors.
func (p *Proxy) ListAllTools(ctx context.Context, opts ...mcpclient.RequestOption) ([]mcpschema.Tool, error) {
	var (
		tools  []mcpschema.Tool
		cursor *string
	)
	for {
		res, err := p.cli.ListTools(ctx, cursor, opts...)
		if err != nil {
			return nil, err
		}
		tools = append(tools, res.Tools...)
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return tools, nil
}

func normalizeToolName(server, name string) string {
	if name == "" {
		return name
	}
	// Display names may carry a multi-segment service namespace before the
	// final method slash (for example inventory:planner/get_plan), while the
	// configured MCP key is flattened (inventory_planner). Compare canonical service
	// keys before stripping so the remote server receives only its local tool
	// name.
	if i := strings.LastIndexByte(name, '/'); i > 0 && serviceKeysEqual(server, name[:i]) {
		return name[i+1:]
	}
	// service:method → method (MCP expects method scoped to this server)
	if i := strings.IndexByte(name, ':'); i != -1 {
		if serviceKeysEqual(server, name[:i]) {
			return name[i+1:]
		}
		return name
	}
	// server/method → method (MCP expects method scoped to this server)
	if i := strings.IndexByte(name, '/'); i != -1 {
		// Only strip when the prefix matches our server; otherwise leave as-is
		if serviceKeysEqual(server, name[:i]) {
			return name[i+1:]
		}
		return name
	}
	// service-method canonical → method when prefix matches
	if i := strings.LastIndexByte(name, '-'); i != -1 {
		return name[i+1:]
	}
	return name
}

func serviceKeysEqual(left, right string) bool {
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		value = strings.NewReplacer("/", "_", ":", "_").Replace(value)
		return value
	}
	return normalize(left) == normalize(right)
}
