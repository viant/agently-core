# Transcript And Context Management Proposal

## Goal

Make `agently-core` the strongest hybrid of:

- Claude-style context-budget management
- Codex-style canonical state and replay
- Agently's existing transcript, message-removal, and event architecture

The target is not a Claude clone. The target is:

- request-time relevance shaping for the current turn
- durable transcript maintenance for long-lived conversations
- state-safe tool and agent orchestration
- replayable, UI-friendly semantics

## Core idea

Separate three concerns that are currently partially mixed:

1. **Transcript truth**
   - durable record of what happened
   - includes all messages, maintenance operations, summaries, and checkpoints

2. **Prompt-binding projection**
   - the model-facing view for a given turn or model call
   - computed when building prompt bindings
   - may hide irrelevant or superseded messages without deleting transcript truth

3. **Canonical UI state**
   - renderable state derived from transcript truth
   - should preserve distinctions like archived, summarized, hidden-by-summary, and checkpoint

If these layers stay separate, Agently can add sophisticated context reduction without losing state coherence.

## Design principles

1. Do not hard-delete transcript history for context management.
2. Treat temporary relevance reduction differently from durable cleanup.
3. Make request-time context projection explicit in prompt binding.
4. Preserve tool/runtime invariants even when older messages are hidden.
5. Prefer turn-level selection over low-level arbitrary message deletion.
6. Keep UI and transcript query behavior deterministic.
7. Default tool outputs to non-cacheable unless explicitly declared safe.

## Current Agently capabilities

Agently already has important pieces:

- request-time overflow pruning in `/Users/awitas/go/src/github.com/viant/agently-core/service/reactor/overflow.go`
- LLM-assisted message removal candidates in `/Users/awitas/go/src/github.com/viant/agently-core/service/reactor/service_context_limit.go`
- durable soft removal via `message:remove` in `/Users/awitas/go/src/github.com/viant/agently-core/protocol/tool/service/message/remove.go`
- transcript/UI hidden-state interpretation in `/Users/awitas/go/src/github.com/viant/agently-core/sdk/ts/src/interpret.ts`
- canonical state/reducer direction in the SDK
- anchor continuation and response-anchor semantics in core generation

What is missing is a single coherent model tying these together.

## Proposed architecture

### 1. Transcript truth

Transcript truth should remain the durable source of record.

Transcript should contain:

- original user/assistant/tool/system messages
- durable summary messages
- archived flags for permanently retired messages
- context-maintenance checkpoints
- metadata needed for replay and debugging

Transcript truth should answer:

- what happened
- what was later summarized or archived
- where maintenance checkpoints occurred
- how to resume from the last checkpoint

### 2. Prompt-binding projection

Projection should be computed when building prompt bindings, not persisted as a primary transcript object.

Projection should support:

- hiding irrelevant prior-turn segments for the current turn
- hiding superseded prior outputs for the current turn
- excluding archived messages by default
- substituting summaries instead of full removed ranges where available

Projection should not mutate transcript truth.

Projection should live in prompt-binding/build state, for example:

```go
type ContextProjection struct {
    // Scope mirrors Agent.Tool.CallExposure ("turn" | "conversation").
    // It is primarily advisory / bookkeeping context for projection policy.
    //
    // Relevance-based hiding of prior turns is valid in BOTH scopes:
    // old user+assistant segments can still be irrelevant even when tool exposure
    // is turn-local.
    //
    // The main difference is that cross-turn tool-result supersession is most
    // useful when Scope == "conversation", because turn-local tool exposure
    // already limits how much prior tool state is carried forward.
    Scope            string
    HiddenTurnIDs    []string // turns hidden by relevance projection; valid in both scopes; matches History.Past[i].ID
    HiddenMessageIDs []string // expanded from HiddenTurnIDs plus any extra superseded tool messages; used by prompt builder
    Reason           string
    TokensFreed      int
}
```

This is a build-time view, not a durable transcript row.
Hidden-for-turn is not the same as archived.
`Scope` does not gate whether turn-level hiding is allowed; it only influences
how much additional tool-state reduction is worth doing.

Turn ID (`History.Past[i].ID`) is the correct key. It is already present in `TurnMeta.TurnID`,
`History.CurrentTurnID`, and the `Turn` write model — no new identifier scheme required.

### 3. Canonical UI state

Canonical UI state should distinguish:

- normal message
- archived message
- summary message
- summarized message
- checkpoint

The UI should not guess these semantics from content. It should read explicit state from transcript and maintenance metadata. Turn-local projection can remain a prompt-binding concern rather than a UI-visible durable state.

## Two reduction classes

Agently should use two separate context reduction classes.

### A. Request-time relevance reduction

Purpose:

- reduce context for the current turn
- hide irrelevant or superseded prior-turn material
- keep transcript untouched

This is `projection` in Agently vocabulary.

### B. Durable maintenance reduction

Purpose:

- retire low-value old history permanently from active default views
- replace with summary
- archive selected messages

This is what `message:remove`, future `Prune`, and future `Compact` should handle.

## Relevance projection

### What it does

At the beginning of a turn:

1. analyze prior turns
2. identify irrelevant or superseded conversation segments
3. hide them from the current turn's model-facing active context
4. keep transcript truth intact

### What it should operate on

Operate on complete turns from `History.Past`, not individual messages.

Runtime expands a selected turn ID to all `Turn.Messages` for that turn.

**Candidate turn eligibility rules** — a turn in `History.Past` is a valid candidate only when:

1. It is not the current turn.
2. It is not in the protected recent tail (`History.Past[len-ProtectedRecentTurns:]`).
3. After per-message filtering (`archived`, `interim`), it has at least one user or assistant text segment to describe to the selector.

Turn-status filtering (`queued`/`canceled`/in-flight) is **not** duplicated in the candidate
builder. Those turns are already excluded from the bound history at
`service/agent/binding_history.go:478-482`, so the selector never sees them. Adding a
redundant status check in the builder would only complicate the code path.

Turn ID is the stable key: already threaded through `TurnMeta.TurnID`, `History.CurrentTurnID`,
and `History.Past[i].ID` — no new identifier scheme needed.

### Projection configuration

Relevance projection is configurable through the `Projection` group on workspace
defaults (`app/executor/config/default.go`). It lives in workspace defaults rather
than on the reactor or per-agent so operators can tune it without touching code.

```go
// Projection groups prompt-history projection settings.
type Projection struct {
    Relevance            *RelevanceProjection  `yaml:"relevance,omitempty"`
    ToolCallSupersession *ToolCallSupersession `yaml:"toolCallSupersession,omitempty"`
}

// RelevanceProjection controls optional selector-based hiding of irrelevant
// prior turns before prompt-history construction. All fields are pointers so
// "unset" can be distinguished from "explicitly zero".
type RelevanceProjection struct {
    Enabled              *bool           `yaml:"enabled,omitempty"`
    ProtectedRecentTurns *int            `yaml:"protectedRecentTurns,omitempty"`
    TokenThreshold       *int            `yaml:"tokenThreshold,omitempty"`
    ChunkSize            *int            `yaml:"chunkSize,omitempty"`
    MaxConcurrency       *int            `yaml:"maxConcurrency,omitempty"`
    Model                *string         `yaml:"model,omitempty"`
    Prompt               *binding.Prompt `yaml:"prompt,omitempty"`
}
```

Default values (returned by helper accessors when fields are unset):

| Field | Default | Helper |
|---|---|---|
| `Enabled` | `true` | `IsEnabled()` |
| `ProtectedRecentTurns` | `1` | `ProtectedTurns()` |
| `TokenThreshold` | `20000` | `Threshold()` |
| `ChunkSize` | `0` (no chunking) | `Chunk()` |
| `MaxConcurrency` | `1` | `Concurrency()` |
| `Model` | agent's default model when unset | — |
| `Prompt` | built-in `prompts.RelevanceProjection` template when unset | — |

The defaults are tuned for production: projection is enabled by default but only
runs when the conversation actually exceeds the token threshold (20k), so small
conversations skip the selector call entirely. `ChunkSize`/`MaxConcurrency` are
covered separately under "Large-conversation relevance delegation".

### Selector model input

The selector receives a `relevanceSelectorInput`. The model is taken from
`RelevanceProjection.Model` (falling back to the agent's default model). The
prompt template is taken from `RelevanceProjection.Prompt` (falling back to the
built-in `prompts.RelevanceProjection` template).

```go
type relevanceSelectorInput struct {
    CurrentTask       string                   `json:"currentTask"`
    ProtectedTurnIDs  []string                 `json:"protectedTurnIds"`
    Candidates        []relevanceTurnCandidate `json:"candidates"`
    ProjectionScope   string                   `json:"projectionScope,omitempty"`
    ConversationID    string                   `json:"conversationId,omitempty"`
    CurrentTurnID     string                   `json:"currentTurnId,omitempty"`
    ApproxTokenBudget int                      `json:"approxTokenBudget,omitempty"`
}

type relevanceTurnCandidate struct {
    TurnID          string `json:"turnId"`                    // History.Past[i].ID
    UserText        string `json:"userText,omitempty"`        // first user message in the turn
    AssistantText   string `json:"assistantText,omitempty"`   // first assistant reply in the turn
    EstimatedTokens int    `json:"estimatedTokens,omitempty"` // sum of token estimates across Turn.Messages
}
```

`AssistantText` is the assistant's actual reply in the turn — not a separately
written summary message. A summary-message field would only have content after a
`Compact` runs, and Compact does not yet exist; the assistant reply is always
available and is what the selector needs to judge relevance.

The trailing optional fields (`ProjectionScope`, `ConversationID`, `CurrentTurnID`,
`ApproxTokenBudget`) are observability/debug context for prompt logs and are not
required for selection.

Example:

```json
{
  "currentTask": "Design async operation handling for shell, child agents, and external MCP services.",
  "protectedTurnIds": ["t-abc900", "t-abc901"],
  "candidates": [
    {
      "turnId": "t-abc120",
      "userText": "Analyze OAuth callback flow.",
      "assistantText": "The redirect URI is registered server-side and the token exchange uses a PKCE verifier...",
      "estimatedTokens": 4200
    },
    {
      "turnId": "t-abc188",
      "userText": "Compare Codex and Claude async orchestration.",
      "assistantText": "Codex models items with explicit lifecycle states; Claude relies on background agents...",
      "estimatedTokens": 6800
    }
  ],
  "projectionScope": "conversation",
  "currentTurnId": "t-abc910",
  "approxTokenBudget": 180000
}
```

### Large-conversation relevance delegation

For very large conversations, relevance selection should be designed as a
delegated workload, not as a single monolithic selector call.

This matters when:

- the main model supports a very large window (for example 1M tokens)
- the conversation is large enough that candidate construction itself is expensive
- one small selector call would still be too large or too slow

In that case, Agently should allow the relevance pass to run as concurrent
smaller-model delegation over chunks of prior turns.

Recommended approach:

1. partition eligible prior turns into windows
2. run the selector model on each window concurrently
3. merge the selected hide/keep recommendations
4. run one final consolidation step before applying `message:project`

The main model should not pay for this selection work. This is a separate
projection budget.

#### Projection budget

Projection must have an explicit token budget independent from the main model's
request budget.

Conceptually:

```go
type ProjectionConfig struct {
    Enabled bool `yaml:"enabled"`
    ProtectedRecentTurns int `yaml:"protectedRecentTurns"`
    TokenThreshold int `yaml:"tokenThreshold"`

    Selector ProjectionSelectorConfig `yaml:"selector"`
}

type ProjectionSelectorConfig struct {
    Model  string         `yaml:"model"`
    Prompt string         `yaml:"prompt,omitempty"`
    Limits SelectorLimits `yaml:"limits"`
}

type SelectorLimits struct {
    // InputTokens caps the total candidate text sent to the selector
    // workload across all chunks.
    InputTokens int `yaml:"inputTokens"`

    // Chunks caps how many windows may be delegated for one turn.
    // This limits total fan-out / total delegated work.
    Chunks int `yaml:"chunks"`

    // Concurrent limits how many selector calls may run at once.
    // This limits runtime parallelism, not total work.
    Concurrent int `yaml:"concurrent"`
}
```

Default posture:

- small conversations: single selector call
- large conversations: chunked concurrent selector calls
- if the selector budget would be exceeded, skip projection rather than letting
  the projection step become its own overflow source

`Chunks` and `Concurrent` are intentionally different:

- `Chunks` limits total delegated windows
- `Concurrent` limits how many of those windows run at the same time

Example:

- `Chunks = 8`
- `Concurrent = 2`

means:

- at most 8 selector jobs total
- only 2 selector jobs running in parallel at once

#### Budget accounting

Agently should account for projection tokens separately from the main turn's
prompt budget:

- `main model budget` — the actual assistant turn request
- `projection budget` — relevance-selection work done before prompt binding

The projection budget should be tracked and bounded so that:

- many concurrent small-model calls do not silently exceed acceptable cost
- projection does not consume so much budget that it defeats the benefit of reduction
- operators can tune the tradeoff between projection quality and cost

#### Merge rule

Chunked selector calls should not directly mutate projection state.

Each chunk should return only:

- candidate turn IDs to hide
- candidate turn IDs to keep
- optional token-saving estimate

Then the runtime merges them, applies protected-tail rules globally, and issues
one final `message:project` action.

This keeps:

- selection parallel
- projection deterministic
- the final active context shape controlled by one runtime decision point

### Selector output — `message:project` tool

The selector model must call the `message:project` tool rather than returning free-form JSON.
Tool-call output gives the runtime a typed, validated action rather than a schema to parse.

**Tool name:** `message:project`

**Input type:**

```go
// ProjectInput is what the selector model passes to message:project.
// Turn IDs match History.Past[i].ID from protocol/prompt/binding.go.
type ProjectInput struct {
    // TurnIDs lists turns to hide from the current turn's active context.
    // Runtime expands each turn ID to all Turn.Messages for that turn.
    TurnIDs []string `json:"turnIds"`

    // Reason is a short human-readable explanation for observability.
    Reason string `json:"reason"`
}
```

Example call:

```json
{
  "turnIds": ["t-abc120"],
  "reason": "Older OAuth analysis is unrelated to the current async operation design task."
}
```

The runtime expands each `turnId` to the concrete `[]Message` in `History.Past[i].Messages`
and records them in `ContextProjection.HiddenTurnIDs`. No transcript mutation occurs.

**Output type:**

```go
type ProjectOutput struct {
    HiddenTurnIDs []string `json:"hiddenTurnIds"`
}
```


## Superseded state reduction

This is a separate and important mechanism.

Problem:

Sometimes the issue is not that a message is old. The issue is that a later message or tool result supersedes it.

Examples:

- `read(F1)` -> old file contents
- `write(F1)`
- `read(F1)` -> new file contents

The first read is not just old. It is superseded for the same referent.

Likewise:

- status checks on the same task ID
- repeated grep/read over the same file/scope
- repeated forecast/status outputs for the same external operation

### Proposed mechanism

Supersession uses a single content-addressed key:

```
supersessionKey = md5(toolName + canonicalJSON(args))
```

No subject extraction. No JSONPath. No per-tool configuration.

If the same tool is called with the same arguments, the two calls hash to the same key and
the newer result supersedes the older one. If the arguments differ (different file path,
different task ID, different query), they produce different keys and do not supersede each other.

```go
// supersessionKey computes a stable key for a tool call based on tool name and
// canonical (sorted) JSON args. Two calls with the same name and args produce
// the same key regardless of map iteration order. See
// service/agent/supersession.go for the canonical implementation.
func supersessionKey(toolName string, args map[string]interface{}) string {
    name := strings.TrimSpace(toolName)
    canonical := canonicalArgsJSON(args) // sorted keys, "{}" for empty
    raw := name + "|" + canonical
    sum := md5.Sum([]byte(raw))
    return hex.EncodeToString(sum[:])
}
```

Empty/nil args canonicalize to `"{}"`. Nested maps are recursively walked so
inner `map[string]interface{}` keys are also sorted before marshal. The
separator (`|`) prevents tool-name/args boundary ambiguity in the hash input.

Examples — same key, different calls:

| Call 1 | Call 2 | Same key? |
|---|---|---|
| `read {path:"/src/main.go"}` | `read {path:"/src/main.go"}` | yes — second supersedes first |
| `read {path:"/src/main.go"}` | `read {path:"/src/other.go"}` | no — independent |
| `grep {root:"/src",pat:"TODO"}` | `grep {root:"/src",pat:"TODO"}` | yes |
| `grep {root:"/src",pat:"TODO"}` | `grep {root:"/src",pat:"FIXME"}` | no |
| `task/status {taskId:"t1"}` | `task/status {taskId:"t1"}` | yes |
| `task/status {taskId:"t1"}` | `task/status {taskId:"t2"}` | no |

The projection builder scans `History.Past` tool messages in chronological order, tracks the
newest message per supersession key, and adds all older ones to `ContextProjection.HiddenMessageIDs`.

**Supersession only runs when `ContextProjection.Scope == "conversation"`** — i.e. when
`Agent.Tool.CallExposure` is `"conversation"`. When exposure is `"turn"`, tool results are
already scoped to their turn; there is no cross-turn state to manage and the scan is skipped.

Only messages whose tool has `Cacheable = true` participate in this scan.

#### Bucketed retention

Supersession partitions cacheable tool messages into two buckets and applies a
per-key retention cap within each bucket. There is **no per-turn-distance
protection** (no special treatment for `N-1`); the protection is implicit —
the most-recent same-key call in each bucket is always kept.

Buckets:

- **Turn** — messages in the current turn (`turnIdx == currentTurnIdx`).
- **History** — messages in any prior turn.

Per-key retention (defaults from `app/executor/config/default.go`):

| Bucket | Default cap | Meaning |
|---|---|---|
| `Turn` | `2` | within the current turn, keep the most recent 2 calls per key; suppress earlier ones |
| `History` | `1` | across all prior turns, keep the single most recent call per key; suppress all earlier ones |

Configuration: `Defaults.Projection.ToolCallSupersession`:

```yaml
projection:
  toolCallSupersession:
    enabled: true
    limit:
      turn: 2
      history: 1
```

Both caps are configurable. Set `enabled: false` to skip the scan entirely.

Rationale for two buckets rather than three (current / `N-1` / `N-2+`):

- supersession by definition fires only on identical key (same tool name, same
  canonicalized args). When the older call is hidden, a newer call with
  the same answer is, by construction, still visible.
- "protect `N-1`" would only matter if the model needs both an older and a
  newer same-key result simultaneously (e.g. before/after comparison). For
  cacheable tools (`read`, `grep`, status checks) this case is vanishingly
  rare; if a tool genuinely needs both outputs visible, mark it
  `Cacheable: false` so it never enters the scan.
- the working-memory rationale that motivates `N-1` protection in
  *relevance projection* (where whole turns can be hidden as "irrelevant")
  does not apply to same-key dedupe, where the question and answer are
  identical and the freshest result is authoritative.

#### Walk

For each cacheable tool message in chronological order:

1. compute `supersessionKey(toolName, args)`
2. assign to `Turn` bucket if `turnIdx == currentTurnIdx`, else `History`
3. group entries by key within each bucket
4. for any group exceeding its bucket's cap, mark the oldest excess entries
   as suppressed and add them to `ContextProjection.HiddenMessageIDs`

## Cacheable tool outputs

Agently should add an explicit `cacheable` concept for tool outputs.

### Unified mechanism

Both local and MCP tools resolve to `llm.ToolDefinition` (`genai/llm/tool.go`).
MCP tools are converted via `ToolDefinitionFromMcpTool()` at registration time.
At runtime, `llm.ToolDefinition.Cacheable` is the single source of truth — the
registry, prompt builder, and supersession logic all read it without knowing the
tool's origin.

Cacheability is *declared* at registration time through one of two
ergonomic helpers, each of which collapses onto `def.Cacheable` before the
definition is exposed:

- internal services may implement `CacheableMethods() map[string]bool`
  (`protocol/tool/service/types.go`); the registry reads it once at registration
  and writes the result onto each `llm.ToolDefinition` it produces for that service
- MCP tools accept `ToolCacheOverride.Cacheable` from server/binder config
  (`protocol/mcp/config/config.go`), applied after `ToolDefinitionFromMcpTool()`

Both helpers are registration-time conveniences, not parallel runtime registries.
Once registration completes, no code path queries `CacheableMethods()` again —
supersession, prompt building, and observability all read `def.Cacheable`. See
`internal/tool/registry/registry.go: applyCacheableOverrideWithMethods` for the
collapse point.

Extend `llm.ToolDefinition` directly:

```go
// In genai/llm/tool.go — single addition to ToolDefinition

type ToolDefinition struct {
    Name         string                 `json:"name" yaml:"name"`
    Description  string                 `json:"description,omitempty" yaml:"description,omitempty"`
    Parameters   map[string]interface{} `json:"parameters,omitempty" yaml:"parameters,omitempty"`
    Required     []string               `json:"required,omitempty" yaml:"required"`
    OutputSchema map[string]interface{} `json:"output_schema,omitempty" yaml:"output_schema,omitempty"`
    Strict       bool                   `json:"strict,omitempty" yaml:"strict,omitempty"`

    // Cacheable marks this tool's output as a supersession candidate.
    // When true, the projection builder computes md5(toolName+canonicalJSON(args))
    // as the supersession key and hides older calls with the same key.
    // Default false — opt-in only.
    Cacheable bool `json:"cacheable,omitempty" yaml:"cacheable,omitempty"`
}
```

No `SupersessionKeyExpr`. No JSONPath. The supersession key is always derived from the
full call signature: `md5(toolName + canonicalJSON(args))`. This requires no per-tool
configuration and works identically for internal and MCP tools.

Default values:

| Field | Default | Meaning |
|---|---|---|
| `Cacheable` | `false` | output is never superseded unless explicitly opted in |

### Internal tools

Internal services declare cacheability per method by implementing
`CacheableMethods() map[string]bool`. The registry reads this map at
registration time and writes the result onto every `llm.ToolDefinition` produced
for that service. After that, the tool definition's `Cacheable` field is the
sole runtime signal.

This is the recommended internal-service pattern:

- cacheability is declared when the service registers — once, not per call
- the result is materialised on `llm.ToolDefinition.Cacheable` at registration
- runtime callers (prompt builder, supersession scan) only read `def.Cacheable`

Example service implementation:

```go
// protocol/tool/service/resources/service.go
func (s *Service) CacheableMethods() map[string]bool {
    return map[string]bool{
        "read":      true,
        "list":      true,
        "search":    true,
    }
}

// protocol/tool/service/message/service.go
func (s *Service) CacheableMethods() map[string]bool {
    return map[string]bool{
        // message:remove and message:project are not cacheable; their outputs
        // describe state changes, not snapshots
    }
}
```

After registration, both shapes resolve to the same runtime value:

```go
llm.ToolDefinition{Name: "resources/read",  Cacheable: true}
llm.ToolDefinition{Name: "message:remove",  Cacheable: false}
```

If registration ergonomics matter, add a helper/builder such as `WithCacheable(true)`,
but still materialize the final value on `llm.ToolDefinition`. Avoid a separate
`CacheableTool` interface or parallel metadata registry.

Examples:

| Tool | `Cacheable` |
|---|---|
| `resources/read` | `true` |
| `grepFiles` | `true` |
| `forecasting/status` | `true` |
| `llm/agent-status` | `true` |
| `message:remove` | `false` |
| `message:project` | `false` |

### External MCP tools

MCP tool definitions arrive via `ToolDefinitionFromMcpTool()` with `Cacheable` defaulting
to `false`. Operators mark external tools cacheable in binder/server config:

```go
// ToolCacheOverride lets operators declare cache policy for external MCP tools
// without requiring server changes.
type ToolCacheOverride struct {
    // ToolName is the exact tool name as returned by the MCP server.
    ToolName  string `yaml:"toolName"`
    Cacheable bool   `yaml:"cacheable"`
}
```

After `ToolDefinitionFromMcpTool()`, the registry applies any matching `ToolCacheOverride`
to set `Cacheable` on the resulting `llm.ToolDefinition`. The supersession key is then
computed automatically as `md5(toolName + canonicalJSON(args))` — no further configuration.

### Where supersession is applied

Supersession runs during prompt-binding projection (part of `ContextProjection` build),
not at tool execution time. The projection builder:

1. walks all messages (current turn + `History.Past`) that have a tool call attached
2. for each tool with `Cacheable == true`, computes `supersessionKey(toolName, args)` (see "Proposed mechanism")
3. assigns each entry to the `Turn` bucket if it belongs to the current turn, else to `History`
4. groups entries by key within each bucket and suppresses the oldest entries past each bucket's cap (`Turn`, `History`)
5. adds suppressed message IDs to `ContextProjection.HiddenMessageIDs`

This keeps transcript truth intact and supersession scoped to the current turn's active context.

## Maintenance strategy

Durable maintenance (class B reduction above) has two implementations that operators choose between via configuration. The model-facing surface stays single — only one `message:remove` tool is registered with the LLM. Strategy is a registration-time decision, not something the model picks.

### Why two strategies, not a swap

The `message.archived = 1` flag in the current codebase is doing three semantically distinct jobs:

1. Durable soft-delete tied to a summary, written by the `message:remove` tool (`protocol/tool/service/message/remove.go:116`). This is the only "maintenance" use.
2. Superseded interim assistant messages within a turn, paired with `superseded_by`, written by `service/agent/run_turn.go:519` and `service/core/modelcall/recorder_observer.go:383`.
3. Processed error messages under `applyPreview`, written by `service/agent/binding_history.go:373`.

Only (1) is a maintenance event. (2) and (3) are per-message lifecycle marks that already pair with `superseded_by` and are emitted many times per turn. Replacing them with checkpoint rows would explode checkpoint count and conflate replay anchors with streaming lifecycle. The strategy switch therefore applies **only to (1)**; (2) and (3) keep using `archived` + `superseded_by` unchanged.

### Strategies

**`archive`** (default, legacy):

- `message:remove` writes a summary message and sets `archived = 1` on each affected message.
- No checkpoint row is written.
- Read-path filters use the existing `IsArchived()` check.
- Replay / transcript slicing not supported; canonical state is reconstructed by reading every row and filtering.

**`checkpoint`**:

- `message:remove` writes a summary message and a checkpoint row (`role='system', type='control', tags='checkpoint'`) whose JSON `content` enumerates `AffectedIDs` and `SummaryIDs`.
- `archived = 1` is **not** set on affected messages — the checkpoint row is the authoritative record of hidden-by-maintenance state.
- Read-path filters consult both `IsArchived()` (for use cases 2/3) **and** a per-conversation hidden-set built from checkpoint rows.
- Transcript slicing from the latest checkpoint works as described in the Checkpoints section below.

Both strategies share the same model-facing tool name, the same `RemoveInput` shape, and the same atomic guarantee that the summary message and the maintenance record are written together.

### Configuration

Strategy selection lives in workspace defaults (`app/executor/config/default.go`), following the existing `Projection` precedent that is already wired through `workspace/config/config.go: DefaultsWithFallback`:

```go
// app/executor/config/default.go
type Maintenance struct {
    // Strategy is "archive" (default) or "checkpoint".
    Strategy *string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
}

// inside Defaults:
Maintenance *Maintenance `yaml:"maintenance,omitempty" json:"maintenance,omitempty"`
```

Resolution order:

1. workspace `Defaults.Maintenance.Strategy`
2. fallback to `archive`

**Agent-level override is deferred.** Conversations are not agent-scoped — parent + child agents share a transcript — so a per-agent strategy could produce mixed-shape history within a single conversation. Workspace-only resolution is the safer default. Introduce `Agent.Maintenance` override only when a concrete use case appears (e.g. a maintenance-only agent that must always emit checkpoints regardless of workspace setting), and document the mixed-history caveat at that point.

### Read-path implications

Switching strategies does not let the read path simplify. Once a conversation has history written under one strategy, switching workspace config later means the transcript contains both shapes. Read filters must handle both unconditionally for the lifetime of the deployment:

```go
func isHidden(m *Message, hidden map[string]struct{}) bool {
    if m.IsArchived() {
        return true
    }
    _, ok := hidden[m.Id]
    return ok
}
```

`hidden` is built once per `GetConversation` call by scanning checkpoint rows in the transcript and unioning their `AffectedIDs`. Sites that need the combined check (all currently call `IsArchived()` directly):

- `app/store/conversation/transcript.go:41`
- `service/agent/summary.go:44`
- `service/agent/relevance.go:199`
- `service/agent/binding_history.go:525,531`
- `protocol/tool/service/message/list_candidates.go:107`
- `service/reactor/service_context_limit.go:131`

The strategy switch is a write-side decision; read-side support for both shapes is permanent.

### Strategy interface

```go
// service/agent/maintenance/strategy.go
type RemoveStrategy interface {
    Apply(ctx context.Context, in *RemoveInput) (*RemoveOutput, error)
}
```

Two implementations:

- `ArchiveStrategy` — body of the current `protocol/tool/service/message/remove.go` (`SetArchived(1)` per affected message).
- `CheckpointStrategy` — writes summary message, then writes one checkpoint row carrying `ContextCheckpoint{Op:"prune", AffectedIDs:..., SummaryIDs:...}`. Does not set `archived = 1`.

The selected strategy is injected when the `message:remove` tool is registered. The model sees one tool either way.

## Checkpoints

Agently should use checkpoints primarily for transcript slicing, replay, and maintenance history.

Checkpoints are most valuable for:

- durable prune applied
  - durable compact applied
  - major maintenance milestones that later transcript loading should recognize

Rule: every durable maintenance operation writes a checkpoint, regardless of
whether it was automatic or user-forced. That includes:

- auto-compact
  - user-forced compact
  - durable prune

### DAO / schema placement

Checkpoints are stored as **message rows** in the existing `message` table.

This is the right placement for three reasons:

1. The `run` table already has `checkpoint_message_id` (`pkg/agently/run/write/run.go`),
   which is a pointer to a message row. The design intent is already there.
2. Message rows are naturally ordered with the rest of the transcript by `sequence`.
   A separate table would require joining back to recover temporal order.
3. Existing transcript query infrastructure (Datly SQL, SDK selectors, `Since`/cursor reads)
   works on message rows — checkpoints become first-class transcript citizens with no new query paths.

#### Message row encoding

Checkpoint messages use **only existing schema values** — no migration required.

| Field | Value | Notes |
|---|---|---|
| `role` | `'system'` | maintenance event, not user/assistant |
| `type` | `'control'` | existing allowed value |
| `tags` | `'checkpoint'` | free-form text column, no constraint — identifies checkpoint rows |
| `status` | `'completed'` | existing allowed value |
| `content` | JSON payload | see `ContextCheckpoint` shape below |
| `conversation_id` | conversation ID | normal FK |
| `turn_id` | turn ID | turn during which maintenance ran |
| `archived` | `0` | checkpoints are never archived |
| `sequence` | next in conversation | the slice anchor for transcript reads |

The `content` field carries the JSON-serialised `ContextCheckpoint` payload:

```go
// ContextCheckpoint is stored in message.content for role='system', type='control', tags='checkpoint' rows.
// ConversationID, TurnID, and CreatedAt are already message row columns — not repeated here.
type ContextCheckpoint struct {
    Op          string   `json:"op"`          // "prune" | "compact"
    AffectedIDs []string `json:"affectedIds"` // message IDs archived
    SummaryIDs  []string `json:"summaryIds"`  // summary message IDs written alongside
    TokensFreed int      `json:"tokensFreed"`
    Reason      string   `json:"reason"`
}
```

#### What Prune and Compact must both write

Both operations write two message rows:

1. **Summary message** — `role='assistant'`, `type='text'`, `status='summary'` — replaces
   archived content in the model's view. Written first so its ID is available for `SummaryIDs`.
2. **Checkpoint message** — `role='system'`, `type='control'`, `tags='checkpoint'` — records
   the maintenance event. Written second; its `sequence` becomes the transcript slice anchor.

A prune or compact without a summary message leaves a gap in the model's context.
Both rows are always written together or not at all.

#### Transcript slice anchor

`message.sequence` is **turn-scoped** — the schema enforces `UNIQUE KEY idx_message_turn_seq (turn_id, sequence)`. It is not conversation-global. A bare `sequence >=` comparison at conversation level is meaningless.

Conversation-global turn ordering is `turn.queue_seq` (`KEY idx_turn_conv_queue_seq (conversation_id, queue_seq)`). Within a turn, messages are ordered by `(sequence, created_at)`.

The correct anchor is `(checkpoint.turn_id, checkpoint.sequence)` derived from the checkpoint message's PK. "Transcript from last checkpoint" means:

- all messages from turns where `turn.queue_seq > checkpoint_turn.queue_seq`
- plus messages in the checkpoint's own turn where `message.sequence >= checkpoint.sequence`

#### `turn.queue_seq` nullability guard

`turn.queue_seq` is `BIGINT DEFAULT NULL`. The existing codebase already handles this with
a consistent fallback pattern (from `queued_turn.sql` and `queued_turns.sql`):

```sql
ORDER BY COALESCE(t.queue_seq, -1) ASC, t.created_at ASC, t.id ASC
```

All transcript slice queries must use the same fallback. Do not compare bare `t.queue_seq`
values — always wrap in `COALESCE(t.queue_seq, -1)` to ensure null-queue_seq turns sort
deterministically before any properly queued turn, with `created_at` and `id` as tiebreakers.

**Checkpoint turn requirement:** if the checkpoint turn's own `queue_seq` is null, the
comparison `COALESCE(t.queue_seq, -1) > COALESCE(checkpoint_turn.queue_seq, -1)` evaluates
to `-1 > -1` which is false, and only the intra-turn `sequence` branch will match. This is
safe — it means the checkpoint and all subsequent messages within that turn are included,
but no later turns are included unless they also have null queue_seq with later created_at.
For reliable cross-turn slicing the checkpoint turn must have a non-null `queue_seq`.

If old turns have null `queue_seq`, do not rely on a DB-specific SQL backfill here.
Prefer either:

1. a portable DAO/app-level backfill job that reads turns ordered by
   `(conversation_id, created_at, id)` and writes `queue_seq` with simple
   `UPDATE turn SET queue_seq = ? WHERE id = ? AND queue_seq IS NULL`, or
2. a conservative fallback to full transcript for conversations whose checkpoint
   turn has null `queue_seq`.

#### Two-step lookup, ID-anchored

```sql
-- Step 1: find the latest checkpoint message ID
-- Uses COALESCE(queue_seq, -1) to handle null queue_seq turns consistently
SELECT m.id FROM message m
JOIN turn t ON m.turn_id = t.id
WHERE m.conversation_id = :conversationId
  AND m.role = 'system'
  AND m.type = 'control'
  AND m.tags = 'checkpoint'
  AND m.archived = 0
ORDER BY COALESCE(t.queue_seq, -1) DESC, m.sequence DESC, m.created_at DESC
LIMIT 1
```

The result is bound to `checkpointId`. The transcript query must follow the existing
Datly pattern already used in this repository:

- define a query input
- attach `.WithPredicate(..., 'expr', ...)`
- let `predicate.Builder()` append it only when the input is present

This should mirror the existing `Since` predicate style in
`dql/agently/conversation/conversation.dql`, not introduce `@if` templating or a
separate hardcoded transcript query path.

Conceptually:

```dql
#define($_ = $CheckpointId<string>(query/checkpointId).WithPredicate(
  1,
  'expr',
  'EXISTS (
      SELECT 1
      FROM message a
      JOIN turn ta ON ta.id = a.turn_id
      WHERE a.id = ?
        AND (
          COALESCE(t.queue_seq, -1) > COALESCE(ta.queue_seq, -1)
          OR (t.id = ta.id AND m.sequence >= a.sequence)
        )
  )'
))
```

Then the normal transcript query continues to use:

```dql
${predicate.Builder().CombineOr($predicate.FilterGroup(1, "AND")).Build("WHERE")}
```

The important part is the mechanism:

- predicate is input-driven
- Datly appends it when `checkpointId` is present
- no alternate query path is required
- no `@if` blocks are needed

All anchor lookups remain PK-based through `a.id = ?`. When no checkpoint input is provided,
Datly emits no extra predicate and the query returns the full transcript.

#### Run pointer

After writing the checkpoint message row, update `run.checkpoint_message_id` to point to it.
`run.checkpoint_data` is demoted to an optional warm-resume cache — not authoritative.

The SDK should be able to:

- get transcript from the last checkpoint (ID-anchored two-step query — see above)
- get full transcript (existing path, unchanged)
- inspect checkpoint history (`WHERE role='system' AND type='control' AND tags='checkpoint' ORDER BY turn.queue_seq DESC`)
- follow `run.checkpoint_message_id` for per-run diagnostics only

Turn-local projection does not produce a checkpoint message. Only Prune and Compact write them.

## Tool and edit invariants

Context reduction must preserve runtime invariants.

Examples:

- if a prior tool output is hidden, the system must still know whether an async task exists or completed
- if a child agent output is hidden, the linked conversation ID must remain accessible for status checks

The supersession mechanism (MD5 of `toolName + canonicalJSON(args)`) ensures the newest
result for any tool call is always kept in the active context. No separate in-memory runtime
state tracking is needed:

- **Task status** — supersession keeps the newest `task/status {taskId}` result visible.
  If the model needs current status, it is already in the projected context.
- **Linked conversations** — `message.linked_conversation_id` is a database column.
  The runtime queries it directly when needed, independent of prompt projection.

No `RuntimeState` type is required.

## Turn timing

### Turn-start projection

Recommended default:

- one relevance projection pass at the beginning of the turn

Order:

1. load transcript truth (`History.Past` + current turn)
2. set `ContextProjection.Scope` from `Agent.Tool.CallExposure`
3. if `ProjectionConfig.TokenThreshold > 0` and current token use is below threshold, skip to step 6
4. build turn candidates from `History.Past` (eligible turns only — terminal status, not fully archived)
5. run selector model → call `message:project` tool → populate `ContextProjection.HiddenTurnIDs`
6. if `Scope == "conversation"`: apply supersession scan → add stale tool messages to `ContextProjection.HiddenMessageIDs`
7. build main model context from projected history
8. run normal ReAct loop

### Mid-turn projection

Mid-turn projection is not planned for initial phases. Remove it as a future concern rather
than leaving it as an underdefined placeholder.

The turn-start pass (step 5–6 above) already handles superseded tool outputs incrementally
via the supersession scan. A second mid-turn pass would only add value if tool outputs
during the ReAct loop themselves become large enough to threaten the context budget within
a single turn — that is an overflow concern handled by `overflow.go`, not a projection concern.

## Durable prune and compact

Durable maintenance should evolve from current `message:remove` and maintenance placeholders.

### Prune

Purpose:

- archive low-value old history
- preserve summary replacements
- modify default long-term active view

Implementation path:

- use existing candidate generation in `/Users/awitas/go/src/github.com/viant/agently-core/service/reactor/service_context_limit.go`
- improve `service/agent/maintenance.go` to call LLM selection and then `message:remove`

### Compact

Purpose:

- create a stronger summary handoff when the conversation is too large or old
- replace a large old history region with summary messages

Implementation path:

- produce summary message(s)
- mark replaced old messages archived or summarized
- emit checkpoint event

## Overflow integration

`overflow.go` is the natural home for request-time projection.

Current state:

- it already prunes oldest user messages from `History.Past`
- it is coarse and not replayable

Recommended evolution:

1. build a `ContextProjection` before final prompt build
2. replace oldest-first pruning with turn-based relevance projection where possible
3. keep fallback oldest-first pruning as emergency path
4. keep projection local to prompt binding; checkpoint only if needed for diagnostics

This gives Agently a real request-time context manager rather than just emergency pruning.

## UI and transcript behavior

UI should render from canonical state and distinguish:

- archived
- summary
- summarized
- hidden-for-turn
- checkpoint

Transcript query tools should be able to access:

- original hidden messages
- summaries
- checkpoint/boundary metadata

Model-facing active context should not automatically include all of that.

This gives:

- a clean prompt
- rich transcript inspection
- deterministic replay

## Implementation phases

### Phase 1: Make current semantics explicit

1. Define prompt-binding `ContextProjection` and durable `ContextCheckpoint` types.
2. Keep transcript as the source of truth; do not persist projection as transcript rows.
3. Keep `message:remove` as durable cleanup only.
4. Add checkpoint persistence for maintenance operations.
5. Add SDK ability to fetch transcript from the last checkpoint.
6. Validate DAO/Datly query paths for checkpoint-aware transcript reads on both MySQL and SQLite.

### Phase 2: Turn-start relevance projection

1. Add `ProjectionConfig` to agent/assistant config. Default `Enabled: false`.
2. Add `ProjectionSelectorInput` / `TurnCandidate` builder over `History.Past` turn IDs.
3. Use the model named in `ProjectionConfig.Model`; fall back to agent's default model if blank.
4. Use the prompt template in `ProjectionConfig.Prompt`; fall back to a built-in default if blank.
5. Implement the `message:project` tool with `ProjectInput` / `ProjectOutput`; tool uses turn ID as key.
6. Runtime expands each `TurnID` in `ProjectInput.TurnIDs` to `Turn.Messages` for that turn.
7. Integrate projection at turn start in `overflow.go` build path; populate `ContextProjection`.
8. Protect the most-recent `ProjectionConfig.ProtectedRecentTurns` turns and the current turn.
9. Skip projection entirely if total token use is below `ProjectionConfig.TokenThreshold`.

### Phase 3: Superseded-state reduction

1. Add `Cacheable bool` to `llm.ToolDefinition` (`genai/llm/tool.go`).
2. Set `Cacheable: true` on internal tools at registration (file reads, grep, status checks).
3. Add `ToolCacheOverride{ToolName, Cacheable}` to MCP server/binder config; apply after `ToolDefinitionFromMcpTool()`.
4. Implement supersession scan in projection builder:
   - walk all cacheable tool messages (current turn + `History.Past`) in chronological order
   - compute `supersessionKey(toolName, args)` per message
   - partition into `Turn` (current turn) and `History` (prior turns) buckets
   - within each bucket, group by key and suppress entries past the bucket's per-key cap (`Turn`, `History` from `Defaults.Projection.ToolCallSupersession.Limit`)
   - add suppressed message IDs to `ContextProjection.HiddenMessageIDs`
5. `canonicalArgsJSON` must recursively sort map keys (including nested `map[string]interface{}`) before marshalling for hash stability.

### Phase 4: Maintenance strategy

1. Add `Maintenance.Strategy` to `app/executor/config/default.go` workspace defaults; merge through `DefaultsWithFallback`. Default `archive`.
2. Define `RemoveStrategy` interface in `service/agent/maintenance` and inject the selected strategy at `message:remove` tool registration. Model surface stays a single tool.
3. Implement `ArchiveStrategy` (current `protocol/tool/service/message/remove.go` body — `SetArchived(1)` per affected message).
4. Implement `CheckpointStrategy` — writes summary message, then writes one checkpoint row (`role='system', type='control', tags='checkpoint'`, JSON `content` carrying `ContextCheckpoint{Op,AffectedIDs,SummaryIDs,...}`); does **not** set `archived = 1`.
5. Build a per-conversation hidden-set from checkpoint rows once per `GetConversation` call; apply alongside `IsArchived()` in the six read-path filters listed under "Maintenance strategy → Read-path implications".
6. Defer agent-level override (`Agent.Maintenance`); revisit only when a concrete use case justifies the mixed-history risk.
7. Implement `Prune` and `Compact` in `service/agent/maintenance.go` against the `RemoveStrategy` interface so both maintenance flows obey the strategy switch.

### Phase 5: Transcript-semantic query support

1. Make transcript query tools able to retrieve checkpoints and summaries.
2. Allow reasoning tools to inspect hidden/archived history when explicitly needed.
3. Keep active prompt context separate from transcript query results.

## DAO / Datly validation

This feature should not be considered complete until the data access layer and
Datly query path are validated for checkpoint-aware transcript loading.

### Required validation areas

1. **Transcript from last checkpoint**
   - step 1: find latest checkpoint `id` via `role='system' AND type='control' AND tags='checkpoint'`
   - step 2: `sequence >= (SELECT sequence FROM message WHERE id = :checkpointId)` — ID-anchored, not bare scalar
   - verify correct results through embedded client, HTTP client, and Datly-backed reads
   - verify null `:checkpointId` returns full transcript via Datly dynamic predicate

2. **Checkpoint message visibility**
   - verify `type='checkpoint'` rows appear in transcript truth queries
   - verify active-context prompt binding excludes them by default
   - verify transcript query tools can retrieve them explicitly

3. **Archived and summary interaction**
   - verify anchor-based transcript reads preserve summary messages
   - verify archived messages (`archived=1`) are excluded
   - verify canonical state construction is consistent before and after compaction

4. **SQLite parity**
   - verify the ID-anchored two-step query produces the same results on SQLite
   - verify the Datly dynamic predicate correctly omits the `sequence >=` clause when `:checkpointId` is null

5. **`run.checkpoint_message_id` scope**
   - verify it is used only for per-run diagnostics and audit
   - verify transcript slicing never depends on it being current
   - verify transcript reconstruction is correct when the pointer is absent or stale

### Recommended implementation checks

1. Add DAO-level tests for:
   - checkpoint ID lookup via `role='system' AND type='control' AND tags='checkpoint' ORDER BY sequence DESC LIMIT 1`
   - transcript slicing via ID-anchored subquery `sequence >= (SELECT sequence FROM message WHERE id = :checkpointId)`
   - fallback to full transcript when no checkpoint exists

2. Add Datly query validation for:
   - the `sequence >=` dynamic predicate on both MySQL and SQLite
   - `Since`/turn/message cursor interaction with checkpoint-based slicing

3. Add SQLite regression coverage for:
   - checkpoint message insertion (no schema migration required)
   - transcript reconstruction from checkpoint sequence
   - archived message exclusion after checkpoint slicing

4. Add end-to-end tests that cover:
   - Prune writes summary message then checkpoint message in the same operation
   - Compact writes summary message then checkpoint message in the same operation
   - SDK fetches transcript from last checkpoint correctly via `sequence >=` predicate
   - canonical state is identical across MySQL and SQLite

### Success criteria

The checkpoint feature is complete only when:

- transcript from last checkpoint works via ID-anchored two-step query through DAO and Datly
- MySQL and SQLite produce equivalent results including null fallback
- Prune and Compact always write both a summary message and a checkpoint message atomically
- canonical state built from checkpoint-sliced transcript matches full-transcript reduction rules
- no schema migration was required — checkpoint rows use existing `role`, `type`, `tags`, `status` values
- `run.checkpoint_message_id` is never on the critical path for transcript correctness

## Vocabulary

- `projection` — request-time active context shaping
- `supersession` — hide older outputs superseded by newer outputs
- `archive` — durable soft removal
- `compact` — durable summary replacement
- `checkpoint` — replayable maintenance event

## Final recommendation

The best design for Agently is:

- **Projection in prompt binding** for request-time relevance
- **Supersession** for old state/input reduction
- **Archive/Compact** for durable maintenance
- **Checkpoints** emitted by every durable maintenance operation for replay and transcript slicing
- **Explicit cacheable metadata** for tools, defaulting to non-cacheable

This gives Agently the hybrid target:

- Claude-grade context management
- Codex-grade coherence and replay
- without inheriting either system's accidental complexity
