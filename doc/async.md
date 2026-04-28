# Async Tool Mechanism

A single, universal mechanism for async tool operations across internal services (`llm/agents`, `system/exec`, skills) and external MCP tools. Built around two primitives — a server-side parking barrier and a stateless narrator — with tool-agnostic metadata (`AsyncConfig`) and a small set of Manager APIs.

This document is the canonical reference for both the design and the current implementation. When design and code disagree, the code is the source of truth and this document is the bug.

---

## 1. Goals and non-goals

### Goals

- One mechanism for every async tool. Internal services, MCP tools, and future additions use the same declaration shape and the same runtime machinery.
- The main LLM sees exactly one `tool_call` and one `tool_result` per parked status call, regardless of how long the underlying work runs. Transcript cost is flat in wait time.
- The end user sees live progress narration on the UI stream, driven by a separate stateless formatter that never touches the main transcript.
- Long-running ops (hours or days) are first-class. An activity-driven idle timer keeps the barrier parked as long as the underlying op produces any change; a wall-clock global timeout is a resource-cleanup safety net, not a main-LLM-responsiveness knob.
- The runtime — not the LLM — owns polling, terminal detection, and cleanup.

### Non-goals

- No full selector DSL. Dot-path extraction (`OperationIDPath`, `IntentPath`, `StatusPath`, etc.) is enough for every payload shape encountered in internal services, skills, and external MCP tools. Complexity is not worth it.
- No "reinforcement" loop. The earlier design re-prompted the main LLM every N seconds during a wait. That path is deleted — the barrier replaces it.
- No per-tool branching in the async mechanism itself. Tool-specific behavior lives on the tool, not on the async runtime.
- No real detach-with-completion-routing yet. The current `detach` mode is a gating bypass; full child-conversation ownership / result resurfacing is future work.
- No barrier completion policies (first-wins, quorum). All referenced ops must reach terminal-class or one must go idle for the barrier to release.

---

## 2. Core concepts

### `AsyncConfig`

Declared on a tool service (or in a tool bundle rule) to mark the tool as async-capable. Lives at [`protocol/async/config.go`](../protocol/async/config.go).

```go
type Config struct {
    Run                  RunConfig
    Status               StatusConfig
    Cancel               *CancelConfig
    DefaultExecutionMode string // "wait" (default) | "detach"
    TimeoutMs            int    // wall-clock hard ceiling; 0 disables
    PollIntervalMs       int
    IdleTimeoutMs        int    // per-op idle soft-release; 0 → DefaultIdleTimeoutMs (10m)
    Narration            string // "none" | "keydata" | "template" | "llm"
    NarrationTemplate    string
}
```

The triplet pattern (`Run` / `Status` / `Cancel`) maps to the three model-visible tools a typical async external service exposes. `ReuseRunArgs` on `StatusConfig` handles the `system/exec` pattern where status comes back on the same tool name.

**Rationale.** A triplet is the lowest common denominator across every external async system encountered in practice. Forcing a single "universal" tool interface would require adapters per tool; declaring three separate tool names + extraction paths is simpler and survives MCP's schema-first contract.

### `ExecutionMode`

Two values and no more:

- `wait` (default) — register the op, poll to terminal, park the status call on the barrier, emit narration.
- `detach` — register the op, return start response immediately, no poller, no barrier, no `TimeoutAt`. Record is reclaimed by GC after `DefaultGCMaxAge` idle.

Mode is resolved, in priority order:

1. `_agently.async.executionMode` on the request envelope (per-call override).
2. `RunConfig.ExecutionModePath` selector into the start-tool request args (config-declared).
3. `Config.DefaultExecutionMode` (default for this tool).
4. Fallback to `wait`.

**Rationale.** The earlier design had a `fork` value intended for child-conversation launches. It behaved identically to `wait` in this package and was never set by any production caller. The "launch a child and wait" concept belongs to the skill/agents layer, not to the async enum. Two values keeps the semantic space honest: the only question the async layer answers is "does the status call park?"

### `OperationRecord`

An in-memory entry in `Manager.ops`, created at registration, mutated on every status update, pruned by GC. Carries the op's identity, mode, wait/cancel tool bindings, current status/payload, idle/global timers, and a pending-change flag for subscribers. See [`protocol/async/manager.go`](../protocol/async/manager.go).

**Rationale.** No durable persistence for records. The conversation message store already captures everything the LLM needs to see in the transcript — the runtime's in-memory map is just the live view for the current process. Process restart discards in-flight records; parked status calls fail cleanly rather than rehydrate (restart rehydration is explicitly deferred — see §11).

---

## 3. Execution flow

### Wait-mode (the common case)

```
main LLM calls Run.Tool
  → tool executes synchronously; returns start response
  → async_tools.go intercepts: register op in Manager; skip poller

main LLM later calls Status.Tool (same turn or later)
  → async_tools.go intercepts: park on Manager.AwaitTerminal(ctx, opIDs)
  → maybeStartAsyncPoller spawns background poller (lazy)
  → poller ticks every PollIntervalMs: calls Status.Tool underneath,
     extracts payload via Selector, calls Manager.Update
  → Manager.Update fires ChangeEvent to subscribers

while parked:
  → narrator subscribes to Manager.Subscribe(opIDs), produces preambles
  → each preamble upserts a single interim assistant message
     (one per parked status call, updated in place)
  → EventTypeAssistantPreamble SSE event drives UI

barrier releases when:
  → all referenced ops terminal-class, OR
  → any referenced op's IdleTimeoutMs fires
  → AggregatedResult returned as tool_result to main LLM
```

### Detach-mode

```
main LLM calls Run.Tool with executionMode: detach
  → tool executes; returns start response
  → register op; no poller, no TimeoutAt, no barrier path
  → response returns to main LLM as a normal tool_result

later in the conversation:
  → system/async:list surfaces the op to the LLM (if still within GC window)
  → LLM can call Status.Tool, which DOES go through the barrier on re-attach
  → GC eventually prunes the record after DefaultGCMaxAge idle
```

**Rationale (lazy poller).** Starting a background poller at `Register` time would burn status-tool calls (and external API cost) on ops the LLM may never park on. Starting on the first status call — the moment the LLM commits to waiting — matches observed usage and saves work. The tradeoff: a wait-mode op that the LLM never polls is not hard-terminated by `TimeoutMs` (see §10 known properties).

---

## 4. Two timers

Only wait-mode ops carry timers.

| Timer | Default | Resets | Fires on | Effect |
|---|---|---|---|---|
| `IdleTimeoutMs` | 10 min (`DefaultIdleTimeoutMs`) | that op's MD5 payload change | soft release | Op still runs; barrier returns with `running_idle` reason; LLM can re-attach. |
| `TimeoutMs` | none (0 = disabled) | never — wall clock from op start | hard terminal | Cancel tool invoked; op transitions to `StateFailed` with `"operation timed out"`. |

Detach ops set neither. GC-age-based cleanup is their only reclamation path (§8).

**Carrier/tool-call semantics.** When `Status.Tool` returns a terminal payload,
the status tool call itself is considered **completed** even if the child op's
own state inside the payload is `failed` or `canceled`. Tool-call transport
success and child-operation outcome are separate contracts. The carrier tool
call is marked `failed` only when the status transport/poll itself fails and no
terminal payload was produced.

**`llm/agents` note.** Child-agent status timeout ownership lives in
`llm/agents:status` itself, not in the generic async wrapper timeout. That
service derives terminal failure from child conversation activity (`20m`
default, `5m` for `waiting_for_user`) and now also terminalizes the child
conversation/run state when it makes that decision.

**Rationale.** Two timers, two jobs:

- `IdleTimeoutMs` is the main-LLM-responsiveness guard. Activity-driven so a 24-hour job emitting hourly heartbeats keeps the barrier parked for the full 24 hours; only a truly stuck op hits it.
- `TimeoutMs` is the resource-cleanup safety net. Wall-clock absolute, sized generously. In practice most ops terminate long before it fires; it exists so no op can live forever in memory or leak external resources.

Collapsing the two into a single timer conflates "main LLM is waiting too long" with "op has been running too long" — different concerns, different units (elapsed since last change vs. elapsed since start), different correct responses (soft release + re-attach vs. hard terminate).

---

## 5. Narrator

Stateless formatter in [`protocol/async/narrator`](../protocol/async/narrator). Takes a `ChangeEvent` (or an `OperationRecord` at start), returns a preamble string or null. Four modes per `AsyncConfig.Narration`:

- `none` — silent.
- `keydata` — no narrator LLM involved. Surface the selector-extracted async
  update directly from `message` / `keyData`. This is the transparency-first
  mode used for child-agent wait flows when we want to show the sub-agent's own
  updates rather than paraphrasing them.
- `template` — substitutes `{{user_ask}}`, `{{intent}}`, `{{summary}}`, `{{message}}`, `{{status}}`, `{{tool}}` into `NarrationTemplate`. Deterministic, no LLM call.
- `llm` — cheap LLM runner pulled from context (`narrator.WithLLMRunner`). Produces a natural-language one-liner.

Fallback ladder (when template empty / LLM absent): `user_ask → intent → summary → message → tool + status → tool`.

### UI surface: one interim assistant message per parked status call

- First change event on a parked status call creates an assistant message with `Interim = 1` and the preamble/progress text.
- Subsequent change events update the **same** message id in place (via `PatchMessage`) and re-emit `EventTypeAssistantPreamble` with the unchanged `assistantMessageId`.
- The pairing `parkedToolCallId → assistantMessageId` is held in `toolstatus.PreamblePairing` for the barrier's lifetime and dropped on release. Multiple parked status calls in the same parent turn may intentionally resolve to the **same** interim assistant message id when the turn already owns a transient narration slot.
- The `Interim = 1` flag is already respected by every prompt-rebuild path ([`binding_history.go:598`](../service/agent/binding_history.go:598), [`summary.go:44`](../service/agent/summary.go:44), [`relevance.go:199`](../service/agent/relevance.go:199), [`agent_classifier.go:278`](../service/agent/agent_classifier.go:278)), so narration never re-inflates into the main LLM's context.
- This is a **single transient bubble slot per parked status call**. Narration
  updates replace each other in place; they do not create sibling assistant
  bubbles. When the real assistant response arrives for the parent turn, that
  response owns the visible bubble.

**Rationale.** Ephemerality is the load-bearing property. The existing preamble primitive (assistant message + interim flag) already gives us exactly that — transient for the model, visible for the user, stored for replay. Reusing it means no new event type, no new storage tier, and the firewall is schema-enforced rather than enforced by discipline. The narrator itself is pure (no tools, no polling, no state) so it cannot drift or hallucinate into dispatch behavior. `keydata` mode is the special case where we intentionally skip narrator-LLM involvement and surface the child/system update directly.

### Debouncing

The backend debounces change events per group (~3–5 s window) before invoking the narrator. At one tick per second the narrator would otherwise fire ~1.5×/sec across three ops; the coalesced invocation produces one preamble per window instead.

---

## 6. `Run.IntentPath` — op intent for the narrator

`AsyncConfig.Run.IntentPath` is a dot-path selector over the **start call's request args** (not the response). Extracted at `Register` time; stored on `OperationRecord.OperationIntent`; threaded to the narrator as the `intent` field.

### Grammar

```
path        := segment ( "." segment )*
segment     := map-key | array-index
map-key     := any non-empty string not containing "."
array-index := base-10 non-negative integer (used when the current node is an array)
```

### Evaluator

Reuses the existing `lookup()` in [`protocol/async/extract.go`](../protocol/async/extract.go). See the numbered rules in that file.

### Coercion

Resolved value → string: `nil → ""`, `string → as-is`, `fmt.Stringer → .String()`, numeric/boolean → `fmt.Sprint(v)`, map/slice → `""`. Then trim + collapse whitespace + truncate to 200 runes.

### Fallback

Empty `IntentPath`, resolution failure, or empty coerced string → fallback to the tool name. Never errors, never fails the start call.

**Rationale.** Same dot-path dialect already used by `OperationIDPath`, `StatusPath`, etc. Adding a new selector DSL for one more field would violate "no selector DSL" from the non-goals. The 200-rune cap keeps the narrator context bounded regardless of how verbose tool args are.

---

## 7. Manager API

### Op lifecycle

- `Register(ctx, input) (*OperationRecord, bool)` — create a new op record. Returns `existed=true` and logs at warn when an op with the same id already exists; the prior record is overwritten.
- `Update(ctx, input) (*OperationRecord, bool)` — apply a status update. Always refreshes `UpdatedAt`; second return is `changed` (whether any payload field actually differed). Meaningful changes fire change events to subscribers.
- `Get(ctx, id) (*OperationRecord, bool)` — snapshot of an op. Returned record is deep-cloned — nested `StatusArgs` / `RequestArgs` values are safe to mutate without affecting the canonical state.
- `ActiveOps(ctx, convID, turnID)`, `OperationsForTurn(ctx, convID, turnID)` — per-turn enumeration for internal gating paths.
- `ListOperations(Filter) []PendingOp` — tool-agnostic LLM-oriented enumeration. Non-terminal only. Backs `system/async:list`.
- `FindActiveByRequest(ctx, convID, turnID, toolName, requestArgsDigest)` — same-tool recall via request digest.

### Barrier / subscription

- `Subscribe(opIDs []string) (<-chan ChangeEvent, uint64)` — fan-out change stream. Returned channel closes when all targets terminal. The uint64 is a handle for `Unsubscribe`.
- `Unsubscribe(subID uint64)` — release a subscription early. Consumers that abandon the channel without all-terminal must call this or the subscription leaks and pins its op ids against GC.
- `AwaitTerminal(ctx, opIDs []string) <-chan AggregatedResult` — barrier waiter. Emits once when all targets reach terminal-class or any reaches idle. Ctx-cancellable; unsubscribes internally.

### Poller lifecycle

- `TryStartPoller(ctx, id) bool` — register a background poller slot. Returns false if one already exists.
- `FinishPoller(ctx, id)` — release the slot.
- `StorePollerCancel(ctx, id, cancel)` — associate a cancel fn so `CancelTurnPollers` can reach it.
- `CancelTurnPollers(ctx, convID, turnID)` — cancel every poller for the given turn.
- `RecordPollFailure(ctx, id, errMsg, transient) (*OperationRecord, bool)` — bump the fail counter; transition to `StateFailed` when the retry cap is hit.
- `ResetPollFailures(ctx, id)` — clear on successful poll.

### Gating (legacy path, still used)

- `HasActiveWaitOps(ctx, convID, turnID) bool`, `ActiveWaitOps(ctx, convID, turnID)` — the parent-turn's "is there wait-mode work outstanding" check. Used by the agent loop's completion gate.
- `ConsumeChanged(convID, turnID)`, `WaitForChange(ctx, convID, turnID)`, `WaitForNextPoll(ctx, convID, turnID)` — per-turn signalling for ops that don't use the subscription channel directly.
- `TerminalFailure(ctx, convID, turnID) (*OperationRecord, bool)` — find the first failed/canceled op in the turn.

### GC

- `Sweep(now, maxAge) int` — one-shot prune. Removes terminal or detach ops whose `UpdatedAt` is older than `maxAge` and that no subscription references. Returns count pruned.
- `StartGC(ctx, interval, maxAge)` — background ticker that calls `Sweep` until ctx cancels. Non-positive args fall back to `DefaultGCInterval` / `DefaultGCMaxAge`.

### Observability

- `Stats() ManagerStats` — lifetime counters (`RegisterCount`, `RegisterOverwriteCount`, `UpdateCount`, `UpdateChangedCount`, `SweepPruneCount`, `SubscribeCount`, `UnsubscribeCount`) and live gauges (`ActiveOps`, `ActiveSubscriptions`, `ActivePollers`). Counters are atomic; safe to call concurrently with any other operation.

### Shutdown

- `Close()` — synchronous teardown. Cancels every registered poller **and waits for each admitted poller goroutine to exit** (via an internal `sync.WaitGroup` tied to goroutine lifecycle), cancels and joins every `StartGC` sweeper, closes every subscription channel, wakes every `WaitForChange` / `WaitForNextPoll` waiter. Idempotent. When `Close` returns, no Manager-spawned or Manager-admitted goroutine is still running. Late `AdmitPoller` / `TryStartPoller` / `StartGC` calls short-circuit (return false / no-op).
- `AdmitPoller(ctx, id, cancel) bool` — atomic admission: checks `closed` + duplicate-id, stores the cancel func, and bumps the poller wait-group, all under `m.mu`. The cross-package poller launcher (`service/shared/toolexec.maybeStartAsyncPoller`) uses this so the cancel is registered in the same critical section as admission — closing the race where a concurrent `Close` fired between `TryStartPoller` and `StorePollerCancel` would have left the admitted poller with no registered cancel.
- `FinishPoller(ctx, id)` — idempotent deregistration. Only the first call (the one that actually removed the map entry) decrements the wait-group, so accidental double-`defer` doesn't underflow. `dropOpLocked` (invoked from `Sweep`) cancels the poller ctx but does NOT delete `m.pollers[id]` — that stays the goroutine's sole responsibility, keeping the wg aligned with goroutine lifecycle.

### Context accessors

- `WithManager(ctx, *Manager)`, `ManagerFromContext(ctx) (*Manager, bool)` — wire a Manager through request context ([`protocol/async/context.go`](../protocol/async/context.go)).

### Constants

- `DefaultIdleTimeoutMs` — 10 min, per-op idle soft-release threshold.
- `DefaultMaxPollFailures` — 3, consecutive status-call failures before transitioning to `errored` / `StateFailed`.
- `DefaultGCMaxAge` — 1 h, record prunable age.
- `DefaultGCInterval` — 5 min, sweep cadence.
- `DefaultPercentSignalThreshold` — 5, minimum percent delta to emit a change event.

---

## 8. GC — record cleanup

`Manager.ops` is an in-memory map. Without cleanup it would grow monotonically over the process lifetime, since terminal and detached ops are never otherwise removed.

### `Manager.Sweep(now, maxAge)`

Prunes records meeting **all three** criteria:

1. Terminal (success / failure / canceled) **OR** detach-mode (non-waits).
2. `now.Sub(rec.UpdatedAt) >= maxAge`.
3. No current subscription references the op id.

Wait-mode non-terminal ops are never pruned — they represent live work the barrier or a future status call is expected to observe.

### `Manager.StartGC(ctx, interval, maxAge)`

Runs `Sweep` in a background goroutine until `ctx` cancels. Both arguments MUST be positive — the `protocol/async` package holds no defaults of its own; when either is non-positive, `StartGC` returns without launching a goroutine. The authoritative defaults are seeded by the workspace loader (see below).

### Workspace configuration

Operator-tunable defaults live in the main workspace `config.yaml` under the existing `default:` section — there is no separate `default.async.yaml` file. This keeps all operator knobs in one place:

```yaml
default:
  model: openai_gpt-5_4
  embedder: openai_text
  agent: chatter
  async:
    gc:
      interval: 5m
      maxAge:   1h
    narrator:
      llmTimeout: 3s
      prompt: |
        Write one short progress preamble for an in-progress async operation.
        Respond with only the preamble text. If nothing meaningful changed,
        return an empty response.
```

All durations parse via `time.ParseDuration`. Any subfield the operator omits falls back to the embedded baseline (`workspace/config.defaultAsync*` constants) — a partial override like setting only `async.gc.interval` preserves `gc.maxAge`, `narrator.llmTimeout`, and `narrator.prompt` from the baseline. Parse errors surface loudly so operator typos fail at bootstrap.

**Single source of truth.** The embedded baseline in `workspace/config` is the only place these defaults are defined. `protocol/async` and `protocol/async/narrator` no longer carry `DefaultGCInterval` / `DefaultGCMaxAge` / `DefaultLLMTimeout` constants; if the workspace loader is bypassed, GC simply does not run and the narrator LLM runner executes with no timeout bound (ctx-driven only).

### Bootstrap flow

1. `workspace/config.Load` reads `<workspace_root>/config.yaml`.
2. `Root.DefaultsWithFallback` seeds the baseline (including `Async`), decodes the `default:` section over it, and returns `*execconfig.Defaults`.
3. `executor.Builder.Build` constructs the agent service (which owns a `Manager`) and then invokes:

   ```go
   if out.Defaults.Async != nil {
       _, _, _, err := out.Defaults.Async.Apply(ctx, out.Agent.AsyncManager())
       // ...
   }
   ```

4. `Apply` (in [`protocol/async/wsconfig`](../protocol/async/wsconfig)) parses the duration strings, calls `narrator.SetLLMTimeout(...)`, and — when both GC durations resolve positive — `manager.StartGC(ctx, interval, maxAge)`.

The narrator LLM runner in `service/agent/run_query.go` resolves its system prompt through a three-level ladder (lowest precedence to highest):

1. **Workspace default** — `default.async.narrator.prompt`
2. **Agent override** — `Agent.AsyncNarratorPrompt` (YAML: `asyncNarratorPrompt`)
3. **Active-skill override** — `Skill.Frontmatter.AsyncNarratorPrompt` (YAML frontmatter: `async-narrator-prompt`)

Each level overrides only when non-empty, so a missing field at any tier falls through to the next lower tier. The resolved source is emitted as LLM metadata `asyncNarratorPromptSource` (`workspace.default` | `agent` | `skill:<name>`) for debug trails. Empty at all three tiers fails the runner loudly (rather than inventing a default) so a misconfigured bootstrap surfaces at first use.

Example per-skill override in frontmatter:

```yaml
---
name: delivery-impact-check
description: Inspect delivery impact for an order.
async-narrator-prompt: |
  Write a crisp one-line progress update for a delivery-impact lookup.
  Mention the order and the dimension being checked. No filler phrases.
---
```

Example per-agent override:

```yaml
name: steward
asyncNarratorPrompt: |
  You are steward's narrator. Keep progress updates terse and audit-friendly.
```

The `wsconfig` package is kept in its own subpackage to avoid an import cycle (the narrator package itself imports `protocol/async`).

**Rationale.** GC handles two leak sources the design would otherwise create:

- Terminal ops accumulate because nothing else deletes them.
- Detach ops accumulate because no poller ever advances them to terminal.

Subscription-referenced ops are preserved because pruning them would strand a waiter. The pre-existing `allTargetsTerminalLocked` bug (treating missing records as non-terminal, which would have prevented subscription close on pruning) is fixed: a missing record is now treated as terminal inside that check, so sweeping a terminal op after its final event does not strand subscribers.

---

## 9. `system/async:list` — LLM-facing enumeration

New universal tool at [`protocol/tool/service/system/async`](../protocol/tool/service/system/async). Exposes the outstanding async ops in the current conversation so the LLM can discover and act on them without guessing identifiers.

### Input schema (LLM-facing)

```json
{ "tool": "llm/agents:start", "mode": "detach" }
```

Both fields optional. **No `conversationId`.** The runtime reads the conversation from trusted context and refuses to return anything if the turn context is absent — prevents prompt-injection or hallucination from leaking ops across conversations.

### Output

```json
{
  "ops": [
    {
      "operationId": "conv-abc",
      "tool": "llm/agents:start",
      "statusTool": "llm/agents:status",
      "operationIdArg": "conversationId",
      "executionMode": "wait",
      "state": "running",
      "intent": "Inspect the repository structure",
      "updatedAt": "2026-04-22T13:14:08Z"
    },
    {
      "operationId": "sess-123",
      "tool": "system/exec:execute",
      "statusTool": "system/exec:execute",
      "sameToolRecall": true,
      "statusArgs": { "sessionId": "sess-123", "action": "status" },
      "executionMode": "detach",
      "state": "running",
      "updatedAt": "2026-04-22T13:10:02Z"
    }
  ]
}
```

Each `PendingOp` carries a ready-to-send `statusArgs` map. The LLM's primary call path is:

```
invoke pendingOp.statusTool  with  pendingOp.statusArgs  verbatim
```

`statusArgs` always includes the op id under the correct arg name plus any `StatusConfig.ExtraArgs` the tool expects. `operationIdArg` and `sameToolRecall` are exposed for introspection (logging, debugging, LLM transparency) but the LLM does not need them to make a correct call — `statusArgs` alone is sufficient.

### Internal vs LLM-facing types

- `async.Filter` is **internal**. `ConversationID` is populated by the runtime from context, never by LLM input. Code paths that build a `Filter` must read the conversation from trusted context.
- `PendingOp` is safe to return to the LLM. Contains no cross-conversation identifiers.

### Why a tool and not a system-prompt injection

Prompt injection forces every turn to pay token cost for pending-ops, even when the LLM has no intent to check. A tool is zero-cost when unused, has clean boundaries, and is easy to test. The complementary prompt-injection path can land later if we find the LLM doesn't proactively call the tool; not needed for the first pass.

---

## 10. Known contract properties

Contract-level properties that reviewers and future implementers should know about explicitly — these are not gaps, they are deliberate design choices.

### 10.1 Lazy poller has a consequence for `TimeoutMs`

The poller starts on the first parked status call, not at `Register`. A wait-mode op whose LLM never calls status is not hard-terminated by `TimeoutMs` — it sits at `StateStarted` until GC reclaims it.

This is acceptable because:
- The normal flow (main LLM always issues status after start) is unaffected.
- GC reclaims the record anyway (default 1 h idle).
- The abandoned op's underlying work, if any, is the external system's concern.

A registration-time watcher that fires the timeout even without a poller is an open item if this becomes a real problem.

### 10.2 `Subscribe` drops events on buffer overflow

Channel is buffered at 16 with non-blocking send. Slow consumers lose events. This is intentional — both the narrator (debounced) and `AwaitTerminal` (re-evaluates on each delivered event and on idle timer fire) tolerate drops. Contract for any future consumer:

> Re-read op state on each delivered event. Do not treat absence of an event as absence of a change. `Subscribe` is a change-notification channel, not an ordered event log.

### 10.3 `Subscribe` close is the "all terminal" signal

Channel closes when every target op is terminal at publish time (or at `Subscribe` call time if already terminal). Consumers treat close as "all targets terminal — re-evaluate and stop waiting."

Invariant: **any code path that transitions an op to terminal must route through `publishChangeLocked`** (today, `Update` and `RecordPollFailure` both do). Violating this will hang `AwaitTerminal` waiters.

### 10.4 `AwaitTerminal` has a bounded startup race

`AwaitTerminal` evaluates state first, then subscribes. A change fired in that gap is not delivered on the subscription channel. The idle timer and subsequent changes trigger re-evaluation, so correctness is preserved — worst-case first-reevaluation latency is bounded by the next event or the idle threshold, not the lost change itself. If this ever matters in practice, flip order: subscribe first, then evaluate.

### 10.5 `running_idle` soft release does not stop the poller

When `IdleTimeoutMs` fires, the barrier releases and the parked status call returns. The op record is still non-terminal; the poller keeps polling; `TimeoutAt` enforcement continues; further `Update` calls fire change events. A subsequent status call re-attaches via a new `AwaitTerminal`.

### 10.6 Detach ops are bounded by GC age, not by `TimeoutMs`

By design. Runtime does not own the work lifetime of a detached op — that belongs to the external system or child conversation. Runtime bounds its own memory via GC: a detach record is reclaimed ~1 h after its last `UpdatedAt`.

### 10.7 Any `Update` call extends the GC window

`UpdatedAt` is refreshed on every `Manager.Update` call, regardless of whether any field actually changed. An LLM that polls a stable detach op via `system/async:list` + `StatusTool` — where the status response is identical every time — still keeps the record alive: each status-call-driven `Update` touches `UpdatedAt` even though `changed == false` and no change event fires. The GC window resets to "last LLM interaction."

`LastPayloadChangeAt` remains the anchor for change detection (idle timer, change digest) so identical responses do not spam subscribers — only `UpdatedAt` is the activity anchor for GC.

### 10.8 `Register` returns `(rec, existed bool)`

`Manager.Register` now returns both the created record and an `existed` flag. A `true` value means a prior op with the same id was overwritten and its state is gone. Overwrites are logged at warn (stderr via Go's `log` package) and counted in `Stats().RegisterOverwriteCount`. Callers that care (unit tests verifying single-registration, debug paths logging duplicate starts) can branch on the flag; the existing production caller in `async_tools.go` discards it since duplicate ids there indicate a bug in the start-tool's `OperationIDPath`.

### 10.9 Returned `StatusArgs` / `RequestArgs` are deep-cloned

`cloneMap` used to be a shallow copy — a consumer mutating a nested map inside `op.StatusArgs["opts"]` would mutate the Manager's canonical record. `Manager.Get`, `ListOperations`, and `Register` now deep-clone through `deepCloneMap` / `deepCloneValue`, which recurse into `map[string]interface{}` and `[]interface{}` and clone `json.RawMessage`. Primitive leaves and unknown reference types are returned as-is.

### 10.10 Stats() is a non-blocking debug surface

`Manager.Stats()` returns lifetime counters (Register, RegisterOverwrite, Update, UpdateChanged, Sweep prunes, Subscribe, Unsubscribe) and live gauges (ActiveOps, ActiveSubscriptions, ActivePollers). Counters are `atomic.Int64`; gauges take `m.mu` briefly. Not on any correctness path — intended for `expvar` / admin endpoints / log lines during incident response.

---

## 11. Deferred / out of scope

These are real open items that are explicitly **not** blockers for the current implementation:

- **Real detach implementation.** The current `detach` mode is a gating bypass — it prevents the barrier from engaging. A full detach feature (child-conversation ownership, detached result routing, completion resurfacing via inbox / notification) is a separate design.
- **Barrier completion policies.** First-event-wins, quorum-of-N, explicit cancel-on-any-failure — all deferred. Current semantics: release when all terminal or any idle fires.
- **Cancellation on user interrupt.** Mid-park teardown of barrier + narrator + ops when the user sends a new message requires explicit plumbing that doesn't exist yet.
- **Restart rehydration.** Parked status calls do not survive process bounces. Implementing this requires durable operation records plus a startup scan that reconnects barriers to their parked callers.
- **Nested async.** A parent op that parks on a child op which itself parks on grandchild ops — the recursion is not modeled explicitly.
- **Richer `user_ask` sourcing.** The current rule is "prefer intake title when present, else the original turn ask." A distilled-ask field filled by the main LLM is possible but not scoped.
- **Testing strategy, migration/rollback plan.** No rollout playbook. `Manager.Stats()` now exposes lifetime counters + live gauges (Register / Update / Sweep / Subscribe / active ops / subscriptions / pollers) for operator debugging; histogram-grade metrics (barrier hold time, narrator fire rate distributions) are still out of scope.

---

## 12. File pointers

| Concern | Path |
|---|---|
| Config types, `ExecutionMode` | [`protocol/async/config.go`](../protocol/async/config.go) |
| Manager, `OperationRecord`, `Filter`, `PendingOp`, subscriptions, GC | [`protocol/async/manager.go`](../protocol/async/manager.go) |
| Selector evaluator, `ExtractIntent` | [`protocol/async/extract.go`](../protocol/async/extract.go) |
| Narrator formatter + LLM runner wiring | [`protocol/async/narrator/`](../protocol/async/narrator/) |
| Debouncer and session helper | [`protocol/async/narrator/debounce.go`](../protocol/async/narrator/debounce.go), [`session.go`](../protocol/async/narrator/session.go) |
| Context accessors | [`protocol/async/context.go`](../protocol/async/context.go) |
| Parking, polling, narration pairing, registration | [`service/shared/toolexec/async_tools.go`](../service/shared/toolexec/async_tools.go) |
| Narration pairing (create-once + update-in-place) | [`service/tool/status/preamble_pairing.go`](../service/tool/status/preamble_pairing.go) via `toolstatus.NarrationPairing` |
| Workspace-config applier (`config.yaml default.async` → `StartGC` + `SetLLMTimeout`) | [`protocol/async/wsconfig/config.go`](../protocol/async/wsconfig/config.go) |
| Workspace baseline (single source of async defaults) | [`workspace/config/config.go`](../workspace/config/config.go) (`DefaultsWithFallback` + `defaultAsync*` constants) |
| `Defaults.Async` type alias + YAML binding | [`app/executor/config/default.go`](../app/executor/config/default.go) |
| Bootstrap wiring of `Apply` | [`app/executor/builder.go`](../app/executor/builder.go) (`Build` — after `out.Agent` is set) |
| Interim message firewall | [`service/agent/binding_history.go:598`](../service/agent/binding_history.go:598), [`summary.go:44`](../service/agent/summary.go:44), [`relevance.go:199`](../service/agent/relevance.go:199), [`agent_classifier.go:278`](../service/agent/agent_classifier.go:278) |
| SSE event type | [`runtime/streaming/event.go:36`](../runtime/streaming/event.go:36) |
| TS reducer for preamble | [`sdk/ts/src/chatStore/reducer.ts:727`](../sdk/ts/src/chatStore/reducer.ts:727) |
| `system/async:list` tool | [`protocol/tool/service/system/async/`](../protocol/tool/service/system/async/) |
| Tool registration in bootstrap | [`internal/tool/registry/registry.go`](../internal/tool/registry/registry.go) (search `system/async`) |

---

## 13. Deferred work

- **Manager sharding by conversation id.** Single `m.mu` serializes all Manager operations. Under load (many concurrent conversations × many concurrent ops per conversation), this is a ceiling. Proposed two-level approach: an RW-mutex guarded shard map `map[convID]*shard`, each shard with its own `sync.Mutex` + ops submap. Subscriptions would stay centralized (they may span ops across convs). `Sweep` and `ListOperations(Filter{})` would iterate shards acquiring each briefly. Not a current bottleneck; defer until benchmarks demand it. Touches every Manager method on implementation, so schedule it as its own atomic change with its own test plan.
