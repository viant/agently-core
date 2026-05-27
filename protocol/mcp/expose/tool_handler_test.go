package expose

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/jsonrpc"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpuiproto "github.com/viant/mcp-ui/appproto"
	mcpuicap "github.com/viant/mcp-ui/capabilities"
	mcpuimeta "github.com/viant/mcp-ui/meta"
	mcpuiresource "github.com/viant/mcp-ui/resource"
)

// Local fixture identifiers — kept inside the test file so the generic MCP
// expose path is no longer coupled to any built-in demo runtime helpers.
const (
	fixtureToolName    = "fixture/show_widget"
	fixtureResourceURI = "ui://test.fixture/view/widget"
	fixtureHTML        = "<html>fixture</html>"
)

func fixtureListResources() ([]mcpschema.Resource, error) {
	desc := "Local in-test MCP UI fixture resource."
	title := "Fixture Widget"
	res, err := mcpuiresource.NewHTMLResource(
		fixtureResourceURI,
		fixtureToolName,
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

type stubCore struct {
	defs []llm.ToolDefinition
}

func (s *stubCore) ToolDefinitions() []llm.ToolDefinition { return s.defs }

type stubExec struct {
	core LLMCore
}

func (s *stubExec) LLMCore() LLMCore { return s.core }
func (s *stubExec) ExecuteTool(ctx context.Context, name string, args map[string]interface{}, timeoutSec int) (interface{}, error) {
	return nil, nil
}
func (s *stubExec) ListResources(ctx context.Context) ([]mcpschema.Resource, error) {
	return fixtureListResources()
}
func (s *stubExec) ReadResource(ctx context.Context, uri string) (*mcpschema.ReadResourceResult, error) {
	return fixtureReadResource(uri)
}

type fallbackExec struct {
	core     LLMCore
	fallback string
	read     *mcpschema.ReadResourceResult
	output   interface{}
}

func (f *fallbackExec) LLMCore() LLMCore { return f.core }
func (f *fallbackExec) ExecuteTool(ctx context.Context, name string, args map[string]interface{}, timeoutSec int) (interface{}, error) {
	return f.output, nil
}
func (f *fallbackExec) ListResources(ctx context.Context) ([]mcpschema.Resource, error) {
	return fixtureListResources()
}
func (f *fallbackExec) ReadResource(ctx context.Context, uri string) (*mcpschema.ReadResourceResult, error) {
	return f.read, nil
}
func (f *fallbackExec) MCPUIToolUI(method string) (mcpuimeta.ToolUI, bool) {
	if method == "" {
		return mcpuimeta.ToolUI{}, false
	}
	return mcpuimeta.ToolUI{
		ResourceUri: fixtureResourceURI,
		Fallback:    f.fallback,
	}, true
}

func TestResolveToolName(t *testing.T) {
	testCases := []struct {
		description string
		raw         string
		defs        []llm.ToolDefinition
		expected    string
		found       bool
		hasError    bool
	}{
		{
			description: "full name exact match",
			raw:         "system/exec:execute",
			defs: []llm.ToolDefinition{
				{Name: "system/exec:execute"},
			},
			expected: "system/exec:execute",
			found:    true,
		},
		{
			description: "method only resolves unique",
			raw:         "execute",
			defs: []llm.ToolDefinition{
				{Name: "system/exec:execute"},
				{Name: "system/patch:apply"},
			},
			expected: "system/exec:execute",
			found:    true,
		},
		{
			description: "method only not found",
			raw:         "missing",
			defs: []llm.ToolDefinition{
				{Name: "system/exec:execute"},
			},
			found: false,
		},
		{
			description: "method only ambiguous",
			raw:         "execute",
			defs: []llm.ToolDefinition{
				{Name: "system/exec:execute"},
				{Name: "other/exec:execute"},
			},
			hasError: true,
		},
	}

	for _, tc := range testCases {
		actual, found, err := resolveToolName(tc.raw, tc.defs)
		if tc.hasError {
			assert.NotNil(t, err, tc.description)
			continue
		}
		assert.Nil(t, err, tc.description)
		assert.EqualValues(t, tc.found, found, tc.description)
		assert.EqualValues(t, tc.expected, actual, tc.description)
	}
}

func TestToolAllowed(t *testing.T) {
	testCases := []struct {
		description string
		patterns    []string
		name        string
		expected    bool
	}{
		{
			description: "service only matches any method",
			patterns:    []string{"system/exec"},
			name:        "system/exec:execute",
			expected:    true,
		},
		{
			description: "service only does not match other service",
			patterns:    []string{"system/exec"},
			name:        "system/patch:apply",
			expected:    false,
		},
		{
			description: "short service matches",
			patterns:    []string{"orchestration"},
			name:        "orchestration:updatePlan",
			expected:    true,
		},
		{
			description: "wildcard matches prefix",
			patterns:    []string{"system/*"},
			name:        "system/exec:execute",
			expected:    true,
		},
	}

	for _, tc := range testCases {
		actual := toolAllowed(tc.patterns, tc.name)
		assert.EqualValues(t, tc.expected, actual, tc.description)
	}
}

func TestMcpToolFromDefinition(t *testing.T) {
	testCases := []struct {
		description string
		def         *llm.ToolDefinition
		expected    *mcpschema.Tool
	}{
		{
			description: "basic conversion includes required and properties",
			def: &llm.ToolDefinition{
				Name:        "system/exec:execute",
				Description: "exec",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{"type": "string"},
					},
				},
				Required: []string{"command"},
				OutputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"status": map[string]interface{}{"type": "integer"},
					},
				},
			},
			expected: &mcpschema.Tool{
				Name:        "system/exec:execute",
				Description: ptr("exec"),
				InputSchema: mcpschema.ToolInputSchema{
					Type:       "object",
					Properties: mcpschema.ToolInputSchemaProperties(map[string]map[string]interface{}{"command": {"type": "string"}}),
					Required:   []string{"command"},
				},
				OutputSchema: &mcpschema.ToolOutputSchema{
					Type:       "object",
					Properties: map[string]map[string]interface{}{"status": {"type": "integer"}},
				},
			},
		},
	}

	for _, tc := range testCases {
		actual := mcpToolFromDefinition(tc.def)
		assert.EqualValues(t, tc.expected, actual, tc.description)
	}
}

func ptr(s string) *string { return &s }

func TestToolHandler_ListResources_ListsFixtureUIResource(t *testing.T) {
	h := NewToolHandler(&stubExec{core: &stubCore{}}, nil)
	result, jerr := h.ListResources(context.Background(), nil)
	if jerr != nil {
		t.Fatalf("ListResources returned error: %v", jerr)
	}
	if len(result.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %#v", result.Resources)
	}
	if result.Resources[0].Uri != fixtureResourceURI {
		t.Fatalf("unexpected uri: %#v", result.Resources[0])
	}
}

func TestToolHandler_ReadResource_ReadsFixtureUIResource(t *testing.T) {
	h := NewToolHandler(&stubExec{core: &stubCore{}}, nil)
	req := &jsonrpc.TypedRequest[*mcpschema.ReadResourceRequest]{
		Request: &mcpschema.ReadResourceRequest{
			Params: mcpschema.ReadResourceRequestParams{Uri: fixtureResourceURI},
		},
	}
	result, jerr := h.ReadResource(context.Background(), req)
	if jerr != nil {
		t.Fatalf("ReadResource returned error: %v", jerr)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 contents entry, got %#v", result.Contents)
	}
	if result.Contents[0].MimeType == nil || *result.Contents[0].MimeType != "text/html;profile=mcp-app" {
		t.Fatalf("unexpected mime type: %#v", result.Contents[0])
	}
	if result.Contents[0].Uri != fixtureResourceURI || result.Contents[0].Text == "" {
		t.Fatalf("unexpected contents: %#v", result.Contents[0])
	}
}

func TestToolHandler_CallTool_EmbedsFallbackOnlyWithoutUICapability(t *testing.T) {
	mime := mcpuicap.ResourceMimeType
	read := &mcpschema.ReadResourceResult{
		Contents: []mcpschema.ReadResourceResultContentsElem{{
			Uri:      fixtureResourceURI,
			MimeType: &mime,
			Text:     fixtureHTML,
		}},
	}
	mcpuimeta.SetReadResultContentsUI(&read.Contents[0], mcpuimeta.ResourceUI{
		Fallback:        mcpuimeta.FallbackEmbedded,
		ContentHash:     mcpuiresource.ContentHash(fixtureHTML),
		ProtocolVersion: "1.0.0",
	})
	exec := &fallbackExec{
		core:     &stubCore{defs: []llm.ToolDefinition{{Name: fixtureToolName}}},
		fallback: mcpuimeta.FallbackEmbedded,
		read:     read,
		output: map[string]interface{}{
			"resourceUri": fixtureResourceURI,
			"status":      "ok",
		},
	}
	h := NewToolHandler(exec, []string{"fixture/*"})
	req := &jsonrpc.TypedRequest[*mcpschema.CallToolRequest]{
		Request: &mcpschema.CallToolRequest{
			Params: mcpschema.CallToolRequestParams{Name: fixtureToolName},
		},
	}

	result, jerr := h.CallTool(context.Background(), req)
	if jerr != nil {
		t.Fatalf("CallTool returned error: %v", jerr)
	}
	if len(result.Content) != 2 {
		t.Fatalf("expected embedded fallback content when UI capability absent, got %#v", result.Content)
	}

	clientCaps := mcpschema.ClientCapabilities{}
	mcpuicap.SetClientCapability(&clientCaps, mcpuicap.Capability{
		ProtocolVersion: "1.0.0",
		MimeTypes:       []string{mcpuicap.ResourceMimeType},
	})
	h.Initialize(context.Background(), &mcpschema.InitializeRequestParams{Capabilities: clientCaps}, nil)

	result, jerr = h.CallTool(context.Background(), req)
	if jerr != nil {
		t.Fatalf("CallTool with UI capability returned error: %v", jerr)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected no embedded fallback when UI capability negotiated, got %#v", result.Content)
	}
}

func TestToolHandler_CallTool_DoesNotEmbedWithoutExplicitFallback(t *testing.T) {
	mime := mcpuicap.ResourceMimeType
	read := &mcpschema.ReadResourceResult{
		Contents: []mcpschema.ReadResourceResultContentsElem{{
			Uri:      fixtureResourceURI,
			MimeType: &mime,
			Text:     fixtureHTML,
		}},
	}
	mcpuimeta.SetReadResultContentsUI(&read.Contents[0], mcpuimeta.ResourceUI{
		ContentHash:     mcpuiresource.ContentHash(fixtureHTML),
		ProtocolVersion: "1.0.0",
	})
	h := NewToolHandler(&fallbackExec{
		core: &stubCore{defs: []llm.ToolDefinition{{Name: fixtureToolName}}},
		read: read,
		output: map[string]interface{}{
			"resourceUri": fixtureResourceURI,
			"status":      "ok",
		},
	}, []string{"fixture/*"})
	req := &jsonrpc.TypedRequest[*mcpschema.CallToolRequest]{
		Request: &mcpschema.CallToolRequest{
			Params: mcpschema.CallToolRequestParams{Name: fixtureToolName},
		},
	}
	result, jerr := h.CallTool(context.Background(), req)
	if jerr != nil {
		t.Fatalf("CallTool returned error: %v", jerr)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected no embedded fallback without explicit embedded opt-in, got %#v", result.Content)
	}
}
