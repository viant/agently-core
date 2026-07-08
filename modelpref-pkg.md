# Model preferences — reuse the existing finder

The model-preferences selector, matcher, and finder already exist end-to-end. There is **no new package**, no new selector, and no type migration. This doc enumerates the small wire-up + cleanup work and what's deferred.

---

## 1. What already exists

| Concern | Location | Notes |
|---|---|---|
| `ModelPreferences` data shape | [genai/llm/preference.go:16](genai/llm/preference.go) | `IntelligencePriority`, `SpeedPriority`, `CostPriority` (float64), `Hints []string`. The MCP→local adapter at [preference.go:39](genai/llm/preference.go) converts `schema.ModelPreferences` to this form. |
| `Matcher` / `ReducingMatcher` | [genai/llm/matcher.go](genai/llm/matcher.go) | `Best(prefs) string`, `BestWithFilter(prefs, allow) string` |
| Scoring algorithm | [internal/matcher/matcher.go](internal/matcher/matcher.go) | hint match + priority weighting + tie-break |
| Finder | [internal/finder/model/model.go](internal/finder/model/model.go) | `Find(id)`, `Best(prefs)`, `BestWithFilter(prefs, allow)` |
| YAML parsing for skills | [protocol/skill/skill.go:297](protocol/skill/skill.go) `parseModelPreferences` | **Accepts both** `hints: [{name: X}]` and `hints: [X]` YAML shapes — hand-rolled parser, shape-tolerant. |
| YAML/JSON parsing for intake config | (proposed, Phase 1) custom `UnmarshalYAML` / `UnmarshalJSON` on `ModelPreferences` in [preference.go](genai/llm/preference.go) | ~25 LoC each. Same shape tolerance as the skill parser. Authors can use either form in workspace YAML; both normalize to `Hints []string` (existing field, no type change). |
| Wire-through | [protocol/tool/service/llm/agents/types.go:50](protocol/tool/service/llm/agents/types.go) → [service/core/generate_input.go:30–78](service/core/generate_input.go) `MatchModelIfNeeded()` | `RunInput.ModelPreferences` flows through `QueryInput` to the matcher. |
| Skill activation consumes prefs | [service/agent/run_query.go:666–689](service/agent/run_query.go) | Active-skill `Frontmatter.ModelPreferencesValue()` already routes through `MatchModelIfNeeded()` end-to-end. |

**Bottom line — accurate state**:

- **Skills**: preferences are consumed end-to-end today via [run_query.go:666–689](service/agent/run_query.go) → `MatchModelIfNeeded()` → existing `Finder`. **Already shipped.** No code change needed.
- **Intake**: preferences are **not** wired today. [protocol/agent/intake.go:19](protocol/agent/intake.go) `Intake` struct has only `Model string`. [service/intake/service.go:60](service/intake/service.go) `Run()` consumes `cfg.Model` directly. The wire-up below adds: (1) the missing `ModelPreferences` field on `Intake`, (2) a `~5-line` resolver block in `Run()` that calls the existing `Finder`, and (3) a custom `UnmarshalYAML`/`UnmarshalJSON` on `ModelPreferences` so workspace YAML accepts both `hints: [X]` and `hints: [{name: X}]` shapes. Total: ~50 LoC including the unmarshallers + tests.

When both wire-ups are done, skills and intake will both consume the same finder. They don't both consume it today.

---

## 2. Wire-up work

**Skills**: already shipped end-to-end (per skill-impr.md S9 status correction). Remaining: targeted tests asserting the wire-up.

**Intake**: add `ModelPreferences *llm.ModelPreferences` field to [protocol/agent/intake.go](protocol/agent/intake.go) `Intake` struct. In [service/intake/service.go](service/intake/service.go) `Run()`, resolve via the existing finder before falling back to legacy `Model` string:

```go
modelID := cfg.Model
if modelID == "" && cfg.ModelPreferences != nil {
    modelID = s.modelFinder.Best(cfg.ModelPreferences)
}
```

`s.modelFinder` is the existing `internal/finder/model.Finder`, injected via constructor. **No new abstraction.**

Total wire-up effort: **~0.5d**.

---

## 3. Cleanup tickets — backlog with trigger conditions

| # | Issue | Location | Trigger to schedule | Effort |
|---|---|---|---|---|
| H1 | ~~`inferConfigFromID` provider inference is heuristic~~ | [internal/finder/model/model.go:147–193](internal/finder/model/model.go) | **Resolved (back-compat-preserving)**: heuristic is now opt-in via `AGENTLY_ALLOW_LEGACY_INFER=1`. Default behavior surfaces `ErrModelNotRegistered` so missing model configs are caught at the boundary. Workspaces that depended on inference flip the env flag during migration to explicit YAML registrations. | shipped |
| H2 | `Finder.Candidates()` rebuilds on every selector call | [internal/finder/model/model.go:223–247](internal/finder/model/model.go) | Profiling shows candidate construction is hot | 0.5d |
| H3 | `Matcher.Best()` substring-matches uniformly; exact-name pinning silently falls back to fuzzy | [internal/matcher/matcher.go:86–108](internal/matcher/matcher.go) | **Bundle with S9 test work** — same code path | 0.5d |

**H4 (replace local `ModelPreferences` with MCP `schema.ModelPreferences`)**: not scheduled. The existing local type + adapter + shape-tolerant parser all work end-to-end. Migrating costs touch on every call site and gains nothing tangible. Reopen if and only if a real fidelity loss surfaces.

---

## 4. Out of scope

- New `genai/modelpref` package (withdrawn early in this proposal cycle).
- Type migration to MCP `schema.ModelPreferences` (H4).
- Latency-driven preferences (`LatencyMsP50` etc.) — out of scope until preference quality complaints surface.
- Workspace-level model-info registry format — separate concern, lives in `workspace/repository/model/`.

---

## 5. Definition of done

- [ ] Intake consumes preferences via `s.modelFinder.Best()` (no parallel selector).
- [ ] Skill model-preferences integration tests cover hint-only, priority-only, combined fixtures (S9).
- [ ] H3 lands with S9 test work.
- [ ] H1 / H2 filed as backlog issues with trigger conditions documented.
