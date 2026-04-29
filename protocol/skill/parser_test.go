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
	if len(diags) != 5 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	for _, diag := range diags {
		if diag.Level != "warn" {
			t.Fatalf("expected warn diagnostics, got %#v", diags)
		}
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
	if len(diags) != 1 || diags[0].Level != "warn" {
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

func TestParse_AcceptsMetadataAgentlyOverrides(t *testing.T) {
	content := `---
name: targeting-tree
description: Discover targeting options.
metadata:
  agently-context: inline
  agently-preprocess: "true"
  agently-preprocess-timeout: "30"
  agently-model: openai_gpt-5.4
allowed-tools: system/exec:execute platform:TargetingTree
---

body`
	s, diags, err := Parse("/tmp/SKILL.md", "/tmp/targeting-tree", "workspace", content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if got := s.Frontmatter.ContextMode(); got != "inline" {
		t.Fatalf("context = %q", got)
	}
	if !s.Frontmatter.PreprocessEnabled() {
		t.Fatal("expected preprocess enabled")
	}
	if got := s.Frontmatter.PreprocessTimeoutValue(); got != 30 {
		t.Fatalf("preprocess timeout = %d", got)
	}
	if got := s.Frontmatter.ModelValue(); got != "openai_gpt-5.4" {
		t.Fatalf("model = %q", got)
	}
}

func TestParse_AcceptsMetadataModelPreferences(t *testing.T) {
	content := `---
name: code-review
description: Review code thoroughly.
metadata:
  model-preferences:
    hints:
      - name: claude-opus
      - name: openai_gpt-5.4
    intelligencePriority: 0.9
    speedPriority: 0.2
    costPriority: 0.1
---

body`
	s, diags, err := Parse("/tmp/SKILL.md", "/tmp/code-review", "workspace", content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	prefs := s.Frontmatter.ModelPreferencesValue()
	if prefs == nil {
		t.Fatal("expected model preferences")
	}
	if len(prefs.Hints) != 2 || prefs.Hints[0] != "claude-opus" || prefs.Hints[1] != "openai_gpt-5.4" {
		t.Fatalf("hints = %#v", prefs.Hints)
	}
	if prefs.IntelligencePriority != 0.9 {
		t.Fatalf("intelligencePriority = %v", prefs.IntelligencePriority)
	}
	if prefs.SpeedPriority != 0.2 {
		t.Fatalf("speedPriority = %v", prefs.SpeedPriority)
	}
	if prefs.CostPriority != 0.1 {
		t.Fatalf("costPriority = %v", prefs.CostPriority)
	}
}

func TestParse_AcceptsRemainingMetadataAgentlyOverrides(t *testing.T) {
	content := `---
name: specialist
description: Specialized helper.
metadata:
  agently-agent-id: steward/specialist
  agently-effort: high
  agently-temperature: "0.3"
  agently-max-tokens: "12000"
  agently-async-narrator-prompt: Keep updates terse.
---

body`
	s, diags, err := Parse("/tmp/SKILL.md", "/tmp/specialist", "workspace", content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if got := s.Frontmatter.AgentIDValue(); got != "steward/specialist" {
		t.Fatalf("agent id = %q", got)
	}
	if got := s.Frontmatter.EffortValue(); got != "high" {
		t.Fatalf("effort = %q", got)
	}
	if temp := s.Frontmatter.TemperatureValue(); temp == nil || *temp != 0.3 {
		t.Fatalf("temperature = %#v", temp)
	}
	if got := s.Frontmatter.MaxTokensValue(); got != 12000 {
		t.Fatalf("max tokens = %d", got)
	}
	if got := s.Frontmatter.AsyncNarratorPromptValue(); got != "Keep updates terse." {
		t.Fatalf("async narrator prompt = %q", got)
	}
}

func TestParse_AcceptsNestedAgentlyMetadataBlock(t *testing.T) {
	content := `---
name: nested-demo
description: Nested metadata demo.
metadata:
  agently:
    context: detach
    agent-id: steward/nested-demo
    model: openai_gpt-5.4
    effort: high
    temperature: 0.2
    max-tokens: 9000
    preprocess: true
    preprocess-timeout: 12
    async-narrator-prompt: Keep it short.
---

body`
	s, diags, err := Parse("/tmp/SKILL.md", "/tmp/nested-demo", "workspace", content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	if got := s.Frontmatter.ContextMode(); got != "detach" {
		t.Fatalf("context = %q", got)
	}
	if got := s.Frontmatter.AgentIDValue(); got != "steward/nested-demo" {
		t.Fatalf("agent id = %q", got)
	}
	if got := s.Frontmatter.ModelValue(); got != "openai_gpt-5.4" {
		t.Fatalf("model = %q", got)
	}
	if got := s.Frontmatter.EffortValue(); got != "high" {
		t.Fatalf("effort = %q", got)
	}
	if temp := s.Frontmatter.TemperatureValue(); temp == nil || *temp != 0.2 {
		t.Fatalf("temperature = %#v", temp)
	}
	if got := s.Frontmatter.MaxTokensValue(); got != 9000 {
		t.Fatalf("max tokens = %d", got)
	}
	if !s.Frontmatter.PreprocessEnabled() {
		t.Fatal("expected preprocess enabled")
	}
	if got := s.Frontmatter.PreprocessTimeoutValue(); got != 12 {
		t.Fatalf("preprocess timeout = %d", got)
	}
	if got := s.Frontmatter.AsyncNarratorPromptValue(); got != "Keep it short." {
		t.Fatalf("async narrator prompt = %q", got)
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
	if len(diags) != 8 {
		t.Fatalf("expected 8 diagnostics, got %#v", diags)
	}
}

func TestParse_LegacyAgentlyTopLevelFieldsEmitWarnDiagnostics(t *testing.T) {
	content := `---
name: forecast
description: Forecast helper.
context: detach
model: openai_gpt-5.4
preprocess: true
preprocess-timeout: 15
---

body`
	_, diags, err := Parse("/tmp/SKILL.md", "/tmp/forecast", "workspace", content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(diags) != 4 {
		t.Fatalf("expected 4 diagnostics, got %#v", diags)
	}
	for _, diag := range diags {
		if diag.Level != "warn" {
			t.Fatalf("expected warn diagnostics, got %#v", diags)
		}
	}
}
