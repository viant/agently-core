package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParse_PortabilityFixtures locks in the cross-runtime portability
// claim from skills.md §16: a SKILL.md authored for Anthropic Claude or
// OpenAI Codex parses through agently-core's portable core unchanged, with
// zero error diagnostics and zero AgentlyOverrides leakage.
//
// Fixtures are static snapshots vendored under testdata/portable/. _SOURCE
// records origin URL + commit SHA. No nightly canary, no quarterly refresh
// process — these prove our parser handles real upstream SKILL.md files
// at the moment they were vendored. Drift is detected when a real user
// drops in a newer upstream skill, not by synthetic CI.
func TestParse_PortabilityFixtures(t *testing.T) {
	repoRoot := portabilityRoot(t)

	t.Run("upstream Anthropic pdf parses cleanly", func(t *testing.T) {
		fixture := filepath.Join(repoRoot, "upstream-anthropic-pdf", "SKILL.md")
		raw := mustReadFile(t, fixture)
		s, diags, err := Parse(fixture, filepath.Dir(fixture), "claude", raw)
		require.NoError(t, err)
		require.NotNil(t, s)
		// Zero error-level diagnostics — the parser must not reject real
		// upstream SKILL.md files.
		for _, d := range diags {
			assert.NotEqual(t, "error", d.Level,
				"unexpected error diag for upstream Anthropic skill: %+v", d)
		}
		assert.Equal(t, "pdf", s.Frontmatter.Name)
		assert.Equal(t, "claude", s.Source)
		// Zero AgentlyOverrides — Anthropic skills never use Agently-only
		// keys, so every accessor returns the default zero-value.
		assertZeroAgentlyOverrides(t, s)
	})

	t.Run("upstream Codex pdf parses cleanly", func(t *testing.T) {
		fixture := filepath.Join(repoRoot, "upstream-codex-pdf", "SKILL.md")
		raw := mustReadFile(t, fixture)
		s, diags, err := Parse(fixture, filepath.Dir(fixture), "codex", raw)
		require.NoError(t, err)
		require.NotNil(t, s)
		for _, d := range diags {
			assert.NotEqual(t, "error", d.Level,
				"unexpected error diag for upstream Codex skill: %+v", d)
		}
		assert.Equal(t, "pdf", s.Frontmatter.Name)
		assert.Equal(t, "codex", s.Source)
		assertZeroAgentlyOverrides(t, s)
	})

	t.Run("agently-modern uses metadata.agently-* — zero deprecation warns", func(t *testing.T) {
		fixture := filepath.Join(repoRoot, "agently-modern", "SKILL.md")
		raw := mustReadFile(t, fixture)
		s, diags, err := Parse(fixture, filepath.Dir(fixture), "agently", raw)
		require.NoError(t, err)
		require.NotNil(t, s)
		// Modern fixture must not emit any warn-level "deprecated" diagnostics.
		for _, d := range diags {
			if d.Level == "warn" {
				assert.NotContains(t, strings.ToLower(d.Message), "deprecated",
					"modern fixture must not emit deprecation warnings: %+v", d)
			}
		}
		// Resolved Agently-side values come from the metadata namespace.
		assert.Equal(t, "fork", s.Frontmatter.ContextMode())
		assert.True(t, s.Frontmatter.PreprocessEnabled())
		assert.Equal(t, 16000, s.Frontmatter.MaxTokensValue())
		assert.Equal(t, 30, s.Frontmatter.PreprocessTimeoutValue())
		assert.Equal(t, "Reviewing the change...", s.Frontmatter.AsyncNarratorPromptValue())
		prefs := s.Frontmatter.ModelPreferencesValue()
		require.NotNil(t, prefs, "metadata.model-preferences must populate ModelPreferencesValue")
		assert.Equal(t, []string{"claude-opus"}, prefs.Hints)
		assert.InDelta(t, 0.9, prefs.IntelligencePriority, 0.0001)
	})

	t.Run("agently-legacy bare keys parse with deprecation warns", func(t *testing.T) {
		fixture := filepath.Join(repoRoot, "agently-legacy", "SKILL.md")
		raw := mustReadFile(t, fixture)
		s, diags, err := Parse(fixture, filepath.Dir(fixture), "agently", raw)
		require.NoError(t, err)
		require.NotNil(t, s)
		// Each deprecated bare key fires a warn-level diagnostic. We want at
		// least one deprecation warn (the legacy fixture sets several).
		warnCount := 0
		for _, d := range diags {
			if d.Level == "warn" {
				warnCount++
			}
		}
		assert.GreaterOrEqual(t, warnCount, 2,
			"legacy fixture should emit ≥2 warn-level diagnostics; got %d total diagnostics: %+v", warnCount, diags)
		// Resolution still works — runtime continues to honor the legacy
		// values until the deprecation window closes (skill-impr.md A.7).
		assert.Equal(t, "fork", s.Frontmatter.ContextMode())
		assert.Equal(t, "claude-opus", s.Frontmatter.ModelValue())
		assert.True(t, s.Frontmatter.PreprocessEnabled())
	})
}

// portabilityRoot returns the absolute path to testdata/portable/. Test
// must run with the package's working directory (default for go test).
func portabilityRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "testdata", "portable")
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "fixture missing: %s", path)
	return string(raw)
}

// assertZeroAgentlyOverrides verifies that pure-spec skills (no
// metadata.agently-* and no bare deprecated keys) produce zero-valued
// Agently-side accessor results. This is the contract that lets
// portable Anthropic/Codex skills round-trip through the agently parser
// without spurious behavior change.
func assertZeroAgentlyOverrides(t *testing.T, s *Skill) {
	t.Helper()
	require.NotNil(t, s)
	// Default ContextMode for a skill that didn't specify is "inline" —
	// the safest cross-runtime behavior. That's a defaulted resolve, not
	// an override.
	assert.Equal(t, "inline", s.Frontmatter.ContextMode())
	assert.Equal(t, "", s.Frontmatter.ModelValue())
	assert.Equal(t, "", s.Frontmatter.EffortValue())
	assert.Equal(t, "", s.Frontmatter.AgentIDValue())
	assert.Equal(t, "", s.Frontmatter.AsyncNarratorPromptValue())
	assert.Nil(t, s.Frontmatter.TemperatureValue())
	assert.Equal(t, 0, s.Frontmatter.MaxTokensValue())
	assert.False(t, s.Frontmatter.PreprocessEnabled())
	assert.Equal(t, 0, s.Frontmatter.PreprocessTimeoutValue())
	assert.Nil(t, s.Frontmatter.ModelPreferencesValue())
}
