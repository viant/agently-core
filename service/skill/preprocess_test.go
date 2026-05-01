package skill

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestExtractPlaceholders_LocksIn covers the body-parsing contract for the
// preprocess feature. Ensures both the fenced ` ```!\ncmd\n``` ` form and
// the inline `!cmd` form are recognized. This is the foundation of S8 —
// without correct extraction the preprocess opt-in (which is gated by
// `metadata.agently-preprocess: true`) wouldn't function.
//
// Lock-in test: future refactors that change the regex must keep these
// shapes recognized or the documented examples in doc/skills.md §4a would
// stop working without warning.
func TestExtractPlaceholders_LocksIn(t *testing.T) {
	// Inline form: `!`cmd` (the literal pattern reInline at preprocess.go:20).
	t.Run("inline command produces single placeholder", func(t *testing.T) {
		body := "Today is `!`date` and that's it."
		got := extractPlaceholders([]byte(body))
		if assert.Len(t, got, 1) {
			assert.Equal(t, "date", strings.TrimSpace(got[0].cmd))
		}
	})

	t.Run("fenced block produces single placeholder", func(t *testing.T) {
		body := "Run this:\n```!\necho hello\n```\nDone."
		got := extractPlaceholders([]byte(body))
		if assert.Len(t, got, 1) {
			assert.Contains(t, got[0].cmd, "echo hello")
		}
	})

	t.Run("body with no markers yields empty slice", func(t *testing.T) {
		body := "Plain markdown with no command markers."
		got := extractPlaceholders([]byte(body))
		assert.Empty(t, got)
	})

	t.Run("multiple inline commands", func(t *testing.T) {
		body := "First `!`date`, then `!`whoami`, finally `!`pwd`."
		got := extractPlaceholders([]byte(body))
		assert.Len(t, got, 3)
	})

	t.Run("fenced and inline coexist", func(t *testing.T) {
		body := "Inline `!`date`. Block:\n```!\nls -la\n```"
		got := extractPlaceholders([]byte(body))
		assert.Len(t, got, 2)
	})

	t.Run("regex syntax notes (lock-in)", func(t *testing.T) {
		// The inline marker pattern is `!`cmd` — not the simpler `!cmd`.
		// Skills authored with `!cmd` are NOT recognized; this test
		// documents the actual pattern so a future refactor that changes
		// the regex notices.
		assert.Empty(t, extractPlaceholders([]byte("Skipped `!date` here")),
			"plain `!cmd` (without leading `!) is intentionally NOT matched")
	})
}
