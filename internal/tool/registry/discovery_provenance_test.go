package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
)

type provenanceManager struct {
	discoveryManagerStub
	reads int
}

func (m *provenanceManager) Options(ctx context.Context, server string) (*mcpcfg.MCPClient, error) {
	m.reads++
	return m.discoveryManagerStub.Options(ctx, server)
}

func TestDiscoveryProvenanceUsesOnlyResolvedConfigurationIdentity(t *testing.T) {
	for _, tc := range []struct{ name, uri, owner, want string }{
		{"declared domain differs from server", "mcp://analysis/connection", "trusted-owner", "mcp://analysis/connection"},
		{"plain config has no inferred namespace", "", "", ""},
		{"missing provider stays unmapped", "mcp://analysis/connection", "", ""},
		{"wrong resource kind stays unmapped", "skill://analysis/connection", "trusted-owner", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &provenanceManager{discoveryManagerStub: discoveryManagerStub{options: &mcpcfg.MCPClient{ResolvedIdentity: &mcpcfg.ResolvedServiceIdentity{ResourceURI: tc.uri, ProviderIdentity: tc.owner}}}}
			registry := &Registry{mgr: manager}
			projection := registry.newOutputArtifactProjection()
			input := &llm.ToolDefinition{Name: "unrelated-server:query", SourceMCPServer: "unrelated-server", Description: "metadata must not assert source ownership", SourceResourceURI: "mcp://forged/connection", SourceProviderIdentity: "forged", DiscoveryNamespace: "forged"}
			for i := 0; i < 5; i++ {
				result := projection.provenance(context.Background(), input)
				require.Equal(t, tc.want, result.SourceResourceURI)
				require.Empty(t, result.DiscoveryNamespace, "effective namespace waits for described gateway ownership")
				encoded, err := json.Marshal(result)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "SourceProviderIdentity")
				require.NotContains(t, string(encoded), "trusted-owner")
				require.NotContains(t, string(encoded), "mcp://")
			}
			require.Equal(t, 1, manager.reads, "reuse configuration within the existing catalog read")
			require.Empty(t, manager.getCallsSnapshot(), "provenance must never open/refetch an MCP catalog")
			require.Equal(t, "forged", input.DiscoveryNamespace, "caller/cache storage is not mutated")
		})
	}
}

func TestVirtualAndOrdinaryDefinitionsCannotClaimConfiguredMCPNamespace(t *testing.T) {
	manager := &provenanceManager{discoveryManagerStub: discoveryManagerStub{options: &mcpcfg.MCPClient{ResolvedIdentity: &mcpcfg.ResolvedServiceIdentity{ResourceURI: "mcp://analysis/connection", ProviderIdentity: "owner"}}}}
	registry := &Registry{mgr: manager, virtualDefs: map[string]llm.ToolDefinition{"remote:virtual": {Name: "remote:virtual"}}}
	projection := registry.newOutputArtifactProjection()
	for _, definition := range []llm.ToolDefinition{{Name: "remote:ordinary"}, {Name: "remote:virtual", SourceMCPServer: "remote"}} {
		require.Empty(t, projection.provenance(context.Background(), &definition).SourceResourceURI)
	}
	require.Zero(t, manager.reads, "a matching server prefix is not proof of MCP origin")
}
