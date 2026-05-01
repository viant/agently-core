package skill

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
)

// TestDiffRegistries covers the diff calculation that powers the watcher's
// {added, changed, removed} payload. Inputs are name→fingerprint maps;
// outputs are sorted name slices.
func TestDiffRegistries(t *testing.T) {
	t.Run("first load reports every name as added", func(t *testing.T) {
		added, changed, removed := diffRegistries(
			nil,
			map[string]string{"a": "1", "b": "1"},
		)
		assert.Equal(t, []string{"a", "b"}, added)
		assert.Empty(t, changed)
		assert.Empty(t, removed)
	})

	t.Run("no diff when identical", func(t *testing.T) {
		current := map[string]string{"a": "1", "b": "2"}
		added, changed, removed := diffRegistries(current, current)
		assert.Empty(t, added)
		assert.Empty(t, changed)
		assert.Empty(t, removed)
	})

	t.Run("addition", func(t *testing.T) {
		added, changed, removed := diffRegistries(
			map[string]string{"a": "1"},
			map[string]string{"a": "1", "b": "1"},
		)
		assert.Equal(t, []string{"b"}, added)
		assert.Empty(t, changed)
		assert.Empty(t, removed)
	})

	t.Run("removal", func(t *testing.T) {
		added, changed, removed := diffRegistries(
			map[string]string{"a": "1", "b": "1"},
			map[string]string{"a": "1"},
		)
		assert.Empty(t, added)
		assert.Empty(t, changed)
		assert.Equal(t, []string{"b"}, removed)
	})

	t.Run("change (same name, different fingerprint)", func(t *testing.T) {
		added, changed, removed := diffRegistries(
			map[string]string{"a": "old"},
			map[string]string{"a": "new"},
		)
		assert.Empty(t, added)
		assert.Equal(t, []string{"a"}, changed)
		assert.Empty(t, removed)
	})

	t.Run("mixed adds, changes, removes — sorted output", func(t *testing.T) {
		added, changed, removed := diffRegistries(
			map[string]string{"keep": "x", "drop1": "y", "drop2": "y", "edit": "old"},
			map[string]string{"keep": "x", "edit": "new", "new1": "z", "new2": "z"},
		)
		assert.Equal(t, []string{"new1", "new2"}, added)
		assert.Equal(t, []string{"edit"}, changed)
		assert.Equal(t, []string{"drop1", "drop2"}, removed)
	})

	t.Run("empty current means everything removed", func(t *testing.T) {
		added, changed, removed := diffRegistries(
			map[string]string{"a": "1", "b": "1"},
			map[string]string{},
		)
		assert.Empty(t, added)
		assert.Empty(t, changed)
		assert.Equal(t, []string{"a", "b"}, removed)
	})
}

// TestExpandDefinitionsForConstraintsWithDiag_ReportsUnmatched verifies the
// S6 contract: the variant returns each allowed-tools pattern that did not
// resolve to any registered tool. Reuses the constraintRegistry fake from
// constraints_test.go (same package).
func TestExpandDefinitionsForConstraintsWithDiag_ReportsUnmatched(t *testing.T) {
	t.Run("nil constraints → no diagnostics", func(t *testing.T) {
		_, unmatched := ExpandDefinitionsForConstraintsWithDiag(nil, &constraintRegistry{}, nil)
		assert.Nil(t, unmatched)
	})

	t.Run("all patterns matched → empty unmatched", func(t *testing.T) {
		c := &Constraints{ToolPatterns: []string{"read"}}
		_, unmatched := ExpandDefinitionsForConstraintsWithDiag(
			nil,
			&constraintRegistry{defs: []llm.ToolDefinition{{Name: "Read"}}},
			c,
		)
		assert.NotNil(t, unmatched)
		assert.Empty(t, unmatched)
	})

	t.Run("one pattern missing → reported", func(t *testing.T) {
		c := &Constraints{ToolPatterns: []string{"read", "missingtool"}}
		got, unmatched := ExpandDefinitionsForConstraintsWithDiag(
			nil,
			&constraintRegistry{defs: []llm.ToolDefinition{{Name: "Read"}}},
			c,
		)
		assert.Len(t, got, 1, "matched defs returned even when one pattern misses")
		assert.Equal(t, []string{"missingtool"}, unmatched)
	})

	t.Run("multiple patterns missing", func(t *testing.T) {
		c := &Constraints{ToolPatterns: []string{"foo", "bar", "baz"}}
		_, unmatched := ExpandDefinitionsForConstraintsWithDiag(
			nil,
			&constraintRegistry{},
			c,
		)
		assert.ElementsMatch(t, []string{"foo", "bar", "baz"}, unmatched)
	})

	t.Run("non-WithDiag wrapper preserves prior behavior", func(t *testing.T) {
		c := &Constraints{ToolPatterns: []string{"read", "missingtool"}}
		out := ExpandDefinitionsForConstraints(
			nil,
			&constraintRegistry{defs: []llm.ToolDefinition{{Name: "Read"}}},
			c,
		)
		assert.Len(t, out, 1, "wrapper still returns matched defs")
	})
}

// TestErrForkCapabilityUnavailable_IsDetectable verifies the typed sentinel
// survives wrapping via fmt.Errorf and remains detectable via errors.Is.
// Callers (e.g. runtime gracefully degrading to inline) need to recover
// programmatically without string-matching.
func TestErrForkCapabilityUnavailable_IsDetectable(t *testing.T) {
	t.Run("direct match", func(t *testing.T) {
		assert.True(t, errors.Is(ErrForkCapabilityUnavailable, ErrForkCapabilityUnavailable))
	})

	t.Run("wrapped with fmt.Errorf", func(t *testing.T) {
		wrapped := fmt.Errorf("requested mode=%q: %w", "fork", ErrForkCapabilityUnavailable)
		assert.True(t, errors.Is(wrapped, ErrForkCapabilityUnavailable),
			"wrapped error must still match via errors.Is")
	})

	t.Run("error message names ExecFn", func(t *testing.T) {
		assert.Contains(t, ErrForkCapabilityUnavailable.Error(), "ExecFn",
			"sentinel must name the actual missing dependency, not 'llm/agents:start' specifically")
	})
}
