package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	registry "github.com/viant/agently-core/internal/tool/registry"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcp/manager"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	promptsvc "github.com/viant/agently-core/protocol/tool/service/prompt"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestResolveTools_WithPromptBundle(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)

	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)

	repo := promptrepo.NewWithStore(fsstore.New("/Users/awitas/go/src/github.com/viant/agently-core/workspace/repository/prompt/testdata"))
	require.NoError(t, reg.AddInternalService(promptsvc.New(repo)))
	reg.Initialize(context.Background())

	svc := &Service{
		registry: reg,
		toolBundles: func(ctx context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{{
				ID: "prompt",
				Match: []llm.Tool{
					{Name: "prompt:*"},
				},
			}}, nil
		},
	}

	tools, err := svc.resolveTools(context.Background(), &QueryInput{
		Agent: &agentmdl.Agent{
			Tool: agentmdl.Tool{
				Bundles: []string{"prompt"},
			},
		},
	})
	require.NoError(t, err)

	var names []string
	for _, tool := range tools {
		names = append(names, tool.Definition.Name)
	}

	assert.Contains(t, names, "prompt/list")
	assert.Contains(t, names, "prompt/get")
}
