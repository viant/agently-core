# Skill Implementation — Roadmap to A Grade

Companion to [doc/skills.md](doc/skills.md). Trimmed roadmap from current composite **B−** to **A**, prioritizing reuse and small targeted improvements over structural rewrites.

---

## 1. Background

agently-core targets the [agentskills.io spec](https://agentskills.io/specification). Primary goal: portability — a skill folder authored once runs unchanged in any compliant runtime.

**Current state, verified**:

| Layer | Location | Status |
|---|---|---|
| Portable core (parser, registry, render) | `protocol/skill/` | shipped |
| Discovery (roots, loader, source detection) | `workspace/repository/skill/` | shipped |
| Service (list/activate, constraints, fork/detach) | `service/skill/` | shipped |
| fsnotify watcher | `service/skill/watcher.go` | shipped |
| Metadata-backed accessors | `protocol/skill/skill.go` | shipped |
| Warn-level legacy-key diagnostics | `protocol/skill/parser.go` | shipped |
| Active-skill model-pref selection | `service/agent/run_query.go:666–689` | shipped |
| `parseModelPreferences` (shape-tolerant: accepts `hints: [{name:X}]` and `hints: [X]`) | `protocol/skill/skill.go:297` | shipped |
| CLI `agently skill <list|activate|diagnostics|show|validate>` | `cmd/agently/main.go:115` | shipped |

**Comparison with Claude / Codex**: agently exceeds (fork/detach, hot-reload, dual-ecosystem roots), matches (spec frontmatter, progressive disclosure, model-driven activation), lags (shadowing diagnostics, install CLI).

---

## 2. Activation model — locked in

| Surface | Mechanism |
|---|---|
| Model-driven | `llm_skills-list` + `llm_skills-activate` tools |
| Programmatic | `service.Activate(agent, name, args)` |

**No user-typed prefix** (`$`/`@`/`/`). The model already routes natural language correctly via descriptions. Reconsider only if real users complain.

---

## 3. Subsystem grade matrix

| # | Subsystem | Today | Target | Effort |
|---|---|---|---|---|
| S2 | Metadata-backed accessors | A− | A | S |
| S3 | Discovery / loader / roots | B | A | S |
| S4 | Hot-reload watcher | B+ | A | S |
| S5 | Service activation | A− | A | S |
| S6 | Constraints / `allowed-tools` | A− | A | S |
| S8 | Preprocess (`!`-blocks) | B− | A | S |
| S9 | MCP `ModelPreferences` end-to-end | A− | A | S |
| S10 | Test coverage | C+ | A | S |
| S12 | Skill installation (CLI `add` only, v1) | F | A | S |

**Dropped from the roadmap** (overkill / premature):

- ~~S1 (split `Frontmatter` struct)~~ — accessor pattern already abstracts the messy fields. Structural cleanup with no caller-visible benefit. Defer indefinitely.
- ~~S7 (window-relative budget)~~ — hardcoded 4000 chars works for users on small windows. Defer until a user reports truncation on a large-window model.
- ~~S11 (tool naming)~~ — already A.

---

## 4. Roadmap

### S2. Accessor docs + tests → A
Add godoc to each accessor stating resolution order; add 1 unit test fixture per accessor exercising (metadata-only, bare-only, both-set). **0.25d.**

### S3. Shadowing diagnostics → A
Track every `(name, root, path)` tuple in [workspace/repository/skill/loader.go:20–57](workspace/repository/skill/loader.go); after scan, for each name with >1 entry, emit `skillproto.Diagnostic{Level: "info", Message: "skill 'X' shadowed: winner=<root> shadowed=<root>"}` via the existing registry diagnostic channel. `agently skill list` already surfaces registry diagnostics. **0.5d.**

### S4. Watcher diff event → A
Decision: **Replace** (no parallel events). In [service/skill/watcher.go](service/skill/watcher.go), capture pre-reload registry snapshot, compute diff (`added`/`changed`/`removed` by name), extend the existing `EventTypeSkillRegistryUpdated` Patch with those arrays. Same `EventType` constant, same publisher. Audit subscribers first to confirm safe payload extension. **0.5d.**

### S5. Fork-capability sentinel error → A
In [service/skill/service.go:589–592](service/skill/service.go) `activateChildConversation`, return typed sentinel when `ExecFn == nil`:

```go
var ErrForkCapabilityUnavailable = errors.New(
    "skill requested fork/detach but ExecFn (used for both llm/agents:start and llm/agents:status) is not bound")
```

`ExecFn` is the single dependency for both `llm/agents:start` (kicks off child) and `llm/agents:status` (polls via `awaitChildConversationTerminal`). Detach mode tells the LLM to poll status itself per the activate-tool description at [service.go:89](service/skill/service.go). Reuse existing `finishNestedToolCall` event path; no new event type. **0.25d.**

### S6. Allowed-tools warn diagnostic → A
In [service/skill/constraints.go:166–197](service/skill/constraints.go), when `ExpandDefinitionsForConstraints()` cannot match a requested tool pattern, return the unmatched pattern; activation path appends `skillproto.Diagnostic{Level: "warn"}` to the existing diagnostics slice that flows into the `skill.activated` event Patch. **0.25d.**

### S8. Preprocess docs + lock-in tests → A
Timeout enforcement is **already implemented** at [preprocess.go:119–124](service/skill/preprocess.go). Decision: **keep** (not remove). Work:

1. Document `metadata.agently-preprocess` and `metadata.agently-preprocess-timeout` in skills.md §4a as Agently extensions with portability warning.
2. Add expansion test (`!date` → output contains today's date) and timeout test (`!sleep 30` + `timeout: 1` → aborts within ~1s). **0.5d.**

### S9. MCP `ModelPreferences` end-to-end → A
**Already shipped** at [run_query.go:666–689](service/agent/run_query.go) — active-skill prefs flow through `MatchModelIfNeeded()` to the existing `Finder.BestWithFilter()`. Remaining: targeted tests covering hint-only, priority-only, combined fixtures. Bundle with [modelpref-pkg.md H3](modelpref-pkg.md) (two-pass matcher) — same code path. `FromEffort` deprecation helper is **not** scheduled v1; check if anyone uses bare `effort:` first. **0.5d.**

### S10. Portability test fixtures → A (trimmed)
Create `protocol/skill/testdata/portable/` with **4 fixtures only** (down from 12):

- `upstream-anthropic-pdf/SKILL.md` — copied verbatim from `anthropics/skills/skills/pdf` at a fixed SHA. `_SOURCE` records origin.
- `upstream-codex-pdf/SKILL.md` — copied verbatim from `openai/skills/skills/.curated/pdf` at a fixed SHA.
- `agently-modern/SKILL.md` — uses `metadata.agently-context: fork`, `metadata.model-preferences`.
- `agently-legacy/SKILL.md` — bare top-level `context: fork`, `model: claude-opus`.

Static snapshots, no refresh process, no nightly canary, no curated catalog. License attributions added to [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Single test asserts: upstream parses with zero errors and zero `AgentlyOverrides`; agently-legacy emits ≥2 warn diagnostics; round-trip preserves metadata. **0.5d.**

### S12. Skill installation — `agently skill add` only → A (staged)

v1 ships **only `agently skill add`**. `remove` is `rm -rf <root>/<name>`; `update` is `add` over top — no separate commands needed.

Extend [cmd/agently/main.go:112,115](cmd/agently/main.go) `skill` switch with `add`. Sources:
- `<git-url>` (with optional `#path=skills/X#ref=v1`)
- `<local-path>`
- `<curated-id>` (e.g. `anthropics/pdf`) — small workspace-config lookup table, not heuristic

Workflow:
1. Fetch into temp dir.
2. Validate via existing `skillproto.Parse`. Reject on error-level diagnostic; show warn diagnostics before confirmation.
3. Show name/description/license/allowed-tools/preprocess opt-in. Loud confirmation if `allowed-tools` non-empty or `agently-preprocess: true`.
4. Require explicit `[y/N]` unless `--yes`.
5. Move into `${AGENTLY_WORKSPACE}/skills/<name>` (default) or `~/.agently/skills/<name>` with `--root user` (add `~/.agently/skills` to discovery roots).
6. Write `_SOURCE` (origin URL, SHA, timestamp).
7. fsnotify watcher (S4) detects directory write and reloads registry. **No install-side event emission.**

Trust model (v1): explicit confirmation; no signing; `_SOURCE` enables auditing. **2d total.**

---

## 5. Sequencing

- **Group 1** (≤0.5d each): S2, S3, S4, S5, S6, S8, S9, S10. ~3d total.
- **Group 2**: S12 (depends on S4 watcher landing first so installs propagate without restart). 2d.

**Total: ~5d** (down from 7–10d).

---

## 6. Backward compatibility

All bare top-level keys keep working with warn diagnostics. No struct splits, no type migrations, no breaking changes. Hard-erroring on bare keys is **out of scope** indefinitely.

---

## 7. Out of scope

- HTTP API, web UI, marketplace.
- Cryptographic skill signing.
- `agently skill remove` / `update` subcommands (use `rm` + `add` instead).
- User-prefix activation (`$`/`/`/`@`).
- `Frontmatter` struct split (S1 deferred).
- Window-relative prompt budget (S7 deferred).
- `FromEffort` deprecation helper (only if real users surface).

---

## 8. Definition of done

- [ ] Every metadata accessor has godoc + 3 test cases (S2)
- [ ] Shadowing diagnostics emitted via existing registry channel (S3)
- [ ] Watcher's existing `EventTypeSkillRegistryUpdated` event extended with `{added, changed, removed}` — no parallel event (S4)
- [ ] `ErrForkCapabilityUnavailable` typed sentinel; reuses existing `finishNestedToolCall` event path (S5)
- [ ] Unavailable-tool requests append warn diagnostics to existing `skill.activated` event Patch (S6)
- [ ] `metadata.agently-preprocess` documented + expansion + timeout tests green (S8)
- [ ] Targeted model-preferences tests green; bundles with H3 cleanup (S9)
- [ ] 4 portability fixtures parse cleanly with expected diagnostics (S10)
- [ ] `agently skill add` ships; `_SOURCE` provenance written; fsnotify watcher handles registry reload (S12)
- [ ] Existing test suite passes unchanged
- [ ] Activation surfaces remain exactly two: `llm/skills:*` tools and `service.Activate()`

When every box is checked, composite grade moves from **B−** to **A**.
