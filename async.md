# Generic Async Tool Handling

## Goal

Add a generic async operation mechanism to `agently-core` that works for:

- internal shell execution
- internal child-agent delegation
- external MCP / Datly-backed services

without requiring every tool to invent its own polling loop or the LLM to manually keep calling status tools.

The design should:

- preserve the existing `tool.Registry.Execute(...)` execution boundary
- work with the current tool bundle model
- integrate with the canonical streaming event pipeline
- support both same-turn waiting and cross-turn background completion
- stay simple enough to configure for external MCP tools

This document proposes a minimal design centered on:

- explicit `start`, `status`, and optional `cancel` tools
- `exprPath`-only extraction for external tool payloads
- runtime-owned orchestration and polling
- dependency-based parent turn gating

## Non-goals

- Do not introduce a full selector DSL such as JSONPath/XPath/JMESPath.
- Do not force all existing synchronous tools to migrate.
- Do not move provider-specific stream parsing into the SDK.
- Do not require the LLM to implement polling loops as the primary control mechanism.

## Why this is needed

Today the codebase has three different patterns for long-running work:

1. Shell / command execution
2. Child-agent execution via `llm/agents`
3. External services that return "running" / "pending" style statuses, such as Datly forecasting

The current system already has useful primitives:

- `tool.Registry.Execute(...)` as the execution seam
- linked conversation support for child runs
- feed notification hooks after tool completion
- approval queue state
- canonical streaming events and SDK reducer

But it does not have one generic lifecycle for async operations. As a result:

- external tools rely on prompt-driven polling
- parent turn completion rules are implicit
- progress handling is tool-specific
- UI/state handling cannot rely on a single async operation model

## Design summary

Introduce a runtime-managed async operation layer with one common lifecycle:

- `started`
- `waiting`
- `running`
- `completed`
- `failed`
- `canceled`

The runtime owns:

- operation identity
- polling / waiting
- progress emission
- terminal result capture
- parent-turn gating

Tools remain normal tools. Async behavior is declared in tool bundle metadata and normalized by runtime.

For external tools, we use:

- explicit `*-start`
- explicit `*-status`
- optional `*-cancel`

and simple `exprPath` field extraction from result payloads.

## Core principle

Async is not a tool API. Async is a runtime lifecycle.

That means the LLM should not need to know how to poll. It should only see:

- initial async start acknowledgement
- progress updates when meaningful changes arrive
- final completion or failure

## High-level architecture

```text
model
  -> tool call
  -> tool registry execute
  -> async operation manager
      -> immediate result
      -> streaming progress
      -> deferred status polling
  -> canonical streaming events
  -> canonical reducer / transcript / UI
```

## Existing seams to build on

### Tool execution boundary

The existing public tool boundary is:

- [protocol/tool/registry.go](/Users/awitas/go/src/github.com/viant/agently-core/protocol/tool/registry.go)

This should remain the narrow execution seam.

### Concrete execution / persistence seam

Tool execution, transcript persistence, payload persistence, feed notification, and approval handling already converge in:

- [service/shared/executil/tool_executor.go](/Users/awitas/go/src/github.com/viant/agently-core/service/shared/executil/tool_executor.go)
- [service/shared/executil/tool_executor_context.go](/Users/awitas/go/src/github.com/viant/agently-core/service/shared/executil/tool_executor_context.go)

This is the best place to integrate async orchestration.

### Child-agent support

Child conversations and status plumbing already exist in:

- [protocol/tool/service/llm/agents/service.go](/Users/awitas/go/src/github.com/viant/agently-core/protocol/tool/service/llm/agents/service.go)
- [protocol/tool/service/llm/agents/run_support.go](/Users/awitas/go/src/github.com/viant/agently-core/protocol/tool/service/llm/agents/run_support.go)
- [service/linking/service.go](/Users/awitas/go/src/github.com/viant/agently-core/service/linking/service.go)

These should become one native async operation adapter.

### Canonical streaming + render model

The event / render path already exists:

- [runtime/streaming/event.go](/Users/awitas/go/src/github.com/viant/agently-core/runtime/streaming/event.go)
- [sdk/canonical.go](/Users/awitas/go/src/github.com/viant/agently-core/sdk/canonical.go)
- [sdk/canonical_reducer.go](/Users/awitas/go/src/github.com/viant/agently-core/sdk/canonical_reducer.go)
- [sdk/canonical_transcript.go](/Users/awitas/go/src/github.com/viant/agently-core/sdk/canonical_transcript.go)

This is where async progress should surface.

## Async operation model

## Operation identity

Every async operation needs durable identity:

```go
type OperationID string

type OperationKind string

const (
    OperationKindShell      OperationKind = "shell"
    OperationKindAgentRun   OperationKind = "agent_run"
    OperationKindExternal   OperationKind = "external"
)
```

## Dependency class

The runtime must know whether unresolved operations block parent completion:

```go
type OperationDependency string

const (
    DependencyRequiredForAnswer OperationDependency = "required_for_answer"
    DependencyBackground        OperationDependency = "background"
    DependencyVerification      OperationDependency = "verification"
    DependencyEnrichment        OperationDependency = "enrichment"
)
```

This is the key gating primitive. Without it, async runs become unsafe.

## Lifecycle state

```go
type OperationState string

const (
    OperationStarted   OperationState = "started"
    OperationWaiting   OperationState = "waiting"
    OperationRunning   OperationState = "running"
    OperationCompleted OperationState = "completed"
    OperationFailed    OperationState = "failed"
    OperationCanceled  OperationState = "canceled"
)
```

## Normalized update payload

```go
type OperationUpdate struct {
    ID          OperationID
    Kind        OperationKind
    State       OperationState
    Message     string
    PartialData any
    FinalData   any
    Error       string
    Percent     *int
    Terminal    bool
}
```

This is what the operation manager emits internally. It is then mapped into canonical streaming events.

## Tool model

Do not introduce a universal `start/wait/status/cancel` method on every tool implementation.

Instead, support explicit async-capable tool families, especially for external systems:

- `foo/start`
- `foo/status`
- `foo/cancel`

This keeps external MCP and Datly integrations simple and generic.

Internal tools can either:

- remain synchronous
- expose equivalent `start/status/cancel` service methods
- or be adapted natively by runtime without exposing all three model-visible methods

## Bundle / tool metadata extension

The best place to declare external async semantics is bundle match metadata, not prompt text.

Reason:

- bundle rules already group tool selection
- this metadata is runtime-owned, not model-owned
- multiple tools in one bundle may differ in async capability

### Proposed metadata

Extend match rules with optional `async` metadata:

```yaml
id: forecasting
title: Forecasting
match:
  - name: forecasting/start
    async:
      role: start
      dependency: required_for_answer
      operationIdPath: taskId
      statusPath: status

  - name: forecasting/status
    async:
      role: status
      operationIdArg: taskId
      statusPath: status
      progressPath: data.progress
      outputPath: data.result
      errorPath: error.message
      pollIntervalMs: 2000

  - name: forecasting/cancel
    async:
      role: cancel
      operationIdArg: taskId
```

This is intentionally constrained:

- no XPath
- no JSONPath
- no nested selector DSL
- only `exprPath` / dot-path extraction

This is sufficient for the target external systems and far easier to validate.

## Why `exprPath` is enough

The external target systems are expected to return structured JSON payloads.

What runtime needs from those payloads is small:

- operation id
- status
- progress summary
- final result
- error

Simple dot paths are enough for that and are much easier to support consistently than XPath or a general-purpose query language.

Examples:

- `taskId`
- `status`
- `data.progress`
- `data.result`
- `error.message`

## Native adapters vs selector-based adapters

The top-level runtime mechanism should be one thing.

But the implementation under it can differ by source.

### Native adapters

Used when runtime owns the lifecycle directly:

- shell execution
- linked child-agent execution

These do not need `exprPath`.

### Selector-based adapters

Used when external tools have fixed contracts:

- Datly / forecasting
- external MCP tools that already expose `start/status/cancel`

These use bundle metadata to extract normalized lifecycle fields.

The runtime operation manager should not care which adapter type produced an update.

## Runtime components to add

## Async operation manager

Add a runtime service, e.g.:

`service/shared/asyncops`

Responsibilities:

- register started operations
- drive internal poll loops for selector-based operations
- subscribe to native progress where available
- emit canonical events on meaningful change
- resolve terminal results
- support cancellation

### Conceptual API

```go
type Manager interface {
    Start(ctx context.Context, spec OperationSpec, startResult string) (*OperationRecord, error)
    Poll(ctx context.Context, id OperationID) (*OperationUpdate, error)
    Cancel(ctx context.Context, id OperationID) error
}
```

This API is runtime-internal, not model-facing.

## Operation spec

```go
type OperationSpec struct {
    ID               OperationID
    Kind             OperationKind
    Dependency       OperationDependency
    ParentConvID     string
    ParentTurnID     string
    ParentToolCallID string
    Source           OperationSource
}
```

Where `OperationSource` is either:

- native shell
- native linked conversation
- selector-based external tool

## Persistence

Need a durable operation row/table or equivalent state storage:

- operation id
- source tool name
- parent conversation / turn
- dependency class
- status
- created / updated timestamps
- latest partial payload
- final payload
- error

Without persistence, cross-turn async handling will be unreliable.

## Event model changes

The canonical event bus should gain explicit operation events or reuse tool-call lifecycle with richer semantics.

Recommended new events:

- `tool_call_started`
- `tool_call_delta`
- `tool_call_completed`
- `tool_call_failed`
- `tool_call_waiting`

If you want a more general name:

- `operation_started`
- `operation_progress`
- `operation_completed`
- `operation_failed`

But if you keep them under tool-call semantics, reuse the existing reducer/UI mental model.

## Parent turn gating

This is mandatory.

Before finalizing a parent turn:

- if any unresolved operation has dependency `required_for_answer`, do not finalize
- if any unresolved operation has dependency `verification`, do not allow success claims
- if unresolved operations are only `background` or `enrichment`, allow turn finalization

This should be enforced by runtime, not left to prompts.

## Same-turn wait vs cross-turn background

## Same-turn wait

Used when operation is required to answer truthfully.

Flow:

1. tool returns start response
2. runtime extracts `operationId`
3. runtime polls/subscribes
4. runtime injects progress updates
5. on terminal update, runtime appends final tool result into the same turn
6. ReAct loop continues

## Cross-turn background

Used when operation is not required for immediate answer.

Flow:

1. tool returns start response
2. runtime stores operation
3. parent turn may finish
4. runtime continues polling/subscribing
5. completion is surfaced later through:
   - linked conversation
   - inbox / notification
   - canonical event

## Mapping to the three target cases

## Shell

### Attached command

- operation source: native
- updates come from stdout/stderr stream directly
- completion from process exit

No selector paths needed.

### Detached command

- operation source: native
- start returns operation id
- runtime tails output / checks process state
- completion from process exit or cancellation

This is where shell becomes a first-class async operation.

## Child agent (`llm/agents`)

This already has the strongest foundation:

- linked child conversation creation
- status method
- child run outcome recovery

Needed changes:

- formalize child run as `OperationKindAgentRun`
- store dependency class
- map `status` to canonical async lifecycle
- gate parent turn completion based on dependency

## External Datly / forecasting

Today the forecasting agent prompt tells the model to poll repeatedly while status is `RUNNING`.

That should move into runtime metadata:

- `forecasting/start`
- `forecasting/status`
- optional `forecasting/cancel`

Bundle metadata handles:

- `operationIdPath`
- `statusPath`
- `progressPath`
- `outputPath`
- `errorPath`

The prompt should no longer encode manual polling loops once runtime support exists.

## Why prompt-driven polling is not enough

Prompt-driven polling causes:

- token waste
- loops
- brittle stopping logic
- difficulty enforcing parent-turn completion rules
- inconsistent UI progress

Runtime-driven orchestration fixes those issues while keeping the model informed through structured updates.

## Relationship to tool feeds

Tool feeds prove the architecture pattern already exists:

- tool executes
- output is inspected
- selector extracts meaningful data
- UI gets a normalized payload

See:

- [protocol/tool/feed.go](/Users/awitas/go/src/github.com/viant/agently-core/protocol/tool/feed.go)
- [protocol/tool/metadata.go](/Users/awitas/go/src/github.com/viant/agently-core/protocol/tool/metadata.go)
- selector application in [internal/tool/registry/registry.go](/Users/awitas/go/src/github.com/viant/agently-core/internal/tool/registry/registry.go)

The new async system should reuse the same philosophy:

- metadata declares extraction
- runtime performs extraction
- canonical events carry normalized state

## Relationship to approval queue

Approval queue already captures a non-terminal lifecycle:

- queued for user approval
- later approved or rejected

See:

- [protocol/tool/approval_queue.go](/Users/awitas/go/src/github.com/viant/agently-core/protocol/tool/approval_queue.go)
- [service/shared/executil/tool_executor.go](/Users/awitas/go/src/github.com/viant/agently-core/service/shared/executil/tool_executor.go)

That is another signal that the codebase already supports runtime-managed non-immediate tool outcomes.

Async operations should be treated similarly: status is runtime state, not prompt text.

## Minimum prerequisites before implementation

1. Define async operation persistence schema
2. Define canonical lifecycle states and dependency classes
3. Extend bundle/match metadata with minimal `async` config
4. Add runtime operation manager
5. Add event publication for operation progress / completion
6. Add parent turn completion gate
7. Migrate one internal case and one external case first

## Suggested incremental rollout

### Phase 1: Internal shell

Implement async operations for detached shell execution first.

Why:

- runtime already owns process lifecycle
- no selector extraction required
- easiest way to validate eventing and gating

### Phase 2: Child agents

Formalize linked child conversations as async operations.

Why:

- status and linkage already exist
- strong fit for dependency-based gating

### Phase 3: External forecasting

Replace prompt-driven polling with selector-based async orchestration for forecasting.

Why:

- validates the external metadata model
- demonstrates value on a real fixed-contract MCP/Datly workflow

## Open questions

1. Should `tool_call_waiting` be a first-class canonical event, or should `tool_call_started` + `status=waiting` be enough?
2. Should background operation completion be injected as a synthetic user/system message, or only as a stream event?
3. Should canceled required operations fail the turn automatically, or hand control back to the model?
4. Should operation updates be persisted verbatim, or only as canonical current state?

## Recommendation

Proceed with a single generic async operation mechanism using:

- explicit `start/status/cancel` tool triplets for external tools
- `exprPath` extraction only
- native adapters for internal shell and child-agent runs
- runtime-owned polling and event publication
- dependency-based parent turn gating

This is the minimum design that is:

- generic enough for external MCP services
- simple enough to configure
- aligned with the current `agently-core` execution seams
- compatible with the canonical streaming + SDK state architecture

## Comparison with Codex and Claude

This section summarizes what the two reference systems do well, and where
`agently-core` currently differs.

### Codex

Codex does not appear to expose a universal async tool API such as:

- `start`
- `wait`
- `status`
- `cancel`

Instead, it normalizes long-running activity into a strong event and item
lifecyle model.

Examples:

- `CommandExecution`
- `McpToolCall`
- `CollabToolCall`
- `TodoList`
- `WebSearch`

These are represented as:

- `item.started`
- `item.updated`
- `item.completed`

with stable item identities and typed payloads.

Relevant references:

- [Codex thread events](/Users/awitas/go/src/github.com/openai/codex/codex-rs/exec/src/exec_events.rs)
- [Codex protocol events](/Users/awitas/go/src/github.com/openai/codex/codex-rs/protocol/src/protocol.rs)

#### Best patterns to borrow from Codex

- Strong item lifecycle semantics
- Stable IDs on in-progress work
- Runtime/event ownership rather than prompt polling
- Clear separation between provider transport and product event model

#### Gap vs Codex

`agently-core` now has a good canonical event model, but it does not yet have
a generic runtime-managed async operation subsystem that unifies:

- shell
- child-agent runs
- external MCP / Datly async tools

Codex is stronger today in the consistency of how long-running work is surfaced
to runtime and UI.

### Claude

Claude is less formal than Codex at the public event contract level, but is
more sophisticated in practical async behavior:

- background subagents
- async shell/background tasks
- remote task polling
- task-specific status/result handles
- heavy prompt guidance around when to wait vs background

Relevant references from the reversed and source-like trees:

- [Reversed Agent tool implementation](/Users/awitas/go/src/github.com/antrophic/claude/reversed/cli.2.1.69.beautified.js:358772)
- [Source-like Agent tool](/Users/awitas/Downloads/claude_src/tools/AgentTool/AgentTool.tsx)
- [Source-like streaming parser](/Users/awitas/Downloads/claude_src/services/api/claude.ts)

Claude effectively supports:

- sync subagents
- async subagents
- detached/background shell execution
- progress updates and later completion notifications

but the logic is more subsystem-specific and more prompt-driven.

#### Best patterns to borrow from Claude

- Dependency-sensitive async behavior
- Parent does not always block on child completion
- Background work is first-class
- External/source-specific polling is acceptable when runtime owns it

#### Gap vs Claude

`agently-core` lacks the practical orchestration layer that decides:

- whether a child or tool should be backgrounded
- whether unresolved work blocks parent completion
- how later completion is reintegrated into parent state

Claude is stronger today in practical async orchestration, even though its
event model is less clean than Codex's.

### Summary of gaps in `agently-core`

Compared to both systems:

1. Missing one generic async operation manager
2. Missing dependency-based parent turn gating
3. Missing selector-based normalization for external async MCP tools
4. Missing unified lifecycle treatment across shell, child agents, and external tools

### Desired end state

The ideal `agently-core` architecture would combine:

- Codex-style event/lifecycle cleanliness
- Claude-style practical async orchestration

Concretely:

- one canonical event model
- one runtime async operation subsystem
- one parent turn gating policy
- one SDK render/replay path

That would put `agently-core` in a stronger position than either:

- Codex, which has stronger lifecycle modeling but no obvious generic async tool contract
- Claude, which has stronger practical async orchestration but less clean public event structure

## Three concrete use cases this mechanism must address

The proposed mechanism is not abstract infrastructure for its own sake. It
must solve three concrete classes of long-running work already present or
immediately needed in this codebase and deployment model.

### 1. Shell execution

Examples:

- local shell command that streams stdout/stderr while running
- detached shell command that may take minutes to finish
- remote shell execution over SSH

Why this needs async handling:

- output can change before terminal completion
- completion status may be unavailable for a long time
- users often want partial progress updates
- the parent agent sometimes needs the final result before answering, and
  sometimes only needs to acknowledge that work continues in the background

What the mechanism must support:

- attached mode: stream stdout/stderr as progress
- detached mode: register operation id and continue polling/waiting
- cancellation support
- dependency gating (`required_for_answer` vs `background`)

Why prompt-only handling is insufficient:

- LLM polling loops are expensive and brittle
- long-running commands should not consume reasoning turns just to check status
- progress should be surfaced even when no model turn is active

### 2. Child-agent delegation (`llm/agents`)

Examples:

- parent agent delegates repository analysis to a child conversation
- parent spawns a verification child
- parent launches a background research child whose answer may arrive later

Why this needs async handling:

- child runs already have linked conversation and status semantics
- some child runs are required before the parent can produce a truthful answer
- others are background/enrichment and should not block the parent turn

What the mechanism must support:

- same-turn synchronous waiting for child completion
- background child runs that outlive the parent turn
- durable linkage between parent turn and child conversation
- cross-turn reinjection of child completion/failure
- explicit dependency class for gating

Why current ad hoc handling is not enough:

- child status exists, but is still special-case logic
- no single runtime operation model spans child agents and other async tools
- parent finalization policy is not generic yet

### 3. External MCP / Datly async services

Examples:

- forecasting tools that return `RUNNING` and require follow-up polling
- MCP tools backed by external systems with fixed status/result contracts
- external jobs that expose a task id and later status checks

Why this needs async handling:

- the server contract is fixed and cannot be redesigned around our runtime
- prompts currently encode polling behavior, which is fragile and expensive
- external outputs are heterogeneous and need normalization into one internal lifecycle

What the mechanism must support:

- explicit `start`, `status`, and optional `cancel` tools
- extraction of operation id, status, progress, output, and error via `exprPath`
- runtime-owned polling and update deduplication
- same canonical event shape as internal async work

Why selector-based metadata is necessary here:

- we cannot require external MCP servers to emit our native async model
- runtime must extract lifecycle fields from arbitrary but structured JSON payloads
- prompts should stop being responsible for the polling loop once this exists

## Common success criteria across all three cases

The mechanism is complete only when all three cases support:

1. a stable operation identity
2. progress updates before terminal completion
3. runtime-owned waiting / polling
4. canonical event emission
5. dependency-based parent turn gating
6. UI replayability from stream and transcript

---

## How existing agentic systems address the async problem

This section gives a detailed breakdown of how production agentic systems handle
long-running tools, background work, child delegation, and parent-turn gating.
It is written to inform the design decisions above, not to advocate for any one
approach.

---

### Claude Code

**Source:** `/Users/awitas/Downloads/claude_src/` (reconstructed source tree)

#### Async tool execution model

Claude Code's tool execution is runtime-owned, not prompt-driven.
The `StreamingToolExecutor` (`services/tools/StreamingToolExecutor.ts`) begins
executing tools **before the model stream ends**, as tool-use blocks arrive
in the stream:

```typescript
// Tool starts executing as its block streams in — model still generating
streamingToolExecutor.addTool(toolBlock, message)
// Non-blocking poll: completed results yielded while stream continues
streamingToolExecutor.getCompletedResults()
```

This is parallel execution at the streaming layer, not queued after the
model finishes. The model call and tool execution overlap in time.

#### Concurrency model

Tools are classified as `isConcurrencySafe` or not. The executor enforces:

- Non-concurrent tools require exclusive access (no other tool running)
- Concurrent-safe tools run up to `CLAUDE_CODE_MAX_TOOL_USE_CONCURRENCY`
  (default 10) in parallel
- Results are yielded in **receipt order** regardless of completion order

Interrupt behavior is per-tool:
- `'cancel'` — killable when user presses ESC
- `'block'` — runs to completion; only Bash errors abort siblings

#### Parent-turn gating

The ReAct loop gates continuation on `needsFollowUp`:

```typescript
if (!needsFollowUp) {
  // no tool calls → check for errors, evaluate completion
} else {
  // tool calls present → inject results → next iteration
}
```

The parent turn does not complete while any tool call is unresolved. However,
Claude Code does not have an explicit `dependency` class concept (like
`required_for_answer` vs `background`). The implicit rule is: all tool calls
in the current model response must resolve before the next model call.

#### Subagent / background work

The `AgentTool` (`tools/AgentTool/AgentTool.tsx`) supports both:

- **Synchronous subagents:** spawn, wait, inject result into parent turn
- **Async/background subagents:** spawn without blocking parent turn

Background subagents are the closest Claude Code equivalent to
`DependencyBackground`. The parent turn can finalize, and the subagent
continues independently. Progress updates surface through a separate event
channel.

#### Recovery inside the loop (not surfaced to user)

Claude Code recovers from context overflow **within the same turn**:

| Error | Recovery action |
|---|---|
| Prompt-too-long (1st) | Collapse drain (cheap structural reduction) |
| Prompt-too-long (2nd) | Reactive compact (full LLM summarization) |
| Prompt-too-long (3rd) | Return terminal `prompt_too_long` |
| Max output tokens (1st) | Escalate 8k → 64k output cap |
| Max output tokens (2-3) | Add recovery message and retry |
| Max output tokens (4th) | Return terminal |

These are transparent to the user. The turn continues after recovery.

#### What Claude Code does well for async

- Streaming-parallel tool execution (runtime owns, not prompt)
- Per-tool interrupt semantics
- Multi-level recovery within the same turn
- Background subagent support with explicit dependency distinction
- Token budget tracking across iterations

#### What Claude Code does not do

- No explicit `start/status/cancel` contract for external tools
- No selector-based extraction for heterogeneous external payloads
- No durable async operation persistence across server restarts
- Cross-turn completion of background work is subsystem-specific, not generic

---

### OpenAI Codex

**Source:** `/Users/awitas/go/src/github.com/openai/codex/`

#### Async model

Codex normalizes long-running work into a typed item lifecycle:

```
item.started
item.updated
item.completed
```

with stable `itemId` on every event. Item types include:
`CommandExecution`, `McpToolCall`, `CollabToolCall`, `TodoList`, `WebSearch`.

This gives a clean event contract where the runtime and UI can track any
in-progress item by id regardless of type.

#### What Codex does well

- Unified lifecycle semantics across all item types
- Stable IDs on in-progress work
- Runtime/event ownership rather than prompt polling
- Clear separation between provider transport and product event model

#### What Codex does not have

- No explicit `required_for_answer` vs `background` dependency classes
- No external MCP polling contract (no `start/status/cancel` convention)
- No selector-based normalization for heterogeneous external payloads

---

### OpenAI Assistants API / Responses API

#### Async model

The Assistants API uses **runs** as the unit of async work:

1. Client creates a `run` on a thread
2. Polls `GET /threads/{id}/runs/{id}` until `status` is terminal
3. Retrieves messages after completion

Run statuses: `queued → in_progress → requires_action → completed | failed | canceled`

`requires_action` is the async gate: when a function call is needed, the run
pauses and the client must submit tool outputs before the run can continue.

#### Tool call handling

Tool calls are batched in a `submit_tool_outputs` step. The client is
responsible for executing all tools in the batch and submitting results. There
is no streaming parallel execution — the client does all work between
`requires_action` checkpoints.

The Responses API (2025) shifts to streaming events:
`response.created`, `response.function_call_arguments.delta`,
`response.function_call_arguments.done`, `response.completed`

#### What OpenAI does well

- Durable run identity (survives server restarts, cross-session)
- Clean `requires_action` gate for batched tool calls
- Run cancellation as a first-class operation
- Streaming events in the Responses API

#### What OpenAI does not do

- No runtime-owned polling — client must poll
- No per-tool dependency classes
- No background work that outlives the run
- External async services still require prompt-driven or client-driven polling

---

### LangGraph

LangGraph is a graph-based agent framework from LangChain.

#### Async model

LangGraph models agent execution as a directed graph with:

- **Nodes** — processing steps (model calls, tools, custom functions)
- **Edges** — conditional routing between nodes
- **State** — shared typed state object flowing through nodes
- **Checkpoints** — durable state snapshots at each node boundary

Long-running work is handled through:

1. **Interrupt points** (`interrupt_before`, `interrupt_after` on nodes):
   human-in-the-loop gates where execution pauses and state is checkpointed
2. **Subgraphs** — nested graphs that can run independently
3. **Background tasks** — nodes that spawn async tasks and store handles in state

#### Parent-turn gating

Explicit via graph structure: conditional edges route to a waiting node or
a terminal node based on whether all required results are in state.

The `interrupt_before` mechanism is the LangGraph equivalent of
`DependencyRequiredForAnswer` — execution cannot cross that node boundary
until the human resolves the interrupt.

#### What LangGraph does well

- Explicit dependency graph makes async gating visible and auditable
- Durable checkpointing at every node (survives restarts)
- Interrupt/resume as a first-class protocol
- State is typed and inspectable at every step

#### What LangGraph does not do

- No streaming-parallel tool execution
- No selector-based normalization for external async services
- Interrupt handling requires explicit graph design per use case
- Background subgraph completion reinjection requires custom routing

---

### AutoGen / AG2

AutoGen models multi-agent systems as a **conversation between agents**. Each
agent is an LLM with a system prompt and optional tool list.

#### Async model

AutoGen handles long-running work via:

1. **GroupChat** — multiple agents converse in a shared thread; a selector
   agent routes between agents based on output
2. **Nested chats** — an agent can trigger a nested conversation and inject
   its result back into the parent
3. **Tool termination** — agents return a termination signal when done

Background tasks are not natively supported. Long-running work either blocks
the agent conversation or is handled by returning an acknowledgement and
starting a separate group chat.

#### Parent-turn gating

Implicit: the conversation continues until a termination condition is met
(keyword, tool return, max turns). There is no explicit `dependency` class.

#### What AutoGen does well

- Simple model: agents converse, tools are just Python functions
- Nested chat provides a clean mechanism for child delegation
- GroupChat selector is an LLM-driven router

#### What AutoGen does not do

- No streaming parallel tool execution
- No durable async operation persistence
- No explicit parent-turn gating based on dependency class
- Background work requires application-level design outside the framework

---

### Semantic Kernel (Microsoft)

Semantic Kernel is a .NET/Python SDK for LLM orchestration with plugins.

#### Async model

Semantic Kernel uses a **process framework** (2024+) that models agentic work
as a graph of steps with typed events:

- Steps emit events to trigger other steps
- Steps can be async tasks
- The process runtime manages step execution and event routing

For tool calls, Semantic Kernel uses:

1. **Auto function calling** — model decides to call a function; SK executes and
   appends the result automatically (similar to Claude's runtime-owned execution)
2. **Function invocation filters** — middleware hooks before/after each function
   call (approvals, logging, retries)
3. **Streaming function calls** — results streamed as they arrive

#### Parent-turn gating

Handled by `FunctionChoiceBehavior`:
- `Auto` — model calls functions freely; loop continues until no more calls
- `Required` — model must call a specific function before responding
- `None` — no tool calls allowed this turn

There is no explicit background dependency concept. Long-running work blocks
the current process step or is moved to a separate parallel step.

#### What Semantic Kernel does well

- Function invocation filters for cross-cutting concerns (retry, audit)
- Process framework for explicit step graphs with typed events
- Strong .NET ecosystem integration

#### What Semantic Kernel does not do

- No streaming-parallel tool execution within a single model call
- No selector-based normalization for external async services
- Background steps require explicit process graph design

---

### Google Gemini Function Calling / Agent Builder

#### Async model

Gemini's function calling is synchronous: the model returns function calls, the
client executes them and submits results in the next turn. No streaming parallel
execution.

Google's **Agent Builder** (Vertex AI Agents) adds:

1. **Datastore tools** — managed async retrieval
2. **Extension tools** — REST API calls with optional async patterns
3. **Agent-to-agent delegation** — an agent can call another agent as a tool

Background tasks are not a first-class concept; the execution model is
request/response with tool call batches between.

#### Parent-turn gating

Implicit: the agent loop continues until the model returns a response without
function calls. Dependency classes do not exist; all tool calls in a batch must
complete before the next model call.

---

### Comparative summary

| System | Parallel tool exec | Runtime-owned polling | Dependency classes | Durable async state | External selector |
|---|---|---|---|---|---|
| **Claude Code** | ✅ streaming-parallel | ✅ runtime owns | ❌ implicit only | ❌ in-process only | ❌ no |
| **OpenAI Codex** | ✅ item lifecycle | ✅ runtime owns | ❌ no | ✅ checkpointed | ❌ no |
| **OpenAI Assistants** | ❌ batched | ❌ client polls | ❌ no | ✅ durable runs | ❌ no |
| **LangGraph** | ❌ sequential nodes | ✅ runtime owns | ✅ via graph structure | ✅ checkpointed | ❌ no |
| **AutoGen** | ❌ sequential | ❌ conversation-driven | ❌ implicit | ❌ no | ❌ no |
| **Semantic Kernel** | ❌ sequential | ✅ process runtime | ❌ no | ✅ process state | ❌ no |
| **Gemini** | ❌ batched | ❌ client-driven | ❌ no | ❌ no | ❌ no |
| **agently-core (target)** | ✅ (planned) | ✅ (planned) | ✅ (proposed) | ✅ (proposed) | ✅ (proposed) |

#### Key observations

1. **No system has all five properties.** Each makes tradeoffs.

2. **Claude Code is strongest at streaming-parallel execution** but lacks
   durable async state and external tool normalization.

3. **LangGraph is strongest at dependency gating** via graph structure but
   requires explicit design per use case and lacks parallel execution.

4. **OpenAI Assistants has the most durable async state** but requires client-
   side polling and has no dependency classes or background work.

5. **No system has selector-based normalization for external async services.**
   agently-core's `exprPath` metadata model is novel and addresses a gap none
   of the above fill generically.

6. **The combination of streaming-parallel execution + dependency classes +
   durable state + external selector** proposed in this document would put
   agently-core ahead of all reference systems in the completeness of its
   async tool handling.

#### What agently-core should borrow from each

| System | Borrow |
|---|---|
| Claude Code | Streaming-parallel tool execution; runtime-owned recovery within the turn |
| Codex | Typed item lifecycle events with stable IDs; clean event/transport separation |
| OpenAI Assistants | Durable run identity; explicit cancellation as a first-class operation |
| LangGraph | Explicit dependency gating; checkpointed state at operation boundaries |
| Semantic Kernel | Invocation filters (equivalent to approval hooks already in place) |
