---
name: agently-modern
description: Synthetic test fixture exercising the modern Agently extension surface — every Agently-only knob lives under metadata.agently-* with vendor prefix, plus model-preferences mirrors the MCP shape. Used by parser portability tests to lock in the canonical "spec-clean" form.
license: Apache-2.0
metadata:
  agently-context: fork
  agently-temperature: 0.4
  agently-max-tokens: 16000
  agently-preprocess: true
  agently-preprocess-timeout: 30
  agently-async-narrator-prompt: "Reviewing the change..."
  model-preferences:
    hints:
      - claude-opus
    intelligencePriority: 0.9
    speedPriority: 0.2
---

# Modern Agently Skill

This is a placeholder body. Real fixture skills can have arbitrary markdown
content; the parser tests only assert on frontmatter resolution.
