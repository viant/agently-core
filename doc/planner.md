# Planner

Planner is a **lightweight fallback orchestration layer** for turns that do
not fit a checked-in prompt profile cleanly.

It sits between:

- **intake**, which classifies the request and decides whether normal static
  routing is good enough
- **reactor**, which executes a validated prompt/tool strategy

The planner is intentionally light:

- it does **not** replace static prompt profiles
- it does **not** replace reactor execution
- it does **not** directly execute tools

Instead, it is a configurable fallback that:

- sees existing scenario assets
- chooses or combines known flows
- compiles execution guidance for one turn
- injects that guidance as runtime system context
- then lets the normal reactor execute the turn

Planner does **not** replace the execution agent. In the improved design it
may run as a **dedicated planner agent** selected by workspace intake config
(`intake.plannerAgentId`), with additional planner-only metadata/tools
available to that agent. The selected main execution agent still performs the
actual turn execution after planner guidance is materialized as runtime-owned
system documents.

## Problem Statement

Static prompt profiles work well for known paths:

- troubleshoot
- signal impact delta
- audience forecast review
- selector impact
- configuration review

The weakness is not execution. The weakness is **strategy selection** when
the request is:

- mixed across several scenario families
- novel enough that no static profile is a strong fit
- explicitly asking for a creative or non-standard approach
- structurally actionable, but poorly served by a rigid predefined playbook

Without a planner fallback, the runtime tends to:

1. overfit to the closest static profile
2. clarify too early even when execution could begin

## Goals

- Preserve static prompt profiles as the default happy path.
- Let intake route to a planner when profile selection is weak, mixed, or
  explicitly creative.
- Let the planner learn from existing scenarios instead of inventing from
  scratch.
- Keep reactor execution generic and deterministic.
- Let planner output be inspectable through `llm.request`, transcript, payloads,
  and execution details.

## Non-Goals

- Replace the reactor with a planner.
- Move tool execution into intake.
- Replace checked-in prompt profiles with generated prompts for every turn.
- Add a large dynamic-profile framework before we prove planner fallback is
  actually needed.

## Proposed Position In The Stack

```text
user request
  -> intake
      -> static profile selected
      -> or planner fallback selected
      -> or clarify
  -> validator
      -> accepts static route
      -> or invokes planner mode
  -> planner agent
      -> planner-mode pass (LLM call #1)
      -> emits turn-specific execution guidance
  -> runtime
      -> injects planner guidance as system context
  -> selected execution agent / reactor
      -> normal orchestrator pass (LLM call #2)
      -> executes the turn normally
```

The split is:

- **intake decides whether planning is needed**
- **the planner agent designs the strategy when planner mode is active**
- **the selected execution agent / reactor executes the strategy**

The planner path therefore costs one extra model round-trip compared to a
strong static route:

1. planner-mode pass to compile guidance
2. normal execution pass to follow that guidance

That extra latency/cost is intentional and should be paid only on turns where
static routing is weak, invalid, or explicitly creative.

## Why Planner Does Not Belong In Reactor

Reactor should remain the owner of:

- tool execution
- async gating
- SSE emission
- transcript persistence
- continuation handling

If reactor also owns higher-level strategy creativity, it becomes:

- too rigid when encoded as code paths
- too opaque when encoded only in prompt prose
- too easy to duplicate across scenario prompts

Planner belongs **before** reactor, not inside it.

## Why Planner Is Not Intake

Intake should remain lightweight and cheap.

It is good at:

- recognizing request shape
- estimating fit/confidence
- choosing between static route, planner route, or clarification

It is not the right place to emit a full execution strategy. That would make
intake too expensive and too coupled to runtime execution details.

## Planner Trigger Conditions

Planner should be a first-class fallback route mode.

Suggested route modes:

- `static` — use predefined profile directly
- `planner` — route through the workspace-configured planner agent first,
  then execute with the selected main agent using planner-mode system context
- `clarify` — ask the user for missing information

Planner mode is appropriate when:

- no strong static profile match exists
- selected static profile fails validation
- the request spans multiple scenario families
- the user explicitly asks for a creative/novel approach
- the request is unusual, but still actionable

Clarification should be reserved for requests that are truly blocked.

## Why LLM Flexibility Still Matters

Static profiles provide the domain rules and tool discipline.

But the LLM is still useful for:

- choosing among several plausible strategies
- deciding evidence order when multiple known flows apply
- proposing bounded novel combinations of known scenario assets
- adapting narration/output stance when the user explicitly asks for a
  creative approach

The flexibility belongs in **planning**, not in raw execution semantics.

## Planner Inputs

Planner should see a compact registry view of:

- available prompt profiles
- their descriptions and `appliesTo` tags
- tool bundles
- templates
- visible skills
- available child agents
- agent topology / delegation metadata
- internal tool details and schemas when needed for planning
- workspace routing rules
- optionally: distilled scenario/eval metadata

In practice this means planner-oriented control-plane tools such as:

- `llm/agents:list`
- `llm/agents:topology`
- `llm/agents:tool_details`
- `llm/skills:list`

`llm/agents:topology` and `llm/agents:tool_details` are planner-only control
tools: the runtime exposes them only during `requestMode=plan`, not during
normal execution turns.

Most importantly, planner should learn from **existing scenario assets**.

That means it should treat checked-in scenarios as planning priors:

- `troubleshoot` = baseline-first
- `signal_impact_delta` = cohort-first delta discovery
- `forecast` = slice-matrix + grouped-cuts forecast review
- `selector_impact` = order-level confirmation / safer expansion

Planner should compose from those known flows rather than inventing arbitrary
tool sequences from scratch.

## Combining Base Profiles

When planner output references more than one base profile, the runtime should
not treat that as literal prompt concatenation. The merge rule should be:

- `messages`: planner-owned; the planner composes the final instruction set
  explicitly rather than relying on implicit concatenation of profile prompts
- `toolBundles`: union of all referenced base profiles plus any planner-added
  bundles, then validated against the agent/tool allow-list
- `templateId`: planner chooses one explicit template; there is no automatic
  first-wins or merge behavior
- `requiredEvidence`, `executionOrder`, `finalizationGuards`: planner-owned,
  but it may derive them from the referenced base profiles

So `baseProfiles` are **planning priors**, not executable merge inputs.

## Light Planner Output

For the first implementation, planner output should be **system-context
instructions**, not a new dynamic-profile runtime type.

That means the planner returns structured guidance such as:

```json
{
  "strategyFamily": "troubleshoot",
  "baseProfiles": ["performance_analysis", "inventory_diagnosis"],
  "toolBundles": ["analyst-performance-tools", "analyst-inventory-tools"],
  "skillsToActivate": ["forecast"],
  "narrationPolicy": {
    "baselineFirst": true,
    "persistMidTurnNote": true
  },
  "requiredEvidence": [
    "scope_orders",
    "signal_candidates",
    "forecast_summary"
  ],
  "executionOrder": [
    "baseline",
    "cohort-ranking",
    "candidate-confirmation",
    "forecast-validation"
  ],
  "finalizationGuards": [
    "do_not_finalize_without_forecast_validation"
  ]
}
```

The runtime then converts that into **system documents** for the turn, the
same way other runtime-owned context is injected today.

This JSON example is **Steward-flavored**, not a universal agently-core schema.
Fields such as `baselineFirst`, `persistMidTurnNote`, `scope_orders`, and
`forecast_summary` are workspace-defined policy/data concepts, not generic core
planner keywords. The generic core contract is only:

- planner can emit structured guidance
- runtime can validate and inject it
- workspace defines most domain-specific fields

## Why Not Dynamic Profile IDs First

Dynamic or ephemeral profile ids may still be a good long-term design.

But they add a lot of machinery up front:

- synthetic profile lifecycle
- profile validation + registry semantics
- replay/serialization rules
- more runtime state to explain and debug

The lighter first step is:

- planner compiles instructions
- runtime injects them as system context
- reactor runs as normal

This preserves one execution model while keeping the experiment cheap.

## System-Context Injection Mechanism

Planner output should not become a visible assistant bubble.

It should be injected via runtime-owned system context, using the same
pattern already used for:

- template documents
- prompt documents
- bootstrap system documents

That gives us:

- inspectability in `llm.request`
- compatibility with transcript/execution details
- no user-visible planning chatter

For a light planner, `message:add` is acceptable **only** when it behaves as
runtime-owned system context, not as a normal assistant message.

Tool-binding enforcement for the planner pass should be explicit:

- planner-mode runs with normal orchestrator knowledge/context
- but tool execution is disabled for that pass
- the safest first implementation is `tools: []` / no tool bindings attached to
  the planner-mode model call

That makes the rule hard rather than advisory: the planner compiles guidance,
the later reactor pass executes tools.

## Validation Layer

Even with the light design, planner output should be validated before the
reactor uses it.

Validator checks should include:

- required tool bundles exist
- required child agents/skills exist
- template id is valid
- required evidence ordering is coherent
- forbidden shortcuts are not requested

If validation fails:

- static profile -> planner fallback
- planner output -> feed the validation error back into the planner once, then
  validate again

If planner validation fails twice:

- runtime does not execute the invalid plan
- it falls back to clarification or a safe blocker response, depending on
  workspace policy

So "retry once" means "retry once with validation feedback", not "run the same
planner prompt again and hope".

## Intake Configuration

Planner is worth explicit intake configuration.

The point is not that intake does the planning itself.
The point is that intake is the right place to decide:

- use static profile
- use planner fallback
- clarify

Suggested future intake config knobs:

```yaml
intake:
  enabled: true
  plannerEnabled: true
  plannerFallbackThreshold: 0.70
  plannerOnValidatorFailure: true
  plannerOnCreativeRequest: true
  plannerSecondFailurePolicy: clarify   # clarify | block
```

Phrase-trigger lists should be treated as **experimental**. The preferred Phase
1 behavior is:

- confidence/fit-based planner fallback
- validator-failure-based planner fallback
- optional creative-request routing when intake can infer it

If trigger phrases are used at all, they should be easy to remove if they do
not pull their weight.

Intake config should answer:

- **when do we invoke planner?**

It should not own:

- evidence sequencing details
- tool flow specifics
- domain guardrails

Those belong in the planner/scenario/validator layers.

## Relationship To Existing Profiles

Existing profiles remain the main source of truth.

Planner should use them as:

- reference strategies
- execution building blocks
- evidence and output exemplars

Examples:

- `signal_impact_delta` remains the canonical cohort-discovery recipe
- `troubleshoot` remains the canonical baseline-first recipe
- `forecast` remains the canonical rich forecast recipe

Planner should be able to:

- choose one directly
- combine aspects of several
- add one bounded follow-up or confirmation step

without replacing the checked-in assets.

Profile allow-lists still apply. If the planner references base profiles or
tool bundles outside the current orchestrator's visible/allowed set, validation
must reject that output rather than silently widening privilege.

## Observability Requirements

Planner mode should be explicit in runtime state.

We should be able to inspect:

- why intake chose planner
- what static profile failed and why
- what planner guidance was generated
- whether validation accepted it
- which final execution strategy actually ran

Planner artifacts should be visible through the same debugging surfaces we
already rely on:

- `llm.request`
- transcript/canonical state
- execution details
- persisted payloads

## What agently-core Already Has

Current core already provides most of the primitives needed:

- intake sidecar
- prompt profiles
- tool bundles
- skills
- child-agent delegation
- templates
- system-document injection
- reactor execution
- async orchestration
- transcript/SSE model

The gap is not basic capability. The gap is a **planner/compiler fallback**
above those primitives.

## Suggested Incremental Rollout

### Phase 1

- add planner route mode to intake
- add intake config knobs for planner fallback
- add planner-mode system prompts for the existing orchestrator agent
- execute planner-mode with tool bindings disabled

### Phase 2

- define planner output schema
- inject planner output as runtime system context
- add validator for planner guidance
- feed validator errors back into one planner retry

### Phase 3

- teach planner to read scenario metadata and eval hints
- add creative-approach trigger path
- add mixed-scenario fallback path

### Phase 4

- add observability:
  - planner trigger reason
  - planner output artifact
  - validator result
  - execution trace linkage

### Phase 5

- only if needed, evaluate upgrading the light planner to a true ephemeral
  profile model

## Open Questions

- How much raw scenario text should planner see versus distilled metadata?
- Should planner output be persisted as a hidden artifact for replay?
- When planner chooses a novel path, should the user be told, or should it stay
  entirely internal?
- At what point does the light planner stop being enough and justify a real
  ephemeral-profile runtime?

## Related Docs

- [planning-and-intake.md](planning-and-intake.md)
- [agent-orchestration.md](agent-orchestration.md)
- [prompts.md](prompts.md)
- [skills.md](skills.md)
- [async.md](async.md)
