# Autonomous goals & continuation

The autonomous subsystem gives a conversation one durable goal and lets the
runtime continue work after each turn using the existing queue machinery. It
does **not** introduce a second execution system: goals are persisted in the
conversation store, exposed through the normal internal tool mechanism, and
continued by enqueuing ordinary turns with controller metadata.

This doc is the feature-level overview of the current autonomous
implementation in `agently-core`.

## Key packages

| Path | Role |
|---|---|
| [service/goal/](../service/goal/) | Goal domain model, controller policy, runtime evaluation |
| [protocol/tool/service/system/goal/](../protocol/tool/service/system/goal/) | Internal `system/goal:*` tool service |
| [pkg/agently/goal/](../pkg/agently/goal/) | Datly-backed goal persistence |
| [service/agent/goal_runtime.go](../service/agent/goal_runtime.go) | Agent-side continuation enqueue bridge + controller snapshot signal gathering |
| [service/agent/run_query.go](../service/agent/run_query.go) | Post-turn hook into goal runtime |
| [pkg/agently/turn/controllerCount/](../pkg/agently/turn/controllerCount/) | Count of controller-owned turns (AutonomousTurnsUsed) |
| [pkg/agently/toolapprovalqueue/pendingCount/](../pkg/agently/toolapprovalqueue/pendingCount/) | Count of pending tool approvals (PendingApproval) |
| [pkg/agently/message/elicitationCount/](../pkg/agently/message/elicitationCount/) | Count of unresolved elicitations (PendingElicitation) |
| [pkg/agently/turn/write/](../pkg/agently/turn/write/) | Queue-turn persistence, including controller metadata |
| [sdk/handler_goals.go](../sdk/handler_goals.go) | Conversation goal HTTP handlers |
| [sdk/http.go](../sdk/http.go) | `/v1/conversations/{id}/goal` client routes |
| `../agently/bootstrap/defaults/tools/bundles/autonomous.yaml` | Default autonomous umbrella bundle (sibling `agently` repo) |
| `../agently/bootstrap/defaults/tools/bundles/system_goal.yaml` | Goal-specific bundle definition (sibling `agently` repo) |
| `../agently/runtime/tool_plugins.go` | Internal tool registration (sibling `agently` repo) |

## What exists today

- one durable goal per conversation
- internal goal tools:
  - `system/goal:get`
  - `system/goal:create`
  - `system/goal:update`
  - `system/goal:pause`
  - `system/goal:resume`
  - `system/goal:clear`
- conversation goal REST surface:
  - `GET /v1/conversations/{id}/goal`
  - `POST /v1/conversations/{id}/goal`
  - `PATCH /v1/conversations/{id}/goal`
  - `DELETE /v1/conversations/{id}/goal`
- post-turn goal accounting + controller evaluation
- controller-owned queued turns carrying:
  - `origin`
  - `goalId`
  - `statusReason`
- default workspace exposure for `coder` and `chatter`
- visible goal surfaces in web and mobile clients
- a user-facing `/goal` composer command (web) as a thin adapter over the goal API
- truthful controller snapshot inputs derived from durable state
  (`PendingApproval`, `PendingElicitation`, `AutonomousTurnsUsed`)
- workspace metadata capability gating for goals via `features.goals.enabled`

## Data model

The goal is conversation-scoped, not workspace-scoped.

Important persisted fields:

- `id`
- `conversation_id`
- `objective`
- `status`
- `status_reason`
- `pause_reason`
- `controller_spec`
- `token_budget`
- `tokens_used`
- `time_used_seconds`

The current implementation intentionally keeps **one goal per conversation**
with no first-class history system.

## Runtime flow

```text
user turn finishes
  -> service/goal/runtime accounts usage
  -> controller evaluates goal + snapshot
  -> if continuation is allowed
       enqueue ordinary queued turn
       mark it origin=controller, goalId=..., statusReason=...
  -> normal queue runner executes that turn
```

Two design choices matter:

- Goals do not bypass the existing queue.
- Chains remain separate from goals. Declared follow-up chains still use the
  existing follow-up path; the autonomous controller only decides goal
  lifecycle and same-conversation continuation.

The continuation hint is no longer always the generic objective fallback. The
agent runtime now prefers stronger signals, in order:

1. an explicit continuation hint emitted by the last turn content (for example
   native tool-result continuation guidance such as overflow/message-show hints)
2. the first meaningful step from `QueryOutput.Plan`
3. the generic goal fallback from `service/goal/runtime.go`

That improves both queued-turn previews and the restart-safe
`last_continuation_fingerprint` / `consecutive_no_progress` guard, because the
fingerprint can now reflect real next-step changes instead of only the static
goal objective.

Async completion policy is now partially live as well. During an in-flight turn
that is waiting on async tool work, the rerun-after-change path now respects
the goal controller's `OnAsyncCompleted` policy:

- `evaluate` keeps the current behavior and reruns the turn when async results
  arrive and the turn still needs another pass
- `wait` suppresses that automatic rerun once async work has actually
  completed, leaving the turn on its existing terminal content/plan path

This is still narrower than the full long-horizon wakeup model the subsystem
can grow into, but it means the persisted async policy is no longer inert in
the current runtime.

Detached async completions can now also re-enter the autonomous goal path after
the original turn is over. For non-wait async operations, the agent runtime
registers a one-shot async completion observer. When that op later reaches a
successful terminal state:

- if there is no active turn for the conversation
- and the goal is still active
- and `OnAsyncCompleted=evaluate`
- and the existing controller idle predicates still pass

the runtime runs the same goal controller evaluation and may enqueue one
controller-owned continuation turn. That closes the gap where detached async
work previously finished without any autonomous follow-up.

Delayed wakeups are now partially implemented too. The controller spec supports
an optional `wakeDelaySeconds`, and when it is set on an otherwise autonomous
goal, the controller returns a delayed wakeup action instead of queueing an
immediate controller turn. The current implementation uses hidden internal
scheduler rows:

- internal schedule rows are filtered out of public schedule / schedule-run
  views
- public scheduler `get`, `delete`, `run-now`, and public `upsert` paths now
  also treat those internal wakeup rows as reserved runtime state instead of
  exposing or mutating them by id
- the wakeup row persists the target conversation, goal, display preview, and
  hidden continuation payload
- when the scheduler fires, the agent re-enters the same conversation instead
  of creating a new scheduled conversation
- delayed wakeups therefore require the existing scheduler watchdog / runner to
  be active in the deployment, just like other scheduled runs
- workspace config now bounds delayed wakeups through `features.wakeups`:
  - `enabled`
  - `maxGlobalWakeupsPerHour`
  - `maxConversationWakeups`
  - `maxGoalWakeups`
  - `minWakeDelaySeconds`
  - `maxWakeDelaySeconds`
- when wakeups are enabled, requested delays are clamped into the configured
  min/max range before a wake row is written
- the scheduler now also enforces the current global / conversation / goal
  wakeup budgets before writing a new hidden wake row
- when wakeups are disabled by workspace policy, the runtime falls back to the
  existing immediate controller-owned queue continuation instead of dropping the
  autonomous step
- before executing the delayed wakeup turn, the agent re-checks that:
  - the goal still exists
  - the goal is still active
  - there is no active turn
  - the queue is empty
  - no pending approval / elicitation / async work should keep the
    conversation blocked

Any normal non-scheduled turn for the conversation also cancels pending
delayed wakeups before the new work proceeds, so stale wakeups do not survive
fresh activity.

Pending wakeups are now also canceled when the goal leaves its active state
through either path:

- controller-owned deactivation (`pause`, `blocked`, `budget_limited`,
  `usage_limited`, `complete`)
- manual goal mutation through the conversation goal API (`update`, `pause`,
  `resume`, `clear`, or objective edit)
- manual tool-surface mutation through `system/goal` (`update`, `pause`,
  `resume`, `clear`)

That bridge now also suppresses duplicate continuation bursts when several
detached async operations complete near-simultaneously for the same
conversation. A conversation-scoped guard ensures only one completion handler
re-enters the controller at a time; later completions see the updated queue
state instead of racing to enqueue multiple near-identical controller turns.

The shared controller enqueue path now also refuses to add a new controller turn
when the conversation already has a queued controller-owned turn for the same
goal. That makes the “one current queued continuation per goal” invariant hold
even outside the async-completion race case.

## Tool contract

`system/goal` is an internal tool service, so it is only visible when assigned
through the existing bundle / tool-item mechanism. There is no special goal
exposure path.

When the workspace disables goals with `features.goals.enabled=false`, the
runtime now enforces that policy on the server side too:

- `system/goal` is not registered as an internal service
- conversation goal REST handlers return a feature-disabled error path through
  the embedded backend
- direct embedded goal client calls fail deterministically instead of silently
  succeeding behind a hidden UI

Important behavior:

- `create` fails if a goal already exists for the conversation
- `update` is restricted to terminal model-facing states:
  - `complete`
  - `blocked`
- `update` requires a durable `reason`
- `pause`, `resume`, and `clear` are explicit operations rather than a wide
  “set any status” API
- tool-side conversation identity is resolved from request context, not from
  model-supplied arguments

## Queue metadata

Controller-created queued turns are ordinary turn rows plus metadata:

- `origin=controller`
- `goalId=<goal id>`
- `statusReason=<why this continuation was queued>`

That metadata flows through:

- turn persistence
- canonical transcript / reducer state
- streaming events
- web queue preview rendering

This lets clients distinguish:

- manual queued turns
- controller-owned continuation

without inventing a second queue system.

The runtime now also emits dedicated goal lifecycle stream events:

- `goal.updated`
- `goal.cleared`
- `goal.controller_scheduled`

These sit alongside queue/transcript events and make autonomous goal state
changes observable without forcing every consumer to infer them indirectly from
transcript reloads alone.

The web shell and SDK stream trackers now treat these as live updates to the
conversation goal feed. That means goal state can refresh immediately from
streaming events, not only from explicit `GET /goal` refreshes or transcript
rehydration.

`goal.controller_scheduled` now also distinguishes the controller scheduling
mode in its patch payload:

- `mode=queue` for immediate controller-owned queued turns
- `mode=wakeup` for delayed scheduler-backed goal wakeups

Delayed wakeups additionally include `wakeAt`, so clients can surface “resume
later” state without inferring it indirectly from scheduler tables.

The web goal summary strip and goal feed panel now read that metadata and show
whether the controller queued the next step immediately or scheduled the goal
to resume later.

iOS and Android goal summary cards now surface the same delayed-resume state
from the streamed goal feed metadata, so mobile clients can also show when an
active goal is scheduled to resume later rather than only exposing raw goal
status and usage counters.

That mobile parity is now also live-update parity:

- Android already read the delayed-resume metadata from the live stream
  snapshot
- iOS now does as well, instead of waiting for the next hydrated conversation
  state refresh

iOS and Android app runtimes now also project streamed `goal` feed snapshots
into `activeGoal`, so mobile goal cards refresh immediately from
`goal.updated`, `goal.cleared`, and `goal.controller_scheduled` without waiting
for a full conversation reload.

Normal goal reads now also carry delayed-resume metadata durably, not only via
stream patches. The embedded conversation goal API and `system/goal` read/write
surfaces project a pending hidden wakeup into `goal.controllerSchedule`, so a
client that reloads after a disconnect can still see that an active goal is
scheduled to resume later.

The scheduler-side wakeup lifecycle is now also covered as a single flow:
while a hidden wakeup is still in the future it projects as pending controller
schedule state, and once it becomes due the scheduler resumes the same
conversation and clears that pending wakeup projection.

That same lifecycle is now also proven through the public conversation goal
HTTP contract. `GET /v1/conversations/{id}/goal` exposes
`goal.controllerSchedule` while the wakeup is pending and returns the same goal
without `controllerSchedule` after the scheduler consumes the wakeup and
resumes the conversation.

## Controller snapshot inputs

After a successful turn, `maybeContinueActiveGoal` builds the controller
[`Snapshot`](../service/goal/controller.go) from durable state rather than the
former hardcoded `false`/`0` placeholders. `gatherControllerSignals` reads the
counts through a narrow capability interface on the concrete data service (the
same pattern used for `PatchTurnQueue`), so the shared `data.Service` interface
is not widened and any missing capability or query error degrades to the safe
zero value:

| Snapshot field | Source | Query |
|---|---|---|
| `QueuedUserTurns` | `turn` table | existing `CountQueuedTurns` |
| `PendingApproval` | `tool_approval_queue` where `status='pending'` | `CountPendingApprovals` |
| `PendingElicitation` | `message` where `elicitation_id` set and `status='pending'` | `CountPendingElicitations` |
| `AutonomousTurnsUsed` | durable goal row controller state | persisted `autonomous_turns_used` |
| `ConsecutiveNoProgress` | durable goal row controller state | persisted `consecutive_no_progress` |
| `PendingAsync` | in-memory async manager | conversation-scoped non-terminal op list |

Each count is a small Datly read component that mirrors the existing
`turn/queuedCount` predicate pattern.

The controller state that must survive restart now lives on the goal row:

- `autonomous_turns_used`
- `consecutive_no_progress`
- `last_continuation_fingerprint`

`ConsecutiveNoProgress` is currently driven by a restart-safe progress
fingerprint stored in `last_continuation_fingerprint`. The runtime now prefers
to fingerprint the actual turn outcome:

- assistant content
- summarized plan steps
- continuation hint fields

If that combined fingerprint repeats on a later autonomous turn, the counter
increments; otherwise it resets to `0`. That is still lighter than a fully
semantic progress model, but it is more truthful than hashing only the static
goal objective or only the continuation hint text.

## Client surfaces

### Web

The active conversation refreshes a local goal feed from the conversation goal
API, queued controller turns render with an `AUTO` badge plus reason, and the
goal feed now exposes visible lifecycle actions for the current goal.

The current web UX is intentionally hybrid:

- a dedicated always-visible goal summary strip in the chat shell
- the goal feed as the richer create/edit/pause/resume/clear surface
- the `/goal` composer command as the fast power-user path
- a dedicated guided goal-drafting thin for creating a new autonomous goal

The dedicated summary strip, the goal feed actions, and the `/goal` command are
all gated by the workspace metadata capability `capabilities.goals`. That
capability is only advertised when:

- workspace config keeps `features.goals.enabled=true` (default bootstrap value)
- the workspace actually exposes goal tools to its agents
- and the workspace keeps `system/goal` enabled in its internal MCP service set

Users drive the goal lifecycle from the chat composer with a thin `/goal`
command that adapts onto the existing conversation goal API (no new backend
mechanism). The conversation id always comes from the active conversation
context, never from the command text:

- `/goal` or `/goal show` — show the current goal
- `/goal set <objective>` (or `/goal <objective>`) — create the goal, or update
  the objective when one already exists
- `/goal pause` — set status `paused`
- `/goal resume` — set status `active`
- `/goal clear` — remove the goal

The command is handled in `submitMessage` before agent submission via
`parseGoalCommand` / `handleGoalCommand` in
`../agently/ui/src/services/chatService.js`.

The current web goal feed also exposes small direct actions over the same
conversation goal API:

- `Create` when no goal exists
- `Edit`
- `Pause`
- `Resume`
- `Clear`

When no goal exists, the web shell now exposes a structured drafting dialog
from the summary strip. It gives the user preset autonomous-goal headers and a
multiline template rather than forcing a single-line objective field. This is a
product-level thin only; it still persists through the same conversation goal
API and does not create a second goal subsystem.

The web surfaces now carry clearer visual hierarchy than the initial
implementation:

- status renders as a semantic pill instead of a raw lowercase token
- token/time usage is visible at a glance in the summary strip and feed panel
- token-budget goals show a progress bar
- the summary strip exposes quick `Pause` / `Resume` actions for the live goal
- destructive `Clear` from the feed panel now goes through confirmation

The goal feed now also has an opted-in generic feed path: when the feed payload
supplies `ui.renderMode=forge` plus `ui.containers`, `ToolFeedDetail` renders
the goal panel through the shared Forge container machinery instead of only
through bespoke React markup. The current implementation keeps the custom goal
panel as a fallback, so existing goal-feed behavior remains stable while the
generic mechanism becomes usable for goal-specific panels.

The default workspace now ships that goal panel as an actual feed spec in the
sibling app repo (`bootstrap/defaults/feeds/goal.yaml`). The client refresh
path only needs to publish the current goal data; the backend feed registry can
serve the schema and Forge container metadata like other default feeds.

Primary files:

- `../agently/ui/src/components/GoalSummaryStrip.jsx`
- `../agently/ui/src/services/chatRuntime.js`
- `../agently/ui/src/services/chatService.js`
- `../agently/ui/src/services/renderRows.js`
- `../agently/ui/src/components/chat/SteerQueue.jsx`
- `../agently/ui/src/components/ToolFeedDetail.jsx`

### iOS / Android

Mobile clients load `activeGoal` with conversation state and render a goal
summary card in the workspace pane. Live streamed `goal` feed snapshots now
also update `activeGoal` in place, so visible mobile goal state stays aligned
with lifecycle stream events instead of waiting for the next full transcript
refresh.

Like the web shell, mobile only renders the goal summary/create cards when the
workspace metadata advertises `capabilities.goals=true`.

Current mobile interaction surface:

- `Create` when no goal exists
- `Edit`
- `Pause`
- `Resume`
- `Clear`

These actions call the same conversation goal API used by web and then reload
conversation state.

The mobile goal cards now mirror more of the improved web hierarchy:

- semantic status chips
- visible token/time usage
- budget progress bars when a token budget exists
- humanized time instead of raw seconds
- destructive clear confirmation before the goal is removed

Primary files:

- `../agently/ios/Sources/AgentlyAppFoundation/App/AppRuntime.swift`
- `../agently/ios/Sources/AgentlyAppFoundation/Workspace/GoalSummaryCard.swift`
- `../agently/android/app/src/main/java/com/viant/agently/android/AppRuntime.kt`
- `../agently/android/app/src/main/java/com/viant/agently/android/MainActivity.kt`
- `../agently/android/app/src/main/java/com/viant/agently/android/GoalSummaryCard.kt`

## Bootstrap and availability

Default workspace bootstraps now include:

- internal MCP service `system/goal`
- tool bundle `system/goal`
- default `coder` and `chatter` agent bundle references

So the feature is available through the same assignment path as every other
internal capability.

## Current limits

- only one goal per conversation
- the `/goal` command is still web-only
- the web goal action surface is still lightweight even though it now includes
  a dedicated summary strip plus feed actions (`Create`, `Edit`, `Pause`,
  `Resume`, `Clear`) in addition to the composer command
- mobile has basic lifecycle and objective editing/create actions but no richer
  dedicated goal editor
- continuation payloads are still relatively simple compared to the richer
  controller policy the subsystem can grow into

## Extensibility

- **Add richer controller policy**: extend [service/goal/spec.go](../service/goal/spec.go) and
  [service/goal/controller.go](../service/goal/controller.go).
- **Add explicit UI controls**: extend the web composer command into dedicated
  buttons/menus, or bring goal actions to mobile, always over the existing
  conversation goal API rather than a second mutation path.
- **Refine delayed wakeups**: the current implementation supports hidden
  scheduler-backed delayed resume for autonomous goals with workspace
  enable/min/max bounds. Future work can add richer quotas, explicit
  wake-reason policy, and more UI/observability over the same runtime-owned
  wakeup state.
- **Refine progress semantics**: today repeated continuation fingerprints drive
  `ConsecutiveNoProgress`. If the product later needs a more semantic notion of
  progress, evolve that signal rather than adding another controller-state
  subsystem.

## Related docs

- [followup-chains.md](followup-chains.md) — existing multi-turn chain system
- [tool-system.md](tool-system.md) — tool bundles and internal tool dispatch
- [conversation-model.md](conversation-model.md) — persisted turn / conversation state
- [streaming-events.md](streaming-events.md) — event projection for queue/UI updates
- [sdk.md](sdk.md) — client surfaces that expose the conversation goal API
