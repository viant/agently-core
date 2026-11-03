package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	registry "github.com/viant/agently-core/internal/tool/registry"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/mcpname"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	toolservice "github.com/viant/agently-core/protocol/tool/service/llm/tools"
	resourcesvc "github.com/viant/agently-core/service/resource"
)

type intakeCatalogReadCounter struct {
	fakeRegistry
	matches int
}

func (r *intakeCatalogReadCounter) MatchDefinition(pattern string) []*llm.ToolDefinition {
	r.matches++
	return r.fakeRegistry.MatchDefinition(pattern)
}

type intakeCatalogVisibility struct {
	*intakeCatalogReadCounter
	fail bool
}

func (r *intakeCatalogVisibility) MCPToolVisible(ctx context.Context, name, audience string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if mcpname.Canonical(name) == mcpname.Canonical("llm/tools:list") {
		if r.fail {
			return false, errors.New("synthetic helper visibility unavailable")
		}
		return false, nil
	}
	return true, nil
}

func TestIntakeNamespaceCardsReusePreparedAuthorizedCatalog(t *testing.T) {
	for _, mode := range []string{"retained", "not retained", "excluded", "caller denied", "disabled", "helper denied", "helper unavailable"} {
		t.Run(mode, func(t *testing.T) {
			svc, input, namespaces := namespaceFixture(mode != "not retained")
			source := &intakeCatalogReadCounter{fakeRegistry: *svc.registry.(*fakeRegistry)}
			svc.registry = source
			if mode == "helper denied" || mode == "helper unavailable" {
				svc.registry = &intakeCatalogVisibility{intakeCatalogReadCounter: source, fail: mode == "helper unavailable"}
			}
			svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { return namespaces, nil }
			if mode == "excluded" {
				original := svc.toolBundles
				svc.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) {
					bundles, err := original(ctx)
					for i := range bundles[0].Match {
						bundles[0].Match[i].Exclude = []string{bundles[0].Match[i].Name}
					}
					return bundles, err
				}
			}
			if mode == "caller denied" {
				input.ToolsAllowed = []string{"remote:query00"}
			}
			ctx := context.Background()
			if mode == "disabled" {
				ctx = withSelectedPromptProfile(ctx, &intent.Profile{ID: "disabled", Execution: &intent.Execution{DisableTools: true}})
			}
			summary, names, hints, err := svc.intakeCapabilitySnapshot(ctx, input)
			require.NoError(t, err)
			if mode == "retained" {
				require.Equal(t, []string{"analysis", "planning"}, hints)
				require.NotContains(t, summary, "remote-query00")
				require.NotContains(t, names, "llm_tools-list", "presentation-only omission must not erase the retained discovery flag")
			} else {
				require.Empty(t, hints)
			}
			expectedReads := 0
			if mode == "caller denied" {
				expectedReads = 1
			}
			require.Equal(t, expectedReads, source.matches, "namespace presentation must use its already-authorized catalog without another source lookup")
		})
	}
}

type nativeIntakeCatalogReads struct {
	*registry.Registry
	matches int
}

func (r *nativeIntakeCatalogReads) MatchDefinitionWithContextResult(ctx context.Context, pattern string) ([]*llm.ToolDefinition, error) {
	r.matches++
	return r.Registry.MatchDefinitionWithContextResult(ctx, pattern)
}
func TestIntakeNamespaceMetadataDoesNotRediscoverNativeHelpers(t *testing.T) {
	ctx := manager.WithToolCatalogReadSnapshot(manager.WithNamesMemo(context.Background()))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	native, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, native.AddInternalService(toolservice.New()))
	require.NoError(t, native.AddInternalService(&discoveryFixture{}))
	counted := &nativeIntakeCatalogReads{Registry: native}
	svc := &Service{registry: counted, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "discovery", Match: []llm.Tool{{Name: "llm/tools:list", AlwaysInclude: true}}}}, nil
	}, namespaceProvider: func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) {
		return []resourcesvc.DiscoveredNamespace{testNamespace("analysis", "owner-a", "Analyze authorized records", "mcp")}, nil
	}}
	summary, names, _, err := svc.intakeCapabilitySnapshot(ctx, &QueryInput{Agent: &agentmdl.Agent{}})
	require.NoError(t, err)
	require.Contains(t, summary, "operations-configure")
	require.NotContains(t, names, "llm_tools-list")
	require.Zero(t, counted.matches, "intake namespace presentation must not call the source matcher after native catalog preparation")
}
