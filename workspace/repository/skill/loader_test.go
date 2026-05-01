package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	execconfig "github.com/viant/agently-core/app/executor/config"
)

// TestLoader_Shadowing covers the discovery rule "first match wins; lower
// precedence duplicates retained as diagnostics" from skills.md §7. The
// shadowing logic itself lives in protocol/skill/registry.go (Registry.Add
// emits a warning diagnostic when a name is already loaded). This test
// exercises the end-to-end path through the loader to lock in the contract:
// the higher-precedence root's skill is the one returned by Get, and a
// shadowing diagnostic is emitted.
func TestLoader_Shadowing(t *testing.T) {
	tmp := t.TempDir()
	rootA := filepath.Join(tmp, "winner")
	rootB := filepath.Join(tmp, "shadowed")
	require.NoError(t, os.MkdirAll(filepath.Join(rootA, "demo"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(rootB, "demo"), 0o755))

	skillBody := func(description string) string {
		return strings.Join([]string{
			"---",
			"name: demo",
			"description: " + description,
			"---",
			"# Demo",
		}, "\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(rootA, "demo", "SKILL.md"), []byte(skillBody("winner")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(rootB, "demo", "SKILL.md"), []byte(skillBody("shadowed")), 0o644))

	defaults := &execconfig.Defaults{
		Skills: execconfig.SkillsDefaults{
			Roots: []string{rootA, rootB},
		},
	}

	reg, err := New(defaults).LoadAll()
	require.NoError(t, err)
	require.NotNil(t, reg)

	got, ok := reg.Get("demo")
	require.True(t, ok, "shadowing rule must yield a winner")
	require.NotNil(t, got)
	assert.Equal(t, "winner", got.Frontmatter.Description, "highest-precedence root wins")
	assert.Equal(t, rootA, filepath.Dir(got.Root), "winner.Root traces to rootA")

	diags := reg.Diagnostics()
	var shadowingDiag bool
	for _, d := range diags {
		if strings.Contains(strings.ToLower(d.Message), "shadow") {
			shadowingDiag = true
			assert.Contains(t, d.Message, "demo", "diagnostic must name the shadowed skill")
			assert.Contains(t, d.Path, rootB, "diagnostic path points at the shadowed source")
		}
	}
	assert.True(t, shadowingDiag, "expected at least one shadowing diagnostic; got=%v", diags)
}

// TestLoader_NoShadowingWhenUnique sanity-checks the no-collision case: a
// skill name that exists in only one root produces zero shadowing
// diagnostics.
func TestLoader_NoShadowingWhenUnique(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "only")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "demo"), 0o755))
	body := "---\nname: demo\ndescription: only one\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "demo", "SKILL.md"), []byte(body), 0o644))

	defaults := &execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{root}}}
	reg, err := New(defaults).LoadAll()
	require.NoError(t, err)

	for _, d := range reg.Diagnostics() {
		assert.NotContains(t, strings.ToLower(d.Message), "shadow",
			"unexpected shadowing diagnostic: %+v", d)
	}
}
