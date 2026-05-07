package planner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	tplbundlerepo "github.com/viant/agently-core/workspace/repository/templatebundle"
	toolbundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestValidate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	mkdir := func(rel string) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, rel), 0o755))
	}
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}

	mkdir("prompts")
	mkdir("tools/bundles")
	mkdir("templates")
	mkdir("templates/bundles")

	write("prompts/repo_analysis.yaml", `
id: repo_analysis
name: Repo Analysis
description: Analyze repository state
`)
	write("prompts/performance_analysis.yaml", `
id: performance_analysis
name: Performance Analysis
description: Analyze performance issues
`)
	write("tools/bundles/analyst_tools.yaml", `
id: analyst-tools
match:
  - name: system/exec
`)
	write("templates/dashboard.yaml", `
id: dashboard
name: dashboard
description: Dashboard template
`)
	write("templates/summary.yaml", `
id: summary
name: summary
description: Summary template
`)
	write("templates/bundles/analytics.yaml", `
id: analytics-templates
templates:
  - dashboard
`)

	store := fsstore.New(root)
	vctx := ValidationContext{
		ProfileRepo:        promptrepo.NewWithStore(store),
		ToolBundleRepo:     toolbundlerepo.NewWithStore(store),
		TemplateRepo:       tplrepo.NewWithStore(store),
		TemplateBundleRepo: tplbundlerepo.NewWithStore(store),
		Agent: &agentmdl.Agent{
			Prompts:  agentmdl.PromptAccess{Bundles: []string{"repo_analysis"}},
			Template: agentmdl.Template{Bundles: []string{"analytics-templates"}},
		},
	}

	// Warm repositories once to surface fixture issues early.
	_, err := vctx.ProfileRepo.LoadAll(ctx)
	require.NoError(t, err)
	_, err = vctx.ToolBundleRepo.LoadAll(ctx)
	require.NoError(t, err)
	_, err = vctx.TemplateRepo.LoadAll(ctx)
	require.NoError(t, err)
	_, err = vctx.TemplateBundleRepo.LoadAll(ctx)
	require.NoError(t, err)

	t.Run("valid output", func(t *testing.T) {
		out := Output{
			"baseProfiles":     []string{"repo_analysis"},
			"toolBundles":      []string{"analyst-tools"},
			"templateId":       "dashboard",
			"requiredEvidence": []string{"baseline", "confirmation"},
			"executionOrder":   []string{"baseline", "confirmation"},
		}
		require.Empty(t, Validate(out, vctx))
	})

	t.Run("unknown profile", func(t *testing.T) {
		errs := Validate(Output{"baseProfiles": []string{"missing"}}, vctx)
		require.Len(t, errs, 1)
		require.Equal(t, "unknown_profile", errs[0].Code)
	})

	t.Run("profile not allowed", func(t *testing.T) {
		errs := Validate(Output{"baseProfiles": []string{"performance_analysis"}}, vctx)
		require.Len(t, errs, 1)
		require.Equal(t, "profile_not_allowed", errs[0].Code)
	})

	t.Run("unknown tool bundle", func(t *testing.T) {
		errs := Validate(Output{"toolBundles": []string{"missing-tools"}}, vctx)
		require.Len(t, errs, 1)
		require.Equal(t, "unknown_bundle", errs[0].Code)
	})

	t.Run("unknown template", func(t *testing.T) {
		errs := Validate(Output{"templateId": "missing"}, vctx)
		require.Len(t, errs, 1)
		require.Equal(t, "unknown_template", errs[0].Code)
	})

	t.Run("template not allowed", func(t *testing.T) {
		errs := Validate(Output{"templateId": "summary"}, vctx)
		require.Len(t, errs, 1)
		require.Equal(t, "template_not_allowed", errs[0].Code)
	})

	t.Run("execution order undeclared", func(t *testing.T) {
		errs := Validate(Output{
			"requiredEvidence": []string{"baseline"},
			"executionOrder":   []string{"baseline", "confirmation"},
		}, vctx)
		require.Len(t, errs, 1)
		require.Equal(t, "execution_order_undeclared", errs[0].Code)
	})
}
