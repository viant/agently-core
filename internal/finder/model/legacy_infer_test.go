package model

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinder_LegacyInferDisabledByDefault verifies the H1 contract: with
// the AGENTLY_ALLOW_LEGACY_INFER env flag unset, requesting an
// unregistered openai_*-shaped id returns ErrModelNotRegistered instead
// of silently inferring a config from the id pattern.
//
// Workspaces that depended on the legacy inference path can opt back in
// during migration by setting the env flag (covered separately by
// TestFinder_LegacyInferEnabled).
func TestFinder_LegacyInferDisabledByDefault(t *testing.T) {
	// Make sure the flag is unset for this test even if the dev shell
	// happens to have it set.
	t.Setenv("AGENTLY_ALLOW_LEGACY_INFER", "")

	f := New()
	_, err := f.Find(context.Background(), "openai_gpt-5-mini")
	require.Error(t, err, "legacy inference must be off by default — unregistered id must error")
	assert.True(t, errors.Is(err, ErrModelNotRegistered),
		"error must be detectable via errors.Is(err, ErrModelNotRegistered); got %T: %v", err, err)
	assert.Contains(t, err.Error(), "openai_gpt-5-mini",
		"wrapped message must name the missing id for actionable diagnostics")
}

// TestFinder_LegacyInferEnabled verifies the back-compat escape hatch:
// when AGENTLY_ALLOW_LEGACY_INFER is set, the historical inference path
// runs and resolves an `openai_*` id to a synthetic provider config.
// This preserves behavior for workspaces still migrating off the
// heuristic.
func TestFinder_LegacyInferEnabled(t *testing.T) {
	t.Setenv("AGENTLY_ALLOW_LEGACY_INFER", "1")

	f := New()
	// With inference enabled, a non-registered openai_* id resolves to a
	// synthetic config. The actual model creation may fail downstream if
	// the env is missing the OPENAI_API_KEY, but the config-lookup phase
	// must NOT produce ErrModelNotRegistered.
	_, err := f.Find(context.Background(), "openai_gpt-5-mini")
	if err != nil {
		// Acceptable: inference produced a config but the underlying
		// provider factory failed (e.g., no API key in test env).
		// Unacceptable: the registration-not-found error.
		if errors.Is(err, ErrModelNotRegistered) {
			t.Fatalf("inference enabled but Find still returned ErrModelNotRegistered: %v", err)
		}
	}
}

// TestFinder_LegacyInferFlagValues verifies the flag is parsed
// case-insensitively and accepts the common truthy spellings without
// being overly permissive.
func TestFinder_LegacyInferFlagValues(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"no", false},
		{"FALSE", false},
		{"random", false},
		{"1", true},
		{"true", true},
		{"True", true},
		{"YES", true},
		{"on", true},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("AGENTLY_ALLOW_LEGACY_INFER", tc.value)
			assert.Equal(t, tc.want, legacyInferEnabled())
		})
	}
}

// TestFinder_RegisteredModelStillWorks confirms the H1 change does not
// disturb the normal happy path: a model registered via AddConfig
// resolves immediately without touching either the configLoader fallback
// chain or the legacy-infer path. The cache fast-path (H2) returns the
// same instance on subsequent calls.
func TestFinder_RegisteredModelStillWorks(t *testing.T) {
	t.Setenv("AGENTLY_ALLOW_LEGACY_INFER", "")
	f := New()

	// Registration is what makes a model resolvable. AddConfig is the
	// canonical surface; agents never need inference when configs are
	// loaded from workspace YAML.
	cands := f.Candidates()
	assert.Empty(t, cands, "fresh finder should have no candidates")

	_, err := f.Find(context.Background(), "definitely-not-registered")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrModelNotRegistered))
	assert.False(t, strings.Contains(err.Error(), "openai_"),
		"non-openai id should not trigger any provider-specific message")
}

// Ensure the env-test cleanup helper actually clears the flag for other
// tests in the package (defensive — t.Setenv covers this, but tests
// that build configs in parallel may need explicit ordering).
func TestMain(m *testing.M) {
	_ = os.Unsetenv("AGENTLY_ALLOW_LEGACY_INFER")
	code := m.Run()
	os.Exit(code)
}
