# Execution Service Proposal

## Objective

Introduce a dedicated execution service that treats the `run` table as the
canonical execution queue for all agent work.

Today, execution ownership is split across scheduler goroutines, agent query
paths, and resume/stale-run watchdogs. That makes restart recovery and
ownership harder to reason about because multiple services can decide whether a
run should continue, fail, or be resumed.

The target model is:

```text
schedule decides when work should exist
run records that work exists
execution service performs the work
```

The execution service should own:

- claiming runnable `run` rows
- setting execution start/runtime metadata
- heartbeating active runs
- executing the agent turn
- recovering unfinished runs after restart
- marking terminal run state
- eventually replacing scheduler and agent watchdog execution logic

The scheduler should become a producer of scheduled `run` rows. Interactive
query paths should become producers of interactive `run` rows. The execution
service should be the single consumer.

## Current State

The current codebase already has most of the required durable fields in `run`:

- `id`
- `turn_id`
- `schedule_id`
- `conversation_id`
- `conversation_kind`
- `status`
- `attempt`
- `resumed_from_run_id`
- `agent_id`
- `model_provider`
- `model`
- `worker_id`
- `worker_pid`
- `worker_host`
- `lease_owner`
- `lease_until`
- `last_heartbeat_at`
- `heartbeat_interval_sec`
- `security_context`
- `auth_authority`
- `auth_audience`
- `effective_user_id`
- `user_cred_url`
- `scheduled_for`
- `started_at`
- `completed_at`

The main gap is not persistence. The gap is ownership.

Current ownership is split:

- `service/scheduler` evaluates schedules, inserts runs, launches goroutines,
  heartbeats scheduled run leases, and reaps stale scheduled runs.
- `service/agent` creates interactive run records and heartbeats them.
- `service/agent/watchdog.go` can resume stale interactive runs, but it is a
  separate recovery loop and is not the same owner as scheduled execution.
- `sdk/handler.go` starts the scheduler watchdog when configured.

The execution service should unify these paths without changing the durable
contract into something fuzzy or UI-derived.

## Design Principles

- The `run` row is the durable execution contract.
- Execution recovery must use exact `run.id` and lease fields.
- No transcript-based guessing to decide whether a run should execute.
- No model/provider/tool-name fallback matching for recovery.
- Auth restoration is part of execution, not a downstream best effort.
- MCP tool management must remain scoped by exact user and conversation.
- Scheduler produces work; executor consumes work.
- Interactive APIs produce work; executor consumes work.
- A run is executable only when its persisted state says it is executable.
- Leases are the concurrency and ownership boundary across processes.
- Restart recovery should be deterministic and bounded by configuration.

## Execution Service

Add a new package:

```text
service/execution
```

Suggested primary type:

```go
type Service struct {
    store Store
    agent *agent.Service
    tokenProvider token.Provider

    leaseOwner string
    leaseTTL time.Duration
    heartbeatEvery time.Duration
    pollEvery time.Duration
    recoveryLookback time.Duration
    maxConcurrentRuns int
    batchSize int
}
```

The service starts with:

```go
bootstrapTime := clock.Now().UTC()
recoverySince := bootstrapTime.Add(-recoveryLookback)
```

Default `recoveryLookback` should be `15m`.

Recovery is intentionally bounded. On bootstrap, the execution service should
only recover unfinished runs whose execution time falls on or after
`recoverySince`. It should not sweep all historical unfinished rows by default.
Older unfinished rows remain visible for diagnostics or manual repair, but they
are outside automatic recovery unless the operator increases
`recoveryLookback` or triggers an explicit administrative recovery.

Configuration should allow:

- `default.execution.enabled`
- `default.execution.pollEvery`
- `default.execution.recoveryLookback`
- `default.execution.leaseTTL`
- `default.execution.heartbeatEvery`
- `default.execution.maxConcurrentRuns`
- `default.execution.batchSize`
- `default.execution.mode`

Possible env overrides:

- `AGENTLY_EXECUTION_ENABLED`
- `AGENTLY_EXECUTION_POLL_EVERY`
- `AGENTLY_EXECUTION_RECOVERY_LOOKBACK`
- `AGENTLY_EXECUTION_LEASE_TTL`
- `AGENTLY_EXECUTION_HEARTBEAT_EVERY`
- `AGENTLY_EXECUTION_MAX_CONCURRENT_RUNS`
- `AGENTLY_EXECUTION_BATCH_SIZE`
- `AGENTLY_EXECUTION_MODE`
- `AGENTLY_EXECUTION_LEASE_OWNER`

## Runnable Run Selection

The execution service needs an explicit store query. Do not reuse stale-run
queries that encode watchdog-specific behavior.

The query must enforce the bootstrap recovery cutoff:

```text
recoverySince = bootstrapTime - recoveryLookback
```

With the default configuration, that means:

```text
recover unfinished runs from the last 15 minutes
```

This window is a safety boundary, not a stale-run heuristic. It prevents a new
executor from reviving old abandoned rows while still covering the normal
restart case where Agently stops halfway through a run and comes back shortly
afterward.

Suggested store contract:

```go
type Store interface {
    ListRunnableRuns(ctx context.Context, input *RunnableRunsInput) ([]*RunView, error)
    GetRun(ctx context.Context, runID string) (*RunView, error)
    TryClaimRun(ctx context.Context, runID, leaseOwner string, leaseUntil time.Time) (bool, error)
    ReleaseRunLease(ctx context.Context, runID, leaseOwner string) (bool, error)
    PatchRuns(ctx context.Context, rows []*RunPatch) error
}
```

`service/execution` should define its own `RunView` and `RunPatch` types. The
Datly adapter can translate to and from generated `pkg/agently/run/*` views, but
the new execution service contract should not expose generated write view types
such as `runwrite.MutableRunView`.

`RunPatch` should express execution-owned mutations, not generated-row details:

```go
type RunPatch struct {
    ID string
    ExpectedLeaseOwner string
    Status *RunStatus
    StartedAt *time.Time
    CompletedAt *time.Time
    LeaseOwner *string
    LeaseUntil *time.Time
    LastHeartbeatAt *time.Time
    ConversationID *string
    Attempt *int
    ErrorMessage *string
}
```

The store adapter is responsible for translating this patch into the current
Datly write shape and for preserving compare-and-set guards such as
`ExpectedLeaseOwner`.

`RunView` must expose enough state for the worker to decide how to execute after
claiming without doing another classification read:

```go
type RunView struct {
    ID string
    TurnID string
    ScheduleID string
    ConversationID string
    ConversationKind string
    Status RunStatus
    Attempt int
    ResumedFromRunID string
    AgentID string
    EffectiveUserID string
    AuthAuthority string
    AuthAudience string
    SecurityContext string
    UserCredURL string
    ScheduledFor *time.Time
    CreatedAt time.Time
    StartedAt *time.Time
    CompletedAt *time.Time
    LeaseOwner string
    LeaseUntil *time.Time
    LastHeartbeatAt *time.Time
}
```

The claim response should either return the claimed `RunView` or the worker
should classify from the pre-claim `RunView` returned by `ListRunnableRuns`.
Fresh `pending` work and expired-lease `running` work are both runnable, but
they are not the same execution mode.

Suggested `RunnableRunsInput`:

```go
type RunnableRunsInput struct {
    Now time.Time
    RecoverySince time.Time
    LeaseOwner string
    Limit int
}
```

Runnable SQL should be explicit:

```sql
SELECT t.*
FROM run t
WHERE t.completed_at IS NULL
  AND t.status IN ('pending', 'queued', 'prechecking', 'running')
  AND COALESCE(t.started_at, t.scheduled_for, t.created_at) >= :recovery_since
  AND (t.scheduled_for IS NULL OR t.scheduled_for <= :now)
  AND (
    t.lease_until IS NULL
    OR t.lease_until < :now
    OR t.lease_owner = :lease_owner
  )
ORDER BY COALESCE(t.scheduled_for, t.created_at), t.created_at
LIMIT :limit
```

The query should not join transcript state or infer from messages.

## Execution Loop

The execution service loop:

1. Load runnable rows since `recoverySince`.
2. For each row, try to claim the run lease.
3. If claim succeeds, submit to a bounded worker pool.
4. Worker patches run status to `running`, sets `started_at` if missing, and
   writes worker metadata.
5. Worker starts heartbeat updates for `lease_until` and `last_heartbeat_at`.
6. Worker restores auth/security context from the row.
7. Worker restores MCP execution identity from the row.
8. Worker executes the agent query.
9. Worker patches terminal run state.
10. Worker releases the run lease.

While executing, the worker must periodically re-read the run status. If another
actor marks the row terminal, for example a user cancels the run while the
worker still holds the lease, the worker must abort promptly and leave the
externally written terminal status intact. This check should happen in the same
heartbeat loop that extends `lease_until`, and it should cancel the execution
context passed into `agent.Query`, model calls, MCP tool calls, and wait-mode
blocks. Cancellation must not depend on a long-running model/tool call finishing
naturally.

Claiming should be atomic:

```sql
UPDATE run
SET lease_owner = :owner,
    lease_until = :lease_until
WHERE id = :run_id
  AND completed_at IS NULL
  AND status IN ('pending', 'queued', 'prechecking', 'running')
  AND (
    lease_until IS NULL
    OR lease_until < :now
    OR lease_owner = :owner
  )
```

## Run Status Model

Normalize success status in Phase 1. The executor should write one canonical
success status and all runnable/terminal queries should use the same status set.

Recommended status constants:

```go
const (
    RunPending     RunStatus = "pending"
    RunQueued      RunStatus = "queued"
    RunPrechecking RunStatus = "prechecking"
    RunRunning     RunStatus = "running"
    RunSucceeded   RunStatus = "succeeded"
    RunFailed      RunStatus = "failed"
    RunSkipped     RunStatus = "skipped"
    RunCanceled    RunStatus = "canceled"
    RunInterrupted RunStatus = "interrupted"
)
```

Treat `completed` as a legacy alias on read, but do not write it from the new
execution service. Normalize `completed` to `succeeded` in `RunView` or migrate
existing rows before executor rollout. This needs to happen before the runnable
query ships; otherwise one component can write `completed` while another checks
only `succeeded`, creating future reclaim bugs.

Until all persisted rows are migrated, the shared terminal-status helper must
treat stored `completed` as terminal even though `RunView.Status` exposes the
canonical `succeeded` value.

Runnable statuses:

- `pending`
- `queued`
- `prechecking`
- `running`

Terminal statuses:

- `succeeded`
- `failed`
- `skipped`
- `canceled`
- `interrupted`

## Execution Semantics

### Pending and Queued Runs

For `pending` or `queued` rows, the execution service should execute the row
directly.

It should patch:

- `status = running`
- `started_at = now` when missing
- `worker_host`
- `worker_pid`
- `lease_owner`
- `lease_until`
- `last_heartbeat_at`

### Running Runs After Restart

For a `running` row with expired lease, there are two possible strategies.

Preferred long-term strategy:

- Reclaim the same `run.id`.
- Continue execution under that durable run identity.
- Patch heartbeat and worker metadata.

Compatibility strategy:

- Create a new run with `resumed_from_run_id = old.id`.
- Mark the old run failed with a message like `worker died, resumed as <new>`.
- Execute the new run.

The compatibility strategy matches the current `service/agent/watchdog.go`
behavior. The long-term strategy is simpler and makes `run.id` the durable
identity for the work. If current agent continuation requires a fresh message or
turn identity, use compatibility first and migrate toward same-run resume once
agent continuation can honor the persisted run identity directly.

Terminal rows should have `completed_at` set. The executor must never claim
terminal rows, including legacy rows whose stored status is `completed`.

## Auth and Token Saturation

Execution recovery must restore the same auth context that the original run had.
This is a source-of-truth requirement, not a convenience for downstream tools.

The executor should restore auth in this order:

1. Restore `run.security_context` when present.
2. Restore `effective_user_id`, `auth_authority`, and `auth_audience` into the
   runtime context.
3. Resolve provider from restored security data, `auth_authority`, or the
   configured auth default.
4. Call the shared `token.Provider.EnsureTokens` with exact
   `(subject, provider)` when a subject is known.
5. For scheduled runs with `user_cred_url`, apply that credential path through
   the existing scheduler/user credential auth flow.
6. If a run requires user auth and no token can be restored, mark the run failed
   or auth-blocked explicitly. Do not silently execute as anonymous.

Token refresh saturation is a real risk during bootstrap recovery. A restart can
make many runs for the same user become runnable at once, and each run may need
fresh MCP or OAuth tokens. The executor must use the shared token provider rather
than refreshing tokens directly.

Required token behavior:

- Use the existing per-key in-process serialization in `internal/auth/token`.
- Use persistent token-store refresh leases when a token store is configured.
- Use CAS token writes so only the lease owner publishes a refreshed token.
- Respect negative miss caching so missing tokens do not create repeated store
  scans and logs.
- Bound executor concurrency globally and, if needed, add a per-auth-key
  concurrency cap for recovered runs.
- Treat refresh failure as run state, not a reason to downgrade identity.

The executor should pass scheduler/run discovery metadata into the context before
calling token restore paths so token diagnostics remain attributable to the exact
schedule and run.

## MCP Tool Management

MCP clients and tool discovery are scoped by user and conversation. The current
MCP manager caches clients by `(userID:conversationID, serverName)` when a user
extractor is configured. That is the correct boundary and the executor must
preserve it during recovery.

Before executing or recovering a run, the executor must put these values back
into context:

- `conversation_id`
- `turn_id`
- `run.id`
- effective user id
- auth provider/audience when known
- scheduler mode metadata for scheduled runs
- schedule id and schedule run id for scheduled runs

Recovered execution must not create a synthetic MCP conversation scope for a run
that already has `conversation_id`. Synthetic discovery scopes are acceptable for
isolated discovery helper flows, but not for recovered execution of a persisted
run.

MCP rules:

- Tool discovery must use the restored conversation id.
- MCP client pooling must use the restored user id and conversation id.
- Cookie jar selection must use the restored user id.
- Auth round-tripper selection must use the restored user id and tokens.
- Tool execution must use the same restored context as discovery.
- If the user/token context cannot be restored, do not attach the run to another
  user's MCP client or anonymous MCP client.
- Detail hydration and tool result association must keep exact run, turn, and
  tool-call identity.

For scheduled runs, `schedule_id` is not enough. The executor also needs the
user identity and conversation identity that own the MCP session. A scheduled run
without a conversation id at start may create one, but once the conversation id
is known it must be persisted back to the run before any later recovery depends
on it.

That write-back is critical path. At the moment the executor learns or creates a
conversation id for a scheduled run, it must patch `run.conversation_id` before
any MCP manager lookup, MCP discovery, MCP tool execution, async wait, or
recovery path can depend on the run. If a scheduled run reaches MCP work before
the conversation id is persisted, recovery cannot reliably restore the correct
`userID:conversationID` MCP manager key.

## Scheduler Integration

### Current Embedded Scheduler

The current scheduler evaluates due schedules and then calls execution directly
through `enqueueAndLaunch` and `executeRun`.

The first migration should split this into:

- scheduler due detection
- scheduler run production
- execution service run consumption

`RunDue` should remain responsible for:

- schedule lease claim
- due-time computation
- duplicate slot protection
- inserting a `run` row with `status = pending`
- advancing `schedule.next_run_at`
- updating schedule bookkeeping when needed

`RunDue` should stop owning:

- agent execution
- run heartbeat
- stale scheduled run terminalization
- direct execution goroutine lifecycle

### Separate Scheduler Process

There is also a separate-process scheduler path in e2e/bootstrap-style apps.
Do not change it immediately.

Down the line, the same rule should apply:

```text
separate scheduler process = producer only
execution process = consumer only
```

The separate scheduler process should be able to run without an agent runtime if
all it does is evaluate schedules and insert due `run` rows. That makes it
deployable as a lightweight cron/scheduler node.

If the separate scheduler still embeds a runtime for other reasons, it should
not use that runtime to execute scheduled work directly once the execution
service is enabled.

## Interactive Query Integration

Interactive query paths should also produce `run` rows.

Short term:

- Keep `agent.Query` synchronous for API compatibility.
- Ensure it creates and updates a `run` row as it does today.
- Start execution service for restart recovery of rows that were interrupted.

Long term:

- HTTP/API query creates a `run` row.
- Execution service claims and executes it.
- Synchronous API waits on run completion or streams run events.
- Async API returns run/conversation identity immediately.

The long-term model removes the distinction between "request goroutine" and
"execution goroutine." The request path becomes a producer and observer.

## Watchdog Deprecation

The execution service should replace both current watchdog responsibilities.

### Scheduler Watchdog

Current role:

- polls schedules
- starts scheduler due pass
- scheduler due pass may execute runs directly
- scheduler stale cleanup handles old scheduled runs

Deprecation path:

1. Keep scheduler watchdog as the schedule due poller.
2. Change scheduler due pass to produce runs only.
3. Move run stale cleanup into execution service.
4. Rename scheduler watchdog behavior to `schedule poller` or keep it scoped to
   schedule evaluation only.

The scheduler poller can remain, but it should not be a run executor.

### Agent Resume Watchdog

Current role:

- finds stale interactive runs
- restores security context
- creates resume runs
- marks old runs failed
- resumes agent query

Deprecation path:

1. Move stale interactive run selection into `ListRunnableRuns`.
2. Move auth/security restoration into execution service.
3. Move resume execution into execution service.
4. Keep `agent.Watchdog` behind a compatibility flag for one release.
5. Disable it by default once execution service is enabled.
6. Remove it after migrations and tests prove execution service coverage.

Suggested flags:

- `default.execution.enabled = true`
- `default.recovery.legacyAgentWatchdog = false`
- `default.scheduler.legacyExecuteInline = false`

During migration, only one owner may execute a run. If execution service is on,
legacy watchdog execution paths must be off.

## Deployment Modes

### Embedded Mode

The API/server process starts:

- scheduler poller, if configured
- execution service, if configured
- HTTP/API handlers

This is simplest for local and small deployments.

### Serverless Mode

Serverless workers can run the execution service as short-lived claim-and-drain
invocations.

Behavior:

- Invocation starts.
- Computes `recoverySince = now - recoveryLookback`.
- Claims up to `batchSize` runnable rows.
- Executes until batch complete or invocation deadline nears.
- Heartbeats by extending `lease_until`.
- Exits without requiring in-memory ownership after completion.

The lease TTL is the safety boundary. If a serverless invocation dies, another
invocation can claim the run after `lease_until + grace`.

Serverless-specific config:

- shorter `leaseTTL`
- shorter `heartbeatEvery`
- bounded `maxRunDuration`
- bounded `batchSize`
- optional `drainUntilDeadline`

Recommended initial defaults:

- `leaseTTL = 2m`
- `heartbeatEvery = 30s`
- `recoveryLookback = 15m`
- `batchSize = 5`
- `maxConcurrentRuns = 2`

Serverless should not rely on process-local watchdog state. It should rely only
on leases and persisted run state.

### Dedicated Node Mode

A dedicated execution node runs continuously.

Behavior:

- Long-running process starts execution service.
- Polls every `pollEvery`.
- Claims runnable rows.
- Executes with bounded concurrency.
- Heartbeats until terminal state.

Dedicated-node config:

- `execution.mode = dedicated`
- `pollEvery = 5s` or `10s`
- `leaseTTL = 1m` to `5m`
- `heartbeatEvery = leaseTTL / 2`
- `maxConcurrentRuns` sized to host capacity

Multiple dedicated nodes can run concurrently. The run lease prevents duplicate
execution.

### Producer-Only Scheduler Node

A dedicated scheduler node can run without executing work:

- polls schedule table
- inserts due `run` rows
- advances next schedule timestamps
- does not call `agent.Query`

This is the desired future shape for separate-process scheduler deployments.

## Configuration Sketch

Workspace config:

```yaml
default:
  execution:
    enabled: true
    mode: dedicated
    pollEvery: 5s
    recoveryLookback: 15m
    leaseTTL: 2m
    heartbeatEvery: 30s
    maxConcurrentRuns: 4
    batchSize: 20

  scheduler:
    enabled: true
    executeInline: false
    pollEvery: 30s
```

Serverless override:

```yaml
default:
  execution:
    enabled: true
    mode: serverless
    recoveryLookback: 15m
    leaseTTL: 2m
    heartbeatEvery: 30s
    maxConcurrentRuns: 2
    batchSize: 5
```

Dedicated scheduler-only node:

```yaml
default:
  scheduler:
    enabled: true
    executeInline: false
    pollEvery: 30s

  execution:
    enabled: false
```

Dedicated executor-only node:

```yaml
default:
  scheduler:
    enabled: false

  execution:
    enabled: true
    mode: dedicated
```

## Implementation Plan

### Phase 1: Execution Service Skeleton

- Add `service/execution`.
- Define execution-owned `RunView`, `RunPatch`, and `RunStatus` types.
- Normalize success state: write `succeeded`, treat `completed` as legacy read
  alias, and update runnable/terminal status sets consistently.
- Add config defaults and env overrides.
- Add service lifecycle: `Start(ctx)`, `DrainOnce(ctx)`, `ClaimAndExecute(ctx)`.
- Add worker identity and lease owner generation.
- Add bounded concurrency.

### Phase 2: Store Support

- Add Datly component for `ListRunnableRuns`.
- Add tests for:
  - pending run selected
  - queued run selected
  - running expired-lease run selected
  - running fresh-lease run skipped
  - runnable `RunView` exposes status, prior lease owner, attempt, and resume id
  - terminal run skipped
  - legacy `completed` run treated as terminal
  - scheduled future run skipped
  - scheduled due run selected

### Phase 3: Execute Interactive Runs

- Move core execution call into execution service.
- Preserve existing `agent.Query` public contract.
- Ensure request path can wait for run completion without bypassing run
  ownership.
- Add restart recovery test for interactive `running` row.

### Phase 4: Execute Scheduled Runs

- Change scheduler `enqueueAndLaunch` to insert run only when execution service
  is enabled.
- Persist `run.conversation_id` immediately when the scheduled execution creates
  or learns the conversation id.
- Keep legacy inline execution behind a compatibility flag.
- Add tests that due schedule creates one pending run and executor consumes it.
- Add a test that scheduled MCP/tool execution persists `conversation_id` before
  recovery or later MCP manager lookup depends on it.

### Phase 5: Deprecate Watchdogs

- Disable agent resume watchdog when execution service is enabled.
- Move scheduler stale-run cleanup to execution service.
- Rename remaining scheduler watchdog concepts to schedule polling.
- Remove direct scheduled execution after compatibility window.

### Phase 6: Separate Process and Serverless Support

- Add command/bootstrap support for executor-only mode.
- Add scheduler-only mode.
- Add serverless `DrainOnce` entry point.
- Document deployment examples.

## Verification Plan

Focused tests should cover:

- run claim atomicity
- no duplicate execution across two execution services
- restart recovery within `recoveryLookback`
- stale run outside `recoveryLookback` is not reclaimed automatically
- worker aborts when a leased run is externally marked `canceled`
- worker aborts when a leased run changes to any terminal status it did not
  write
- execution service writes `succeeded`, not `completed`
- legacy `completed` rows are terminal and never reclaimed
- worker classifies pending-vs-resume behavior from returned `RunView`
- recovered run restores `security_context` before execution
- many recovered runs for one user trigger one coordinated token refresh path
- missing user tokens fail or block explicitly and never execute anonymously
- MCP discovery uses restored user id and conversation id
- recovered MCP tool execution reuses the exact conversation-scoped manager key
- scheduled run persists `conversation_id` before MCP/tool recovery depends on
  it
- serverless drain respects lease TTL and terminal state
- scheduler produces due run without executing it
- scheduled run is executed by execution service
- interactive run resumes after process restart
- legacy watchdog disabled when execution service enabled

Integration tests should cover:

- embedded API process with scheduler and executor enabled
- separate scheduler node plus separate executor node
- two executor nodes competing on the same database
- serverless-style repeated `DrainOnce` invocations

## Open Decisions

- Should restarted `running` rows execute under the same `run.id`, or
  should they create a child run with `resumed_from_run_id` first?
- Should schedule due detection stay in `service/scheduler`, or should it move
  into a more general producer framework later?
- Should `execution.mode=serverless` prevent long-running `Start(ctx)` usage and
  expose only `DrainOnce(ctx)`?

## Recommendation

Start with a conservative migration:

1. Build execution service and runnable-run store query.
2. Wire it in embedded mode behind `default.execution.enabled`.
3. Use it first for recovery of unfinished interactive runs.
4. Change scheduler to produce-only behind `scheduler.executeInline=false`.
5. Move separate-process scheduler to producer-only after embedded behavior is
   proven.

This keeps the change rooted in the current durable `run` contract while moving
the system toward a single execution owner.
