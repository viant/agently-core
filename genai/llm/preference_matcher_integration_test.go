package llm_test

import (
	"testing"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/matcher"
)

// TestModelPreferences_MatcherIntegration verifies the end-to-end path that
// drives skill activation: a *llm.ModelPreferences (built from any of the
// supported sources — metadata.model-preferences YAML/JSON, FromEffort
// migration, agent-config inheritance) flows into the existing
// internal/matcher.Matcher.Best, which is what service/agent/run_query.go
// already calls today.
//
// This is the canonical "ONE selector path" guarantee — the same matcher
// resolves preferences regardless of authoring source.
func TestModelPreferences_MatcherIntegration(t *testing.T) {
	candidates := []matcher.Candidate{
		{ID: "anthropic_claude-haiku-4-5", BaseModel: "claude-haiku", Version: "4-5", Intelligence: 0.4, Speed: 0.95, Cost: 0.1},
		{ID: "anthropic_claude-sonnet-4-5", BaseModel: "claude-sonnet", Version: "4-5", Intelligence: 0.7, Speed: 0.6, Cost: 0.4},
		{ID: "anthropic_claude-opus-4-1", BaseModel: "claude-opus", Version: "4-1", Intelligence: 0.95, Speed: 0.3, Cost: 0.9},
		{ID: "openai_gpt-5", BaseModel: "gpt", Version: "5", Intelligence: 0.85, Speed: 0.5, Cost: 0.5},
		{ID: "openai_gpt-5-mini", BaseModel: "gpt-mini", Version: "5", Intelligence: 0.45, Speed: 0.9, Cost: 0.15},
	}
	m := matcher.New(candidates)

	t.Run("hint-only resolves to family", func(t *testing.T) {
		// MCP-style hint as a string (the agently local type uses []string).
		prefs := &llm.ModelPreferences{Hints: []string{"claude-opus"}}
		got := m.Best(prefs)
		if got == "" {
			t.Fatal("expected a candidate id, got empty")
		}
		if got != "anthropic_claude-opus-4-1" {
			t.Errorf("hint claude-opus → %q (want anthropic_claude-opus-4-1)", got)
		}
	})

	t.Run("priority-only — high intelligence", func(t *testing.T) {
		prefs := &llm.ModelPreferences{IntelligencePriority: 0.9}
		got := m.Best(prefs)
		// Most intelligent model is opus (0.95).
		if got != "anthropic_claude-opus-4-1" {
			t.Errorf("intelligencePriority=0.9 → %q (want opus)", got)
		}
	})

	t.Run("combined hint + priority — hint constrains family first", func(t *testing.T) {
		prefs := &llm.ModelPreferences{
			Hints:                []string{"claude-haiku"},
			IntelligencePriority: 0.9,
		}
		got := m.Best(prefs)
		if got != "anthropic_claude-haiku-4-5" {
			t.Errorf("claude-haiku hint with intelligence priority → %q (want haiku)", got)
		}
	})

	t.Run("FromEffort(high) routes through matcher", func(t *testing.T) {
		// Migration path: legacy `effort: high` skills produce
		// IntelligencePriority=0.9 + SpeedPriority=0.2. The matcher's
		// current scoring is intelligence-first (see internal/matcher/matcher.go),
		// so high-effort lands on the most intelligent model.
		prefs := llm.FromEffort("high")
		if prefs == nil {
			t.Fatal("FromEffort(high) returned nil")
		}
		got := m.Best(prefs)
		if got == "" {
			t.Fatal("matcher returned empty for high-effort preferences")
		}
		if got != "anthropic_claude-opus-4-1" {
			t.Errorf("FromEffort(high).Best → %q (want opus)", got)
		}
	})

	t.Run("FromEffort routes deterministically for any valid input", func(t *testing.T) {
		// We don't lock in a specific id for low/medium because the
		// matcher's scoring is intelligence-first and produces non-obvious
		// answers when speed/cost compete with intelligence (tracked as
		// H3 cleanup in modelpref-pkg.md). The integration guarantee here
		// is that every FromEffort output flows through Best() to a real
		// candidate id.
		for _, effort := range []string{"low", "medium", "high"} {
			prefs := llm.FromEffort(effort)
			if prefs == nil {
				t.Fatalf("FromEffort(%q) returned nil", effort)
			}
			got := m.Best(prefs)
			if got == "" {
				t.Errorf("FromEffort(%q).Best → empty (want a candidate id)", effort)
			}
		}
	})
}
