package skill

import "testing"

func TestParse_AcceptsFrontmatterOverrides(t *testing.T) {
	temp := 0.2
	content := `---
name: legal-review
description: Formal legal review.
context: fork
model: openai_gpt-5.4
effort: high
temperature: 0.2
max-tokens: 8000
allowed-tools: system/exec:execute
---

body`
	s, diags, err := Parse("/tmp/SKILL.md", "/tmp/legal-review", "workspace", content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if s == nil {
		t.Fatal("expected skill")
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if s.Frontmatter.Model != "openai_gpt-5.4" {
		t.Fatalf("model = %q", s.Frontmatter.Model)
	}
	if s.Frontmatter.ContextMode() != "fork" {
		t.Fatalf("context = %q", s.Frontmatter.ContextMode())
	}
	if s.Frontmatter.Effort != "high" {
		t.Fatalf("effort = %q", s.Frontmatter.Effort)
	}
	if s.Frontmatter.Temperature == nil || *s.Frontmatter.Temperature != temp {
		t.Fatalf("temperature = %#v", s.Frontmatter.Temperature)
	}
	if s.Frontmatter.MaxTokens != 8000 {
		t.Fatalf("max tokens = %d", s.Frontmatter.MaxTokens)
	}
}

func TestParse_DefaultContextModeIsFork(t *testing.T) {
	content := `---
name: demo
description: Demo
---

body`
	s, diags, err := Parse("/tmp/SKILL.md", "/tmp/demo", "workspace", content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if got := s.Frontmatter.ContextMode(); got != "fork" {
		t.Fatalf("default context = %q", got)
	}
}

func TestParse_AcceptsAsyncNarratorPromptOverride(t *testing.T) {
	content := `---
name: delivery-impact-check
description: Inspect delivery impact for an order.
async-narrator-prompt: |
  Write a crisp one-line progress update for a delivery-impact lookup.
  Mention the order and the dimension being checked. No filler phrases.
---

body`
	s, diags, err := Parse("/tmp/SKILL.md", "/tmp/delivery-impact-check", "workspace", content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	want := "Write a crisp one-line progress update for a delivery-impact lookup.\nMention the order and the dimension being checked. No filler phrases."
	if s.Frontmatter.AsyncNarratorPrompt != want {
		t.Fatalf("AsyncNarratorPrompt = %q want %q", s.Frontmatter.AsyncNarratorPrompt, want)
	}
	// Raw should NOT leak the field (it has a dedicated slot now).
	if _, ok := s.Frontmatter.Raw["async-narrator-prompt"]; ok {
		t.Fatalf("async-narrator-prompt should not be present in Raw")
	}
}

func TestParse_RejectsInvalidOverrideValues(t *testing.T) {
	content := `---
name: legal-review
description: Formal legal review.
context: sideways
effort: extreme
temperature: 9
max-tokens: 300000
---

body`
	_, diags, err := Parse("/tmp/SKILL.md", "/tmp/legal-review", "workspace", content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(diags) != 4 {
		t.Fatalf("expected 4 diagnostics, got %#v", diags)
	}
}
