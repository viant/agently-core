# Autonomous System — Implementation Plan

Companion to [autonomous.md](autonomous.md). The proposal doc decides *what*
the autonomous system is; this doc decides *where it goes in the codebase*,
*how it is phased*, and *what each implementation slice contains*.

This plan aims to combine:

- Codex-style **durable goal state + runtime accounting + idle continuation**
- Agently-style **dynamic follow-up execution + queued turn UX + steering**

without collapsing them into a single overloaded concept.

---

## Scope

The target end state is:

- one durable goal per conversation
- model-visible goal tools so the model can explicitly say `complete` or
  `blocked`
- runtime accounting of token/time usage while the goal is active
- a controller that enqueues autonomous continuation only when the conversation
  is idle and policy allows it
- existing follow-up chains stay intact and run on their current path
- existing queue UX remains the user-visible override layer

This document is intentionally implementation-first:

- concrete package/file targets
- schema and API additions
- runtime hook points
- UI data ownership
- phased PR plan

---

## Design Rules

The implementation must preserve these boundaries:

- **Goal** = durable objective state
- **Goal tools** = model-visible state transitions
- **Goal runtime** = lifecycle event dispatcher
- **Goal continuation** = idle-time scheduling policy
- **Chain** = post-turn follow-up execution primitive
- **Queue** = user-visible next-step override surface

Non-negotiable constraints:

- do not store goal truth in scratchpad
- do not store goal truth in `ChainContext.Context`
- do not reconstruct controller state from transcript heuristics
- do not make the controller choose between "chain vs follow-up" as if they
  were equivalent actions
- do not emit synthetic visible user bubbles for continuation prompts

### Workspace feature policy

Workspace-level feature gates are required. Platform support alone must not
make a capability available everywhere.

Recommended config shape:

```yaml
features:
  goals:
    enabled: true
    modelToolsEnabled: true
    uiEnabled: true
  followups:
    enabled: true
    chainsEnabled: true
    inlineEnabled: false
  controller:
    enabled: true
    autonomousContinuationEnabled: true
  queue:
    enabled: true
    uiEnabled: true
  steering:
    enabled: true
    forceEnabled: true
  wakeups:
    enabled: false
    modelToolsEnabled: false
    maxGlobalWakeupsPerHour: 5
    maxConversationWakeups: 3
    maxGoalWakeups: 2
    minWakeDelaySeconds: 60
    maxWakeDelaySeconds: 3600
```

Precedence rule:

- workspace config is the upper bound
- agent config, turn input, and UI preferences may narrow
- nothing below workspace level may broaden a disabled capability
- the same rule applies to delayed wakeups: the model can see the effective
  limit, but it can never raise it

---

## Target Package Layout

### New persistence package

```text
pkg/agently/goal/
pkg/agently/goal/read/
pkg/agently/goal/write/
```

This mirrors the current `conversation`, `turn`, `run`, `toolcall`, and
`turnqueue` structure already used by the Datly-generated persistence layer.

### New runtime package

```text
service/goal/
service/goal/runtime.go
service/goal/accounting.go
service/goal/continuation.go
service/goal/types.go
service/goal/store.go
```

Why `service/goal/` and not `service/controller/`:

- the new behavior is centered on the durable goal lifecycle
- "controller" is a policy role inside this package, not a better top-level
  ownership name than `goal`
- it stays parallel to `service/planner`, `service/intake`, `service/elicitation`

### New system tool package

```text
protocol/tool/service/system/goal/
protocol/tool/service/system/goal/doc/
```

This fits the current internal system-tool layout:

- `system/exec`
- `system/patch`
- `system/os`
- `system/image`
- `system/async`

### Existing files that will be touched

Backend:

- `sdk/handler.go`
- `service/agent/run_query.go`
- `service/agent/query.go`
- `service/agent/chain.go`
- `service/agent/agent.go`
- `service/agent/run_turn.go`
- `service/shared/toolexec/*` only if hidden controller-context needs execution
  flags
- `app/store/conversation/*` if canonical conversation projections need goal
  state exposure

Frontend / `agently`:

- `ui/src/services/chatService.js`
- `ui/src/services/chatRuntime.js`
- `ui/src/services/renderRows.js`
- `ui/src/components/chat/SteerQueue.jsx`
- `metadata/window/chat/new/*/dialog/panel/settings.yaml`
- `metadata/window/chat/new/*/dialog/settings.yaml`
- `metadata/window/chat/new/*/main.yaml`

Schema / DB:

- `script/schema.ddl`
- `script/mysql/schema.ddl`
- versioned migration files

---

## Data Model

### Goal row

Add a new logical table:

```sql
goal (
  id                TEXT / VARCHAR(...) PRIMARY KEY,
  conversation_id   TEXT / VARCHAR(...) NOT NULL UNIQUE,
  objective         TEXT NOT NULL,
  status            TEXT NOT NULL,
  status_reason     TEXT NULL,
  pause_reason      TEXT NULL,
  controller_spec   JSON / TEXT NULL,
  token_budget      BIGINT NULL,
  tokens_used       BIGINT NOT NULL DEFAULT 0,
  time_used_seconds BIGINT NOT NULL DEFAULT 0,
  created_at        TIMESTAMP NOT NULL,
  updated_at        TIMESTAMP NOT NULL
)
```

Indexes:

- hard uniqueness on `conversation_id`
- read by conversation id
- optional status index if future list/admin views need it

Allowed statuses:

- `active`
- `paused`
- `blocked`
- `complete`
- `budget_limited`
- `usage_limited`

### Queue extension

Controller-owned queued turns must be identifiable. Prefer extending queue or
turn projection rows with:

- `origin`
- `goal_id`
- `status_reason`

If the durable source of queued turns is the `turn` row, the easiest v1 path is
to persist these as new columns on `turn` or to attach them through queue view
joins. Do not keep them client-local only.

Suggested enum:

```go
type TurnOrigin string

const (
    TurnOriginUser       TurnOrigin = "user"
    TurnOriginSteer      TurnOrigin = "steer"
    TurnOriginController TurnOrigin = "controller"
    TurnOriginChain      TurnOrigin = "chain"
)
```

### Future wake request row

If delayed wakeups are added later, they need their own durable row rather than
being encoded as hidden prompt state.

Suggested shape:

```sql
goal_wake (
  id                   TEXT / VARCHAR(...) PRIMARY KEY,
  conversation_id      TEXT / VARCHAR(...) NOT NULL,
  goal_id              TEXT / VARCHAR(...) NOT NULL,
  reason               TEXT NOT NULL,
  wake_at              TIMESTAMP NOT NULL,
  wake_count           BIGINT NOT NULL DEFAULT 0,
  max_wake_count       BIGINT NOT NULL DEFAULT 1,
  cancel_on_user_input BOOLEAN NOT NULL DEFAULT TRUE,
  created_at           TIMESTAMP NOT NULL,
  updated_at           TIMESTAMP NOT NULL
)
```

This should be added only when wakeups are actually implemented, but the design
should assume durable wake state from the start.

### Goal store contract

Do not expose generated Datly types directly from the runtime service. Wrap
them behind a stable store contract:

```go
type Store interface {
    GetGoal(ctx context.Context, conversationID string) (*Goal, error)
    InsertGoal(ctx context.Context, in *InsertGoalInput) (*Goal, error)
    UpdateGoal(ctx context.Context, in *UpdateGoalInput) (*Goal, error)
    ClearGoal(ctx context.Context, conversationID string) error
    AccountUsage(ctx context.Context, in *AccountGoalUsageInput) (*Goal, error)
}
```

Why separate `InsertGoal` and `UpdateGoal`:

- `InsertGoal` fails if one exists
- `UpdateGoal` changes status/budget/objective on the existing logical goal

This matches the current simplified implementation: one goal per conversation,
created once, updated in place, and cleared when removed.

### Persisted goal package — Datly shape

The persisted goal should follow the same Datly-backed package pattern already
used by:

- `pkg/agently/conversation/*`
- `pkg/agently/turn/*`
- `pkg/agently/run/*`
- `pkg/agently/toolcall/*`

Recommended package layout:

```text
pkg/agently/goal/
  goal/
    goal.sql
  read/
    input.go
    sql/
      by_conversation_id.sql
  write/
    constructors.go
    goal.go
    input.go
    input_init.go
    sql/
      cur_ids.sql
      cur_goal.sql
```

Recommended `write/input.go` shape:

```go
package write

import "embed"

//go:embed sql/*.sql
var FS embed.FS

type Input struct {
    Goals []*MutableGoalView `parameter:",kind=body,in=data"`

    CurIDs *struct{ Values []string } `parameter:",kind=param,in=Goals,dataType=goal/write.MutableGoalViews" codec:"structql,uri=sql/cur_ids.sql"`

    Cur []*MutableGoalView `parameter:",kind=view,in=Cur" view:"Cur" sql:"uri=sql/cur_goal.sql"`

    CurByID map[string]*MutableGoalView
}

func (i *Input) EmbedFS() (fs *embed.FS) { return &FS }
```

This matches the existing generated/manual pattern already used in packages like
`pkg/agently/run/write` and `pkg/agently/toolcall/write`.

### StructQL usage

For goal persistence, use `StructQL` exactly the same way the existing write
packages do:

- `CurIDs` is a parameter object
- `Values []string` is populated from incoming mutable rows
- `codec:"structql,uri=sql/cur_ids.sql"` expands that collection into the SQL
  side

This is the mechanism the patch layer uses to fetch current DB rows before
deciding:

- insert vs update
- which fields actually changed
- whether optimistic compare-and-set constraints still hold

Recommended purpose of the SQL files:

- `cur_ids.sql`
  - project the incoming row ids for StructQL-backed lookup
- `cur_goal.sql`
  - fetch the current persisted goal rows for those ids
  - include enough columns for patch comparison:
    - `id`
    - `conversation_id`
    - `status`
    - `pause_reason`
    - `controller_spec`
    - `token_budget`
    - `tokens_used`
    - `time_used_seconds`
    - `status_reason`
    - `updated_at`

### Mutable view + constructors

Follow the same pattern used by the existing write packages.

Recommended helpers:

```go
func NewMutableGoalView(opts ...MutableGoalViewOption) *MutableGoalView
func NewMutableGoalViews(rows ...*MutableGoalView) *MutableGoalViews

func WithGoalID(v string) MutableGoalViewOption
func WithGoalConversationID(v string) MutableGoalViewOption
func WithGoalObjective(v string) MutableGoalViewOption
func WithGoalStatus(v string) MutableGoalViewOption
func WithGoalPauseReason(v string) MutableGoalViewOption
func WithGoalTokenBudget(v int64) MutableGoalViewOption
func WithGoalTokensUsed(v int64) MutableGoalViewOption
func WithGoalTimeUsedSeconds(v int64) MutableGoalViewOption
func WithGoalControllerSpec(v string) MutableGoalViewOption
func WithGoalSupersededAt(v time.Time) MutableGoalViewOption
```

And the generated setters in `goal.go` should follow the normal mutable-view
pattern:

- set field value
- ensure `Has`
- mark `Has.<Field> = true`

### Patch semantics

Goal persistence should use the standard patch flow, not bespoke direct-SQL
updates for the common path.

That means the package must support:

1. **insert**
   - create active goal row with required fields
2. **update**
   - patch a subset of fields on the current goal row
3. **replace**
   - implemented as:
     - supersede the old row
     - insert the new row
   - done in one write transaction

The important point is:

- insert and update are both exercised through the same patch discipline the
  repo already uses for other entities
- the goal package should look boring and familiar to existing maintainers

---

## Tool Surface

### New model-visible tools

Add three internal tools:

- `goal:get`
- `goal:create`
- `goal:update`

These tools are only registered when:

- `features.goals.enabled=true`
- `features.goals.modelToolsEnabled=true`

These tools are only registered when:

- `features.goals.enabled=true`
- `features.goals.modelToolsEnabled=true`

### Tool contracts

`goal:get`

```json
{}
```

Returns current goal or null:

```json
{
  "goal": {
    "id": "goal_123",
    "conversationId": "conv_123",
    "objective": "Keep improving the benchmark until p95 is below 120ms",
    "status": "active",
    "statusReason": null,
    "tokenBudget": 200000,
    "tokensUsed": 41250,
    "timeUsedSeconds": 930
  }
}
```

`goal:create`

```json
{
  "objective": "string",
  "tokenBudget": 200000
}
```

Rules:

- fail if current goal already exists
- creates `active` goal
- intended for dynamic goal creation by the model from the current user task
  when the task is clearly multi-step and durable
- should not be used for trivial one-shot asks

`goal:update`

```json
{
  "status": "complete" | "blocked",
  "reason": "string"
}
```

Rules:

- model may only set `complete` or `blocked`
- model must provide a non-empty `reason`
- that reason is persisted as durable `status_reason` and returned as
  `statusReason`
- model may not set:
  - `paused`
  - `budget_limited`
  - `usage_limited`
- pause/resume/clear remain user/system actions

### Why not scratchpad?

There is already a scratchpad tool family under
[`protocol/tool/service/scratchpad`](protocol/tool/service/scratchpad), but goal
state should not be built on top of it because:

- scratchpad is tool-oriented model state, not durable conversation contract
- scratchpad is not the natural source for UI hydration
- scratchpad does not naturally support accounting, lifecycle transitions, or
  queue invalidation semantics
- scratchpad encourages prompt-owned truth instead of runtime-owned truth

Scratchpad may still be useful later for:

- storing controller notes
- planner hints
- model-authored rationale

But not for goal truth.

---

## API Surface

Add conversation-level endpoints:

- `GET /v1/conversations/{id}/goal`
- `POST /v1/conversations/{id}/goal`
- `PATCH /v1/conversations/{id}/goal`
- `DELETE /v1/conversations/{id}/goal`

These routes are only active when `features.goals.enabled=true`. When the
workspace disables goals, handlers should return deterministic feature-disabled
errors.

Suggested request bodies:

`POST`

```json
{
  "objective": "Keep improving the benchmark until p95 is below 120ms",
  "tokenBudget": 200000
}
```

`PATCH`

```json
{
  "objective": "optional",
  "status": "active|paused|blocked|complete|budget_limited|usage_limited",
  "statusReason": "optional durable comment",
  "tokenBudget": 250000
}
```

Important distinction:

- recommended v1 REST PATCH acceptance:
  - `objective`
  - `tokenBudget`
  - `status` only for trusted user-facing transitions such as `active` and
    `paused`
- model-visible `goal:update` tool must remain restricted to `complete` and
  `blocked`
- model-visible `goal:create` is the path that lets the runtime prompt policy
  say "the model may set a durable goal dynamically from the user's task"

Add stream events:

- `goal.updated`
- `goal.cleared`
- optional later: `goal.controller_scheduled`

Future optional wakeup surface:

- `wakeup:get_limits`
- `wakeup:schedule`

If added, these are only registered when:

- `features.wakeups.enabled=true`
- `features.wakeups.modelToolsEnabled=true`

---

## Runtime Architecture

### Main types

```go
type Runtime struct {
    store         Store
    accounting    *Accounting
    continuation  *Continuation
}

type Accounting struct {
    mu                sync.Mutex
    turnBaseline      map[string]TokenUsage
    wallClockBaseline map[string]time.Time
    budgetReported    map[string]bool
}

type Continuation struct {
    mu sync.Mutex
}
```

The exact shape can vary, but two separate locks are recommended:

- accounting lock
- continuation lock

### Public runtime entrypoint

The outer runtime API should be event-driven, even if the internal controller
logic is snapshot-driven.

Recommended shape:

```go
func (r *Runtime) Apply(ctx context.Context, event GoalRuntimeEvent, input *RuntimeInput) error
```

Internal structure:

```go
snapshot, err := r.BuildSnapshot(ctx, input.ConversationID)
action, err := r.Evaluate(ctx, snapshot, event)
err = r.applyAction(ctx, input, action)
```

This reconciles the two mental models:

- externally: runtime lifecycle events
- internally: snapshot evaluation + action application

### Runtime event dispatcher

Add a single dispatcher entrypoint, something like:

```go
func (r *Runtime) Apply(ctx context.Context, event GoalRuntimeEvent, input *RuntimeInput) error
```

Suggested events:

- `TurnStarted`
- `ToolCompleted`
- `GoalToolCompleted`
- `TurnFinished`
- `TaskAborted`
- `AsyncCompleted`
- `QueueDrained`
- `ExternalGoalUpdated`
- `ExternalGoalCleared`
- `ConversationResumed`
- `MaybeContinueIfIdle`

This avoids duplicating continuation logic in multiple places.

### Event hooks

#### `run_query.go`

Hook runtime calls in this order:

1. turn start
2. after each non-goal tool completion
3. after goal-tool completion
4. turn finished
5. aborted / canceled path
6. after queue drain or rerun loop exit where relevant

Each hook must also respect feature gates:

- if `features.controller.enabled=false`, skip runtime/controller hooks entirely
- if `features.followups.chainsEnabled=false`, skip chain execution path
- if `features.queue.enabled=false`, do not schedule queued continuations

#### async completion path

Any place that currently re-enters the conversation after async completion
should emit:

- `AsyncCompleted`
- then `MaybeContinueIfIdle`

#### external API / UI mutation path

Goal REST updates should emit:

- `ExternalGoalUpdated`
- possibly inject live objective update context if a turn is running
- then `MaybeContinueIfIdle` if status becomes `active`

Goal deletion should emit:

- `ExternalGoalCleared`

---

## Accounting Semantics

### Per-turn token accounting

At turn start:

- record token baseline for the active goal

At tool completion / turn completion:

- compute delta since last baseline
- accumulate into `tokens_used`
- refresh baseline
- increment or reset `ConsecutiveNoProgress` depending on whether the turn
  produced a controller-recognized forward-progress signal

### Wall-clock accounting

For any period where the goal is active:

- accumulate elapsed time into `time_used_seconds`

Reset wall-clock baseline when:

- goal status changes
- goal is externally replaced
- goal is resumed

### Budget transition

If `token_budget != nil` and `tokens_used >= token_budget`:

- set `status = budget_limited`
- stop future continuation
- avoid repeated budget-limit steering injection

### Usage-limited transition

If runtime/provider limits prevent progress:

- set `status = usage_limited`
- stop future continuation

### Suppression around goal tool completion

When the model explicitly marks `complete` or `blocked`, do not let that same
tool completion trigger duplicate steering or budget-limit chatter. This needs
an explicit `GoalToolCompleted` path rather than treating it like any other tool
completion.

### Restart semantics

The runtime should be explicit about what survives restart and what does not.

Recommended v1 rule:

- the durable goal row survives restart
- `controller_spec` survives restart
- long-lived counters such as `AutonomousTurnsUsed` and
  `ConsecutiveNoProgress` should survive restart
- in-memory per-turn accounting baselines do **not** need to survive restart
- on resume, baselines are re-established from current totals and runtime state
- partial in-flight turn accounting may be conservatively dropped rather than
  risk double-counting

---

## Continuation Policy

### Candidate predicate

Continuation is only allowed when all are true:

- controller feature enabled
- autonomous continuation feature enabled
- goals feature enabled
- current goal exists
- goal status is `active`
- no turn is currently running
- no queued user turn is pending
- no pending elicitation blocks forward motion
- no pending approval blocks forward motion
- current mode allows autonomy

### Continuation output

Continuation should:

- enqueue a new queued turn
- mark it `origin=controller`
- include a hidden controller-context payload
- include visible preview text for queue display

It should not:

- create a visible user message saying "continue"
- bypass the queue
- call chain execution directly

If `features.queue.enabled=false`, continuation must not silently bypass queue
semantics. It should resolve to:

- no continuation
- or a deterministic feature-disabled runtime outcome

V1 recommendation: no continuation.

### Future delayed wakeups

If delayed wakeups are added later:

- wakeups are scheduler/runtime-owned, not prompt-owned
- the model may request a wake only within effective workspace /
  conversation / goal quotas
- the runtime must expose those effective limits back to the model

Important consequence:

- if the workspace maximum is `5`, the model can see `5`
- if current remaining budget is `3`, the model can see `3`
- the model may schedule within `3`
- it may never raise the cap above `5`

Enforcement points:

- reject wake requests above workspace or goal limits
- reject wake delays outside min/max bounds
- cancel pending wakeups on goal clear / complete / blocked
- cancel on user input when the wake policy requires it
- re-check goal and queue validity before firing a wake

### Ordering with chains

Keep existing chain path unchanged:

1. parent turn completes
2. `executeChains()` runs declared follow-ups
3. when idle again, continuation may enqueue a next step

This is simpler and safer than teaching the controller to select between chain
dispatch and queue scheduling.

---

## Pause / Stop Cases

The runtime should distinguish these clearly.

### Pause

Meaning:

- goal is still valid
- stop autonomous continuation
- resumable without redefining objective

Examples:

- user explicitly pauses
- user interrupts active work
- user starts unrelated higher-priority work
- user queues manual turns that should take precedence
- workflow enters a human-review checkpoint

### Blocked

Meaning:

- cannot continue until an external dependency changes

Examples:

- missing credential
- waiting on human response
- missing approval
- required system unavailable

### Complete

Meaning:

- success criteria met
- no further continuation

### Budget-limited / usage-limited

Meaning:

- controller must stop due to system-owned limit conditions

These transitions should not be model-settable through unrestricted tool calls.

---

## UI Implementation

### Settings as activation surface

Use the existing chat settings dialog as v1 goal editor.

Current structure:

- wrapper dialog:
  [`metadata/window/chat/new/web/dialog/settings.yaml`](../agently/metadata/window/chat/new/web/dialog/settings.yaml)
- panel form:
  [`metadata/window/chat/new/web/dialog/panel/settings.yaml`](../agently/metadata/window/chat/new/web/dialog/panel/settings.yaml)

Important current reality:

- wrapper uses `dataSourceRef: settings`
- panel uses `dataSourceRef: meta`

In v1:

- read goal state from `meta.goal`
- submit goal mutations through explicit chat service handlers
- refresh `meta` and `queueTurns` after mutation

Recommended UI controls:

- `goalEnabled`
- `goalObjective`
- `goalTokenBudget`
- read-only `goalStatus`
- read-only `goalUsage`
- actions:
  - Create
  - Save
  - Pause
  - Resume
  - Clear

### Queue as override surface

Current queue card:

- [`ui/src/components/chat/SteerQueue.jsx`](../agently/ui/src/components/chat/SteerQueue.jsx)

Current queue services:

- [`ui/src/services/chatService.js`](../agently/ui/src/services/chatService.js)
- [`ui/src/services/renderRows.js`](../agently/ui/src/services/renderRows.js)
- [`ui/src/services/chatRuntime.js`](../agently/ui/src/services/chatRuntime.js)

Required additions:

- propagate `origin`, `goalId`, `statusReason` through queue row projections
- render badge for controller-owned rows
- hide or disable `Steer` for controller-owned queued turns
- keep `Edit`, `Move`, `Delete`
- no `Run now` in v1

### Deletion / cleanup

When:

- goal cleared
- goal completed
- goal blocked / usage-limited / budget-limited
- new user turn arrives
- conversation deleted

The backend must decide which controller-owned queued turns are removed or
marked stale. The UI should reflect queue truth after refresh; it should not
attempt to infer invalidation heuristics locally.

### Event wiring

Add stream handlers for:

- `goal.updated`
- `goal.cleared`

The settings panel and queue surfaces should consume a goal-state holder or
`goalBus`, not derive lifecycle state from queue rows.

---

## Phase Plan

### Phase 1 — Persistence + API + tools

Goal: durable goal truth and model-visible goal tools exist, but no autonomous
continuation yet.

Work:

- add `goal` schema + migrations
- add `pkg/agently/goal/*`
- add workspace feature config shape + loader wiring
- add REST CRUD handlers
- add `goal:get`, `goal:create`, `goal:update`
- add basic tests for goal CRUD and tool contracts

Concrete persistence tasks:

- create `pkg/agently/goal/read`
- create `pkg/agently/goal/write`
- add `StructQL`-based `CurIDs` / `Cur` lookup path
- add constructors mirroring other mutable view packages
- wire patch-style insert/update through the normal data service

Acceptance:

- user can create/read/update/clear goal through API
- model can dynamically create/read/mark complete or blocked
- no queue/controller behavior yet

### Phase 2 — Runtime accounting

Goal: token/time usage accumulates correctly while a goal is active.

Work:

- add `service/goal/accounting.go`
- hook:
  - turn start
  - tool complete
  - goal-tool complete
  - turn finish
  - task abort
- add budget and usage transitions

Acceptance:

- active goal usage updates over normal turns
- budget-limited and usage-limited transitions are persisted
- no duplicate budget steering

### Phase 3 — Idle continuation

Goal: active goals can schedule controller-owned queued turns when the
conversation is idle.

Work:

- add `service/goal/continuation.go`
- add continuation candidate predicate
- enqueue hidden-context controller turns
- add queue metadata fields
- add server-side invalidation on new user turns

Acceptance:

- idle active goal queues one controller-owned next step
- no visible synthetic user bubble is created
- user turn arrival invalidates stale controller-owned queued turns

### Phase 4 — UI integration

Goal: users can see and manage goals and controller-owned queued turns.

Work:

- settings panel goal section
- queue badge / stale-state rendering
- event wiring for `goal.updated` / `goal.cleared`
- queue refresh + meta refresh

Acceptance:

- goal can be created/paused/resumed/cleared in UI
- queue shows controller-owned rows distinctly
- UI stays in sync with backend events

### Phase 5 — Resume / async / polish

Goal: continuation works cleanly across async completion and process restart.

Work:

- hook `AsyncCompleted`
- hook `ConversationResumed`
- restore active goal runtime state on startup
- add observability / metrics

Acceptance:

- async completion can trigger continuation reevaluation
- active goal survives restart
- metrics expose goal lifecycle and continuation behavior

---

## Testing Plan

### Unit tests

Goal store:

- insert fails when goal exists
- replace resets counters
- update respects expected goal id
- accounting updates usage

Goal tools:

- `goal:create` creates active goal
- `goal:update` only accepts `complete` / `blocked`
- `goal:get` returns null or goal

Accounting:

- token delta baseline
- wall-clock accumulation
- budget-limit dedup

Continuation:

- no continuation when turn active
- no continuation when queued user work exists
- continuation when idle and active
- hidden context attached
- no continuation when no explicit next-action hint exists
- no continuation when workspace controller/queue gates disable it

### Integration tests

- turn completion with active goal schedules queue row
- user submit invalidates controller-owned queued row
- pause blocks continuation
- complete clears controller-owned queued rows
- blocked status suppresses continuation
- async completion reevaluates continuation
- restart re-establishes baselines without double-counting

### SQLite data-driven tests

For persisted goal specifically, follow the same style already used in
`app/store/data/service_test.go`.

Recommended shape:

- add a `goal patch` subsection to the existing data-driven patch suite
- or add a sibling test file if isolation is cleaner

The important part is to keep the same pattern:

- build a seeded SQLite-backed service
- define table-driven insert/update cases
- patch through the real Datly service path
- read back and assert final state

Suggested test skeleton:

```go
t.Run("goal patch", func(t *testing.T) {
    svc := newSeededService(t, seedForPatchBaseline)
    cases := []struct {
        name    string
        rows    []*aggoalwrite.MutableGoalView
        wantErr bool
    }{
        {
            name: "insert",
            rows: []*aggoalwrite.MutableGoalView{
                aggoalwrite.NewMutableGoalView(
                    aggoalwrite.WithGoalID("g-patch"),
                    aggoalwrite.WithGoalConversationID("c-base"),
                    aggoalwrite.WithGoalObjective("reduce p95"),
                    aggoalwrite.WithGoalStatus("active"),
                ),
            },
        },
        {
            name: "update",
            rows: []*aggoalwrite.MutableGoalView{
                aggoalwrite.NewMutableGoalView(
                    aggoalwrite.WithGoalID("g-base"),
                    aggoalwrite.WithGoalStatus("paused"),
                ),
            },
        },
    }
    ...
})
```

Minimum persistence assertions:

- insert goal row through patch path
- update goal row through patch path
- read back inserted row
- read back updated row
- validate missing required create fields fail

This should be SQLite-backed, data-driven, and should use the same patch
machinery style as existing entity patch tests.

### UI tests

- settings panel shows goal state from `meta.goal`
- queue card renders autonomous badge for `origin=controller`
- stale-state badge rendering
- clear goal removes controller-owned queued rows after refresh

---

## Migration Notes

### Schema rollout

1. add `goal` table
2. add queue metadata columns or extend queue views
3. deploy read-safe first
4. then enable write paths

### Feature gating

Use the workspace-level `features.*` policy as the authoritative gate:

- `features.goals.*`
- `features.followups.*`
- `features.controller.*`
- `features.queue.*`
- `features.steering.*`
- `features.wakeups.*`

Optional deploy/runtime flags may still exist for staged rollout, but they
should only be able to narrow availability further. They should not replace the
workspace policy model.

---

## Open Decisions

1. Should controller-owned queued turns be hard-deleted or soft-marked stale?
   Recommendation: hard-delete on clear/complete; stale on pause.

3. Should linked child conversations spawned by chains inherit active goal id?
   Recommendation: not in v1 unless accounting genuinely needs it.

4. Should controller-generated continuation preview text be model-authored or
   runtime-authored?
   Recommendation: runtime-authored in v1 for determinism.

---

## Recommendation

Build the feature in this order:

1. durable goal store
2. goal REST API
3. model-visible goal tools
4. accounting runtime
5. continuation runtime
6. queue metadata extension
7. settings + queue UI
8. async/resume polish

That gives Agently the strongest parts of Codex goal semantics without giving
up the dynamic follow-up and queue model it already does well.
