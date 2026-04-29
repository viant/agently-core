# Prompt Profiles and Tool Bundles — Simplifying Orchestrator Workspaces

Workspace paths:
- Runtime: `/Users/awitas/go/src/github.com/viant/agently-core`
- Application: `/Users/awitas/go/src/github.com/viant/agently`

---

> **Reading guide — existing vs implemented vs proposed**
>
> | Marker | Meaning |
> |---|---|
> | **EXISTING** | Was already in `agently-core` before the implementation plan |
> | **IMPLEMENTED** | Was PROPOSED and is now fully implemented (all 11 phases complete) |
> | **PROPOSED** | Not yet implemented (no remaining phases) |
>
> All 11 phases of the Implementation Plan are complete as of 2026-04-15.
> Sections without a marker are context, analysis, or example workspace guidance.

---

## Objective

Reduce example workspace complexity without losing controllability.

- reduce narrowly differentiated agent definitions
- reduce prompt and tool-bundle duplication across specialists
- make output shaping reusable across business scenarios
- keep routing, capability boundaries, and evaluation understandable
- lower the cost of adding new campaign-analysis scenarios

The desired end state is not fewer YAML files. It is simpler configuration, clearer ownership of behavior, and safer evolution.

---

## Problems

### 1. Agent proliferation
Many specialist agents differ mostly in prompt wording or output shape, not in tool access or reasoning style. This creates routing ambiguity, instruction drift, and high maintenance cost.

### 2. Prompt duplication
Most specialists share the same execution pattern: inspect hierarchy → run tools → interpret → recommend. Copying this across agents makes consistency expensive to maintain.

### 3. Output-contract duplication
Formatting expectations (dashboard, compact report, executive summary) are embedded in prompts rather than in reusable templates. Changes propagate inconsistently.

> **EXISTING — partially solved today.**
> `template:list` and `template:get` with agent-scoped template bundles are fully implemented in `agently-core` (`protocol/tool/service/template/service.go`). Bundle-based filtering (`allowedTemplates()`, lines 167–218) and system-document injection (`injectTemplateDocument()`, lines 235–258) work today without any code changes. Problem 3 can be addressed immediately by assigning template bundles to agents and instructing them to call `template:get` before producing output. The prompt-profile proposal builds on this foundation — it does not replace it.

### 4. Weak reasoning/presentation separation
When a single prompt handles task definition, domain rules, tool guidance, and output formatting, each agent becomes hard to change safely.

### 5. Hard-to-measure routing quality
With many overlapping agents, failures are hard to attribute. Is it routing, task framing, tool access, or output shape? With one large agent, those concerns are mixed.

### 6. High configuration overhead
Every new scenario currently requires a new agent, new prompt set, new tool bundle, and new starter-task wiring.

---

## Core Design: Three Layers

Replace the proliferation of specialist agents with three layers:

**1. `orchestrator`** — public-facing orchestrator
- understands user intent
- selects scenario profile
- delegates to worker
- synthesizes final answer

**2. `data-analyst`** — internal worker
- executes data-heavy analysis
- uses tools selected for the scenario
- returns structured findings

**3. Scenario profiles** — configuration units, not agents
- instruction messages (system + user)
- tool bundle set
- optional output template
- optional resources

Do not encode every scenario as a separate agent. Encode most scenarios as profiles.

Separate workers are only justified when the tool family, validation rules, or failure modes are fundamentally different (e.g. a detached forecasting skill path, `platform-lookup`).

---

## Prompt Profiles — IMPLEMENTED

> **IMPLEMENTED** — All of the following are in production code:
> - `protocol/prompt/profile.go` — `Profile`, `Message`, `MCPSource`, `Expansion` types + `EffectiveMessages()`
> - `protocol/prompt/render.go` — `Profile.Render()` supporting local text/URI messages and MCP-sourced messages
> - `workspace/repository/prompt/` — profile repository
> - `protocol/tool/service/prompt/service.go` — `prompt:list` / `prompt:get` tools
> - `protocol/tool/service/llm/agents/types.go` — `RunInput` extended with `PromptProfileId`, `ToolBundles`, `TemplateId`
> - `protocol/tool/service/llm/agents/profile_resolve.go` — runtime profile expansion in child turns
>
> **Profile access control:**
> `agent.Prompts.Bundles` is a direct allow-list of profile IDs — only those profiles are returned by `prompt:list` for that agent.
> `Profile.ToolBundles` is unrelated: it lists tool bundles activated for the worker when the profile is applied.
> Profiles have no access-control field of their own.

### Shape

```yaml
id: performance_analysis
name: Performance Analysis
description: >
  Analyze pacing, spend velocity, and KPI health.
  Use when the user asks about campaign delivery or budget posture.
appliesTo:
  - performance
  - pacing
  - kpi
messages:
  - role: system
    text: |
      You are a performance analyst for digital advertising campaigns.
      Focus on pacing posture, spend velocity, and KPI deviation.
      Return actionable recommendations backed by evidence from the data.
  - role: user
    text: |
      Analyze the campaign hierarchy provided and return the top optimization
      actions with supporting evidence.
toolBundles:
  - workspace-performance-tools
template: analytics_dashboard
expansion:
  mode: llm          # optional: sidecar expansion at delegation time
  model: haiku
  maxTokens: 600
```

**Fields:**

| Field | Purpose |
|---|---|
| `id` | Unique identifier used in `RunInput.promptProfileId` |
| `description` | Selection guidance — answers "when should I pick this?", not "what does this do?" |
| `appliesTo` | Tag vocabulary used by intake sidecar for classification |
| `messages` | Ordered `{role, text\|uri}` sequence — primary form |
| `instructions` | Shorthand for a single system message (simple cases only) |
| `mcp` | Source from MCP server instead of local messages |
| `toolBundles` | Capability floor — always enforced by runtime |
| `preferredTools` | Advisory hints within the bundle boundary |
| `template` | Default output template for this scenario |
| `resources` | Optional knowledge sources |
| `expansion` | Sidecar LLM config for task-specific instruction synthesis |

### Message Format and MCP Alignment

Profile instructions are defined as an ordered sequence of `{role, text|uri}` messages.
This is intentionally aligned with the MCP `GetPromptResult.Messages` format.

- `system` messages — reasoning persona and constraints
- `user` messages — task framing for the scenario
- `assistant` messages — few-shot scaffolds (optional)

`text` is a velty/go-template rendered against `Binding` at call time.
`uri` points to a file that can be edited without redeploying.

```yaml
messages:
  - role: system
    uri: workspace://prompts/instructions/performance_system.md
  - role: user
    uri: workspace://prompts/instructions/performance_task.md
```

MCP-sourced profiles replace local messages with a server reference:

```yaml
mcp:
  server: workspace-data-server
  prompt: performance_analysis_v2
  args:
    dateRange: "{{.context.dateRange}}"
```

Regardless of source — inline text, file URI, or MCP — `prompt:get` always **renders** the profile to `[]PromptMessage` and returns those messages in the response body.

When `includeDocument: true`, each message is **additionally injected** into the conversation via `AddMessage()` using its authored role:

- `system` → `WithRole("system")`, `WithMode(SystemDocumentMode)`, `WithTags(SystemDocumentTag)`
- `user` → `WithRole("user")`
- `assistant` → `WithRole("assistant")`

When `includeDocument` is absent or `false`, the messages appear only in the response body — no conversation injection occurs.

**What is grounded in existing behavior vs what is proposed:**

- System-role injection (`SystemDocumentMode` + `SystemDocumentTag`) is grounded in the existing `template:get` implementation (`protocol/tool/service/template/service.go:247`). This path exists and works today.
- User-role and assistant-role injection via `AddMessage()` is also implemented for prompt profiles. `prompt:get` injects each rendered message using its authored role; only `system` messages receive `SystemDocumentMode` / `SystemDocumentTag`.

The same injection loop applies wherever profile messages are injected — whether from `prompt:get` with `includeDocument: true` (Option 2) or from the runtime during child conversation setup (Options 1 and 4). Both paths use identical per-role branching logic.

### `prompt:list` and `prompt:get` — IMPLEMENTED

> **IMPLEMENTED** — mirrors the existing `template:list` / `template:get` pattern.
> Confirmed behavior of `template:get` (`protocol/tool/service/template/service.go:151–162`):
> - **Always returns** `Instructions`, `Fences`, `Schema`, `Examples` in the response body, regardless of `includeDocument`
> - `includeDocument` controls **only injection** — whether messages are also written into the conversation via `AddMessage()`. System-role messages are stored as system documents (`SystemDocumentMode`); user and assistant messages are stored with their natural roles. Not all injected messages become system documents.
>
> `prompt:get` must follow the same contract. `includeDocument` is not a content-visibility flag.

`prompt:list` returns selection metadata only — no instruction content:

```json
{
  "profiles": [
    {
      "id": "performance_analysis",
      "name": "Performance Analysis",
      "description": "Analyze pacing, spend velocity, and KPI health. Use when the user asks about campaign delivery.",
      "appliesTo": ["performance", "pacing", "kpi"],
      "toolBundles": ["workspace-performance-tools"],
      "template": "analytics_dashboard"
    }
  ]
}
```

`prompt:get` always returns the full profile including rendered messages — the `includeDocument` flag only controls whether those messages are additionally written into the conversation via `AddMessage()` with their authored roles (`system` messages as system documents; `user` and `assistant` messages with natural roles):

```json
{
  "id": "performance_analysis",
  "toolBundles": ["workspace-performance-tools"],
  "preferredTools": ["workspace-MetricsAdCube", "workspace-AdHierarchy"],
  "template": "analytics_dashboard",
  "resources": ["workspace-campaign-kb"],
  "messages": [
    { "role": "system", "text": "You are a performance analyst..." },
    { "role": "user",   "text": "Analyze the campaign hierarchy..." }
  ],
  "injected": false
}
```

| `includeDocument` | Response contains messages | What gets injected |
|---|---|---|
| absent or `false` | yes | nothing |
| `true` | yes | each message injected with its authored role: `system` messages as system documents (`SystemDocumentMode` + `SystemDocumentTag`); `user` and `assistant` messages with their natural roles |

`includeDocument: true` does **not** flatten all messages into system documents. Only messages whose `role` is `system` receive `SystemDocumentMode`. User and assistant messages are injected as their authored role and are not tagged as system documents. This is implemented in `prompt:get`; the distinction still matters for understanding the contract because it is a per-role injection loop, not a single `injectTemplateDocument()` call.

**Default rule for hybrid mode (Option 4):**

> In the recommended hybrid path, `orchestrator` calls `prompt:list` to select a profile and passes only `promptProfileId` to `llm/agents:run`. **`orchestrator` does not call `prompt:get` in the normal flow.** The runtime resolves instructions from the profile at delegation time, keeping them out of the orchestrator's context entirely.

`prompt:get` without injection (`includeDocument: false`) is an explicit escape hatch for cases where the orchestrator *deliberately* needs to read instruction content before deciding — for example, to adapt the objective text, or to confirm resource requirements. It is not a "metadata-only" shortcut and should not be the default step in the hybrid flow. Using it by default reintroduces instruction content into the orchestrator's context and partially recreates the context bloat the proposal is trying to avoid.

### Outbound MCP Exposure

The agently-core MCP server's `ListPrompts` and `GetPrompt` handlers (currently stubbed) are wired to the profile registry. External MCP clients see agently-core prompt profiles as standard MCP prompts. No translation is needed — profiles already produce `[]PromptMessage`.

---

## Tool Bundle Enforcement

### Pass Identifiers, Not Prose

Do not embed tool names in prompt text:

```
❌  "Use workspace-MetricsAdCube, workspace-SiteMetricsAdCube, workspace-AdHierarchy..."
✓   toolBundles: ["workspace-performance-tools"]
```

Bundle IDs are small, the runtime enforces access directly, and tool catalogs stay out of LLM context.

### Three-Tier Bundle Resolution

```
1. profile.toolBundles         — structural floor, always included
2. TurnContext.appendToolBundles — intake sidecar additions, task-driven
3. RunInput.toolBundles         — orchestrator explicit override
```

Each tier appends only. No tier removes what a lower tier established.
The profile floor is always safe.

### Three-Tier Template Resolution

```
1. profile.template        — scenario default
2. TurnContext.templateId  — intake sidecar suggestion, driven by user phrasing
3. RunInput.templateId     — orchestrator explicit override (highest priority)
```

Template selection is not additive — one template per turn. An empty value defers to the next tier down.

---

## Package Organization — IMPLEMENTED

> **IMPLEMENTED** — `protocol/prompt` was renamed to `protocol/binding` (Phase 1). `protocol/prompt` now contains profile types.

### The Naming Problem

The existing `protocol/prompt` package contains rendering primitives: `Prompt` (text-template engine), `Binding` (conversation context), `Persona`. Prompt profiles are a different concept. Both cannot cleanly share the `prompt` package name.

### Option A — Keep `protocol/prompt`, add `protocol/promptprofile`

No rename. Separate package for profiles. Low disruption but perpetual disambiguation.

### Option B — Rename `protocol/prompt` to `protocol/binding` (recommended)

`Binding`, `Prompt` (text renderer), and `Persona` are all about how data is bound and rendered into conversation text. `binding` describes the package correctly.

After rename:
- `protocol/binding` — rendering primitives (Binding, Prompt text engine, Persona)
- `protocol/prompt` — scenario profiles, profile repository, `prompt:list`/`prompt:get` service

`prompt.Profile` reads clearly. No disambiguation needed. No separate `promptprofile` package.

The `context` package name was considered and rejected — it collides with Go's stdlib `context.Context`, requiring import aliasing in every file. `binding` has no such collision.

Import churn is 43 files — a one-time cost.

---

## Routing: Who Calls `prompt:get`

### Current `llm/agents` Mechanism — Baseline and Shortcomings — EXISTING

> **EXISTING** — `llm/agents:run` with `agentId` + `objective` works today. The shortcomings below describe limitations of using it *without* the proposed extensions.

`llm/agents:run` remains the delegation primitive and is not replaced.
The proposal extends `RunInput` with new optional fields. All existing calls continue to work unchanged.

Today a typical delegation looks like:

```json
{
  "agentId": "workspace-performance",
  "objective": "Why is campaign 4821 underpacing this week? Use workspace-MetricsAdCube and workspace-AdHierarchy to check pacing. Return a brief summary."
}
```

Everything else — tool selection, output shape, reasoning framing — is either hardcoded in the target agent's YAML or embedded in the objective text.

**Shortcomings of this pattern:**

| Problem | Root cause |
|---|---|
| Tool access is static | Worker's tool set is fixed in agent YAML; no per-delegation narrowing |
| Scenario framing via objective text | Natural-language instructions are advisory — the worker may or may not follow them |
| Context bloat | Tool names, format guidance, and entity context embedded in objective text inflate every call |
| No output template pre-selection | Worker infers format from prose or produces generic output |
| No entity extraction | Worker re-extracts campaign IDs, dates, constraints from unstructured text |
| Specialist proliferation as the only escape valve | Different tool sets or reasoning modes require a separate agent YAML file per scenario |
| Routing opacity | Failures are hard to attribute — routing, framing, tool access, and output format are all entangled in one objective string |

**Extended `RunInput` — backward compatible:**

```json
{
  "agentId": "data-analyst",
  "objective": "Why is campaign 4821 underpacing this week?",
  "promptProfileId": "performance_analysis",
  "toolBundles": ["workspace-forecast-tools"],
  "templateId": ""
}
```

All new fields are optional. When absent, behavior is identical to today.
When present, the runtime resolves the profile, enforces bundles, and injects instructions — without changing the `llm/agents:run` call surface or the worker agent's YAML.

The options below describe who populates these new fields and when.

This is a design decision with real tradeoffs.

### Option 1: Runtime-automatic

`orchestrator` passes `promptProfileId` in `RunInput`. Runtime resolves profile, injects instructions, expands bundles — LLM never sees profile metadata.

- ✓ enforcement is structural
- ✓ context stays minimal
- ✗ selection reasoning is opaque
- ✗ agent cannot adapt based on context it observed

### Option 2: Agent-instructed

`orchestrator` calls `prompt:get` with `includeDocument: true` before delegating. Profile instructions are injected into orchestrator's conversation. Orchestrator reads `toolBundles` and `template` from the response and passes them to `llm/agents:run` as `toolBundles` and `templateId` respectively.

**Field name mapping** — profile/response fields map to `RunInput` fields as follows:

| `prompt:get` response field | `RunInput` field |
|---|---|
| `toolBundles` | `toolBundles` |
| `template` | `templateId` |

Instruction shape for `orchestrator`:
```
Before delegating to data-analyst, call prompt:get with the most relevant profile id.
Use includeDocument: true. Pass response.toolBundles to llm/agents:run as toolBundles,
and response.template as templateId. Do not summarize or repeat the profile instructions.
```

- ✓ selection reasoning is visible and auditable
- ✓ agent can adapt to context
- ✗ relies on instruction discipline
- ✗ profile instructions enter the orchestrator's context

### Option 3: autoPrompt — workspace/agent declares candidates

Runtime classifies the task at delegation time and injects the matching profile automatically.

- ✓ zero instruction burden
- ✗ classification introduces a new failure mode
- ✗ opaque by default

### Option 4: Hybrid — agent selects id, runtime enforces (recommended)

`orchestrator` calls `prompt:list`, reasons about which profile fits, passes `promptProfileId` in `RunInput`.
Runtime resolves instructions and expands bundles. Agent never receives raw instruction text.

- ✓ selection is transparent and auditable (id visible in history)
- ✓ runtime owns enforcement
- ✓ instructions never enter the orchestrator's context
- ✗ `prompt:list` descriptions must be clear selection guidance

| Dimension | Option 1 | Option 2 | Option 4 |
|---|---|---|---|
| Selection visible in history | no | yes (+ instructions) | yes (id only) |
| Instructions in orchestrator context | no | yes | no |
| Agent reasons about fit | no | yes | yes (via metadata) |
| Bundle enforcement | structural | via instruction | structural |
| Debuggability | low | high | high |
| Context overhead | minimal | moderate | minimal |

**Recommendation:** Option 4 for production. Option 2 as a pragmatic starting point. Options 1 and 3 become branches of the intake sidecar model (see below).

### Hybrid Mode: Detailed Flow

**Step 1 — Discovery.** `orchestrator` calls `prompt:list`. Sees descriptions and `appliesTo` tags. No instruction content.

**Step 2 — This step is skipped in the normal hybrid flow.** `orchestrator` does not call `prompt:get`. It selects a profile from `prompt:list` and delegates directly with `promptProfileId`. Instructions stay out of the orchestrator's context.

`prompt:get` is available as an escape hatch when the orchestrator has a specific reason to read instruction content before delegating — but this is the exception, not the default. Calling it routinely reintroduces instruction content into the orchestrator's context.

**Step 3 — Delegation.**

```json
{
  "agentId": "data-analyst",
  "objective": "Why is campaign 4821 underpacing this week?",
  "promptProfileId": "performance_analysis",
  "toolBundles": [],
  "templateId": ""
}
```

`promptProfileId` is the only routing field `orchestrator` passes. `toolBundles` and `templateId` are left empty — the profile is the source of truth. Orchestrator may extend `toolBundles` only when the task requires it.

**Step 4 — Runtime profile expansion** (before `BuildBinding()`):

1. Look up profile from repository
2. Render `messages` via velty/go-template against child `Binding`
   — or call `cli.GetPrompt()` if profile has `mcp` source
3. Inject rendered messages into child conversation, role-preserving
4. Resolve effective bundles: `profile.toolBundles` + `RunInput.toolBundles`
5. Set `QueryInput.ToolBundles` before `BuildBinding()` runs
6. Apply effective template

**What is visible after the fact:**
- `orchestrator` turn: `prompt:list` call + `llm/agents:run` with `promptProfileId`
- child turn: profile messages injected by runtime — `system`-role messages tagged `system_doc`; `user` and `assistant` messages with natural roles
- child turn: tool calls within resolved bundle

Selection is auditable. Enforcement is structural. Instruction content never duplicated in parent.

---

## Sidecar Pipeline — IMPLEMENTED

> **IMPLEMENTED** — both sidecars are fully implemented:
> - Expansion sidecar: `protocol/tool/service/llm/agents/expand.go` — `expandMessages()`, called from `resolveProfile` when `profile.Expansion.Mode == "llm"`
> - Intake sidecar: `service/intake/service.go` + `service/agent/intake_query.go` — runs before tool selection when `agent.Intake.Enabled = true`

Two lightweight LLM calls — one at turn intake, one at delegation — handle work that would otherwise require either verbose agent instructions or LLM reasoning about bundle names.

| Sidecar | When | Input | Output |
|---|---|---|---|
| **Intake** | turn received, before routing | user message + history | `TurnContext` |
| **Expansion** | at delegation, before worker | profile messages + objective | task-specific `[]PromptMessage` |

### Intake Sidecar

Runs before `orchestrator` does any routing. Produces `TurnContext`:

```json
{
  "title": "Campaign 4821 Underpacing — Week of April 15",
  "intent": "diagnosis",
  "entities": {
    "campaignId": "4821",
    "issue": "underpacing",
    "timeframe": "this week",
    "urgency": "end-of-week"
  },
  "suggestedProfileId": "performance_analysis",
  "appendToolBundles": ["workspace-forecast-tools"],
  "templateId": "compact_report",
  "confidence": 0.91,
  "clarificationNeeded": false,
  "clarificationQuestion": ""
}
```

`TurnContext` is stored in `Binding.Context` and flows through the entire downstream pipeline.
The extraction cost is paid once; the benefit (title, entities, routing hints) is multiplied across all downstream steps.

**Profile classification.** The runtime provides the intake sidecar with `appliesTo` tag vocabulary from the profile registry. The sidecar classifies user intent against these tags and returns `suggestedProfileId` by registry lookup — it never needs to know profile instruction content.

**Auto tool selection.** The sidecar receives bundle registry metadata (id + one-line description). It detects task signals (e.g. "compare to forecast") and returns `appendToolBundles`. Tool names never enter LLM context.

**Auto template selection.** The sidecar detects output format signals ("give me a dashboard", "quick summary", "for stakeholders") and returns `templateId`. Empty means defer to the profile's default.

**Unification of Options 3 and 4.** The intake sidecar makes Options 3 and 4 two branches of one decision:

```
confidence >= threshold  →  auto-delegate with suggestedProfileId      (Option 3 path)
confidence < threshold   →  orchestrator calls prompt:list, reasons          (Option 4 path)
clarificationNeeded      →  elicitation first, then re-run intake
```

One `confidenceThreshold` in config separates them. Evaluation data informs where to set it.

#### Agent-Level Scope Toggle

The sidecar has two capability classes:

**Class A — Metadata (always safe, any agent):**
`title`, `entities`, `intent`, `clarification`

**Class B — Delegation hints (orchestrators only, opt-in):**
`profile`, `tools`, `template`

Class B output only takes effect at `llm/agents:run` time. Running it on workers that rarely delegate is wasteful — and when a worker does delegate, the suggestions were produced without knowing the delegation objective and may be stale.

```yaml
# agent: orchestrator  (orchestrator — full scope)
intake:
  enabled: true
  scope: [title, entities, intent, clarification, profile, tools, template]
  model: haiku
  maxTokens: 400
  confidenceThreshold: 0.85
  triggerOnTopicShift: true
  topicShiftThreshold: 0.65

# agent: data-analyst  (worker — metadata only)
intake:
  enabled: true
  scope: [title, entities, intent]
  model: haiku
  maxTokens: 200

# skill-first forecasting path (narrow worker-equivalent)
intake:
  enabled: true
  scope: [title]
```

Workspace default (`enabled: false`, Class A scope only). Orchestrators opt in to Class B explicitly.

If `profile` is not in scope, `suggestedProfileId` is absent, the auto-route path is unavailable, and the agent always falls through to the manual Option 4 path. A worker without orchestration scope cannot accidentally auto-delegate.

**When to run:**

| Condition | Run? |
|---|---|
| First turn of new conversation | yes |
| Topic shift detected | yes |
| Continuation of clear ongoing task | no — reuse prior `TurnContext` |
| Simple follow-up ("yes", "show more") | no |

**Must not:** access tools or data, make routing decisions (suggests only), rewrite user message, produce prose output (JSON envelope only), output Class B fields when not in scope.

### Expansion Sidecar

Runs at delegation time, between profile rendering and injection into the worker.

Static rendering produces the profile's generic messages verbatim. The expansion sidecar synthesizes task-specific instructions from the combination of generic profile messages and the actual user objective.

```
profile messages (generic, rendered)
    ↓
expansion sidecar  ←  user objective + TurnContext.entities
    ↓
task-specific []PromptMessage
    ↓
injected into child conversation
    ↓
worker LLM starts turn
```

Declared in the profile:

```yaml
expansion:
  mode: llm
  model: haiku
  maxTokens: 600
```

The sidecar receives the profile's rendered messages + the user task. It returns refined messages scoped to the specific campaign, timeframe, and decision horizon — without the worker having to infer these from a generic instruction set.

**Constraints:** no tools, no data access, no tool names in output, bounded by `maxTokens`, role structure preserved. The runtime validates output shape before injecting.

**When to use:** profiles that are intentionally generic across many tasks, or when tasks carry specific entities/dates/constraints. Narrow profiles don't need it. Works with MCP-sourced profiles — the sidecar receives MCP-rendered messages as input and is agnostic to their origin.

### Full Flow

```
user message
    ↓
[intake sidecar — if enabled and scope matches]
    → TurnContext { title, intent, entities,
                    suggestedProfileId, appendToolBundles, templateId,
                    confidence, clarificationNeeded }
    ↓
clarificationNeeded? → elicitation → re-run intake
    ↓
confidence >= threshold AND profile in scope?
    yes → auto-delegate:
            llm/agents:run {
              agentId:         data-analyst,
              objective:       user message,
              promptProfileId: TurnContext.suggestedProfileId,
              toolBundles:     TurnContext.appendToolBundles,
              templateId:      TurnContext.templateId
            }
    no  → orchestrator calls prompt:list, reasons, selects profile,
          may extend toolBundles if needed
    ↓
[runtime: profile lookup + resolution]
    effective bundles  = profile.toolBundles
                       + TurnContext.appendToolBundles
                       + RunInput.toolBundles
    effective template = RunInput.templateId
                      || TurnContext.templateId
                      || profile.template
    QueryInput.ToolBundles = effective bundles
    QueryInput.TemplateId  = effective template
    ↓
[expansion sidecar — if profile.expansion.mode = llm]
    input:  profile messages + TurnContext.entities + user objective
    output: task-specific []PromptMessage
    ↓
worker LLM starts turn:
    - task-specific instructions injected (role-preserving)
    - tool set = profile floor + sidecar appends + orchestrator overrides
    - Binding.Context enriched with TurnContext.entities
    - output template pre-selected
    - conversation title already set
```

Neither `orchestrator` nor the worker LLM performed entity extraction, title generation, bundle selection, or instruction synthesis. All handled by lightweight sidecars before either main model call.

---

## Orchestrator-Specific Guidance

### Start Here — What You Can Do Today

Before building anything, the existing `template:list` / `template:get` system already addresses output-contract duplication (Problem 3) in full:

- Assign `template.bundles` to each agent in its YAML
- Instruct agents to call `template:get` with `includeDocument: true` before producing final output
- The runtime injects formatting instructions as a system document — no code changes, no new infrastructure

This is the lowest-risk, highest-value first step. It eliminates duplicated formatting instructions from prompts and makes output shape configurable per agent. The prompt-profile system is not required for this.

**Do this first.** Validate it in production. Then evaluate how much of the remaining complexity justifies the prompt-profile investment.

### Incremental Implementation Path

1. **Output templates** — EXISTING — `template:list` / `template:get` + `template.bundles` on agents, no code changes
2. **Prompt profiles** — IMPLEMENTED IN CORE — `prompt:list` / `prompt:get`, profile repository, and `RunInput.promptProfileId` resolution are in place; any remaining work is refinement and documentation alignment
3. **Agent collapse** — only after profiles are stable in production (Phase after 6)

### Architecture Layers

**Public:** `orchestrator` — routing, answer quality, escalation, recommendation persistence

**Worker:** `data-analyst` — performance, inventory, recommendation, summary, verification scenarios

**Optional later workers** (only if profile-based control proves too loose):
- forecasting skill path — distinct data contracts, different reasoning pattern
- `platform-lookup` — narrow and operationally specific

**Configuration:** scenario profiles

| Profile | Bundles |
|---|---|
| `performance_analysis` | workspace-performance-tools |
| `performance_summary` | workspace-performance-tools |
| `inventory_diagnosis` | workspace-inventory-tools |
| `configuration_review` | workspace-configuration-tools |
| `recommendation` | workspace-performance-tools |
| `verification` | workspace-verification-tools |
| `site_list_recommendation` | workspace-platform-tools |

> `configuration_review` uses `workspace-configuration-tools`, not `workspace-inventory-tools`. Configuration review is a distinct concern from inventory diagnosis — the example workspace has a dedicated `workspace-configuration` specialist for a reason. If the actual example workspace does not have a `workspace-configuration-tools` bundle, the right resolution is to create one rather than collapse configuration into inventory. Collapsing them would be an example of the capability bleed this proposal is designed to prevent.
| `forecasting` | workspace-forecast-tools |

### What To Collapse

Good collapse candidates: agents that differ mostly by wording, report shape, or small analytical emphasis.

- `workspace-performance` + `workspace-performance-summary` → `performance_analysis` + `performance_summary` profiles
- recommendation/reporting variants that differ by output shape → profiles + output templates

### What Not To Collapse

Keep separate when the real difference is the tool family or validation logic:

- **forecasting** — distinct data contracts and reasoning pattern
- **platform lookup / site-list** — narrow, operationally specific
- **verification / overlap** — distinct analysis style and evidence interpretation

### Decision Rule

- Specialists differ mostly in wording or output contract → collapse into profiles
- Specialists differ mainly in tool family, validation rules, or failure modes → keep structural separation

---

## Risks

### Boundary Loss from Over-Collapsing

The biggest risk is not model quality — it is capability bleed.

Today, separate specialists give you:
- explicit capability partitioning
- stable behavior per domain
- easier regression detection

A prompt template can say "only use platform tools" but if the agent has access to unrelated tools, the boundary is advisory, not structural. Simplification only works if profile selection and tool bundle selection move together.

Collapsing too early to one orchestrator + one worker typically produces:

1. **Prompt sprawl** — the single agent accumulates scenario-specific instructions
2. **Capability bleed** — wrong tools used for wrong scenarios
3. **Evaluation collapse** — failures become impossible to attribute to routing, framing, tools, or output

The profile + bundle model specifically prevents all three. Profiles are scenario config, not agent prompts. Bundles enforce capability structurally.

### Key Principle

Do not pass tool names or capability enforcement through natural-language prompt text.

```
❌  "Use only tools A, B, C" in prompt text
✓   runtime constrains tool set to bundle A; prompt explains reasoning intent
```

The control plane is structural (bundle ids, runtime enforcement), not instructional (prompt text hoping the LLM complies).

---

## Implementation Plan — ALL PHASES COMPLETE

All eleven phases are implemented as of 2026-04-15. The descriptions below are preserved for historical context and as a reference for how the system was designed to work.

---

### Phase 1 — Rename `protocol/prompt` → `protocol/binding`

**Why first:** every subsequent phase imports `protocol/binding`. Do this once as a mechanical refactor before any new code is written.

**Files to create:**

| New path | Source |
|---|---|
| `protocol/binding/prompt.go` | `protocol/prompt/prompt.go` — change `package prompt` → `package binding` |
| `protocol/binding/binding.go` | `protocol/prompt/binding.go` — same |
| `protocol/binding/persona.go` | `protocol/prompt/persona.go` — same |
| `protocol/binding/history_test.go` | `protocol/prompt/history_test.go` — same |
| `protocol/binding/adapter/document.go` | `protocol/prompt/adapter/document.go` — same |
| `protocol/binding/adapter/tool.go` | `protocol/prompt/adapter/tool.go` — same |

**Import path changes (43 files):**

```
"github.com/viant/agently-core/protocol/prompt"  →  "github.com/viant/agently-core/protocol/binding"
prompt.Binding  →  binding.Binding
prompt.Prompt   →  binding.Prompt
prompt.Persona  →  binding.Persona
```

Use `gofmt` + `sed` or IDE refactor. Delete `protocol/prompt/` after all imports are updated.

**Verification checkpoint:**
```
go build ./...          # must pass with zero errors
go test ./...           # all existing tests pass
grep -r "protocol/prompt" . --include="*.go"   # must return zero results
```

---

### Phase 2 — Prompt Profile Types

**New package:** `protocol/prompt/` (now free after Phase 1)

**New files:**

`protocol/prompt/profile.go`
```go
package prompt

type Profile struct {
    ID           string      `yaml:"id"                      json:"id"`
    Name         string      `yaml:"name,omitempty"          json:"name,omitempty"`
    Description  string      `yaml:"description,omitempty"   json:"description,omitempty"`
    AppliesTo    []string    `yaml:"appliesTo,omitempty"     json:"appliesTo,omitempty"`
    Messages     []Message   `yaml:"messages,omitempty"      json:"messages,omitempty"`
    Instructions string      `yaml:"instructions,omitempty"  json:"instructions,omitempty"`
    MCP          *MCPSource  `yaml:"mcp,omitempty"           json:"mcp,omitempty"`
    ToolBundles  []string    `yaml:"toolBundles,omitempty"   json:"toolBundles,omitempty"`
    PreferredTools []string  `yaml:"preferredTools,omitempty" json:"preferredTools,omitempty"`
    Template     string      `yaml:"template,omitempty"      json:"template,omitempty"`
    Resources    []string    `yaml:"resources,omitempty"     json:"resources,omitempty"`
    Expansion    *Expansion  `yaml:"expansion,omitempty"     json:"expansion,omitempty"`
}

type Message struct {
    Role string `yaml:"role" json:"role"` // "system" | "user" | "assistant"
    Text string `yaml:"text,omitempty" json:"text,omitempty"`
    URI  string `yaml:"uri,omitempty"  json:"uri,omitempty"`
}

type MCPSource struct {
    Server string            `yaml:"server" json:"server"`
    Prompt string            `yaml:"prompt" json:"prompt"`
    Args   map[string]string `yaml:"args,omitempty" json:"args,omitempty"`
}

type Expansion struct {
    Mode      string `yaml:"mode"                json:"mode"`       // "llm"
    Model     string `yaml:"model,omitempty"     json:"model,omitempty"`
    MaxTokens int    `yaml:"maxTokens,omitempty" json:"maxTokens,omitempty"`
}

// EffectiveMessages returns the messages to render.
// If Instructions is set and Messages is empty, wraps as single system message.
func (p *Profile) EffectiveMessages() []Message {
    if len(p.Messages) > 0 {
        return p.Messages
    }
    if p.Instructions != "" {
        return []Message{{Role: "system", Text: p.Instructions}}
    }
    return nil
}
```

`protocol/prompt/render.go`
```go
// Render resolves the profile instruction source and returns []schema.PromptMessage.
// Sources: local Messages/Instructions (velty rendered) or MCP server GetPrompt.
func (p *Profile) Render(ctx context.Context, b *binding.Binding, mgr mcpmanager.Manager) ([]schema.PromptMessage, error)
```

**Verification checkpoint:**
```go
// protocol/prompt/profile_test.go
p := &Profile{Instructions: "Focus on pacing."}
msgs := p.EffectiveMessages()
assert.Equal(t, 1, len(msgs))
assert.Equal(t, "system", msgs[0].Role)
```

---

### Phase 3 — Profile Repository

**New file:** `workspace/repository/prompt/repository.go`

```go
package prompt

import "github.com/viant/agently-core/workspace/repository/base"
import promptmdl "github.com/viant/agently-core/protocol/prompt"

type Repository = base.Repository[promptmdl.Profile]

func New(workspace string) *Repository {
    return base.New[promptmdl.Profile](workspace, workspace.KindPrompt)
}
```

**Edit:** `workspace/workspace.go` — add constant:
```go
KindPrompt = "prompts"
```

**Test data:** create `workspace/repository/prompt/testdata/prompts/performance_analysis.yaml`:
```yaml
id: performance_analysis
name: Performance Analysis
description: "Use when the user asks about pacing or KPI health."
appliesTo: [performance, pacing, kpi]
messages:
  - role: system
    text: "You are a performance analyst."
  - role: user
    text: "Analyze the campaign hierarchy."
toolBundles:
  - workspace-performance-tools
template: analytics_dashboard
```

**Verification checkpoint:**
```go
repo := New("/path/to/testdata")
profile, err := repo.Get(ctx, "performance_analysis")
assert.NoError(t, err)
assert.Equal(t, "performance_analysis", profile.ID)
assert.Equal(t, 2, len(profile.Messages))
```

---

### Phase 4 — Extend `RunInput`

**Edit:** `protocol/tool/service/llm/agents/types.go`

Add three optional fields to `RunInput`:
```go
type RunInput struct {
    AgentID          string                 `json:"agentId"`
    Objective        string                 `json:"objective"`
    Context          map[string]interface{} `json:"context,omitempty"`
    Async            *bool                  `json:"async,omitempty"            internal:"true"`
    ConversationID   string                 `json:"conversationId,omitempty"`
    Streaming        *bool                  `json:"streaming,omitempty"`
    ModelPreferences *llm.ModelPreferences  `json:"modelPreferences,omitempty"`
    ReasoningEffort  *string                `json:"reasoningEffort,omitempty"`
    // New — all optional, backward compatible
    PromptProfileId  string                 `json:"promptProfileId,omitempty"`
    ToolBundles      []string               `json:"toolBundles,omitempty"`
    TemplateId       string                 `json:"templateId,omitempty"`
}
```

No logic changes. Fields are read in Phase 6.

**Verification checkpoint:**
```
go build ./...          # zero errors
go test ./...           # all existing tests pass — no behavior change
```

---

### Phase 5 — `prompt:list` and `prompt:get` Service

**New files:**

`protocol/tool/service/prompt/types.go`
```go
package prompt

type ListInput struct{}

type ListItem struct {
    ID          string   `json:"id"`
    Name        string   `json:"name,omitempty"`
    Description string   `json:"description,omitempty"`
    AppliesTo   []string `json:"appliesTo,omitempty"`
    ToolBundles []string `json:"toolBundles,omitempty"`
    Template    string   `json:"template,omitempty"`
}

type ListOutput struct {
    Profiles []ListItem `json:"profiles"`
}

type GetInput struct {
    ID              string `json:"id"`
    IncludeDocument *bool  `json:"includeDocument,omitempty"`
}

type GetOutput struct {
    ID             string   `json:"id"`
    Name           string   `json:"name,omitempty"`
    Description    string   `json:"description,omitempty"`
    ToolBundles    []string `json:"toolBundles,omitempty"`
    PreferredTools []string `json:"preferredTools,omitempty"`
    Template       string   `json:"template,omitempty"`
    Resources      []string `json:"resources,omitempty"`
    Injected       bool     `json:"injected,omitempty"` // true when includeDocument=true and messages were injected
}
```

`protocol/tool/service/prompt/service.go`
- Constructor mirrors template service: `New(repo *promptrepo.Repository, opts ...func(*Service)) *Service`
- Options: `WithConversationClient(c apiconv.Client)`, `WithAgentFinder(f agentmdl.Finder)`
- `list` method: load all profiles from repo, return `ListOutput`
- `get` method:
  - always return `GetOutput` metadata
  - if `includeDocument == true`: call `profile.Render()`, inject each `PromptMessage` via `AddMessage()` role-preserving

**Edit:** `app/executor/builder.go` — register service:
```go
promptRepo := promptrepo.New(out.Workspace.Location)
if err := tool.AddInternalService(out.Registry,
    promptsvc.New(promptRepo,
        promptsvc.WithConversationClient(out.Conversation),
        promptsvc.WithAgentFinder(out.Agent),
    )); err != nil {
    return nil, err
}
```

**Verification checkpoint:**
```bash
# Create workspace/prompts/performance_analysis.yaml
# Start agently-core, call via tool:
prompt:list → returns [{id: "performance_analysis", ...}]
prompt:get {id: "performance_analysis", includeDocument: false} → returns metadata + messages, no conversation injection
prompt:get {id: "performance_analysis", includeDocument: true}  → returns metadata + messages + conversation has injected messages (system-role → system_doc tagged; user/assistant → natural roles)
```

---

### Phase 6 — Runtime Profile Expansion in `run_support.go`

This is the core behavioral phase — where `promptProfileId` in `RunInput` actually takes effect.

**Edit:** `protocol/tool/service/llm/agents/run_support.go`

Add profile expansion step after `qi` (child `QueryInput`) is initialized and before `BuildBinding()`:

```go
// resolveProfile expands promptProfileId into tool bundles, template, and injected messages
func (s *Service) resolveProfile(ctx context.Context, ri *RunInput, qi *QueryInput, turn *TurnMeta) error {
    if ri.PromptProfileId == "" {
        return nil
    }
    profile, err := s.promptRepo.Get(ctx, ri.PromptProfileId)
    if err != nil {
        return fmt.Errorf("profile %q not found: %w", ri.PromptProfileId, err)
    }

    // 1. Render messages (static or MCP source)
    messages, err := profile.Render(ctx, qi.Binding, s.mcpManager)
    if err != nil {
        return err
    }

    // 2. Optionally run expansion sidecar
    if profile.Expansion != nil && profile.Expansion.Mode == "llm" {
        messages, err = s.expandMessages(ctx, messages, ri.Objective, profile.Expansion)
        if err != nil {
            return err
        }
    }

    // 3. Inject messages into child conversation, role-preserving
    for _, msg := range messages {
        if err := s.injectMessage(ctx, turn, msg); err != nil {
            return err
        }
    }

    // 4. Merge tool bundles: profile floor + RunInput additions
    qi.ToolBundles = append(profile.ToolBundles, ri.ToolBundles...)

    // 5. Resolve template (RunInput > profile default)
    if qi.TemplateId == "" {
        if ri.TemplateId != "" {
            qi.TemplateId = ri.TemplateId
        } else if profile.Template != "" {
            qi.TemplateId = profile.Template
        }
    }
    return nil
}
```

**Remove/gate** the existing `delegatedToolAllowList()` heuristic — replace with profile-driven expansion. Keep it behind a flag or remove entirely once profile-based routing is validated.

**Thread `promptRepo` and `mcpManager` into the `llm/agents` Service** via constructor options (mirrors how `conv` is passed today).

**Verification checkpoint:**
```go
// integration test: delegate with promptProfileId
ri := &RunInput{
    AgentID:         "data-analyst",
    Objective:       "Why is campaign 4821 underpacing?",
    PromptProfileId: "performance_analysis",
}
// after run:
// - child conversation has system_doc message with profile instructions
// - child QueryInput.ToolBundles == ["workspace-performance-tools"]
// - child turn uses analytics_dashboard template
// - existing call without PromptProfileId behaves identically to before
```

---

### Phase 7 — Agent Profile Access Control ✅ IMPLEMENTED

**`protocol/agent/agent.go`** — `PromptAccess` struct + `Prompts PromptAccess` field on `Agent`.

**`protocol/prompt/profile.go`** — `Profile.Bundles []string` field added (separate from `ToolBundles`).

**Access control contract (as implemented):**

`agent.Prompts.Bundles` is a **direct allow-list of profile IDs**.
When empty, all profiles are visible (open access).
When set, only profiles whose `id` appears in the list are returned by `prompt:list`.

`profile.ToolBundles` is unrelated — it controls which tool bundles the worker receives, not visibility.

**Example:**
```yaml
# agent: data-analyst
prompts:
  bundles:
    - performance_analysis   # profile id
    - inventory_diagnosis    # profile id
```

---

### Phase 8 — MCP Source Rendering

**Edit:** `protocol/prompt/render.go` — add MCP branch in `Render()`:

```go
if p.MCP != nil {
    cli, err := mgr.Get(ctx, convID, p.MCP.Server)
    if err != nil {
        return nil, err
    }
    args := renderArgs(p.MCP.Args, b) // velty-render each arg value
    result, err := cli.GetPrompt(ctx, &schema.GetPromptRequestParams{
        Name:      p.MCP.Prompt,
        Arguments: args,
    })
    if err != nil {
        return nil, err
    }
    return result.Messages, nil
}
```

**Thread `mcpManager`** into `prompt.Service` via `WithMCPManager(mgr)` option.

**Verification checkpoint:**
```yaml
# profile with mcp source:
id: performance_analysis
mcp:
  server: workspace-data-server
  prompt: performance_analysis_v2
  args:
    dateRange: "{{.context.dateRange}}"
toolBundles: [workspace-performance-tools]
```
```bash
# start mock MCP server that returns test PromptMessages
# call prompt:get — verify messages come from MCP, not local YAML
# call with includeDocument: true — verify MCP messages injected into conversation
```

---

### Phase 9 — MCP Server Exposure

Wire the currently-stubbed `ListPrompts` and `GetPrompt` handlers.

**Edit:** `protocol/mcp/expose/tool_handler.go`

```go
func (h *ToolHandler) ListPrompts(ctx context.Context, cursor *string) (*schema.ListPromptsResult, *jsonrpc.Error) {
    profiles := h.profileRepo.List(ctx)
    result := &schema.ListPromptsResult{}
    for _, p := range profiles {
        desc := p.Description
        result.Prompts = append(result.Prompts, schema.Prompt{
            Name:        p.ID,
            Title:       &p.Name,
            Description: &desc,
        })
    }
    return result, nil
}

func (h *ToolHandler) GetPrompt(ctx context.Context, params *schema.GetPromptRequestParams) (*schema.GetPromptResult, *jsonrpc.Error) {
    profile, err := h.profileRepo.Get(ctx, params.Name)
    if err != nil {
        return nil, jsonrpc.NewInvalidParams(err.Error(), nil)
    }
    messages, err := profile.Render(ctx, nil, h.mcpManager)
    if err != nil {
        return nil, jsonrpc.NewInternalError(err.Error(), nil)
    }
    return &schema.GetPromptResult{Messages: messages}, nil
}
```

**Edit:** `protocol/mcp/localclient/service_handler.go` — same wiring for in-process MCP client.

**Verification checkpoint:**
```bash
# connect an MCP client to agently-core's MCP endpoint
mcp_client.list_prompts()   # returns agently-core profiles
mcp_client.get_prompt("performance_analysis", {})  # returns rendered messages
```

---

### Phase 10 — Expansion Sidecar

**New file:** `protocol/tool/service/llm/agents/expand.go`

```go
// expandMessages calls a sidecar LLM to synthesize task-specific instructions
// from generic profile messages + the user objective.
func (s *Service) expandMessages(
    ctx context.Context,
    messages []schema.PromptMessage,
    objective string,
    cfg *promptmdl.Expansion,
) ([]schema.PromptMessage, error)
```

The sidecar uses a fixed meta-prompt (hardcoded in the runtime, not configurable by agents):
- system: "You are a prompt refinement assistant. Refine the given instructions to be task-specific. Preserve role structure. Do not add tool names. Return only the refined messages."
- user: profile messages + objective

Output is validated: role structure must match input, total token count bounded by `cfg.MaxTokens`.

**Edit:** `protocol/prompt/profile.go` — `Expansion.Mode` drives the branch in `run_support.go`.

**Edit:** `app/executor/builder.go` — pass `expansionModel` config to llm/agents service.

**Verification checkpoint:**
```go
// test with generic profile + specific objective
messages, err := svc.expandMessages(ctx, genericMessages, "campaign 4821 underpacing since Tuesday", cfg)
// verify: output references "campaign 4821" and "Tuesday"
// verify: role structure unchanged (system stays system, user stays user)
// verify: no tool names in output
```

---

### Phase 11 — Intake Sidecar

**New package:** `service/intake/`

`service/intake/context.go`
```go
type TurnContext struct {
    Title               string            `json:"title"`
    Intent              string            `json:"intent"`
    Entities            map[string]string `json:"entities,omitempty"`
    SuggestedProfileId  string            `json:"suggestedProfileId,omitempty"`
    AppendToolBundles   []string          `json:"appendToolBundles,omitempty"`
    TemplateId          string            `json:"templateId,omitempty"`
    Confidence          float64           `json:"confidence"`
    ClarificationNeeded bool              `json:"clarificationNeeded"`
    ClarificationQuestion string          `json:"clarificationQuestion,omitempty"`
}
```

`service/intake/service.go`
- `Run(ctx, userMessage, history, scope, registries) (*TurnContext, error)`
- Receives profile registry, bundle registry, template registry as metadata (id + description only)
- Filters output fields by `scope` before returning
- Class B fields (`SuggestedProfileId`, `AppendToolBundles`, `TemplateId`) only populated when in scope

**New config types:**

`protocol/agent/intake.go`
```go
type Intake struct {
    Enabled              bool     `yaml:"enabled"                        json:"enabled"`
    Scope                []string `yaml:"scope,omitempty"                json:"scope,omitempty"`
    Model                string   `yaml:"model,omitempty"                json:"model,omitempty"`
    MaxTokens            int      `yaml:"maxTokens,omitempty"            json:"maxTokens,omitempty"`
    ConfidenceThreshold  float64  `yaml:"confidenceThreshold,omitempty"  json:"confidenceThreshold,omitempty"`
    TriggerOnTopicShift  bool     `yaml:"triggerOnTopicShift,omitempty"  json:"triggerOnTopicShift,omitempty"`
    TopicShiftThreshold  float64  `yaml:"topicShiftThreshold,omitempty"  json:"topicShiftThreshold,omitempty"`
}
```

**Edit:** `protocol/agent/agent.go` — add `Intake Intake` field to `Agent` struct.

**Edit:** `service/agent/run_query.go` or `service/agent/agent.go` — before routing, check `agent.Intake.Enabled`, run intake sidecar if triggered, store `TurnContext` in `Binding.Context`.

**Scope constants:**
```go
const (
    IntakeScopeTitle         = "title"
    IntakeScopeEntities      = "entities"
    IntakeScopeIntent        = "intent"
    IntakeScopeClarification = "clarification"
    IntakeScopeProfile       = "profile"   // Class B
    IntakeScopeTools         = "tools"     // Class B
    IntakeScopeTemplate      = "template"  // Class B
)
```

**Auto-routing integration:** if `TurnContext.SuggestedProfileId != ""` and `confidence >= threshold`, populate `RunInput.PromptProfileId` and `RunInput.ToolBundles` automatically before orchestrator makes any tool call.

**Verification checkpoints:**
```go
// unit test: scope filtering
tc := intake.Run(ctx, "Why is campaign 4821 underpacing?", ..., []string{"title", "entities"}, ...)
assert.NotEmpty(t, tc.Title)
assert.NotEmpty(t, tc.Entities["campaignId"])
assert.Empty(t, tc.SuggestedProfileId)   // not in scope

// unit test: Class B scope
tc = intake.Run(ctx, ..., []string{"title", "entities", "profile", "tools", "template"}, ...)
assert.Equal(t, "performance_analysis", tc.SuggestedProfileId)

// integration test: auto-routing
// orchestrator with confidenceThreshold: 0.85, intake includes profile scope
// user message → intake runs → confidence 0.91 → llm/agents:run auto-populated with promptProfileId
// verify: orchestrator never called prompt:list manually
```

---

### Implementation Order Summary — ALL COMPLETE ✅

| Phase | What | Key files | Status |
|---|---|---|---|
| 1 | `protocol/prompt` → `protocol/binding` rename | 43 files | ✅ |
| 2 | `prompt.Profile` types + `Render()` | `protocol/prompt/profile.go`, `render.go` | ✅ |
| 3 | Profile repository | `workspace/repository/prompt/` | ✅ |
| 4 | Extend `RunInput` | `protocol/tool/service/llm/agents/types.go` | ✅ |
| 5 | `prompt:list`/`prompt:get` service | `protocol/tool/service/prompt/` | ✅ |
| 6 | Runtime profile expansion | `run_support.go`, `profile_resolve.go` | ✅ |
| 7 | Agent access control (`Profile.Bundles` + `Agent.Prompts`) | `protocol/agent/agent.go`, `protocol/prompt/profile.go`, prompt service | ✅ |
| 8 | MCP source rendering | `protocol/prompt/render.go` | ✅ |
| 9 | MCP server exposure | `protocol/mcp/expose/tool_handler.go`, `localclient/service_handler.go` | ✅ |
| 10 | Expansion sidecar | `protocol/tool/service/llm/agents/expand.go` | ✅ |
| 11 | Intake sidecar | `service/intake/`, `service/agent/intake_query.go`, `protocol/agent/intake.go` | ✅ |
