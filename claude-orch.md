# Claude Context Orchestration — Reference & agently-core Status

This document summarizes how Claude Code orchestrates context, prompts, and
model selection, and tracks what is implemented in `agently-core`.

---

## Executive summary

Claude does not treat "context management" as a single fallback summarizer.
It uses multiple coordinated layers:

- system-prompt assembly that changes by session state
- cheap housekeeping calls on a smaller model
- pre-request context reduction (`snip`, `microcompact`)
- provider-native context edits
- heavyweight compaction with structured handoff prompts
- post-compaction cache/state cleanup

**The key design idea**: context is budgeted in layers, and not all information
is treated equally.

---

## Implementation status in agently-core

| Mechanism | Claude Code | agently-core |
|---|---|---|
| Multi-section system prompt | ✅ runtime-assembled | ✅ `binding.go` assembles sections per turn |
| Small model for housekeeping | ✅ Haiku for naming, away summary | ✅ `summaryModel` config, `SummaryModel` in defaults |
| Auto-summarize title + summary | ✅ session naming | ✅ `autoSummarize` on agent, `service/agent/summary.go` |
| Immediate title from query | ✅ kebab-case via Haiku | ✅ set from query text at conversation creation (new) |
| Full compaction prompt | ✅ structured handoff | ✅ `service/agent/prompts/compact.md` (prompt exists, execution is placeholder) |
| Prune prompt | ✅ message selection | ✅ `service/agent/prompts/prune_prompt.md` (prompt exists, execution is placeholder) |
| Context recovery modes | ✅ multiple thresholds | ✅ `compact`, `pruneCompact` in `runtime/memory/context_recovery.go` |
| Context limit detection | ✅ threshold + circuit breaker | ✅ `ErrContextLimitExceeded` in `service/core/generate.go` |
| Microcompact / pre-request snip | ✅ content-class reduction | ❌ not implemented |
| Provider-native context edits | ✅ `clear_tool_uses`, `clear_thinking` | ❌ not implemented |
| Post-compaction cache invalidation | ✅ explicit cleanup pass | ❌ not implemented |
| Compact execution | ✅ implemented | ❌ placeholder in `service/agent/maintenance.go` |
| Prune execution | ✅ implemented | ❌ placeholder in `service/agent/maintenance.go` |

---

## Main prompt orchestration

### Claude Code

The main system prompt is assembled in `constants/prompts.ts:444`.
`getSystemPrompt(...)` returns a list of prompt sections, not a single string.

It composes:
- base intro / identity
- core system rules
- task behavior
- language and output style
- environment/model info
- memory prompt
- MCP server instructions
- scratchpad / function-result-clearing sections
- optional feature-gated sections (brief/token-budget guidance)

### agently-core

Equivalent assembly lives in `service/agent/binding.go` — system prompt is built
per turn from agent config, knowledge, resources, and conversation history.
MCP server-level instructions are injected via the MCP manager.

---

## MCP prompt extension

### Claude Code

Claude injects server-level MCP instructions into the system prompt via
`ConnectedMCPServer.instructions` (application convention, not MCP core spec).

### agently-core

MCP server metadata is available via the MCP manager. Server-level instruction
injection into the system prompt is not yet implemented as a first-class feature.

---

## Model selection

### Claude Code

Centralized in `utils/model/model.ts`:

- `getSmallFastModel()` — Haiku for cheap housekeeping
- `getMainLoopModel()` — main coding model
- Current defaults: Opus `claude-opus-4-6`, Sonnet `claude-sonnet-4-6`, Haiku `claude-haiku-4-5-20251001`

Housekeeping tasks that use Haiku explicitly:
- API key verification
- "while you were away" recap
- Session/topic naming
- Explore built-in agent

### agently-core

Model selection is per-agent via `modelRef` + finder. Two model slots relevant
to context management exist:

- `default.model` — main loop model
- `default.summaryModel` — used for `autoSummarize` title/summary generation
  and for `message` service chunked summarization

```yaml
# config.yaml
default:
  model: openai_gpt-5_4
  summaryModel: openai_gpt-5-mini
```

The `summaryModel` is the agently-core equivalent of Claude's Haiku routing for
lightweight generation tasks.

---

## Auto-summarize

### Claude Code

Conversation naming uses a minimal Haiku prompt:

```
Generate a short kebab-case name (2-4 words) that captures the main topic.
Return JSON with a "name" field.
```

### agently-core

The `autoSummarize` feature runs after a turn completes
(`service/agent/summary.go`). It uses `summaryModel` with the prompt at
`service/agent/prompts/summary.md`:

```
Given conversation history, and summary. Generate conversation title and summary.
The first line should contain a short, clear title.
After that, provide a bullet-point summary listing the main topics covered
in the conversation.

Example Output Format:

Title: Improving Go Memory Profiling with runtime.scanobject Insights

- Discussed high GC time spent in runtime.grayobject and scanobject
- Compared OS memory usage vs Go heap metrics
- Suggested using pprof and GODEBUG flags for deeper analysis
- Mentioned possible fragmentation and pointer-heavy structs
```

For A2A and turn-created conversations, the query text is also set as an
immediate title at conversation creation (before `autoSummarize` runs).

---

## Context recovery stack

### Claude Code

Request path applies compaction layers in order:

1. `snip` compaction (feature-gated)
2. `microcompact` — targeted content-class reduction
3. threshold / auto-compact check
4. full compaction with structured handoff prompt
5. post-compact cleanup
6. optional session-memory compaction

### agently-core

The recovery mode is set from agent config (`contextRecoveryMode`) and injected
into context via `memory.WithContextRecoveryMode`. Two modes:

- `compact` — invoke `Compact` (LLM summarization + archive)
- `pruneCompact` (default) — invoke `Prune` then `Compact`

Context limit is detected in `service/core/generate.go` via
`ErrContextLimitExceeded`. Recovery is triggered in the plan loop when this error
is seen.

**Current state**: the recovery modes are wired in `run_query.go` and prompts
exist, but `Compact` and `Prune` in `service/agent/maintenance.go` are
placeholders — the execution path is not implemented.

---

## Compaction prompt

### Claude Code

Heavyweight compaction prompt in `services/compact/prompt.ts`. Key traits:

- aggressive no-tools preamble
- `<analysis>` scratchpad
- `<summary>` structured output
- sections for task, files, errors, current work, next step

### agently-core

Full compaction prompt lives in `service/agent/prompts/compact.md`:

```
## Context Checkpoint Compaction — Knowledge Restoration Handoff

You are preparing a **handoff summary** for another LLM that must resume this
work **without loss of critical knowledge**.

Your goal is to preserve **decisions, facts, references, and implicit
assumptions** needed to continue accurately.

### Required Sections

1. **Current Progress & Decisions**
   - Work completed so far
   - Key decisions, assumptions, and rationale
   - Things explicitly ruled out or deferred

2. **Essential Context & Constraints**
   - Non-obvious background knowledge
   - User preferences, conventions, and hard constraints
   - Formatting, tone, or structural rules that must be followed

3. **Facts Required for Knowledge Restoration**
   - Domain facts the next LLM must *know* to proceed correctly
   - Definitions, mappings, constants, IDs, versions, or terminology
   - Canonical interpretations (e.g., "X means Y in this context")
   - Any information that would otherwise require re-discovery

4. **Critical References & Artifacts**
   - Prompts, schemas, examples, documents, or links
   - File names, APIs, tools, or identifiers already in use
   - Previously agreed-upon templates or structures

5. **Outstanding Work & Next Steps**
   - What remains to be done
   - Clear, ordered next actions
   - Known risks or decision points ahead

### Guidelines
- Be concise, factual, and explicit
- Prefer bullet points over prose
- Do **not** re-analyze or re-justify decisions
- Treat this as a **state snapshot**, not a discussion
- Optimize for fast restoration of context and intent
```

This is structurally equivalent to Claude's handoff prompt — it preserves
engineering state, not just conversation text.

---

## Prune prompt

### Claude Code

Auto-compact uses threshold + circuit breaker + feature gates. Context reduction
is done by content class (tool results, thinking blocks, etc.) before full
compaction.

### agently-core

Prune prompt lives in `service/agent/prompts/prune_prompt.md`. It receives the
overflow error, candidate message IDs, and constraints, and calls `message-remove`:

```
The last LLM call failed due to context overflow. Here is the exact error:
ERROR_MESSAGE: {{ERROR_MESSAGE}}

The whole conversation history is provided in this request. I prepared the main
CANDIDATES for removal:
{{CANDIDATES}}

**GOAL**
Use {{ERROR_MESSAGE}} to infer the model's max context and how far it was
exceeded. Remove and replace (via summaries) the least important messages from
conversation where {{CANDIDATES}} are preferred, so the remaining conversation
fits under the dynamic limit.

**TOOL USAGE RESTRICTION (CRITICAL)**
You **must use only one tool: `message-remove`**.

**SELECTION RULES**
Keep as long as possible:
- Most recent messages relevant to the current user task.
- System/developer instructions and guardrails.
- Tool calls/results that changed state or produced artifacts.
- Messages with code/config/IDs/paths/URLs referenced later.

Remove:
- Acknowledgements/small talk/thanks and repeated explanations.
- Obsolete tool logs or large raw payloads if summarized or superseded later.
- Older/off-topic content unrelated to the current task.

**TARGET REMOVAL COUNT**
- Select between {{REMOVE_MIN}} and {{REMOVE_MAX}} message IDs to remove.

**SUMMARY REQUIREMENTS (for each tuple)**
- ≤ 500 characters, single paragraph, plain text.
- Capture only essentials: purpose → action → key outcome(s).
- Include critical IDs/paths/commands/URLs verbatim if short.
```

---

## What is not yet implemented in agently-core

### Microcompact / pre-request snip

Claude's `microcompact` does targeted content reduction **before** an API call:

- identify compactable tool results by content class
- clear old tool-result content selectively
- preserve API structural invariants
- time-based microcompact passes

agently-core has no equivalent. Every request sends the full conversation history.

### Provider-native context edits

Claude supports `clear_tool_uses_20251015`, `clear_thinking_20251015` — API-level
context edits delegated to the provider. agently-core does not support this.

### Post-compaction cleanup

Claude explicitly invalidates caches after compaction:
- microcompact state
- classifier approvals
- session message cache
- prompt section state

agently-core has `maintenanceGuards` (concurrent operation locks) but no
post-compaction cache invalidation pass.

### Compact and Prune execution

Both `Compact` and `Prune` in `service/agent/maintenance.go` are currently
placeholder stubs. The prompts are ready; the execution pipeline is not wired.

---

## Practical takeaways

Claude's orchestration has three especially relevant ideas:

1. **Use a smaller model for cheap housekeeping**
   - session naming, away summary, API verification, lightweight classification
   - agently-core: `summaryModel` config is the right hook — extend it to title
     generation, routing classification, and any other non-critical LLM calls

2. **Do not rely on one compaction mechanism**
   - cheap structural reduction first (microcompact equivalent)
   - provider-native edits when possible
   - full summarization only when necessary
   - agently-core: prompts for all three tiers exist; execution needs wiring

3. **Treat compaction as a lifecycle event**
   - compaction changes caches, prompt state, and continuity assumptions
   - cleanup is part of the feature, not an afterthought
   - agently-core: post-compaction invalidation hooks are missing

**Most important lesson**:

> Claude orchestrates context with multiple coordinated mechanisms, not a single
> compact operation. The layering — snip → micro → full → cleanup — is what
> makes it reliable at long context lengths.

---

## ReAct Loop Deep Dive

### Structure

Claude's ReAct loop (`query.ts:241`) is a **generator function** over an
infinite `while (true)` (line 307) driven by an explicit `State` object:

```typescript
interface State {
  messages: Message[]
  toolUseContext: ToolUseContext
  turnCount: number
  maxOutputTokensRecoveryCount: number
  hasAttemptedReactiveCompact: boolean
  transition?: Continue          // reason for reaching this iteration
  autoCompactTracking?: AutoCompactTrackingState
  pendingToolUseSummary?: Promise
  stopHookActive?: boolean
}
```

One loop iteration maps to one full ReAct cycle: Reason → Act → inject results → Reason again.

### Phase 1 — Reasoning (model streaming)

Entry: `query.ts:659`

```typescript
for await (const message of deps.callModel({ ... })) {
  // process streaming deltas
}
```

- Assistant messages buffered in `assistantMessages[]`
- Tool-use blocks extracted into `toolUseBlocks[]` as they arrive
- `needsFollowUp = true` when any tool block is seen
- Certain errors are **withheld** from yielding until post-stream recovery:
  `prompt_too_long`, `max_output_tokens`, media-size errors

Key: while the model is **still streaming**, tool execution begins in parallel
(streaming tool executor). Tool results are yielded as they complete, not after
the stream ends.

### Phase 2 — Tool execution

Entry: `query.ts:1363`

Two execution paths controlled by feature gate:

**Streaming executor** (`services/tools/StreamingToolExecutor.ts`):
- Starts tools as tool-use blocks arrive in the stream
- Respects `isConcurrencySafe` per tool
- Non-concurrent tools: exclusive access (no other tools may run simultaneously)
- Concurrent-safe tools: up to `CLAUDE_CODE_MAX_TOOL_USE_CONCURRENCY` (default 10) in parallel
- Results yielded in receipt order

**Legacy executor** (`services/tools/toolOrchestration.ts`):
- Partitions tool list into batches — each batch is either one serial tool
  or N concurrent-safe tools
- Batches run with `Promise.all`; context modifiers applied after each batch

### Phase 3 — Reasoning completion check

Entry: `query.ts:1062`

```typescript
if (!needsFollowUp) {
  // model finished without tool calls
  // run recovery / exit checks
} else {
  // tool calls present — continue to next iteration
}
```

Recovery ladder (no tools path):

| Error | Recovery | Guard |
|---|---|---|
| Prompt-too-long (first) | collapse drain | `transition.reason !== 'collapse_drain_retry'` |
| Prompt-too-long (second) | reactive compact | `!hasAttemptedReactiveCompact` |
| Prompt-too-long (third) | return terminal `prompt_too_long` | — |
| Max output tokens (first) | escalate 8k → 64k cap | `maxOutputTokensRecoveryCount < 1` |
| Max output tokens (2-3) | add recovery message + retry | count < `MAX_OUTPUT_TOKENS_RECOVERY_LIMIT` (3) |
| Image size error | return terminal `image_error` | — |

### Phase 4 — Continuation decision

Entry: `query.ts:1705`

```typescript
if (maxTurns && nextTurnCount > maxTurns) {
  // yield max_turns_reached attachment
  return { reason: 'max_turns', turnCount: nextTurnCount }
}
state = { messages: [...prevMessages, ...assistantMessages, ...toolResults], turnCount: nextTurnCount, ... }
// loop back to line 307
```

### All terminal exit reasons

```
completed               — normal end, no tool calls, no errors
max_turns               — turnCount > maxTurns
aborted_streaming       — user interrupted during model stream
aborted_tools           — user interrupted during tool execution
prompt_too_long         — recovery exhausted
image_error             — media size error, not retried
model_error             — crash
stop_hook_prevented     — stop hook blocked continuation
hook_stopped            — hook explicitly stopped the loop
```

### All continue transition reasons

```
next_turn               — normal next iteration (has tool calls)
collapse_drain_retry    — PTL, first recovery (drain)
reactive_compact_retry  — PTL, second recovery (full compact)
max_output_tokens_escalate  — output too long, bump cap
max_output_tokens_recovery  — output too long, add recovery msg
stop_hook_blocking      — stop hook running (blocking)
token_budget_continuation   — within token budget, continue
```

### Interruption handling

```typescript
// During streaming (query.ts:1015)
if (abortController.signal.aborted) {
  // consume remaining tool results (generates synthetic 'cancelled' results)
  return { reason: 'aborted_streaming' }
}

// During tool execution (query.ts:1484)
if (abortController.signal.aborted) {
  // submit-interrupt: skip interruption message
  // ESC/other: add interruption message
  return { reason: 'aborted_tools' }
}
```

Tools define their own `interruptBehavior()`:
- `'cancel'` — killable on user interrupt
- `'block'` — runs to completion regardless of interrupt
  (only Bash aborts siblings; others fail independently)

### Proactive compaction (before model call)

Runs at the top of each iteration, before the model call:

```
1. snip compaction        — feature-gated, cheap history reduction
2. microcompact           — content-class reduction (clear old tool results)
3. context collapse       — read-time projection (feature-gated)
```

Reactive compaction only fires mid-loop on PTL failure (not proactively).

### Token budget

`query/tokenBudget.ts`:

- `COMPLETION_THRESHOLD = 0.9` — stop when 90% of budget consumed
- `DIMINISHING_THRESHOLD = 500` — stop if delta < 500 tokens after 3+ continuations
- Subagents stop immediately on budget expiry (no continuation)
- Main agent continues with `reason: 'token_budget_continuation'`

### agently-core equivalent

agently-core's plan loop lives in `service/agent/run_query.go`. Comparison:

| Claude Code | agently-core |
|---|---|
| `while (true)` with State | `runPlanLoop` iterating model calls |
| `needsFollowUp` flag | tool calls in response trigger next iteration |
| `maxTurns` param | `MaxIterations` in run record |
| Streaming tool executor | `ExecuteToolStep` per tool call |
| Concurrent tools via `isConcurrencySafe` | `ParallelToolCalls` agent config |
| PTL → collapse drain → reactive compact | `ErrContextLimitExceeded` → `ContextRecoveryMode` (placeholder) |
| `reason: 'completed'` | turn finalized with `succeeded` status |
| `abortController.signal` | `cancelReg` + context cancellation |
| `maxOutputTokensRecovery` | no equivalent — model errors bubble up |
| Withheld errors + post-stream recovery | errors returned from model call, handled in loop |

**Key gap**: Claude's loop handles PTL/max-tokens recovery **within the same
turn** without surfacing to the user. agently-core's PTL recovery
(`Compact`/`Prune`) is triggered but not yet executed — the conversation fails
rather than recovering transparently.
