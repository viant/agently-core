---
name: agently-legacy
description: Synthetic test fixture exercising the deprecated bare-key surface — context, model, effort, temperature, and preprocess at the top level instead of under metadata.agently-*. The parser keeps these working but emits warn-level diagnostics urging migration to the metadata namespace.
license: MIT
context: fork
model: claude-opus
effort: high
temperature: 0.4
max-tokens: 16000
preprocess: true
preprocess-timeout: 30
async-narrator-prompt: "Reviewing the change..."
agent-id: forecast/legacy
---

# Legacy Agently Skill

Authors with skills in this shape see warn diagnostics on parse but the
runtime continues to honor the resolved values until the deprecation window
closes (skill-impr.md A.7 timeline).
