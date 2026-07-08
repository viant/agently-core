# Intake Improvements

Trimmed proposal: a small workspace-level intake layer that replaces today's overlapping pre-agent classification path. Reuses every existing primitive (`agent_selector`, `Finder`, `Matcher`, `streaming.Event`, `jaccardWordSimilarity`). No new infrastructure beyond the orchestration layer itself.

---

## 1. Background

### Current state, verified

| Surface | Location | Status |
|---|---|---|
| Agent-scoped intake config (`Intake` struct) | [protocol/agent/intake.go](protocol/agent/intake.go) | shipped |
| Intake service | [service/intake/service.go](service/intake/service.go) `Service.Run()` | shipped |
| `TurnContext` output | [service/intake/context.go](service/intake/context.go) — has `Context map[string]string` | shipped |
| Sidecar invocation | [service/agent/intake_query.go](service/agent/intake_query.go) `maybeRunIntakeSidecar()` | shipped |
| Topic-shift Jaccard helper | [service/agent/intake_query.go:154–175](service/agent/intake_query.go) `jaccardWordSimilarity()` | shipped, generic |
| Auto agent selection (LLM router) | [service/agent/agent_resolution.go:229](service/agent/agent_resolution.go) `resolveAgentIDForConversation()` | shipped, **separate path** |
| Capability discovery | [agent_resolution.go:186–205](service/agent/agent_resolution.go) `isCapabilityDiscoveryQuery()` (heuristic markers) → routes to `agent_selector` agent | shipped |
| `agent_selector` built-in agent | [service/agent/agent.go:65,92](service/agent/agent.go), wired at [run_context.go:167–169](service/agent/run_context.go) | shipped — runs through standard `agent.Query()` pipeline |
| Workspace-level intake | — | **does not exist** |

**Key duplication today**: when `agentId=auto`, the LLM router in `resolveAgentIDForConversation` classifies the message; if an agent is then chosen, agent intake classifies it again with a different prompt. Two LLM calls for what should be one classification plus refinement.

---

## 2. Goal

**This is in-place improvement of two existing functions, not new layers added alongside them.**

| Existing function | Existing role | What this proposal adds (in place) |
|---|---|---|
| `resolveAgentIDForConversation` ([agent_resolution.go:229](service/agent/agent_resolution.go)) — pre-agent classification | Picks an agent ID. Internal branches: LLM router → token-match → continuity → workspace default. | LLM-router branch gains: configurable workspace prompt, typed `Mode` (`route` / `clarify`), cross-turn reuse via existing `jaccardWordSimilarity`, configurable `capabilityPhrasePatterns` (today's `isCapabilityDiscoveryQuery` markers become the built-in default). Token-match and continuity branches unchanged. |
| `maybeRunIntakeSidecar` ([intake_query.go](service/agent/intake_query.go)) — agent refinement | Produces `TurnContext` overrides for the chosen agent. Never picks a different agent. | Same contract. May gain richer override fields over time. **Pure override layer — no selection logic.** |

There are **two functions**, exactly as today. Neither is parallel to the other. There are no fallback chains between them — they run sequentially: classify → refine. The token-match and continuity heuristics are internal branches of `resolveAgentIDForConversation`, not separate layers.

Deferred until measured-need surfaces: combined intake calls, prompt caching, strict-spec mode, schema versioning, configurable routing-context schemas.

---

## 3. Architecture

```
user message
    │
    ▼
┌──────────────────────────────┐
│ skip rules (zero-LLM path)   │
│  - empty message             │
│  - explicit agentId          │
│  - workspaceIntake provided  │
│  - capability-phrase pattern │
│  - cross-turn reuse hit      │
└─────────┬────────────────────┘
          │ no skip
          ▼
┌──────────────────────────────┐
│ workspace intake (1 LLM call)│
│  - mode: route OR clarify    │
│  - selectedAgentId (if route)│
└─────────┬────────────────────┘
          │
          ▼
┌──────────────────────────────┐
│ agent resolution             │
└─────────┬────────────────────┘
          │
          ▼
┌──────────────────────────────┐
│ agent intake (optional)      │
│  - field refinement only     │
│  - never redoes selection    │
└─────────┬────────────────────┘
          │
          ▼
┌──────────────────────────────┐
│ standard agent.Query() turn  │
└──────────────────────────────┘
```

**Workspace decides who. Agent decides how.**

---

## 4. Modes — collapsed to two

| Mode | Meaning | What runs |
|---|---|---|
| `route` | Normal turn — pick an agent | resolve `selectedAgentId`; standard pipeline |
| `clarify` | Ambiguous — ask user for more | chosen agent (or workspace default) emits clarification via standard `agent.Query()` |

**No `answer_capability` mode** — capability questions short-circuit before workspace intake via the capability-phrase skip rule and route to `agent_selector` (the existing built-in agent), exactly as today. No new path.

**No `degrade` mode** — if workspace intake fails, fall back to the existing token-match auto-select chain in `agent_resolution.go` (token scorer → continuity → workspace default). Hard-error only if nothing resolves.

---

## 5. Capability questions — keep using `agent_selector`

The existing flow already handles this correctly:

1. Workspace intake's skip rules detect a capability-phrase pattern (replaces the hardcoded markers in `isCapabilityDiscoveryQuery`).
2. Runtime sets `input.AgentID = "agent_selector"` (same as today at [run_context.go:167–169](service/agent/run_context.go)).
3. `agent_selector` runs through the standard `agent.Query()` pipeline — same prompt loop, same transcript writer, same SSE channel as any other agent turn. **No new persistence path. No UI bypass.**
4. `agent_selector`'s prompt may grow to consume richer workspace metadata over time, but that's an agent-prompt change, not a runtime architecture change.

The only thing this proposal changes is **detection** (configurable patterns + LLM intake instead of hardcoded markers). The response path is unchanged.

---

## 6. Data model

**One shape: `TurnContext`. Both intake layers produce and consume it. No separate "agent hints" type.**

```go
type TurnContext struct {
    // Existing fields preserved (no breaking change):
    Title, Intent string
    Context       map[string]string  // legacy free-form, kept as wire form
    SuggestedProfileId string
    TemplateId    string
    AppendToolBundles []string
    Confidence    float64

    // New fields (additive):
    SelectedAgentID string  // populated by workspace intake only
    Mode            string  // "route" | "clarify"
    Source          string  // "workspace" | "agent" | "reused" | "caller-provided" | "fallback"
    ActivateSkills  []string
}
```

This matches the type already shipped at [service/intake/context.go:6](service/intake/context.go) — the existing agent-intake output. No new struct, no parallel `AgentTurnHints` type.

### Field-population conventions per layer

The shape is shared; the **invariant about who writes which field** is what separates the layers:

| Field | Workspace intake | Agent intake | Caller-provided (`workspaceIntake` on `RunInput`) |
|---|---|---|---|
| `SelectedAgentID` | **writes** (primary owner) | **never writes** (rule from §3) | writes (must validate against authorized set) |
| `Mode` (`route` / `clarify`) | **writes** | never writes | writes |
| `Source` | sets to `"workspace"` | sets to `"agent"` | sets to `"caller-provided"` |
| `Confidence` | writes | may write (overrides if more confident) | writes |
| `Title`, `Intent` | may write | may write (refinement) | may write |
| `Context`, `RoutingContext` (free-form) | may write | may write | may write |
| `SuggestedProfileId`, `TemplateId` | may write | may write (refinement) | may write |
| `ActivateSkills` | may write (validated against agent's visible skills) | may write (refinement) | may write (validated) |
| `AppendToolBundles` | may write (validated against `allowedAppendBundles`) | may write (validated) | may write (validated) |
| no separate clarification field | clarify should ask directly | clarify should ask directly | clarify should ask directly |

The hard rule from §3 ("agent decides how, not who") is enforced as: **agent intake never touches `SelectedAgentID` or `Mode`.** This is a runtime check — if agent intake's output sets either, the runtime drops those fields and emits a diagnostic.

**No typed `RoutingContext`, no schema validation.** Existing `Context map[string]string` keeps working. Workspaces that want typed routing data can layer it on later if a real need surfaces.

**No `Version` field, no schema-versioning rules.** Bump structurally only when first breaking change happens; treat older serialized values as expired and re-run intake (cheap).

---

## 7. Reuse rules

For a follow-up turn in the same conversation, intake reuses the prior `TurnContext` only when **all** of:

1. **Prior `Mode == "route"`** (clarify-mode results never reuse — they reflect an ambiguous prior state).
2. `triggerOnTopicShift: true` (workspace config).
3. `1 - jaccardWordSimilarity(prevTokens, currTokens)` ≤ `topicShiftThreshold` (default 0.65) — uses the existing helper at [intake_query.go:154–175](service/agent/intake_query.go), no new helper needed.
4. Prior `selectedAgentId` still authorized.
5. User message has no explicit agent/skill switch markers.

Reused `TurnContext` carries `Source: "reused"`.

---

## 8. Configuration — minimal

**Existing today** ([protocol/agent/intake.go:19](protocol/agent/intake.go)): `Intake` struct has `Model string` only (no preferences field). [service/intake/service.go:60](service/intake/service.go) reads `cfg.Model` directly. The wire-up below adds a new `ModelPreferences *llm.ModelPreferences` field — that's the actual code change in Phase 1.

Workspace YAML after wire-up:

```yaml
intake:
  enabled: true
  prompt: |
    You are the workspace intake selector. Pick the best authorized agent for the turn,
    or set mode=clarify if ambiguous. Output JSON matching the provided schema.
  model: claude-haiku        # existing field, kept; resolved before modelPreferences
  modelPreferences:          # NEW additive field on Intake struct (Phase 1)
    hints:                   # matches the existing Go type Hints []string in preference.go:15
      - claude-haiku
      - gpt-5-mini
    intelligencePriority: 0.2
    speedPriority: 0.9
  confidenceThreshold: 0.85
  defaultAgent: orchestrator # used if intake degrades
  capabilityPhrasePatterns:  # zero-LLM short-circuits to agent_selector
    - "(?i)^what can you do"
    - "(?i)^list (agents|skills)"
```

YAML `hints` accepts **both shapes**: the simple string-list form (`hints: [claude-haiku]`) and the MCP-style object form (`hints: [{name: claude-haiku}]`). To make this true for the intake config (which goes through `yaml.Unmarshal` against the `Intake` struct), Phase 1 adds a custom `UnmarshalYAML`/`UnmarshalJSON` on `ModelPreferences` in [preference.go](genai/llm/preference.go) (~25 LoC each) that normalizes either input into the existing `Hints []string`. This matches the tolerance the skill parser already has at [protocol/skill/skill.go:319–340](protocol/skill/skill.go), so authors can copy-paste MCP-spec examples or write the simpler form interchangeably. No author-side migration; one place of "magic" instead of divergent rules.

That's it. Five required knobs (`enabled`, `prompt`, `model`/`modelPreferences`, `confidenceThreshold`, `defaultAgent`) + one optional (`capabilityPhrasePatterns`).

**Deferred knobs**: `combinedMode`, `promptCache`, `strictSpec`, `routingContextSchema`, `allowedAppendBundles` (just use existing skill-visibility), `capabilityModelPreferences` (workspace `intake.modelPreferences` is enough), `trivialPhrasePatterns`, `reuseTtlSec` (just use topic-shift). Add only when measured need surfaces.

Agent-level intake config keeps its existing shape — no changes.

---

## 9. Runtime sequence

```
1. receive user turn
2. evaluate skip rules (zero-LLM path):
   a. empty message → workspace.defaultAgent, skip intake
   b. explicit agentId not "auto" → skip workspace intake; goto 5
   c. caller passes workspaceIntake (a *TurnContext) on the run request →
        validate it against authorization (same checks workspace intake's own
        output goes through: selectedAgentId in authorized set, skills visible
        to chosen agent, bundles allowlisted, RoutingContext fields valid).
        If validation passes: use it as the turn's TurnContext with
        Source="caller-provided"; goto 5.
        If validation fails: drop it, log diagnostic, fall through to (d).
   d. user matches capabilityPhrasePatterns → input.AgentID="agent_selector"; goto 5
   e. cross-turn reuse hit (§7) → use prior TurnContext (Source="reused"); goto 5
3. agentId=auto AND no skip rule matched:
   a. run workspace intake (1 LLM call)
   b. validate selectedAgentId against authorized agents (drop if unauthorized)
   c. if mode == "clarify" → resolve chosen agent (or defaultAgent); pass clarification hint
   d. if confidence < threshold → fall back to existing token-match auto-select chain
4. (reserved)
5. resolve selected agent
6. if agent declares intake AND scope gap exists → run agent intake (1 LLM call, refinement only)
7. activate selected skills (validated against agent's visible skills)
8. run the turn through standard agent.Query() pipeline
```

**Round-trip cost**: explicit agent / `workspaceIntake` provided / capability skip / reuse hit = 0 intake LLM calls. Fresh routing = 1. Fresh routing + agent refinement scope gap = 2 (rare). The duplicated LLM-router classification today (workspace router + agent intake on the same message) is eliminated.

### `workspaceIntake` field on the run request

Symmetric with how skill activation accepts a caller-supplied `agentId`. Add a new optional field on `RunInput` / `QueryInput` ([protocol/tool/service/llm/agents/types.go:32–64](protocol/tool/service/llm/agents/types.go), [service/agent/query.go](service/agent/query.go)):

```go
type RunInput struct {
    AgentID         string
    // ... existing fields ...
    WorkspaceIntake *TurnContext  // optional; when set, skips the workspace-intake LLM call
                                  // for this turn. Same validation rules apply.
}
```

Use cases:
- **First-turn pre-provision**: programmatic clients with their own classifier pay zero intake LLM cost on conversation start.
- **UI-driven routing**: user picks an agent + fills title/intent in a form; UI sends a populated `TurnContext`.
- **Cached prior**: caller passes the prior turn's `TurnContext` to keep stickiness without trusting topic-shift heuristics.
- **Cross-conversation seed**: start a new conversation with an existing conversation's last `TurnContext`.

The `Source: "caller-provided"` annotation flows through to the `intake.workspace.completed` event so operators can distinguish caller-driven from runtime-driven routing decisions.

**No new mechanism**: this is just an additional skip rule and a one-field addition to the request struct. The validation path is the same one workspace intake's own output traverses.

---

## 10. Failure modes

| Failure | Fallback |
|---|---|
| Workspace intake LLM fails / times out | token-match auto-select → continuity → workspace.defaultAgent → hard error if nothing |
| Output JSON invalid | one retry; then same fallback chain |
| `selectedAgentId` not authorized | drop, fall back to chain |
| Confidence below threshold | mode=`clarify` with auto-question, or fall back to chain |
| Skill not visible to chosen agent | drop that skill, keep others |
| Empty message | workspace.defaultAgent, skip intake |
| No `defaultAgent` configured AND chain exhausted | hard error to caller |

Intake never blocks a turn; every failure has a deterministic fallback.

**Prompt-injection defense**: the real defense is **output validation**. `selectedAgentId` is checked against the authorized agent set after the LLM returns. The LLM cannot pick an unauthorized agent regardless of what the user message tries to instruct.

---

## 11. Observability — minimal

Two events using the existing `streaming.Event` infrastructure:

| Event | Payload |
|---|---|
| `intake.workspace.completed` | `{turnId, mode, selectedAgentId, confidence, durationMs, source}` |
| `intake.workspace.failed` | `{turnId, reason, fallbackAgentId}` |

Skip-rule and reuse paths are silent in v1 (no events). Add finer events when telemetry needs them.

---

## 12. Backward compatibility

- `TurnContext.Context map[string]string` field preserved unchanged.
- New fields (`SelectedAgentID`, `Mode`, `Source`, `ActivateSkills`) are additive; zero-value defaults for legacy callers.
- Existing agent-intake config and behavior unchanged.
- The current `resolveAgentIDForConversation` path keeps working until workspace intake reaches parity (Phase 2 retires the LLM-router branch).
- A regression test asserts existing intake fixtures produce identical `TurnContext.Title/Intent/Profile/Template` outputs.

---

## 13. Migration plan — two phases

**Phase 1 — Improve the existing LLM-router branch in place** (~5d)
- Inside `resolveAgentIDForConversation` ([agent_resolution.go:229](service/agent/agent_resolution.go)), the LLM-router branch (`classifyAgentIDWithLLM`) gains:
  - Configurable workspace prompt (`workspace.intake.prompt`).
  - Typed `Mode` output (`route` / `clarify`).
  - `selectedAgentId` validated against authorized-agent set.
  - Confidence threshold; below threshold → `Mode: "clarify"` with auto-question.
  - Cross-turn reuse via the existing `jaccardWordSimilarity` helper.
- Capability detection becomes data-driven: `isCapabilityDiscoveryQuery`'s hardcoded marker list moves to `workspace.intake.capabilityPhrasePatterns` config, with the current markers as built-in defaults so workspaces inherit today's behavior. The `agent_selector` routing path is unchanged.
- Two events via existing `streaming.Publisher`: `intake.workspace.completed`, `intake.workspace.failed`.
- Token-match and continuity branches inside the same function are not touched.

**Phase 2 — Test parity and shake out** (~2d)
- Integration tests asserting workspaces with `intake.enabled: false` produce identical agent-resolution outcomes to today (regression gate).
- Tests with `intake.enabled: true` exercising route, clarify, capability-pattern, reuse-hit, and failure-mode paths.
- No code is retired in this phase. The LLM-router branch is the *same code path* — just enriched with the new config inputs.

**Total: ~7d** (down from 17d in earlier drafts). No parallel paths. No fallbacks between layers. Two functions stay two functions; each gains capability where it already lived.

---

## 14. Out of scope

Everything below is deferred until measured need surfaces. Each is independently re-openable:

- Combined workspace+agent intake call (premature optimization).
- Prompt caching infrastructure (optimization, not architecture).
- Strict-spec mode (no real authors complaining about field names).
- Typed `RoutingContext` schema with workspace-defined fields.
- `TurnContext.Version` migration policy.
- Round-trip budget metric / CI assertion.
- Routing-context schema in workspace config.
- Per-conversation routing schema, embedding-based topic-shift, cross-user caching.
- Default intake prompt template enumeration in this doc.
- Test fixture appendix with worked examples (covered by integration tests).
- Agent intake escalation (not pursued — agent's normal response IS the pushback).
- `WrongAgentForScope` typed error.
- `OutOfScope` signal.
- `autoSelectable` agent flag (workspace authorization is sufficient).
- Capability synthesizer struct / `CapabilityAnswer` type (existing `agent_selector` covers it).

---

## 15. Open design questions

1. **`defaultAgent` requirement**: should workspaces be required to declare one? Recommend yes — failure to do so means the auto-select chain has no terminal step beyond hard error. Validate at workspace load.
2. **Capability-phrase patterns at workspace vs default level**: ship a small built-in default set (matching today's hardcoded markers) so workspaces inherit useful behavior without explicit config. Workspaces opt out or extend.

---

## 16. Definition of done

- [ ] Workspace intake runs when `agentId=auto` and produces a `TurnContext` with `Mode` ∈ `{route, clarify}`.
- [ ] `RunInput.WorkspaceIntake *TurnContext` field added; when present and validated, skips the workspace-intake LLM call with `Source="caller-provided"`. Same validation rules as workspace intake's own output.
- [ ] Capability-phrase patterns in workspace config replace `isCapabilityDiscoveryQuery` hardcoded markers; same `agent_selector` routing path used.
- [ ] Cross-turn reuse honors topic-shift threshold and `Mode == "route"` rule via existing `jaccardWordSimilarity`.
- [ ] Failure modes (§10) all degrade through deterministic fallback chain; turn never blocked.
- [ ] Output validation prevents prompt-injection-driven unauthorized agent selection.
- [ ] Two observability events (`intake.workspace.completed/failed`) emitted via existing `streaming.Publisher`.
- [ ] Existing agent intake continues to work unchanged (regression test green).
- [ ] Workspace intake layered above the existing `resolveAgentIDForConversation` chain; existing LLM router, token-match, and continuity all kept as fallbacks. Nothing existing is removed.
- [ ] `isCapabilityDiscoveryQuery` markers preserved as the built-in default set for `capabilityPhrasePatterns`. Workspaces extend; nothing is deleted.
- [ ] No new types beyond the workspace intake service + additive `TurnContext` fields. Both layers (workspace intake + agent intake) produce and consume the same `TurnContext` shape — no parallel `AgentTurnHints` struct.
- [ ] Runtime drops `SelectedAgentID` / `Mode` if agent intake attempts to write them, with a diagnostic. The "agent decides how, not who" rule is enforced in code, not just docs.
