package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcpname"
	schema "github.com/viant/mcp-protocol/schema"
)

type outputSchemaManager struct{ outputCaptureManager }

func (m *outputSchemaManager) Options(ctx context.Context, server string) (*mcpcfg.MCPClient, error) {
	if server != "service" {
		return &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic}, nil
	}
	return m.policy, nil
}

func TestOutputArtifactAuthoringProjectsOnlyConfiguredLiteralIdentity(t *testing.T) {
	rawSchema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"File": map[string]interface{}{"type": "string", "contentEncoding": "base64"}}}
	mgr := &outputSchemaManager{outputCaptureManager: outputCaptureManager{policy: &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic, OutputArtifacts: map[string]mcpcfg.OutputArtifact{"Download": {Name: "file.xlsx"}}}}}
	reg := newReconnectTestRegistry(&mgr.scriptedReconnectManager)
	reg.mgr = mgr
	for _, name := range []string{"service/Download", "service/download", "service/Ordinary", "other/Download"} {
		server, method := splitToolName(name)
		reg.cache[name] = &toolCacheEntry{def: llm.ToolDefinition{Name: name, OutputSchema: rawSchema}, mcpDef: schema.Tool{Name: method}}
		_ = server
	}
	// The other server has no policy: exact names never authorize another server.
	mgr.policy.OutputArtifacts["Download"] = mcpcfg.OutputArtifact{Name: "file.xlsx"}
	for _, name := range []string{"service/Download", mcpname.Canonical("service/Download"), "service:Download"} {
		definition, ok := reg.GetDefinitionWithContext(context.Background(), name)
		require.True(t, ok)
		public, err := json.Marshal(definition.OutputSchema)
		require.NoError(t, err)
		require.Contains(t, string(public), "resources")
		require.NotContains(t, string(public), "contentEncoding")
		require.NotContains(t, string(public), "File")
	}
	for _, name := range []string{"service/download", "service/Ordinary", "other/Download"} {
		definition, ok := reg.GetDefinitionWithContext(context.Background(), name)
		require.True(t, ok)
		require.Equal(t, rawSchema, definition.OutputSchema)
	}
	raw, ok := reg.GetRawDefinitionWithContext(context.Background(), "service/Download")
	require.True(t, ok)
	require.Equal(t, rawSchema, raw.OutputSchema)
	require.Equal(t, rawSchema, reg.cache["service/Download"].def.OutputSchema)
	definitions := reg.MatchDefinitionWithContext(context.Background(), "service:Download")
	require.Len(t, definitions, 1)
	require.Contains(t, definitions[0].OutputSchema, "properties")
}
