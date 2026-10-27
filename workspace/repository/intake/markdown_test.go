package intake

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestPlaybookMarkdownSaveRoundTripAndYAMLCompatibility(t *testing.T) {
	root := t.TempDir()
	repo := NewWithStore(fsstore.New(root))
	ctx := context.Background()
	profile := &intent.Profile{
		ID: "review", Name: "Review", Description: "Assess findings", Model: "gpt-4o",
		AppliesTo: []string{"report"}, ToolBundles: []string{"readers"},
		Messages: []intent.Message{
			{Role: "system", Text: "# Instructions\nKeep evidence.\n"},
			{Role: "user", URI: "../knowledge/request.md"},
			{Role: "assistant", Text: "  exact text  \n"},
		},
	}
	require.NoError(t, repo.Save(ctx, "review", profile))
	md, err := os.ReadFile(filepath.Join(root, workspace.KindPlaybook, "review.md"))
	require.NoError(t, err)
	require.Contains(t, string(md), "role: system")
	require.Contains(t, string(md), "# Instructions")
	loaded, err := repo.Load(ctx, "review")
	require.NoError(t, err)
	require.Equal(t, profile.ID, loaded.ID)
	require.Equal(t, profile.Name, loaded.Name)
	require.Equal(t, profile.Description, loaded.Description)
	require.Equal(t, profile.Model, loaded.Model)
	require.Equal(t, profile.AppliesTo, loaded.AppliesTo)
	require.Equal(t, profile.ToolBundles, loaded.ToolBundles)
	require.Equal(t, profile.Messages, loaded.Messages)
	names, err := repo.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"review"}, names)

	path := filepath.Join(root, workspace.KindPlaybook, "existing.yml")
	require.NoError(t, os.WriteFile(path, []byte("id: existing\nname: before\n"), 0600))
	require.NoError(t, repo.Save(ctx, "existing", &intent.Profile{ID: "existing", Name: "after"}))
	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(updated), "after")
	yamlLoaded, err := repo.Load(ctx, "existing")
	require.NoError(t, err)
	require.Equal(t, "after", yamlLoaded.Name)
	_, err = os.Stat(filepath.Join(root, workspace.KindPlaybook, "existing.md"))
	require.True(t, os.IsNotExist(err), "updating YAML must not create Markdown duplicate")
}

func TestPlaybookMarkdownImportsAndDuplicateFormats(t *testing.T) {
	root := t.TempDir()
	repo := NewWithStore(fsstore.New(root))
	ctx := context.Background()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "playbooks"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "knowledge"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "knowledge", "rules.md"), []byte("Trusted rules"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "rules.md"), []byte("---\nid: rules\n---\n---\nrole: system\n---\n$import(../knowledge/rules.md)"), 0600))
	profile, err := repo.Load(ctx, "rules")
	require.NoError(t, err)
	require.Equal(t, []intent.Message{{Role: "system", Text: "Trusted rules"}}, profile.Messages)
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "rules.yaml"), []byte("id: rules\n"), 0600))
	_, err = repo.List(ctx)
	require.ErrorContains(t, err, "duplicate")
	_, err = repo.Load(ctx, "rules")
	require.ErrorContains(t, err, "duplicate")
	require.NoError(t, os.Remove(filepath.Join(root, "playbooks", "rules.yaml")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "rules.md"), []byte("---\nid: rules\n---\n---\nrole: system\nengine: velty\n---\nbody"), 0600))
	_, err = repo.Load(ctx, "rules")
	require.ErrorContains(t, err, "engine")
}

func TestPlaybookMarkdownPreservesProfileSourcePrecedenceAndValidatesKind(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "playbooks"), 0700))
	file := filepath.Join(root, "playbooks", "hybrid.md")
	require.NoError(t, os.WriteFile(file, []byte("---\nkind: playbook\nid: hybrid\ninstructions: fallback\nmcp: {server: owner, prompt: canonical}\n---\n---\nrole: system\n---\n# Local instructions"), 0600))
	repo := NewWithStore(fsstore.New(root))
	profile, err := repo.Load(context.Background(), "hybrid")
	require.NoError(t, err)
	require.Equal(t, "fallback", profile.Instructions)
	require.NotNil(t, profile.MCP)
	require.Equal(t, "canonical", profile.MCP.Prompt)
	require.Equal(t, []intent.Message{{Role: "system", Text: "# Local instructions"}}, profile.Messages)
	// Existing runtime semantics still prioritize the MCP source over messages.
	require.Equal(t, "canonical", profile.MCP.Prompt)
	require.NoError(t, os.WriteFile(file, []byte("---\nkind: agent\nid: hybrid\n---\nbody"), 0600))
	_, err = repo.Load(context.Background(), "hybrid")
	require.ErrorContains(t, err, "expected playbook kind")
}

func TestPlaybookLoadAllRejectsDifferentFilesWithSameDeclaredID(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"playbooks", "intents"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "playbooks", "first.md"), []byte("---\nid: same\n---\nbody"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "intents", "second.yaml"), []byte("id: same\n"), 0600))
	_, err := NewWithStore(fsstore.New(root)).LoadAll(context.Background())
	require.ErrorContains(t, err, "duplicate declared playbook id")
}
