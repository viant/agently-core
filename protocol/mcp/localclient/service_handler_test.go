package localclient

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/jsonrpc"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpuiproto "github.com/viant/mcp-ui/appproto"
	mcpuicap "github.com/viant/mcp-ui/capabilities"
	mcpuimeta "github.com/viant/mcp-ui/meta"
	mcpuiresource "github.com/viant/mcp-ui/resource"
)

// Local in-test MCP UI fixture identifiers — the generic localclient path is
// not coupled to any built-in demo runtime.
const (
	fixtureToolMethod  = "show_widget"
	fixtureResourceURI = "ui://test.fixture/view/widget"
	fixtureHTML        = "<html>fixture</html>"
)

func fixtureListResources() ([]mcpschema.Resource, error) {
	desc := "Local in-test MCP UI fixture resource."
	title := "Fixture Widget"
	res, err := mcpuiresource.NewHTMLResource(
		fixtureResourceURI,
		fixtureToolMethod,
		&desc,
		&title,
		nil,
		mcpuimeta.ResourceUI{
			ContentHash:     mcpuiresource.ContentHash(fixtureHTML),
			ProtocolVersion: mcpuiproto.Version,
			Fallback:        mcpuimeta.FallbackEmbedded,
		},
	)
	if err != nil {
		return nil, err
	}
	return []mcpschema.Resource{*res}, nil
}

func fixtureReadResource(uri string) (*mcpschema.ReadResourceResult, error) {
	if uri != fixtureResourceURI {
		return nil, fmt.Errorf("unknown ui resource uri: %s", uri)
	}
	contents, err := mcpuiresource.NewReadResultHTMLContents(
		fixtureResourceURI,
		fixtureHTML,
		mcpuimeta.ResourceUI{
			ContentHash:     mcpuiresource.ContentHash(fixtureHTML),
			ProtocolVersion: mcpuiproto.Version,
			Fallback:        mcpuimeta.FallbackEmbedded,
		},
	)
	if err != nil {
		return nil, err
	}
	return &mcpschema.ReadResourceResult{
		Contents: []mcpschema.ReadResourceResultContentsElem{*contents},
	}, nil
}

type testService struct{}

func (t *testService) Name() string { return "test/service" }
func (t *testService) Methods() svc.Signatures {
	return svc.Signatures{
		{Name: "list", Description: "public", Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&map[string]string{})},
		{Name: "topology", Description: "planner-only", Internal: true, Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&map[string]string{})},
	}
}
func (t *testService) Method(name string) (svc.Executable, error) {
	return func(ctx context.Context, input, output interface{}) error {
		if out, ok := output.(*map[string]string); ok {
			*out = map[string]string{"method": name}
		}
		return nil
	}, nil
}

type testMCPUIFixtureService struct{}

func (t *testMCPUIFixtureService) Name() string { return "test/mcpui" }
func (t *testMCPUIFixtureService) Methods() svc.Signatures {
	return svc.Signatures{
		{Name: fixtureToolMethod, Description: "fixture", Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&map[string]string{})},
	}
}
func (t *testMCPUIFixtureService) Method(name string) (svc.Executable, error) {
	if name != fixtureToolMethod {
		return nil, svc.NewMethodNotFoundError(name)
	}
	return func(ctx context.Context, input, output interface{}) error {
		out, ok := output.(*map[string]string)
		if !ok {
			return svc.NewInvalidOutputError(output)
		}
		*out = map[string]string{
			"status":      "ok",
			"resourceUri": fixtureResourceURI,
		}
		return nil
	}, nil
}
func (t *testMCPUIFixtureService) MCPUIToolUI(method string) (mcpuimeta.ToolUI, bool) {
	if method != fixtureToolMethod {
		return mcpuimeta.ToolUI{}, false
	}
	return mcpuimeta.ToolUI{
		ResourceUri: fixtureResourceURI,
		Fallback:    mcpuimeta.FallbackEmbedded,
	}, true
}
func (t *testMCPUIFixtureService) MCPListResources(ctx context.Context) ([]mcpschema.Resource, error) {
	return fixtureListResources()
}
func (t *testMCPUIFixtureService) MCPReadResource(ctx context.Context, uri string) (*mcpschema.ReadResourceResult, error) {
	return fixtureReadResource(uri)
}

func TestServiceHandler_ListTools_HidesInternalPlannerOnlyMethodsOutsidePlanMode(t *testing.T) {
	h := &serviceHandler{service: &testService{}}
	h.init()

	result := h.listTools(context.Background())
	var names []string
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	require.Contains(t, names, "list")
	require.NotContains(t, names, "topology")

	planCtx := runtimerequestctx.WithRequestMode(context.Background(), "plan")
	result = h.listTools(planCtx)
	names = names[:0]
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	require.Contains(t, names, "list")
	require.Contains(t, names, "topology")
}

func TestServiceHandler_ListResources_And_ReadResource_ForFixtureService(t *testing.T) {
	h := &serviceHandler{service: &testMCPUIFixtureService{}}
	h.init()

	list, jerr := h.ListResources(context.Background(), nil)
	require.Nil(t, jerr)
	require.Len(t, list.Resources, 1)
	require.Equal(t, fixtureResourceURI, list.Resources[0].Uri)

	readReq := &jsonrpc.TypedRequest[*mcpschema.ReadResourceRequest]{
		Request: &mcpschema.ReadResourceRequest{
			Params: mcpschema.ReadResourceRequestParams{Uri: fixtureResourceURI},
		},
	}
	read, jerr := h.ReadResource(context.Background(), readReq)
	require.Nil(t, jerr)
	require.Len(t, read.Contents, 1)
	require.NotNil(t, read.Contents[0].MimeType)
	require.Equal(t, "text/html;profile=mcp-app", *read.Contents[0].MimeType)
	require.NotEmpty(t, read.Contents[0].Text)
}

func TestServiceHandler_CallTool_BlocksInternalMethodsOutsidePlanMode(t *testing.T) {
	h := &serviceHandler{service: &testService{}}
	h.init()

	req := &jsonrpc.TypedRequest[*mcpschema.CallToolRequest]{
		Request: &mcpschema.CallToolRequest{
			Params: mcpschema.CallToolRequestParams{
				Name: "topology",
			},
		},
	}

	result, jerr := h.CallTool(context.Background(), req)
	require.Nil(t, result)
	require.NotNil(t, jerr)
	require.Contains(t, jerr.Message, "Unknown tool")
}

func TestServiceHandler_CallTool_AllowsInternalMethodsInPlanMode(t *testing.T) {
	h := &serviceHandler{service: &testService{}}
	h.init()

	req := &jsonrpc.TypedRequest[*mcpschema.CallToolRequest]{
		Request: &mcpschema.CallToolRequest{
			Params: mcpschema.CallToolRequestParams{
				Name: "test/service:topology",
			},
		},
	}

	result, jerr := h.CallTool(runtimerequestctx.WithRequestMode(context.Background(), "plan"), req)
	require.Nil(t, jerr)
	require.NotNil(t, result)
	require.NotNil(t, result.StructuredContent)
	structured, ok := result.StructuredContent.(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "topology", structured["method"])
}

func TestServiceHandler_CallTool_EmbedsFallbackOnlyWithoutUICapability(t *testing.T) {
	h := &serviceHandler{service: &testMCPUIFixtureService{}}
	h.init()
	req := &jsonrpc.TypedRequest[*mcpschema.CallToolRequest]{
		Request: &mcpschema.CallToolRequest{
			Params: mcpschema.CallToolRequestParams{
				Name: fixtureToolMethod,
			},
		},
	}

	result, jerr := h.CallTool(context.Background(), req)
	require.Nil(t, jerr)
	require.Len(t, result.Content, 2)

	clientCaps := mcpschema.ClientCapabilities{}
	mcpuicap.SetClientCapability(&clientCaps, mcpuicap.Capability{
		ProtocolVersion: "1.0.0",
		MimeTypes:       []string{mcpuicap.ResourceMimeType},
	})
	h.Initialize(context.Background(), &mcpschema.InitializeRequestParams{Capabilities: clientCaps}, nil)

	result, jerr = h.CallTool(context.Background(), req)
	require.Nil(t, jerr)
	require.Len(t, result.Content, 1)
}
