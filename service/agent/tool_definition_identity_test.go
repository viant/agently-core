package agent

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/mcpname"
	approval "github.com/viant/agently-core/protocol/tool/approvalqueue"
	"strings"
	"testing"
)

func TestPreparedURIProviderAliasesHaveOneModelDefinition(t *testing.T) {
	server := "mcp://example/source"
	got := dedupeDefinitions([]llm.ToolDefinition{
		{Name: server + "/Lookup", SourceMCPServer: server},
		{Name: server + ":Lookup", SourceMCPServer: server, AlwaysInclude: true},
		{Name: "mcp://other/source/Lookup", SourceMCPServer: "mcp://other/source"},
	})
	require.Len(t, got, 2)
	require.Equal(t, mcpname.Canonical(server+":Lookup"), got[0].Name)
	require.True(t, got[0].AlwaysInclude)
}

func TestURIModelAliasRetainsConfiguredApproval(t *testing.T) {
	raw := "mcp://example/source/Lookup"
	def := llm.ToolDefinition{Name: raw, SourceMCPServer: "mcp://example/source"}
	cfg := &llm.ApprovalConfig{Mode: "prompt"}
	entry := &resolvedToolSurface{Definitions: []llm.ToolDefinition{def}, ApprovalByID: map[string]*llm.ApprovalConfig{strings.ToLower(mcpname.Canonical(raw)): cfg}}
	normalizeToolSurfaceDefinitions(entry)
	ctx := approval.WithState(context.Background())
	(&Service{}).applyResolvedToolSurfaceMetadata(ctx, entry)
	require.True(t, approval.RequiresPrompt(ctx, entry.Definitions[0].Name))
	require.Same(t, cfg, entry.ApprovalByID[strings.ToLower(mcpname.Canonical(raw))])
}
