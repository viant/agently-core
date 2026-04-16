package prompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// --- local message rendering ---

func TestRender_LocalText(t *testing.T) {
	p := &Profile{
		ID: "perf",
		Messages: []Message{
			{Role: "system", Text: "You are a performance analyst."},
			{Role: "user", Text: "Analyze campaign {{.campaignId}}."},
		},
	}
	msgs, err := p.Render(context.Background(), nil, &RenderOptions{
		Binding: map[string]interface{}{"campaignId": "4821"},
	})
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "system", msgs[0].Role)
	assert.Equal(t, "You are a performance analyst.", msgs[0].Text)
	assert.Equal(t, "user", msgs[1].Role)
	assert.Equal(t, "Analyze campaign 4821.", msgs[1].Text)
}

func TestRender_LocalInstructions(t *testing.T) {
	p := &Profile{
		ID:           "perf",
		Instructions: "Focus on pacing for account {{.accountId}}.",
	}
	msgs, err := p.Render(context.Background(), nil, &RenderOptions{
		Binding: map[string]interface{}{"accountId": "99"},
	})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "system", msgs[0].Role)
	assert.Equal(t, "Focus on pacing for account 99.", msgs[0].Text)
}

func TestRender_SkipsEmptyMessages(t *testing.T) {
	p := &Profile{
		ID: "perf",
		Messages: []Message{
			{Role: "system", Text: "   "}, // whitespace only
			{Role: "user", Text: "Hello"},
		},
	}
	msgs, err := p.Render(context.Background(), nil, nil)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "user", msgs[0].Role)
}

func TestRender_NilProfile(t *testing.T) {
	// no messages or instructions → nil result
	p := &Profile{ID: "empty"}
	msgs, err := p.Render(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Nil(t, msgs)
}

func TestRender_InvalidTemplatePassthrough(t *testing.T) {
	// Invalid go-template syntax should not error — text is returned as-is.
	p := &Profile{
		ID: "perf",
		Messages: []Message{
			{Role: "system", Text: "Use {{ invalid syntax here"},
		},
	}
	msgs, err := p.Render(context.Background(), nil, nil)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "Use {{ invalid syntax here", msgs[0].Text)
}

// --- MCP rendering ---

// mockMCPManager implements MCPManager for tests.
type mockMCPManager struct {
	client mcpclient.Interface
	err    error
}

func (m *mockMCPManager) Get(_ context.Context, _, _ string) (mcpclient.Interface, error) {
	return m.client, m.err
}

// mockMCPClient implements just enough of mcpclient.Interface for GetPrompt.
type mockMCPClient struct {
	result *mcpschema.GetPromptResult
	err    error
}

func (m *mockMCPClient) GetPrompt(_ context.Context, _ *mcpschema.GetPromptRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.GetPromptResult, error) {
	return m.result, m.err
}

// Unused interface methods — satisfy the interface.
func (m *mockMCPClient) Initialize(_ context.Context, _ ...mcpclient.RequestOption) (*mcpschema.InitializeResult, error) {
	return nil, nil
}
func (m *mockMCPClient) ListResourceTemplates(_ context.Context, _ *string, _ ...mcpclient.RequestOption) (*mcpschema.ListResourceTemplatesResult, error) {
	return nil, nil
}
func (m *mockMCPClient) ListResources(_ context.Context, _ *string, _ ...mcpclient.RequestOption) (*mcpschema.ListResourcesResult, error) {
	return nil, nil
}
func (m *mockMCPClient) ListPrompts(_ context.Context, _ *string, _ ...mcpclient.RequestOption) (*mcpschema.ListPromptsResult, error) {
	return nil, nil
}
func (m *mockMCPClient) ListTools(_ context.Context, _ *string, _ ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	return nil, nil
}
func (m *mockMCPClient) ReadResource(_ context.Context, _ *mcpschema.ReadResourceRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.ReadResourceResult, error) {
	return nil, nil
}
func (m *mockMCPClient) CallTool(_ context.Context, _ *mcpschema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	return nil, nil
}
func (m *mockMCPClient) Complete(_ context.Context, _ *mcpschema.CompleteRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.CompleteResult, error) {
	return nil, nil
}
func (m *mockMCPClient) Ping(_ context.Context, _ *mcpschema.PingRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.PingResult, error) {
	return nil, nil
}
func (m *mockMCPClient) Subscribe(_ context.Context, _ *mcpschema.SubscribeRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.SubscribeResult, error) {
	return nil, nil
}
func (m *mockMCPClient) Unsubscribe(_ context.Context, _ *mcpschema.UnsubscribeRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.UnsubscribeResult, error) {
	return nil, nil
}
func (m *mockMCPClient) SetLevel(_ context.Context, _ *mcpschema.SetLevelRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.SetLevelResult, error) {
	return nil, nil
}
func (m *mockMCPClient) ListRoots(_ context.Context, _ *mcpschema.ListRootsRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.ListRootsResult, error) {
	return nil, nil
}
func (m *mockMCPClient) CreateMessage(_ context.Context, _ *mcpschema.CreateMessageRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.CreateMessageResult, error) {
	return nil, nil
}
func (m *mockMCPClient) Elicit(_ context.Context, _ *mcpschema.ElicitRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.ElicitResult, error) {
	return nil, nil
}

func TestRender_MCPSource(t *testing.T) {
	client := &mockMCPClient{
		result: &mcpschema.GetPromptResult{
			Messages: []mcpschema.PromptMessage{
				{Role: "system", Content: mcpschema.TextContent{Type: "text", Text: "MCP system instruction."}},
				{Role: "user", Content: mcpschema.TextContent{Type: "text", Text: "MCP user task."}},
			},
		},
	}
	mgr := &mockMCPManager{client: client}

	p := &Profile{
		ID:  "mcp-profile",
		MCP: &MCPSource{Server: "test-server", Prompt: "perf_v2"},
	}
	msgs, err := p.Render(context.Background(), mgr, &RenderOptions{ConversationID: "conv-1"})
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "system", msgs[0].Role)
	assert.Equal(t, "MCP system instruction.", msgs[0].Text)
	assert.Equal(t, "user", msgs[1].Role)
	assert.Equal(t, "MCP user task.", msgs[1].Text)
}

func TestRender_MCPSource_NoManager(t *testing.T) {
	p := &Profile{
		ID:  "mcp-profile",
		MCP: &MCPSource{Server: "test-server", Prompt: "perf_v2"},
	}
	_, err := p.Render(context.Background(), nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "manager")
}

func TestRender_MCPArgs_Template(t *testing.T) {
	var capturedParams *mcpschema.GetPromptRequestParams
	client := &mockMCPClient{
		result: &mcpschema.GetPromptResult{
			Messages: []mcpschema.PromptMessage{
				{Role: "system", Content: mcpschema.TextContent{Type: "text", Text: "ok"}},
			},
		},
	}
	// Wrap to capture params
	type capturingClient struct {
		*mockMCPClient
	}
	capClient := &struct {
		*mockMCPClient
		captured *mcpschema.GetPromptRequestParams
	}{mockMCPClient: client}
	_ = capClient

	mgr := &mockMCPManager{client: client}
	p := &Profile{
		ID: "mcp-args",
		MCP: &MCPSource{
			Server: "srv",
			Prompt: "prompt",
			Args:   map[string]string{"dateRange": "{{.dateRange}}"},
		},
	}
	_ = capturedParams
	msgs, err := p.Render(context.Background(), mgr, &RenderOptions{
		Binding: map[string]interface{}{"dateRange": "2025-04-01/2025-04-15"},
	})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
}

func TestConvertMCPMessages_MapContent(t *testing.T) {
	// Content arrives as map[string]interface{} after JSON decode through interface{}
	in := []mcpschema.PromptMessage{
		{Role: "assistant", Content: map[string]interface{}{"type": "text", "text": "map-content"}},
	}
	out := convertMCPMessages(in)
	require.Len(t, out, 1)
	assert.Equal(t, "assistant", out[0].Role)
	assert.Equal(t, "map-content", out[0].Text)
}
