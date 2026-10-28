package sdk

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	agents "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcp/manager"
	promptsvc "github.com/viant/agently-core/protocol/tool/service/prompt"
	"github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	intakerepo "github.com/viant/agently-core/workspace/repository/intake"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultAuthoredDefinitionsUseSharedGatewaySDKAndNativePrompt(t *testing.T) {
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	root := t.TempDir()
	old, oldRuntime := workspace.Root(), workspace.RuntimeRoot()
	workspace.SetRoot(root)
	workspace.SetRuntimeRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old); workspace.SetRuntimeRoot(oldRuntime) })
	for _, dir := range []string{"templates/bundles", "intents", "prompts"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0700))
	}
	tpl := []byte("id: original-template\nname: Original Template\ninstructions: Original instructions\nformat: json\n")
	canonical := []byte("id: original-profile\nmodel: OriginalModel\nmessages: [{role: system, text: Original canonical instruction}]\n")
	legacy := []byte("id: legacy-profile\nmessages: [{role: system, text: Legacy instruction}]\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "templates/output.yaml"), tpl, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "templates/bundles/output.yaml"), []byte("id: original-bundle\ntemplates: [original-template]\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "intents/profile.yaml"), canonical, 0600))
	// Legacy roots remain readable, but must not duplicate a canonical identity.
	// Cross-root duplicate rejection is covered by repository/intake tests.
	require.NoError(t, os.WriteFile(filepath.Join(root, "prompts/legacy.yaml"), legacy, 0600))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	rt, err := executor.NewBuilder().WithMCPManager(mgr).WithAgentFinder(&streamTestAgentFinder{agent: &agents.Agent{Identity: agents.Identity{ID: "owned"}}}).WithModelFinder(&streamTestModelFinder{model: &streamTestModel{}}).Build(context.Background())
	require.NoError(t, err)
	defer rt.Close(context.Background())
	client, err := newBackendFromRuntime(rt)
	require.NoError(t, err)
	require.Same(t, rt.PrimitiveProviders, client.primitiveProviders)
	list, err := client.ListTemplates(context.Background(), &ListTemplatesInput{})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Equal(t, "template://workspace/output", list.Items[0].URI)
	got, err := client.GetTemplate(context.Background(), &GetTemplateInput{Name: "Original Template"})
	require.NoError(t, err)
	require.Equal(t, "Original instructions", got.Instructions)
	prof := resource.NewIntentReader(rt.PrimitiveProviders)
	profiles, err := prof.LoadAll(context.Background())
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	p, err := prof.Load(context.Background(), "original-profile")
	require.NoError(t, err)
	require.Equal(t, "OriginalModel", p.Model)
	legacyProfile, err := prof.Load(context.Background(), "legacy-profile")
	require.NoError(t, err)
	require.Equal(t, "Legacy instruction", legacyProfile.Messages[0].Text)
	tool := promptsvc.New(intakerepo.NewWithReader(prof))
	get, err := tool.Method("get")
	require.NoError(t, err)
	out := &promptsvc.GetOutput{}
	require.NoError(t, get(context.Background(), &promptsvc.GetInput{ID: "original-profile"}, out))
	require.Len(t, out.Messages, 1)
	require.Equal(t, "Original canonical instruction", out.Messages[0].Text)
	for file, expected := range map[string][]byte{"templates/output.yaml": tpl, "intents/profile.yaml": canonical, "prompts/legacy.yaml": legacy} {
		actual, err := os.ReadFile(filepath.Join(root, file))
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
}
