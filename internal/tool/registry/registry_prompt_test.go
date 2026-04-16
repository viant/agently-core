package tool_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/manager"
	promptsvc "github.com/viant/agently-core/protocol/tool/service/prompt"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestRegistry_InternalPromptServiceMatchesPromptWildcard(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)

	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)

	repo := promptrepo.NewWithStore(fsstore.New("/Users/awitas/go/src/github.com/viant/agently-core/workspace/repository/prompt/testdata"))
	require.NoError(t, reg.AddInternalService(promptsvc.New(repo)))

	reg.Initialize(context.Background())

	defs := reg.MatchDefinition("prompt:*")
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		names = append(names, def.Name)
	}

	require.Contains(t, names, "prompt/get")
	require.Contains(t, names, "prompt/list")
}
