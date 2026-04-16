package prompt

import (
	"context"
	"os"
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
	assert.Contains(t, p.AppliesTo, "pacing")
	assert.Len(t, p.Messages, 2)
	assert.Equal(t, "system", p.Messages[0].Role)
	assert.Equal(t, "You are a performance analyst.\nFocus on KPI health, and concise evidence-backed observations.\n", p.Messages[0].Text)
	assert.Equal(t, []string{"steward-performance-tools"}, p.ToolBundles)
	assert.Equal(t, "analytics_dashboard", p.Template)
	assert.Equal(t, []string{"analytics_dashboard", "site_list_planner"}, p.Templates)
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
	assert.Equal(t, "Analyze the campaign hierarchy.\n", p.Messages[1].Text)
	assert.Equal(t, []string{"analytics_dashboard", "site_list_planner"}, p.Templates)
	_ = workspace.KindPrompt // ensure constant is accessible
}
