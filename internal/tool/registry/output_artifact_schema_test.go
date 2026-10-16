package tool

import (
	"context"
	"encoding/json"
	"fmt"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpclient "github.com/viant/mcp/client"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcpname"
	schema "github.com/viant/mcp-protocol/schema"
)

type outputSchemaManager struct {
	outputCaptureManager
	namesCalls int
}

func (m *outputSchemaManager) Names(ctx context.Context) ([]string, error) {
	m.namesCalls++
	return []string{"service"}, nil
}

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

func TestInternalToolArtifactProjectionDoesNotDiscoverRemoteCatalogs(t *testing.T) {
	mgr := &outputSchemaManager{}
	reg := newReconnectTestRegistry(&mgr.scriptedReconnectManager)
	reg.mgr = mgr
	if reg.internal == nil {
		reg.internal = map[string]mcpclient.Interface{}
	}
	reg.internal["ui/window"] = nil
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	for _, name := range []string{"ui/window/open", "ui/window:open", "ui_window-open"} {
		definition := &llm.ToolDefinition{Name: name}
		require.Same(t, definition, reg.projectOutputArtifactDefinition(ctx, definition))
	}
	require.Zero(t, mgr.namesCalls, "internal tool authoring must not enumerate unrelated authenticated servers")
}

type countedProjectionManager struct {
	outputSchemaManager
	optionsCalls map[string]int
}

func (m *countedProjectionManager) Options(ctx context.Context, server string) (*mcpcfg.MCPClient, error) {
	m.optionsCalls[server]++
	return m.outputSchemaManager.Options(ctx, server)
}

func TestDefinitionsPreparesArtifactPoliciesPerServerNotPerTool(t *testing.T) {
	mgr := &countedProjectionManager{optionsCalls: map[string]int{}}
	reg := newReconnectTestRegistry(&mgr.scriptedReconnectManager)
	reg.mgr = mgr
	// Multi-segment advertised methods previously enumerated every server for
	// every tool, including ordinary tools with no artifact policy.
	for i := 0; i < 500; i++ {
		method := fmt.Sprintf("group/method.%d", i)
		name := "service/" + method
		reg.cache[name] = &toolCacheEntry{def: llm.ToolDefinition{Name: name}, mcpDef: schema.Tool{Name: method}}
	}
	require.Len(t, reg.DefinitionsWithContext(context.Background()), 500)
	require.Equal(t, 2, mgr.namesCalls, "one discovery inventory and one policy inventory per listing")
	require.LessOrEqual(t, mgr.optionsCalls["service"], 3, "policy preparation must stay bounded independently of tool count")
	// A new request must observe newly configured policy instead of retaining
	// the previous request's options, including when its actor changes.
	mgr.policy = &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic, OutputArtifacts: map[string]mcpcfg.OutputArtifact{"group/method.0": {Name: "file.xlsx"}}}
	defs := reg.DefinitionsWithContext(context.Background())
	projected := false
	for _, def := range defs {
		if def.Name == "service/group/method.0" {
			projected = strings.Contains(def.Description, "owned immutable artifact")
		}
	}
	require.True(t, projected)
}
