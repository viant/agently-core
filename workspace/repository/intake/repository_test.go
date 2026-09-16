package intake

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestRepository_LoadAll(t *testing.T) {
	// Point the workspace at our testdata directory so the repository
	// picks up the prompts sub-folder.
	root := "testdata"
	// Ensure the directory exists.
	if _, err := os.Stat(root); err != nil {
		t.Skipf("testdata directory not found: %v", err)
	}

	store := fsstore.New(root)
	repo := NewWithStore(store)

	ctx := context.Background()
	profiles, err := repo.LoadAll(ctx)
	assert.NoError(t, err)
	assert.Len(t, profiles, 1)

	p := profiles[0]
	assert.Equal(t, "performance_analysis", p.ID)
	assert.Equal(t, "Performance Analysis", p.Name)
	assert.Contains(t, p.AppliesTo, "performance")
	assert.Contains(t, p.AppliesTo, "health")
	assert.Len(t, p.Messages, 2)
	assert.Equal(t, "system", p.Messages[0].Role)
	assert.Equal(t, "You are a systems analyst.\nFocus on operational health and concise evidence-backed observations.\n", p.Messages[0].Text)
	assert.Equal(t, []string{"analyst-performance-tools"}, p.ToolBundles)
	assert.Equal(t, "analytics_dashboard", p.Template)
	assert.Equal(t, []string{"analytics_dashboard", "resource_list_review"}, p.Templates)
	assert.Equal(t, []string{"product-knowledge"}, p.Knowledge[0].RootIDs)
	assert.Equal(t, 3, p.Knowledge[0].MaxDocuments)
	require.NotNil(t, p.Knowledge[0].MinScore)
	assert.InDelta(t, 0.55, *p.Knowledge[0].MinScore, 0.0001)
}

func TestRepository_Load(t *testing.T) {
	root := "testdata"
	if _, err := os.Stat(root); err != nil {
		t.Skipf("testdata directory not found: %v", err)
	}

	store := fsstore.New(root)
	repo := NewWithStore(store)

	ctx := context.Background()
	p, err := repo.Load(ctx, "performance_analysis")
	assert.NoError(t, err)
	assert.NotNil(t, p)
	assert.Equal(t, "performance_analysis", p.ID)
	assert.Equal(t, "Analyze the resource hierarchy.\n", p.Messages[1].Text)
	assert.Equal(t, []string{"analytics_dashboard", "resource_list_review"}, p.Templates)
	_ = workspace.KindPrompt // ensure constant is accessible
}

func TestRepository_IntentPrecedence(t *testing.T) {
	for _, backend := range []string{"store", "afs"} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			write := func(name, body string) {
				full := filepath.Join(root, name)
				require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
				require.NoError(t, os.WriteFile(full, []byte(body), 0600))
			}
			var repo *Repository
			if backend == "store" {
				repo = NewWithStore(fsstore.New(root))
			} else {
				t.Chdir(root)
				previous := workspace.Root()
				workspace.SetRoot(root)
				t.Cleanup(func() { workspace.SetRoot(previous) })
				repo = New(afs.New())
			}
			ctx := context.Background()
			write("prompts/shared.yaml", "id: shared\nname: legacy\n")
			write("prompts/legacy/legacy.yaml", "id: legacy\n")
			// A missing intent directory must not hide legacy profiles.
			names, err := repo.List(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"legacy", "shared"}, names)
			write("intents/shared.yaml", "id: shared\nname: preferred\n")
			write("intents/new/new.yaml", "id: new\nmessages: $import(messages.yaml)\n")
			write("intents/new/messages.yaml", "- role: system\n  text: intent relative import\n")
			profiles, err := repo.LoadAll(ctx)
			require.NoError(t, err)
			require.Len(t, profiles, 3)
			require.Equal(t, "legacy", profiles[0].ID)
			require.Equal(t, "intent relative import", profiles[1].Messages[0].Text)
			require.Equal(t, "preferred", profiles[2].Name)
			raw, err := repo.GetRaw(ctx, "shared")
			require.NoError(t, err)
			require.Contains(t, string(raw), "preferred")
			// Invalid preferred definitions must not silently activate legacy content.
			write("intents/shared.yaml", "messages: [")
			_, err = repo.Load(ctx, "shared")
			require.Error(t, err)
			_, err = repo.LoadAll(ctx)
			require.Error(t, err)
			require.NoError(t, os.Remove(filepath.Join(root, "intents/shared.yaml")))
			profile, err := repo.Load(ctx, "shared")
			require.NoError(t, err)
			require.Equal(t, "legacy", profile.Name)
			require.NoError(t, repo.Save(ctx, "saved", profile))
			_, err = os.Stat(filepath.Join(root, "intents/saved.yaml"))
			require.NoError(t, err)
		})
	}
}

func TestRepository_IntentOnly(t *testing.T) {
	root := t.TempDir()
	store := fsstore.New(root)
	require.NoError(t, store.Save(context.Background(), workspace.KindIntent, "only", []byte("id: only")))
	profiles, err := NewWithStore(store).LoadAll(context.Background())
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	require.Equal(t, "only", profiles[0].ID)
}
