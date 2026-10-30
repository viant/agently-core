package agent

import (
	"context"
	"fmt"
	runtimediscovery "github.com/viant/agently-core/runtime/discovery"
	"github.com/viant/jsonrpc"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/auth"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/service/intake"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

func TestRequiredHealthyBundleDoesNotRequireUnrelatedProviderDiscovery(t *testing.T) {
	for _, execution := range []bool{false, true} {
		for _, scheduled := range []bool{false, true} {
			for _, forbidden := range []bool{false, true} {
				t.Run(fmt.Sprintf("execution=%v/scheduled=%v/forbidden=%v", execution, scheduled, forbidden), func(t *testing.T) {
					ctx, warnings := withWarnings(auth.WithUserInfo(context.Background(), &auth.UserInfo{Subject: "synthetic-user"}))
					ctx = runtimediscovery.MergeMode(ctx, runtimediscovery.Mode{Scheduler: scheduled})
					optionalCalls := 0
					mgr, err := manager.New(nil)
					require.NoError(t, err)
					t.Cleanup(func() { mgr.CloseConversation("") })
					healthy := &punctuatedDiscoveryService{names: []string{"run"}}
					for _, name := range []string{"healthy", "optional"} {
						require.NoError(t, mgr.RegisterLocal(ctx, name, &config.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: name, ToolsListVisibility: config.ToolsListVisibilityPrivate}, func(context.Context) (mcpclient.Interface, error) {
							if name == "optional" {
								optionalCalls++
								if forbidden {
									return nil, jsonrpc.NewHTTPStatusError(http.StatusForbidden)
								}
								return nil, fmt.Errorf("Post https://example.invalid/mcp: %w", context.DeadlineExceeded)
							}
							return healthy, nil
						}))
					}
					reg, err := registry.NewWithManager(mgr)
					require.NoError(t, err)
					service := &Service{registry: reg, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
						return []*toolbundle.Bundle{{ID: "healthy", Match: []llm.Tool{{Name: "healthy:run"}}}}, nil
					}}
					input := selectionInput(&intake.ToolSelection{Names: []string{"healthy:run"}, Complete: true})
					input.Agent.Tool.Bundles = nil
					input.ToolBundles = []string{"healthy"}
					for attempt := 0; attempt < 2; attempt++ {
						if execution {
							tools, err := service.resolveTools(ctx, input)
							require.NoError(t, err)
							require.NotEmpty(t, tools)
						} else {
							tools, err := service.buildToolSignatures(ctx, input)
							require.NoError(t, err)
							require.NotEmpty(t, tools)
						}
						for _, warning := range append(reg.LastWarnings(), (*warnings)...) {
							require.False(t, strings.Contains(warning, "optional"), "unrelated provider errors must not enter the turn: %s", warning)
						}
					}
					require.Zero(t, optionalCalls, "unrelated provider must not be contacted")
					// Explicitly choosing the unavailable provider still reports its failure.
					input.ToolBundles = nil
					input.ToolsAllowed = []string{"optional:run"}
					_, err = service.appendToolSelectionsWithMatcher(context.WithValue(ctx, selectedSourceResolutionKey{}, true), nil, []string{"optional:run"}, service.matchDefinitionsResult)
					require.Error(t, err)
				})
			}
		}
	}
}
