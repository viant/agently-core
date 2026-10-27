# Intent Profiles and Tool Bundles — Simplifying Orchestrator Workspaces

## Objective

Reduce example workspace complexity without losing controllability.

- reduce narrowly differentiated agent definitions
- reduce prompt and tool-bundle duplication across specialists
- make output shaping reusable across business scenarios
- keep routing, capability boundaries, and evaluation understandable
- lower the cost of adding new project-analysis scenarios

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

Separate workers are only justified when the tool family, validation rules, or failure modes are fundamentally different (e.g. a detached forecasting skill path, `platform-entity-lookup`).

---

## Intake Profiles — IMPLEMENTED

> **IMPLEMENTED** — All of the following are in production code:
> - `../protocol/intent` — `Profile`, `Message`, `MCPSource`, `Expansion` types + `EffectiveMessages()`
> - `../protocol/intent` — `Profile.Render()` supporting local text/URI messages and MCP-sourced messages
> - `workspace/repository/intake/` — profile repository
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
  Analyze performance, trend velocity, and metric health.
  Use when the user asks about project health or status drift.
appliesTo:
  - performance
  - health
  - kpi
messages:
  - role: system
    text: |
      You are a performance analyst for complex projects.
      Focus on trend posture, change velocity, and metric deviation.
      Return actionable recommendations backed by evidence from the data.
  - role: user
    text: |
      Analyze the project hierarchy provided and return the top optimization
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
| `id` | Unique identifier used in `RunInput.intentProfileId` |
| `description` | Selection guidance — answers "when should I pick this?", not "what does this do?" |
| `appliesTo` | Tag vocabulary used by intake sidecar for classification |
| `messages` | Ordered `{role, text\|uri}` sequence — primary form |
| `instructions` | Shorthand for a single system message (simple cases only) |
| `mcp` | Source from MCP server instead of local messages |
| `toolBundles` | Capability floor — always enforced by runtime |
| `preferredTools` | Advisory hints within the bundle boundary |
| `template` | Default output template for this scenario |
| `resources` | Optional knowledge sources |
| `knowledge` | Profile-scoped semantic matches activated only for the selected intent profile |
| `expansion` | Sidecar LLM config for task-specific instruction synthesis |

### Profile-scoped knowledge

Intent profiles may activate semantic retrieval for the current turn without
adding knowledge matching to every turn of the parent agent:

```yaml
knowledge:
  - rootIds: [viant-product-knowledge]
    queryMode: multi
    maxQueries: 4
    maxFragments: 16
    maxDocuments: 3
    minScore: 0.60
    neighborFragmentsBefore: 1
    neighborFragmentsAfter: 1
    documentMode: explicit
    maxDocumentBytes: 32000
    maxTotalBytes: 48000
    canonicalSourceOnly: true
```

`maxFragments` controls the total vector-search anchor budget and is distributed
across decomposed subqueries. `maxDocuments` controls
the distinct documents injected after URI deduplication. In `multi` query mode,
explicit comma/`and` topic lists are matched separately and interleaved so one
topic cannot consume every document slot. Neighbor fragments are loaded by
Embedius from the indexed source sequence. `documentMode: explicit` expands to
the complete article only when the user asks for the full/whole article. All
injected content remains subject to per-document and total-byte limits.

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
- User-role and assistant-role injection via `AddMessage()` is also implemented for intake profiles. `prompt:get` injects each rendered message using its authored role; only `system` messages receive `SystemDocumentMode` / `SystemDocumentTag`.

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
      "description": "Analyze performance, trend velocity, and metric health. Use when the user asks about project health.",
      "appliesTo": ["performance", "health", "kpi"],
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
  "preferredTools": ["workspace-MetricsCube", "workspace-EntityHierarchy"],
  "template": "analytics_dashboard",
  "resources": ["workspace-entity-kb"],
  "messages": [
    { "role": "system", "text": "You are a performance analyst..." },
    { "role": "user",   "text": "Analyze the project hierarchy..." }
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

> In the recommended hybrid path, `orchestrator` calls `prompt:list` to select a profile and passes only `intentProfileId` to `llm/agents:run`. **`orchestrator` does not call `prompt:get` in the normal flow.** The runtime resolves instructions from the profile at delegation time, keeping them out of the orchestrator's context entirely.

`prompt:get` without injection (`includeDocument: false`) is an explicit escape hatch for cases where the orchestrator *deliberately* needs to read instruction content before deciding — for example, to adapt the objective text, or to confirm resource requirements. It is not a "metadata-only" shortcut and should not be the default step in the hybrid flow. Using it by default reintroduces instruction content into the orchestrator's context and partially recreates the context bloat the proposal is trying to avoid.

### Outbound MCP Exposure

The agently-core MCP server's `ListPrompts` and `GetPrompt` handlers (currently stubbed) are wired to the profile registry. External MCP clients see agently-core intake profiles as standard MCP prompts. No translation is needed — profiles already produce `[]PromptMessage`.

---

## Tool Bundle Enforcement

### Pass Identifiers, Not Prose

Do not embed tool names in prompt text:

```
❌  "Use workspace-MetricsCube, workspace-EntityMetrics, workspace-EntityHierarchy..."
✓   toolBundles: ["workspace-performance-tools"]
```

Bundle IDs are small, the runtime enforces access directly, and tool catalogs stay out of LLM context.

### Three-Tier Bundle Resolution

```
1. profile.toolBundles         — structural floor, always included
2. intake.Context.Prompting.AppendToolBundles — intake sidecar additions, task-driven
3. RunInput.toolBundles         — orchestrator explicit override
```

Each tier appends only. No tier removes what a lower tier established.
The profile floor is always safe.

### Three-Tier Template Resolution

```
1. profile.template        — scenario default
2. intake.Context.Prompting.TemplateID — intake sidecar suggestion, driven by user phrasing
3. RunInput.templateId     — orchestrator explicit override (highest priority)
```

Template selection is not additive — one template per turn. An empty value defers to the next tier down.

---

## Sidecar Pipeline — IMPLEMENTED

> **IMPLEMENTED** — both sidecars are fully implemented:
> - Expansion sidecar: `protocol/tool/service/llm/agents/expand.go` — `expandMessages()`, called from `resolveProfile` when `profile.Expansion.Mode == "llm"`
> - Intake sidecar: `service/intake/service.go` + `service/agent/intake_query.go` — runs before tool selection when `agent.Intake.Enabled = true`

Two lightweight LLM calls — one at turn intake, one at delegation — handle work that would otherwise require either verbose agent instructions or LLM reasoning about bundle names.

| Sidecar | When | Input | Output |
|---|---|---|---|
| **Intake** | turn received, before routing | user message + history | intake `Context` |
| **Expansion** | at delegation, before worker | profile messages + objective | task-specific `[]PromptMessage` |

### Intake Sidecar

Runs before `orchestrator` does any routing. Produces intake `Context`:

```json
{
  "title": "Project 4821 Below Target — Week of April 15",
  "intent": "diagnosis",
  "entities": {
    "projectId": "4821",
    "issue": "below target",
    "timeframe": "this week",
    "urgency": "end-of-week"
  },
  "suggestedProfileId": "performance_analysis",
  "appendToolBundles": ["workspace-forecast-tools"],
  "templateId": "compact_report",
  "confidence": 0.91,
  "mode": "route"
}
```

The intake `Context` is stored in `Binding.Context` and flows through the downstream pipeline as routing/prompting metadata. It is distinct from turn runtime execution state such as active inline skill activation.
The extraction cost is paid once; the benefit (title, entities, routing hints) is multiplied across all downstream steps.

**Profile classification.** The runtime provides the intake sidecar with `appliesTo` tag vocabulary from the profile registry. The sidecar classifies user intent against these tags and returns `suggestedProfileId` by registry lookup — it never needs to know profile instruction content.

**Auto tool selection.** The sidecar receives bundle registry metadata (id + one-line description). It detects task signals (e.g. "compare to forecast") and returns `appendToolBundles`. Tool names never enter LLM context.

**Auto template selection.** The sidecar detects output format signals ("give me a dashboard", "quick summary", "for stakeholders") and returns `templateId`. Empty means defer to the profile's default.

**Unification of Options 3 and 4.** The intake sidecar makes Options 3 and 4 two branches of one decision:

```
confidence >= threshold  →  auto-delegate with suggestedProfileId      (Option 3 path)
confidence < threshold   →  orchestrator calls prompt:list, reasons          (Option 4 path)
clarify/action ask       →  elicitation first, then re-run intake
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
| Continuation of clear ongoing task | no — reuse prior intake `Context` |
| Simple follow-up ("yes", "show more") | no |

**Must not:** access tools or data, make routing decisions (suggests only), rewrite user message, produce prose output (JSON envelope only), output Class B fields when not in scope.

### Expansion Sidecar

Runs at delegation time, between profile rendering and injection into the worker.

Static rendering produces the profile's generic messages verbatim. The expansion sidecar synthesizes task-specific instructions from the combination of generic profile messages and the actual user objective.

```
profile messages (generic, rendered)
    ↓
expansion sidecar  ←  user objective + intake.Context entities
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

The sidecar receives the profile's rendered messages + the user task. It returns refined messages scoped to the specific project, timeframe, and decision horizon — without the worker having to infer these from a generic instruction set.

**Constraints:** no tools, no data access, no tool names in output, bounded by `maxTokens`, role structure preserved. The runtime validates output shape before injecting.

**When to use:** profiles that are intentionally generic across many tasks, or when tasks carry specific entities/dates/constraints. Narrow profiles don't need it. Works with MCP-sourced profiles — the sidecar receives MCP-rendered messages as input and is agnostic to their origin.

### Full Flow

```
user message
    ↓
[intake sidecar — if enabled and scope matches]
    → intake.Context { title, intent, entities,
                    suggestedProfileId, appendToolBundles, templateId,
                    confidence }
    ↓
clarify? → elicitation → re-run intake
    ↓
confidence >= threshold AND profile in scope?
    yes → auto-delegate:
            llm/agents:run {
              agentId:         data-analyst,
              objective:       user message,
              intentProfileId: intake.Context.Prompting.SuggestedProfileID,
              toolBundles:     intake.Context.Prompting.AppendToolBundles,
              templateId:      intake.Context.Prompting.TemplateID
            }
    no  → orchestrator calls prompt:list, reasons, selects profile,
          may extend toolBundles if needed
    ↓
[runtime: profile lookup + resolution]
    effective bundles  = profile.toolBundles
                       + intake.Context.Prompting.AppendToolBundles
                       + RunInput.toolBundles
    effective template = RunInput.templateId
                      || intake.Context.Prompting.TemplateID
                      || profile.template
    QueryInput.ToolBundles = effective bundles
    QueryInput.TemplateId  = effective template
    ↓
[expansion sidecar — if profile.expansion.mode = llm]
    input:  profile messages + intake.Context entities + user objective
    output: task-specific []PromptMessage
    ↓
worker LLM starts turn:
    - task-specific instructions injected (role-preserving)
    - tool set = profile floor + sidecar appends + orchestrator overrides
    - Binding.Context enriched with intake.Context entities
    - output template pre-selected
    - conversation title already set
```

Neither `orchestrator` nor the worker LLM performed entity extraction, title generation, bundle selection, or instruction synthesis. All handled by lightweight sidecars before either main model call.

---

## Authorization policy

When `policy.authorization.intent` is enabled, prompt-profile discovery and
runtime injection are filtered through the workspace MCP visibility policy.
When `policy.authorization.starterPrompt` is enabled, suggested starter prompts
are filtered from workspace metadata. These checks only reduce the existing
agent/profile and workspace candidate sets. See
[doc/authorization-policy.md](authorization-policy.md).
