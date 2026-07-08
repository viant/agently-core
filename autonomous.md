# Autonomous System Proposal

**Status:** Proposal
**Author:** awitas
**Date:** 2026-06-02
**Scope:** `agently-core`, later `agently` UI integration

---

## 1. Goal

Add a lightweight **autonomous system** to `agently-core` that can keep
working on a durable objective across turns without overloading the existing
follow-up and chain mechanisms.

The controller should answer:

- Is there an active objective for this conversation?
- Should the system continue automatically now?
- Should it pause, block, or wait for the user instead?
- Should the next step be a queued turn, a supervised chain, or no action?

This proposal deliberately keeps three layers separate:

- **Goal**: durable objective state
- **Controller**: policy that decides whether and how to continue
- **Follow-up / chain**: concrete next action chosen by the controller

The main recommendation is:

- keep `service/agent/chain.go` as the execution primitive for post-turn work
- keep queued turns and steering as the user-visible control surface
- add a separate controller layer that owns autonomy policy and durable goal
  lifecycle

This follows the same broad separation that Codex uses, where goal state is
durable and autonomous continuation is a consequence of that state rather than
the definition of the next step itself.

---

## 2. Why not just extend chains?

`agently-core` already has real chain execution:

- declarative agent follow-ups in
  [`protocol/agent/agent.go`](protocol/agent/agent.go)
- chain dispatch in [`service/agent/chain.go`](service/agent/chain.go)
- queued turn / steering behavior in
  [`service/agent/run_query.go`](service/agent/run_query.go)

Those mechanisms are good at one thing:

- deciding **what should run after this turn**

They are not the right abstraction for:

- a single durable objective for the whole conversation
- accumulated token / time budget tracking
- pause / resume / blocked / complete lifecycle
- autonomy policy like "continue only when idle" or "do not continue while the
  user has queued work"

If we turn chains into goals, several semantics get mixed together:

- workflow orchestration
- durable objective state
- background continuation policy
- user override / queue UX

That makes both the runtime and the UI harder to reason about.

---

## 3. Existing substrate we should reuse

### 3.1 Follow-up and chain execution

Current post-turn chain execution already exists in
[`service/agent/chain.go`](service/agent/chain.go):

- `executeChains()` filters and dispatches declared follow-ups
- `evalChainWhen()` evaluates `WhenSpec`
- `ensureChainConversation()` supports `reuse` vs `link`
- `runChainSync()` executes the child and re-enters the parent

This is the right place for controller-selected follow-up actions to land.

### 3.2 Queued turn and steering model

Current active-turn follow-up behavior already exists in:

- [`service/agent/run_query.go`](service/agent/run_query.go)
- [`doc/followup-chains.md`](doc/followup-chains.md)
- [`doc/conversation-model.md`](doc/conversation-model.md)

This gives us:

- queued turns while work is in flight
- explicit mid-turn steering
- reorder / edit / drop semantics
- a user-visible queue in the UI

This is the right user-facing override surface for autonomy.

### 3.3 Conversation / turn persistence

Current persistence already has the correct backbone:

- `conversation`
- `turn`
- `run`
- `turn_queue`

We should add a durable goal record beside these instead of encoding goal state
indirectly in chain definitions or queued turns.

### 3.4 Async re-entry

Current async design already supports the idea that work may resume later:

- [`doc/async.md`](doc/async.md)

The controller should treat async completion as one more signal that can trigger
re-evaluation of whether a conversation should continue.

---

## 3.5 Workspace feature gates

Not every workspace should expose every autonomy primitive.

We should add explicit workspace-level feature gates so repos/products can opt
into only the parts they want:

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

Recommended semantics:

- `goals.enabled`
  - enables durable goal state and goal API/runtime
- `goals.modelToolsEnabled`
  - enables `goal:get`, `goal:create`, `goal:update`
- `goals.uiEnabled`
  - shows goal controls in UI
- `followups.enabled`
  - master gate for follow-up behavior
- `followups.chainsEnabled`
  - enables `Agent.FollowUps` execution
- `followups.inlineEnabled`
  - enables ad hoc `/followup`-style or composer-defined follow-up specs
- `controller.enabled`
  - enables the goal runtime/controller package
- `controller.autonomousContinuationEnabled`
  - allows the controller to enqueue next-step continuations
- `queue.enabled`
  - enables queued-turn behavior while a turn is in flight
- `queue.uiEnabled`
  - shows queue surfaces in UI
- `steering.enabled`
  - enables explicit steering endpoints / UX
- `steering.forceEnabled`
  - enables force-steer actions on queued turns

Important rule:

- workspace feature gates are **upper bounds**
- agent config, per-turn inputs, and UI toggles may narrow behavior further
- they may not broaden behavior beyond what the workspace allows

Example:

- workspace disables `goals.modelToolsEnabled`
- a model must not see any goal tools even if an agent asks for them

Example:

- workspace disables `followups.chainsEnabled`
- `Agent.FollowUps` definitions may still exist in YAML, but runtime must not
  execute them

This is important because "feature exists in the platform" and "feature is
allowed in this workspace" are not the same thing.

---

## 4. Core model

Add a new durable `Goal` concept for a conversation.

Suggested shape:

```go
type GoalStatus string

const (
    GoalStatusActive        GoalStatus = "active"
    GoalStatusPaused        GoalStatus = "paused"
    GoalStatusBlocked       GoalStatus = "blocked"
    GoalStatusComplete      GoalStatus = "complete"
    GoalStatusBudgetLimited GoalStatus = "budget_limited"
    GoalStatusUsageLimited  GoalStatus = "usage_limited"
)

type Goal struct {
    ID               string
    ConversationID   string
    Objective        string
    Status           GoalStatus
    PauseReason      *GoalPauseReason
    Controller       *GoalControllerSpec
    TokenBudget      *int64
    TokensUsed       int64
    TimeUsedSeconds  int64
    LastControllerAt time.Time
    CreatedAt        time.Time
    UpdatedAt        time.Time
}
```

Important design rules:

- one active goal per conversation
- status is conversation-level state, not turn-level state
- `TokensUsed` and `TimeUsedSeconds` belong to the goal, not to chains
- the controller may **read** and **advance** usage, but follow-up execution
  should not own the state model
- do not store goal state in scratchpad-like memory, `ChainContext.Context`, or
  queue-only metadata; those layers are not restart-safe and are not reliable
  UI truth

Suggested policy block:

```go
type GoalPauseCase string
type GoalStopCase string
type GoalContinueMode string
type GoalTurnPolicy string
type GoalAsyncPolicy string

type GoalControllerSpec struct {
    ContinueMode       GoalContinueMode
    PauseConditions    []GoalPauseCase
    StopConditions     []GoalStopCase
    OnTurnFinished     GoalTurnPolicy
    OnAsyncCompleted   GoalAsyncPolicy
    MaxAutonomousTurns *int
}
```

Suggested pause reason enum:

```go
type GoalPauseReason string

const (
    GoalPauseReasonUserRequested         GoalPauseReason = "user_requested"
    GoalPauseReasonUserInterrupt         GoalPauseReason = "user_interrupt"
    GoalPauseReasonUserNewTurn           GoalPauseReason = "user_new_turn"
    GoalPauseReasonHumanReviewCheckpoint GoalPauseReason = "human_review_checkpoint"
    GoalPauseReasonModeChange            GoalPauseReason = "mode_change"
    GoalPauseReasonSupervisorPolicy      GoalPauseReason = "supervisor_policy"
)
```

Suggested policy enums:

```go
const (
    GoalPauseCaseUserInterrupt         GoalPauseCase = "user_interrupt"
    GoalPauseCaseUserNewTurn           GoalPauseCase = "user_new_turn"
    GoalPauseCaseHumanReviewCheckpoint GoalPauseCase = "human_review_checkpoint"
    GoalPauseCaseModeChange            GoalPauseCase = "mode_change"
)

const (
    GoalStopCaseComplete      GoalStopCase = "complete"
    GoalStopCaseBlocked       GoalStopCase = "blocked"
    GoalStopCaseBudgetLimited GoalStopCase = "budget_limited"
    GoalStopCaseUsageLimited  GoalStopCase = "usage_limited"
)
```

### 4.1 Goal persistence semantics

Goal persistence needs more than a generic upsert.

Suggested operations:

- `InsertGoal(conversationID, objective, tokenBudget)`:
  - creates a new active goal
  - fails if a current goal already exists
- `UpdateGoal(conversationID, expectedGoalID, patch)`:
  - updates status, objective, or budget for the current goal
  - compare-and-set on `expectedGoalID` to avoid racing UI / runtime updates
- `AccountGoalUsage(conversationID, expectedGoalID, tokenDelta, timeDelta)`:
  - accumulates usage onto the active goal
  - may transition status when limits are exceeded

Recommended v1 persistence rule:

- store `Goal.Controller` durably with the goal
- simplest shape: JSON-encoded `controller_spec` field on the goal row
- do not treat controller policy as prompt-only or request-only state

---

## 5. Controller responsibility

The controller is a policy engine, not a chain executor.

Its responsibilities are:

- load the current goal for a conversation
- observe runtime state
- decide whether the system is allowed to continue
- decide what kind of continuation should happen
- emit a follow-up action
- stop when user input or lifecycle state says to stop

It should not:

- replace the planner
- replace chain execution
- replace the queue
- invent a new transcript ownership model
- own streaming or narration

### 5.1 Split the runtime into focused collaborators

To keep the implementation testable, "controller" should be a thin policy layer
above three narrower services:

- **Goal store**
  - durable goal read/write/accounting persistence
- **Goal runtime**
  - lifecycle event dispatcher
  - translates turn/tool/async/user events into goal actions
- **Goal continuation**
  - idle-time policy that decides whether to enqueue a controller-owned next
    step

This keeps the responsibilities clean:

- persistence is deterministic
- accounting is deterministic
- continuation is policy-driven
- chains remain unchanged as the follow-up execution primitive

---

## 6. Decision inputs

On each evaluation, the controller should inspect:

- current goal state
- whether a turn is currently running
- whether the queue already has pending user work
- whether an elicitation is pending
- whether async operations are still active
- latest conversation transcript summary
- latest turn output and status
- token and elapsed-time usage for the goal
- whether the most recent continuation already failed or produced no progress

This can be represented as:

```go
type ControllerSnapshot struct {
    Goal               *Goal
    RunningTurnID      string
    QueuedTurnCount    int
    HasPendingUserWork bool
    HasPendingApproval bool
    HasPendingAsync    bool
    LatestTurnStatus   string
    LatestTurnSummary  string
    ConsecutiveNoProgress int
    AutonomousTurnsUsed   int
    TokensUsed         int64
    TimeUsedSeconds    int64
}
```

---

## 7. Controller output

The controller should produce a single explicit decision:

```go
type ControllerActionKind string

const (
    ControllerActionNone          ControllerActionKind = "none"
    ControllerActionQueueTurn     ControllerActionKind = "queue_turn"
    ControllerActionPauseGoal     ControllerActionKind = "pause_goal"
    ControllerActionBlockGoal     ControllerActionKind = "block_goal"
    ControllerActionCompleteGoal  ControllerActionKind = "complete_goal"
    ControllerActionBudgetLimited ControllerActionKind = "budget_limited"
    ControllerActionUsageLimited  ControllerActionKind = "usage_limited"
)

type ControllerAction struct {
    Kind   ControllerActionKind
    Reason string
    Prompt string
}
```

Interpretation:

- `queue_turn`: synthesize the next user-like turn for the same conversation
- `pause_goal`: goal remains durable but autonomy stops
- `block_goal`: goal requires user or external input
- `complete_goal`: objective is done; controller must not continue
- `budget_limited`: objective stops because budget was exhausted
- `usage_limited`: objective stops because runtime/provider usage blocked progress
- `none`: no autonomous action now

---

## 8. Continue policy

Autonomous continuation should be conservative.

The controller should only continue when all of the following are true:

- goal exists
- goal status is `active`
- no turn is currently running
- no queued user turn exists
- no pending elicitation blocks forward progress
- no pending approval blocks forward progress
- no conversation-level stop flag is set

Recommended first policy:

1. If goal is not `active`, do nothing.
2. If user work is queued, do nothing.
3. If a turn is running, do nothing.
4. If budget or usage limit has been reached, transition goal status.
5. If `ConsecutiveNoProgress` exceeds policy, block or pause the goal instead of
   continuing indefinitely.
6. If `MaxAutonomousTurns` is reached, pause for human review or wait per
   policy.
7. If the last turn concluded with a clear next action, apply
   `Goal.Controller.OnTurnFinished`.
8. Otherwise do nothing and wait for the user.

This makes the controller an **idle-time continuation policy**, not a loop that
restarts work aggressively.

### 8.1 Hidden continuation payloads

Controller-generated continuation should be queued as ordinary queued turns for
visibility and override, but the continuation instruction itself should be
treated as hidden controller context rather than a visible user-authored bubble.

That means:

- queue entry is visible
- transcript remains clean
- queue UI can still show preview text
- the eventual running turn receives controller context as internal input

### 8.2 What counts as a "clear next action"

This is a load-bearing rule and should not be left implicit.

Recommended v1 behavior:

- the controller must **not** infer a next action from arbitrary free-text
  transcript output
- continuation should only be scheduled when the runtime has an explicit
  continuation hint from one of:
  - a controller-owned follow-up payload already attached to the turn
  - a future structured model signal dedicated to next-step continuation
  - a deterministic runtime-authored next step for a known workflow

If no explicit continuation hint exists, `OnTurnFinished = evaluate` should
resolve to **no automatic continuation**.

### 8.3 Post-iteration policy

Suggested supporting enums:

```go
type GoalContinueMode string
type GoalTurnPolicy string
type GoalAsyncPolicy string
```

Suggested values:

```go
const (
    GoalContinueModeIdleOnly   GoalContinueMode = "idle_only"
    GoalContinueModeManualOnly GoalContinueMode = "manual_only"
)

const (
    GoalTurnPolicyEvaluate GoalTurnPolicy = "evaluate"
    GoalTurnPolicyWait     GoalTurnPolicy = "wait"
)

const (
    GoalAsyncPolicyEvaluate GoalAsyncPolicy = "evaluate"
    GoalAsyncPolicyWait     GoalAsyncPolicy = "wait"
)
```

---

## 9. Relation to queue and steering

The controller should integrate with the existing queue model rather than
inventing a parallel one.

### 9.1 Controller-generated continuation should be queued

When the controller wants to continue in the same conversation, it should create
an ordinary queued turn.

Benefits:

- existing UI already knows how to show it
- users can edit, reorder, or delete it
- the queue remains the single visible "what happens next" surface

Important implementation rule:

- the controller does **not** merely enqueue and hope an idle queue runner
  exists
- when the conversation is idle and continuation is approved, the controller
  must:
  1. insert the controller-owned queued turn as the durable next-step record
  2. immediately trigger the existing execution/start path for that queued turn
     in the same control flow

So the queue is the durable visible handoff surface, but the controller is the
component that kicks execution server-side for idle autonomous continuation.

### 9.2 Mark controller-owned queued turns explicitly

Extend queued turn metadata with something like:

```go
type TurnOrigin string

const (
    TurnOriginUser       TurnOrigin = "user"
    TurnOriginSteer      TurnOrigin = "steer"
    TurnOriginController TurnOrigin = "controller"
    TurnOriginChain      TurnOrigin = "chain"
)
```

Then the UI can label:

- queued user follow-up
- queued autonomous continuation
- queued chain result

### 9.3 Steering still wins

If the user steers while the controller has queued an autonomous continuation:

- steering should take priority
- the queued controller turn should either remain behind the new user turn or be
  invalidated and recomputed

Recommended MVP rule:

- any new user turn invalidates stale controller-generated queued turns for the
  same conversation unless they were explicitly preserved

---

## 10. Relation to chains

Chains remain useful, but they should **not** become a controller action kind.
They continue to run from the existing chain path after a parent turn finishes.

Use chains when:

- the next action is naturally a child agent task
- a declared `WhenSpec` already captures the transition well
- the follow-up should run in a linked conversation
- the follow-up should publish through existing chain semantics

Use queued continuation turns when:

- the next step is just "continue the same task in the same conversation"
- the user should be able to inspect or edit the next step directly
- no child conversation is needed

This split keeps the controller simple:

- **same-conversation continuation** -> queued turn
- **specialized follow-up execution** -> existing chain runtime, outside the
  controller action enum

Recommended execution ordering:

1. parent turn completes
2. existing chain system runs any declared follow-ups
3. once the conversation is idle again, goal continuation may enqueue the next
   controller-owned turn

This avoids making the controller responsible for selecting between two
different orchestration systems.

---

## 11. Suggested runtime hooks

The controller should be invoked from a narrow set of points, but should be
driven through a single internal event dispatcher rather than several unrelated
call paths.

Suggested internal event shape:

```go
type GoalRuntimeEvent string

const (
    GoalRuntimeTurnStarted         GoalRuntimeEvent = "turn_started"
    GoalRuntimeToolCompleted       GoalRuntimeEvent = "tool_completed"
    GoalRuntimeTurnFinished        GoalRuntimeEvent = "turn_finished"
    GoalRuntimeTaskAborted         GoalRuntimeEvent = "task_aborted"
    GoalRuntimeAsyncCompleted      GoalRuntimeEvent = "async_completed"
    GoalRuntimeQueueDrained        GoalRuntimeEvent = "queue_drained"
    GoalRuntimeExternalGoalUpdated GoalRuntimeEvent = "external_goal_updated"
    GoalRuntimeExternalGoalCleared GoalRuntimeEvent = "external_goal_cleared"
    GoalRuntimeConversationResumed GoalRuntimeEvent = "conversation_resumed"
    GoalRuntimeMaybeContinueIfIdle GoalRuntimeEvent = "maybe_continue_if_idle"
)
```

The implementation should emit `maybe_continue_if_idle` from several places but
centralize the actual idle predicate and continuation launch logic in one
function.

### 11.1 After turn completion

After `runPlanAndStatus()` and finalization in
[`service/agent/run_turn.go`](service/agent/run_turn.go), evaluate the
controller once the parent turn is stable.

### 11.2 After async terminal updates

When an async op completes and re-enters the reactor, evaluate again. Async is
already modeled as conversation re-entry, so the controller can remain mostly
stateless.

### 11.3 After queue drain

When the queue becomes empty and no turn is active, evaluate whether an active
goal should continue.

### 11.4 After explicit goal mutation

When goal status or objective changes:

- `active` may schedule continuation
- `paused`, `blocked`, `complete` must stop continuation

### 11.5 On explicit goal-tool completion

When the model uses the future goal update tool to mark a goal `complete` or
`blocked`, the runtime should:

- account usage first
- suppress duplicate budget-limit steering from the same tool completion
- persist the terminal status
- stop future continuation immediately

### 11.6 Pause on explicit cases

The runtime should pause active goals on at least these cases in v1:

- user interrupt
- user submits a new non-steering turn while the goal is active
- conversation enters a human-review checkpoint
- current mode disables autonomy

### 11.7 Future wakeup primitive

A delayed wakeup primitive is likely useful later, but it should be introduced
only as a tightly constrained runtime capability.

It is a distinct concern from both goal state and follow-up execution:

- **goal** = what objective remains active
- **follow-up / queue** = what should happen next
- **wakeup** = when reevaluation is allowed again

Recommended initial decision:

- do **not** ship a general self-wakeup tool in the first controller release
- rely first on runtime-owned wake triggers:
  - async completion
  - queue drain
  - explicit goal resume
  - external goal mutation
  - conversation resume

Recommended future design:

- add an internal-only wake scheduling primitive
- feature-gate it at the workspace level
- require durable wake records owned by runtime/scheduler
- never allow the model to broaden its own wake limits

Suggested policy shape:

```go
type GoalWakePolicy struct {
    Enabled                  bool
    MaxGlobalWakeupsPerHour  int
    MaxConversationWakeups   int
    MaxGoalWakeups           int
    MinWakeDelaySeconds      int
    MaxWakeDelaySeconds      int
    CancelOnUserInput        bool
    CancelOnGoalStateChange  bool
    AllowedReasons           []string
}
```

Suggested durable wake request shape:

```go
type GoalWakeRequest struct {
    ID                string
    ConversationID    string
    GoalID            string
    Reason            string
    WakeAt            time.Time
    WakeCount         int
    MaxWakeCount      int
    CancelOnUserInput bool
}
```

Hard rules:

- workspace policy is the upper bound
- model may request a wakeup only within those limits
- model must be able to **see** the effective limits
- model must not be able to increase those limits

Example:

- workspace `MaxGlobalWakeupsPerHour = 5`
- if current remaining visible budget is `3`, the model may schedule within
  that remaining budget
- it cannot request `6`, and it cannot rewrite the workspace cap from `5` to a
  larger number

If a model-visible wake tool is ever added, its contract should expose both:

- the requested wakeup
- the effective current limits / remaining wake budget

So the model can reason about:

- whether waking later is still allowed
- how many wakeups remain
- whether it should spend a wakeup now or wait for a stronger signal

Recommended future helper surface:

- `wakeup:get_limits`
- `wakeup:schedule`

Where:

- `wakeup:get_limits` is read-only and returns effective workspace /
  conversation / goal limits plus remaining counters
- `wakeup:schedule` can only request a wakeup inside those bounds

Cancellation rules for pending wakeups:

- cancel on goal clear
- cancel on goal complete
- cancel on goal blocked
- cancel on goal pause if policy says so
- cancel on new user input when `CancelOnUserInput=true`

Fire-time rules:

- before executing a wakeup, runtime must re-check:
  - goal still exists
  - goal still active
  - queue still empty
  - no fresher user work exists
  - wake quotas not exceeded

This keeps wakeup power with the runtime, while still allowing the model to
request delayed reevaluation in a bounded, inspectable way.

---

## 12. Persistence and API

### 12.1 Backend persistence

Add a new store package, parallel to the existing conversation entities:

- `pkg/agently/goal/`

Suggested operations:

- `GetGoal(conversationID)`
- `InsertGoal(...)`
- `UpdateGoal(...)`
- `ClearGoal(conversationID)`
- `AccountGoalUsage(conversationID, tokenDelta, timeDelta)`

### 12.2 Public API

Add a minimal API surface:

- `GET /v1/conversations/{id}/goal`
- `POST /v1/conversations/{id}/goal`
- `PATCH /v1/conversations/{id}/goal`
- `DELETE /v1/conversations/{id}/goal`

Suggested patch fields:

- `objective`
- `tokenBudget`
- `status` only for trusted user-facing transitions such as:
  - `active`
  - `paused`

`blocked` / `complete` should come from the model-visible goal tool path.
`budget_limited` / `usage_limited` should come from runtime-owned transitions.

All routes above must respect workspace feature gates:

- if `goals.enabled=false`, goal routes return deterministic feature-disabled
  errors
- if `goals.uiEnabled=false`, UI should not expose goal controls even if the
  backend feature exists

### 12.3 Model-visible tool surface

Add first-class goal tools for the model.

Suggested tool surface:

- `goal:get`
  - returns current goal, usage, and remaining budget
- `goal:create`
  - creates a new active goal
  - fails if a current goal already exists
- `goal:update`
  - restricted status updates from the model
  - allowed status values: `complete`, `blocked`
  - requires a non-empty `reason`
  - persists that comment as durable `statusReason`

Important rule:

- the model may create a goal dynamically from the user task when no current
  goal exists and the runtime/prompt policy allows it
- the model must **not** be allowed to set `paused`, `budget_limited`, or
  `usage_limited`
- those states belong to the user or system runtime
- when the model marks a goal `complete` or `blocked`, it must include a short
  durable reason/comment so the user can see why the lifecycle changed
- if `goals.modelToolsEnabled=false`, these tools must not be exposed to the
  model at all

Recommended creation policy:

- allow `goal:create` when the task is clearly a multi-step objective that
  benefits from durable continuation
- do not require the user to type `/goal` explicitly
- do require the model to avoid creating a goal for trivial one-shot questions
  or purely conversational asks
- fail `goal:create` if a current goal already exists

In other words, the model should be able to transform:

- "keep improving this benchmark until p95 is under 120ms"

into a durable goal by tool call, but should not create one for:

- "what does this function do?"

### 12.4 Events

Emit goal events on the existing stream:

- `goal.updated`
- `goal.cleared`
- optionally `goal.controller_scheduled`

The UI should not infer goal state from queued turns alone.

---

## 13. UI direction for `agently`

This proposal does not require a full new dashboard.

Minimal UI:

- show active goal summary in the conversation header or settings panel
- show status, objective, token budget, usage
- show whether the next queued turn is controller-generated
- let the user:
  - pause
  - resume
  - edit objective
  - clear goal

Queue UI should remain primary for immediate control.

Recommended queue affordances for controller-owned turns:

- badge: `autonomous`
- action: `Edit next step`
- action: `Drop`

This fits naturally with the existing queue patterns already surfaced in the
chat UI.

### 13.1 Activation entry points

The current chat UI already has two natural activation surfaces:

- the chat settings dialog mounted from
  [`metadata/window/chat/new/web/main.yaml`](../agently/metadata/window/chat/new/web/main.yaml)
  and rendered from
  [`metadata/window/chat/new/web/dialog/panel/settings.yaml`](../agently/metadata/window/chat/new/web/dialog/panel/settings.yaml)
- the queue surface rendered by
  [`ui/src/components/chat/SteerQueue.jsx`](../agently/ui/src/components/chat/SteerQueue.jsx)

Recommended activation model:

1. **Settings panel owns goal state**
   - add a new "Autonomous Goal" section to the chat settings form
   - fields:
     - `goalEnabled` toggle
     - `goalObjective` textarea or long text input
     - `goalTokenBudget` numeric input
     - read-only status / usage summary
   - actions:
     - `Create goal` when no goal exists
     - `Save goal` when editing an existing goal
     - `Pause`
     - `Resume`
     - `Clear`
   - only render this section when `goals.uiEnabled=true`

2. **Queue surface owns next-step control**
   - queued autonomous turns appear in the existing queue list
   - the queue remains the primary place to inspect, edit, reorder, or delete
     controller-generated next steps
   - only render queue controls when `queue.uiEnabled=true`

3. **Topbar / header only shows summary**
   - the existing "View settings" topbar menu in
     [`ui/src/components/MenuBar.jsx`](../agently/ui/src/components/MenuBar.jsx)
     should not become the full goal editor
   - it may show a compact indicator later, but v1 editing belongs in the chat
     settings dialog to avoid splitting state across two UI paths

### 13.2 Concrete settings-panel wiring

The existing settings dialog is metadata-driven and already handles chat
behavior toggles like `chainsEnabled`, `allowedChains`, `autoSummarize`, and
tool exposure. Goal activation should follow the same pattern.

Important distinction in the current UI:

- the dialog wrapper at
  [`metadata/window/chat/new/web/dialog/settings.yaml`](../agently/metadata/window/chat/new/web/dialog/settings.yaml)
  is mounted as `dataSourceRef: settings`
- the actual form panel at
  [`metadata/window/chat/new/web/dialog/panel/settings.yaml`](../agently/metadata/window/chat/new/web/dialog/panel/settings.yaml)
  binds to `dataSourceRef: meta`

Today, behavior fields like `chainsEnabled`, `allowedChains`, and
`autoSummarize` already live on the `meta` form payload. To stay consistent,
goal state should also be read from `meta` in v1.

Recommended additions to the `meta` datasource payload:

```json
{
  "goal": {
    "enabled": true,
    "objective": "Keep improving the benchmark until p95 latency is under 120ms",
    "status": "active",
    "tokenBudget": 200000,
    "tokensUsed": 41250,
    "timeUsedSeconds": 930
  }
}
```

The `settings` dialog should continue to own footer actions like `Save` /
`Cancel`, but the authoritative live goal state shown inside the panel should be
backed by `meta`, not a second independent `settings.goal` copy.

Recommended metadata fields:

- `goalEnabled`: toggle
- `goalObjective`: multiline text
- `goalTokenBudget`: numeric input
- `goalStatus`: read-only label
- `goalUsage`: read-only label
- `goalActions`: buttons bound to chat handlers

Suggested service handlers to add beside the existing settings handlers in
[`ui/src/services/chatService.js`](../agently/ui/src/services/chatService.js):

- `createGoal`
- `updateGoalObjective`
- `pauseGoal`
- `resumeGoal`
- `clearGoal`

These should call the new goal API directly and then trigger `dsTick(context)`
just like queue mutations already do.

After any goal mutation, v1 should refresh:

- `meta`
- `queueTurns`

It should not rely on `settings` as a separate source of truth for goal state.

### 13.3 Queue rendering and autonomous-turn affordances

The queue UI already supports:

- force steering
- reorder up/down
- edit
- delete

That behavior should remain unchanged for controller-owned turns. The only UI
addition needed is origin-aware rendering.

Extend queued turn payloads with:

```json
{
  "id": "turn_123",
  "origin": "controller",
  "goalId": "goal_abc",
  "statusReason": "scheduled_from_goal",
  "preview": "Continue by validating the latest benchmark changes and summarizing whether p95 is below target."
}
```

Rendering rules for `origin=controller`:

- show badge: `Autonomous`
- show subtitle or tooltip: `Scheduled from active goal`
- keep existing actions:
  - `Edit`
  - `Move up/down`
  - `Delete`
- hide or disable the current `Steer` action for controller-owned items
- do **not** introduce a `Run now` label in v1

Rationale:

- the current `Steer` button in
  [`ui/src/components/chat/SteerQueue.jsx`](../agently/ui/src/components/chat/SteerQueue.jsx)
  calls `chat.forceSteerQueuedTurn`
- that backend path is defined for forcing a queued turn into a running-turn
  steering flow, not for "execute this queued turn immediately while idle"
- a true `Run now` affordance needs a dedicated backend endpoint or queue
  promotion semantics and should be introduced in a later phase

V1 behavior should remain:

- user-owned queued turns behind a running turn may use `Steer`
- controller-owned queued turns may use `Edit`, `Move`, and `Delete`
- when the conversation is idle, controller-owned queued turns should be picked
  up by the normal queue runner, not by a bespoke UI button

Feature-gate rules:

- if `queue.enabled=false`, submitting new work while a turn is active should
  not create queued turns; runtime should return a deterministic "queue
  disabled" response
- if `steering.enabled=false`, explicit steering endpoints/buttons must be
  hidden or rejected
- if `steering.forceEnabled=false`, force-steer affordances must stay hidden

This means the queue card becomes the single practical override surface for
autonomy without introducing a fake action that the backend cannot yet honor.

### 13.4 Transcript and feed behavior

The current chat runtime already pushes queued turns into the local `queue` tool
feed in
[`ui/src/services/chatRuntime.js`](../agently/ui/src/services/chatRuntime.js).
Goal-driven queued turns should reuse that feed instead of creating a separate
goal feed.

Rules:

- do not render controller continuation prompts as visible user bubbles
- do render controller-owned queued turns in `queuedTurns`
- do include `origin`, `goalId`, and `statusReason` in queue feed rows
- do not require the transcript renderer to infer goal state from queue state

The transcript remains transcript-owned; the queue remains next-step-owned.

Implementation note:

- backend queue rows must expose `origin`, `goalId`, and `statusReason`
- `buildCanonicalTranscriptRows()` in
  [`ui/src/services/renderRows.js`](../agently/ui/src/services/renderRows.js)
  must preserve those fields in `turnPreview()`
- `onFetchQueuedTurns()` in
  [`ui/src/services/chatService.js`](../agently/ui/src/services/chatService.js)
  should pass them through unchanged into the queue datasource collection
- the local `queue` feed payload emitted by `chatRuntime.js` must include the
  same fields so queue cards and any other queue surfaces render consistently

### 13.5 UI refresh and event ownership

Goal state should be refreshed from explicit goal events and datasource refresh,
not inferred from optimistic client mutations alone.

Recommended ownership:

- **server owns goal truth**
  - goal CRUD API responses
  - `goal.updated`
  - `goal.cleared`
- **queue API / datasource owns next-step truth**
  - queued turn rows
  - controller-owned queued turn deletion or invalidation

Client rules:

- after any goal mutation, refresh `meta` and `queueTurns`
- after any queue mutation, refresh `queueTurns`
- if `goal.cleared` arrives, clear any local "goal active" badge immediately
- if `goal.updated` changes status away from `active`, disable controller-owned
  continuation affordances until queue state refreshes

Event wiring:

- add goal stream handlers in the same client event pipeline that currently
  handles transcript and feed refresh in
  [`ui/src/services/chatRuntime.js`](../agently/ui/src/services/chatRuntime.js)
- route those events into a small goal-state holder or `goalBus`
- the settings panel and queue surfaces should consume the goal holder rather
  than scraping queue state for lifecycle truth

V1 event types:

- `goal.updated`
- `goal.cleared`

Optional later type:

- `goal.controller_scheduled`

### 13.6 Deletion and cleanup semantics

The current draft talks about invalidation, but the UI and backend need explicit
cleanup rules.

Recommended lifecycle rules:

1. **Clear goal**
   - delete all queued turns with `origin=controller` for that conversation
   - preserve user-owned queued turns
   - preserve historical transcript rows

2. **Pause goal**
   - do not schedule any new controller continuations
   - preserve existing controller-owned queued turns, but mark them stale
   - stale rendering means:
     - badge: `Paused`
     - disable any force-run / steer affordance
     - keep `Edit` and `Delete`
     - optionally keep reorder if queue semantics still allow it

3. **Complete goal**
   - delete all controller-owned queued turns
   - leave transcript and linked child conversations intact

4. **Blocked / usage-limited / budget-limited**
   - suppress further autonomous scheduling
   - preserve queued controller turns only if they are still actionable under
     the new state; otherwise drop them server-side
   - non-actionable rendering means:
     - badge: `Blocked`, `Budget limited`, or `Usage limited`
     - disable continuation-specific actions
     - keep `Delete`

5. **New user turn**
   - invalidate stale controller-owned queued turns for the same conversation
     unless a future policy explicitly marks them persistent
   - this invalidation must be **server-side**
   - the UI should only observe the change through `queueTurns` refresh and
     goal events; submit handlers should not attempt to implement invalidation
     heuristics locally

6. **Conversation deletion**
   - cascade delete goal row
   - cascade delete controller-owned queued turns
   - apply the same tree deletion rules already used for conversation cleanup

7. **Conversation switch / reload**
   - hydrate goal state from the goal datasource or goal event snapshot
   - in v1, hydrate read state from `meta.goal`
   - hydrate queue state from `queueTurns`
   - never reconstruct controller-owned turns from transcript heuristics

### 13.7 Initial UI implementation order

For the UI portion specifically, the safest implementation order is:

1. add read-only goal summary to settings
2. add `Create / Pause / Resume / Clear` actions
3. add queue origin badge + stale-state rendering rules
4. add deletion / invalidation wiring
5. add stream event wiring for `goal.updated` / `goal.cleared`
6. add optional header indicator later

This keeps state entry and state observation in one place before adding a
second surface in the topbar or conversation header.

### 13.8 Command activation surface

If command-style activation is added later, it should also obey feature gates:

- `/goal` only when `goals.enabled=true`
- `/followup` only when `followups.enabled=true`
- queue/steer subcommands only when their workspace gates are enabled

---

## 14. Controller implementation sketch

```go
type Runtime interface {
    Apply(ctx context.Context, event GoalRuntimeEvent, input *RuntimeInput) error
}
```

Internal flow:

```go
snapshot, err := BuildSnapshot(...)
action, err := Evaluate(snapshot, event)
err = applyAction(action, input)
```

`applyAction()` behavior:

- `queue_turn` -> insert queued turn with `origin=controller` and kick
  execution server-side
- `pause_goal` -> persist paused status
- `block_goal` -> persist blocked status
- `complete_goal` -> persist complete status
- `budget_limited` -> persist budget-limited status
- `usage_limited` -> persist usage-limited status
- `none` -> return

---

## 15. Accounting model

If we add goals, we should also add first-class accounting from day one.

At minimum:

- token usage attributed to turns while a goal is active
- elapsed time while a goal is active

Transitions:

- `budget_limited` when token budget is exceeded
- `usage_limited` when provider/runtime constraints stop progress
- `paused` when user interrupts or explicitly pauses
- `blocked` when the controller or model determines outside input is required
- `complete` when the model or user marks the objective done

This is the main feature that chains do not currently provide.

### 15.1 Runtime accounting details

The implementation should include:

- **per-turn token baseline**
  - reset at turn start
  - account only deltas since the last goal-accounted point
- **wall-clock baseline**
  - reset on status changes and external mutations
- **budget-limit dedup**
  - if the controller injects a budget-limit warning once, it should not inject
    it repeatedly on every later accounting event
- **continuation lock**
  - ensure only one continuation scheduling decision is in flight
- **accounting lock**
  - ensure goal usage and status transitions are applied atomically

Without these, the controller will be noisy, race-prone, and difficult to make
deterministic under concurrent async completion and user steering.

---

## 16. Phased delivery

### Phase 1: durable goal state + model-visible goal tools

- add goal persistence
- add goal CRUD API
- add `goal:get`, `goal:create`, `goal:update`
- no autonomous continuation yet

### Phase 2: accounting + controller-generated queued turns

- add accounting and lifecycle event wiring
- controller evaluates after turn completion / async completion / queue drain
- queue autonomous continuation turns when idle
- mark queued turn origin as `controller`

### Phase 3: ordering with existing chains + invalidation

- keep chain execution on the existing path
- define ordering between chain execution and goal continuation
- add invalidation rules when user work appears

### Phase 4: external controls + UI integration

- add settings-panel goal controls
- add queue origin badges and stale-state rendering
- add goal events and hydration wiring

### Phase 5: budget / blocked / usage lifecycle polish

- refine token and time accounting
- controller-enforced stop conditions
- better messaging around why autonomy stopped
- add metrics / observability

---

## 17. Non-goals

- replacing existing chain execution
- replacing queued turns
- building a general workflow engine
- introducing nested autonomous controller trees in v1
- automatically synthesizing chain YAML into the workspace
- making every conversation autonomous by default
- making the controller choose between chain execution and queued continuation
  as if they were the same primitive

---

## 18. Recommendation

Implement an **Autonomous System** as a new layer above the current follow-up
system.

Do not redefine chains as goals.

The clean split should be:

- **Goal** = durable objective state
- **Controller** = policy for whether to continue
- **Queue** = visible next-step override surface
- **Chain** = specialized follow-up execution primitive

That gives Agently:

- the practical follow-up behavior it already does well
- a path to Codex-like durable autonomy
- without collapsing everything into a single overloaded concept

---

## 19. Initial file targets

Likely first implementation files:

- new `pkg/agently/goal/*`
- new `service/goal/runtime.go`
- new `service/goal/accounting.go`
- new `service/goal/continuation.go`
- new goal tool package under `protocol/tool/service/system/goal/*` or
  equivalent
- hook controller evaluation from
  [`service/agent/run_query.go`](service/agent/run_query.go)
- extend queue/turn metadata in conversation projection
- extend queue row shape in
  [`ui/src/services/renderRows.js`](../agently/ui/src/services/renderRows.js)
- extend queue fetch passthrough in
  [`ui/src/services/chatService.js`](../agently/ui/src/services/chatService.js)
- add goal API routes in `sdk/handler.go` and related handlers
- add goal summary + controller-owned queue badges in `agently` UI

---

## 20. Open questions

1. Should controller-generated continuations always become queued turns first,
   or may they execute immediately when the conversation is idle?
   Recommendation: queue first for transparency.

2. Goal cardinality is fixed in v1: exactly one current goal per conversation.
   No goal history is kept.

3. Should blocked detection be model-driven, controller-rule-driven, or both?
   Recommendation: both, but start with explicit user/model signals.

4. Should chain execution count against the parent goal budget?
   Recommendation: yes, when triggered by the controller under an active goal.
