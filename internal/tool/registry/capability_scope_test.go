package tool

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"testing"
)

type capabilityScopeManager struct{ discoveryManagerStub }

func (*capabilityScopeManager) Options(_ context.Context, name string) (*cfg.MCPClient, error) {
	visibility := cfg.ToolsListVisibilityPrivate
	if name == "shared" {
		visibility = cfg.ToolsListVisibilityPublic
	}
	return &cfg.MCPClient{ToolsListVisibility: visibility}, nil
}
func TestCapabilityScopesUseDeclaredVisibilityRatherThanEndpointReachability(t *testing.T) {
	registry := &Registry{mgr: &capabilityScopeManager{}, virtualDefs: map[string]llm.ToolDefinition{"session:inspect": {Name: "session:inspect"}}}
	scopes := registry.CapabilityCacheScopes(context.Background(), []llm.ToolDefinition{
		{Name: "management:list", SourceMCPServer: "management"},
		{Name: "shared:list", SourceMCPServer: "shared"},
		{Name: "session:inspect"},
		{Name: "native:help"},
	})
	require.Equal(t, "user", scopes["management:list"])
	require.Equal(t, "public", scopes["shared:list"])
	require.Equal(t, "conversation", scopes["session:inspect"])
	require.Equal(t, "public", scopes["native:help"])
}
