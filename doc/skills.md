# Agent Skills Support in agently-core

Status: implemented for inline, fork, and detach skills.

## 1. Goal

Add support for the open `SKILL.md` format used by Codex, Claude Code, Cursor,
and related runtimes so the same skill folder can run in Agently with minimal
or no changes.

The primary goal is portability.
The secondary goal is clean integration with Agently's existing agent, tool,
workspace, and MCP architecture.

Agently should target full implementation of the official Anthropic / Agent
Skills core spec before adding Agently-specific extensions.

Reference spec: [agentskills.io/specification](https://agentskills.io/specification)

## 2. Design principles

1. Preserve the standard skill contract.
   Agently should consume `SKILL.md` skills as-authored, not require a new
   Agently-specific skill format.

2. Keep the portable core small.
   The MVP should focus on discovery, parsing, metadata exposure, explicit load,
   and resource/script access under Agently policy controls.

3. Treat Agently integration as a separate layer.
   Agent YAML wiring, HTTP endpoints, SDK methods, CLI commands, MCP exposure,
   and watcher support are Agently conveniences, not the core compatibility
   contract.

4. Prefer additive behavior.
   Workspaces and agents that do not use skills should behave exactly as they do
   today.

5. Implement the official core spec first.
   Agently-specific behavior should be layered on top of the portable core, not
   mixed into it from the start.

6. Minimize Agently-only extensions.
   If Agently supports extra frontmatter or execution behavior, it should be
   optional and clearly separated from the core portable format.

## 3. Non-goals

- Defining a new skill standard
- Building a marketplace or packaging system
- Replacing the existing reactor or prompt pipeline
- Requiring authors to rewrite Codex- or Claude-authored skills for Agently

## 4. Compatibility target

Agently should aim to support the standard skill folder layout:

```text
<skill-root>/
├── SKILL.md
├── scripts/
├── references/
├── assets/
└── agents/   # optional, per-runtime metadata only
```

### Core behavior to support

- Parse `SKILL.md` frontmatter and markdown body
- Surface metadata at startup or turn-begin time
- Load the full body on demand
- Let the model or runtime read referenced resources lazily
- Run referenced scripts only through Agently's existing approval and tool policy

### Core spec compliance target

Agently should fully implement the official Anthropic / Agent Skills core
behavior, including:

- canonical `SKILL.md` discovery
- required frontmatter parsing and validation
- progressive disclosure
- portable folder layout support
- preservation of unknown frontmatter fields
- compatibility with skills authored for Anthropic-style runtimes without
  requiring skill rewrites

### Forward-compatibility

Unknown frontmatter fields should be preserved and ignored unless Agently
explicitly chooses to honor them.

That keeps Agently aligned with the existing ecosystem and avoids breaking
skills authored for other runtimes.

### Current `context:` behavior

Agently now parses `context:` as a first-class frontmatter field. A portable
skill containing:

```yaml
---
name: demo
description: Demo
context: fork
---
```

will:

- parse successfully
- normalize `context:` to one of `inline`, `fork`, or `detach`
- activate inline when `context` is empty or `inline`
- launch a child `llm/agents:start` run when `context` is `fork` or `detach`
- use `llm/agents:status` to wait for terminal child output in `fork` mode

Fork/detach reuse the existing `llm/agents` runtime rather than introducing a
second skill-specific orchestration loop. The child run executes the same
explicit skill invocation inside the child conversation, so preprocess,
allowed-tools, tool-surface augmentation, and skill constraints are enforced
there through the existing inline skill path.

## 5. Workspace model

This is the main architectural decision that must be settled early.

Today Agently centers its workspace on the `.agently` root and predefined kinds
such as `agents`, `models`, `tools`, `mcp`, `workflows`, and `templates`.

Skills are different because they are intended to be portable across runtimes.

### Recommended approach

Use `<agently_workspace>/skills` as the canonical Agently-managed root, while
also supporting compatible external roots.

Support both:

1. Primary Agently-managed root
   Example: `<agently_workspace>/skills`

2. Compatible external roots
   Examples: `./skills`, `./.codex/skills`, `./.claude/skills`,
   `~/.codex/skills`, `~/.claude/skills`

This keeps Agently aligned with its existing workspace model while preserving
drop-in compatibility with other skill ecosystems.

### Default lookup list in workspace config

The default lookup order should be declared in workspace default config rather
than hard-coded only inside the skill loader.

Recommended shape:

```yaml
default:
  skills:
    roots:
      - ${AGENTLY_WORKSPACE}/skills
      - ./skills
      - ./.claude/skills
      - ./.codex/skills
      - ~/.claude/skills
      - ~/.codex/skills
```

This keeps precedence explicit, makes local policy configurable, and matches the
existing pattern where workspace defaults influence runtime behavior.

The loader should use this configured list when present and fall back to the
built-in default order only when the config is absent.

### Recommendation details

- Treat `<agently_workspace>/skills` as the first-class managed location.
- Still allow discovery from compatible Claude and Codex roots.
- Treat external roots as additional discovery sources, not as proof that the
  whole workspace layout has changed.
- Update workspace bootstrap, predefined kinds, transfer, and hotswap
  intentionally if `skills` becomes a first-class workspace kind.
  Runtime scanning and watching should start after workspace initialization, not
  inside the bootstrap package itself.

## 7. Discovery and precedence

Discovery should support multiple roots in precedence order.

Recommended default order:

1. Roots listed in workspace default config
2. Built-in fallback order when config is absent:
   `<agently_workspace>/skills`, `./skills`, `./.claude/skills`,
   `./.codex/skills`, `~/.claude/skills`, `~/.codex/skills`
3. Optional embedded system skills

First match wins on name collision.
Lower-precedence duplicates are ignored but retained as diagnostics so the
runtime and CLI can explain which definition was selected and which were
shadowed.

The exact precedence can be tuned later, but the rule should be deterministic
and documented.

### Load timing

Skill metadata must be loaded **at runtime startup**, not lazily on the first
turn that needs it. Concretely:

- `<agently_workspace>/skills` is scanned after workspace initialization has
  completed and the runtime is being assembled.
- Configured fallback roots are scanned in the same runtime-start pass so that
  precedence and shadowing diagnostics are known before the first turn runs.
- Only the metadata tier (frontmatter `name` + `description`) is held in
  memory; bodies stay on disk until `llm/skills-activate` is called or the user
  explicitly activates a skill.

Reasons:

- first-turn latency is not spent on filesystem scans
- parse and validation errors surface at startup, not mid-conversation
- startup-time CLI such as `agently skill list` and the workspace-status
  endpoint see the full registry immediately
- watcher-driven hot reload in the MVP is an incremental patch over the
  runtime-start registry, not a fallback for never-having-loaded

## 9. Activation model

The plan should support two activation modes.

### Explicit activation

Support compatibility-friendly invocations such as:

- `/skill-name`
- `$skill-name`

These should map to the same activation path.

### Model-driven activation

Expose two tool-level capabilities:

- `llm/skills-list` — return the high-level list (`name` + `description`) so
  the model can decide which skill fits the task
- `llm/skills-activate` — activate one skill: inject the `SKILL.md` body into
  the current turn, augment the bound tool surface from the skill's
  `allowed-tools` patterns, then apply skill constraints/narrowing and emit a
  `skill.activated` event. Does **not** execute scripts — scripts run through
  the normal tool surface after activation.

Important contract:

- Inline skill activation is now allowed to add specialized tools that the
  owning agent did not permanently expose.
- The active skill, not the owning agent, is the capability boundary for those
  specialized tools.
- The final exposed tool set for the turn is:
  1. the existing bound tool surface
  2. plus any tool definitions matched from the active skill's
     `allowed-tools`
  3. then constrained back down to the active skill's allowed surface
- This keeps large orchestrator agents small by default while still letting a
  skill contribute its own MCP/tool access when explicitly activated.

This matches progressive disclosure and keeps the full skill body out of the
prompt until needed. There is deliberately no install/load tool at the LLM
surface: the filesystem watcher (Phase 1) handles new-skill pickup, and the
agentskills.io spec itself does not define such a tool.

## 10. Prompt strategy

Skills should plug into the existing prompt and binding pipeline.

Recommended model:

- inject only metadata by default
- carry structured skill metadata in `Skills`
- carry the rendered compact handoff block in `SkillsPrompt`
- inject the full body only when explicitly activated or requested via
  `llm/skills-activate`
- never auto-inject `references/`, `assets/`, or script contents
- cap the listing block at a configurable budget, defaulting to about 2% of the
  active context window; overflow should drop lowest-precedence entries first
  and include a truncation marker

This keeps prompts small and aligns with the behavior used by other runtimes.

## 11. Script execution

Script execution is useful, but it must remain subordinate to Agently's
existing safety model.

Rules:

- scripts run only through Agently's tool and approval system
- default workspace stance is allow; deployments may tighten review explicitly
- a skill can narrow the allowed tool set, but not widen it
- working directory should be the skill root
- relative paths should resolve from the skill root

Interpreter detection can be based on shebang and file extension.

## 12. Agent integration

Agent-level skill scoping is useful, but it should not be part of the MVP if it
slows down basic compatibility.

Recommended shape for a later phase: **closed by default**. An agent sees a
skill only when its `skills:` whitelist matches the skill's `name`.

```yaml
# agent YAML — later-phase addition
skills:
  - "*"                 # all discovered skills
  # or any mix of the following:
  - release-notes       # exact name
  - pr-*                # prefix glob
  - "*-review"          # suffix glob
  - "!legal-*"          # negation — subtracts from the matched set
```

Rules:

- `skills:` omitted → agent sees **no** skills (closed default, same posture
  as `tool.bundles`)
- `skills: []` → same as omitted (explicit empty)
- `skills: ["*"]` → all discovered skills
- Positive entries union; negation entries (`!prefix`) subtract last
- Wildcards match against skill `name`, not path

This should be framed as Agently integration, not as part of the portable skill
contract.

## 13. API and SDK scope

HTTP and SDK support are valuable, but they are not required for the first
portable implementation.

If added later, they should expose:

- list skill metadata
- fetch `SKILL.md` body in a read-only way
- validate a skill
- reload or rescan the registry

These are management APIs, not the core runtime design.
