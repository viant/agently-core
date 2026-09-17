# Root-turn restart recovery: minimal implementation plan

Status: implemented in the working tree; SQLite/focused package tests pass. MySQL smoke coverage is present and runs when `AGENTLY_TEST_MYSQL_DSN` is configured.

## 1. Objective

After an Agently instance restarts, reclaim the interrupted root interactive turn and let the existing ReAct loop resume it.

Recovery is bootstrap, not a second executor or retry engine:

1. Find the interrupted root run.
2. Atomically claim that same run.
3. Reconstruct the loop input and durable history.
4. Re-enter the existing `runPlanLoop` at the interrupted boundary.
5. Let the existing model, tool, continuation, finalization, and queue behavior operate normally.

Do not create a new conversation, turn, run, or user message. Do not call ordinary `Query` with an empty request. Preserve the existing invariant that an interactive `run.id` and `turn.id` are the same identity.

The only new behavior is the internal control-flow seam that enters an already-admitted turn at a restored phase. All work behind that seam must use existing primitives.

## 2. Hard constraints

- Retry only the root turn. Do not recursively recover child conversations.
- Reuse existing sparse PATCH components for every mutation.
- Add no tables, columns, indexes, migrations, or embedded schema changes.
- Add no direct SQL, `database/sql`, driver-specific store, or custom transaction path.
- SQL/DQL changes are limited to extending existing Datly predicates/views and the condition supported by the existing run PATCH component.
- Prefer the least amount of code necessary.
- Do not add synchronous database/network round trips to the normal Query or ReAct request path, and do not increase ordinary service response time.
- Do not introduce `executor_*.go` files in `service/agent`. If execution is ever extracted into several files, create a cohesive executor package. This implementation should first reuse the loop in place and avoid that extraction.

## 3. Existing behavior to preserve

The repository already has the execution and retry behavior required after recovery:

- `service/agent/run_query.go:runPlanLoop` is the ReAct continuation loop. It rebuilds binding/history, asks the model for the next plan, processes tool results, handles steering/elicitation/async state, and continues until terminal.
- `service/core/generate.go` and `service/core/stream.go` already perform safe model/provider retries.
- `service/shared/toolexec/tool_executor_retry.go` already applies the configured transient tool retry policy.
- `service/agent/run_turn.go` already owns heartbeat, finalization, and queue-drain handoff.
- `service/agent/watchdog.go` already scans stale runs, restores auth, bounds recovery concurrency, and limits attempts.

Restart recovery must not duplicate any of those policies. Once the same turn is reconstructed, the existing loop owns execution again.

Primitive reuse is explicit:

| Recovery need | Existing primitive |
| --- | --- |
| Find interrupted work | watchdog plus stale-run Datly view |
| Claim/heartbeat run | existing run sparse PATCH with an added optional condition |
| Restore history/current turn | `BuildBinding` and `binding.History` |
| Parse a completed model tool plan | reactor's existing response-to-plan logic |
| Execute missing plan steps | reactor pending-step launcher and `toolexec.ExecuteToolStep` |
| Retry model/tool transport | existing core/toolexec retry policies |
| Persist calls/results | existing model-call/tool-call/message/payload sparse PATCH components |
| Continue reasoning | existing `runPlanLoop` |
| Finish | existing turn/run/conversation finalization and queue drain |

## 4. Existing persistence is sufficient

Use only existing fields and records:

| State | Existing source |
| --- | --- |
| Root conversation | `conversation.conversation_parent_id` |
| Interactive/scheduled ownership | `run.conversation_kind`, `run.schedule_id` |
| Turn/run identity | `run.id`, `run.turn_id`, `turn.run_id` |
| Active/terminal state | run and turn `status` |
| Ownership | `run.lease_owner`, `run.lease_until` |
| Activity | `run.last_heartbeat_at`, `started_at`, `created_at` |
| Loop position | `run.iteration` plus persisted model/tool calls |
| Recovery attempts | `run.attempt` |
| Agent/model | existing run fields |
| Auth | `security_context`, effective user/auth fields |
| User request, steering, history, and last turn | existing `BuildBinding` / `binding.History` projection |
| Durable call boundary | persisted model calls, tool calls, and payload links |
| Optional checkpoint hints | existing `checkpoint_*` fields |

Do not add a recovery table, execution-instance table, generation column, cancel column, resume snapshot, journal, or outbox.

`lease_owner` is the execution fence. Use a new opaque value for every normal execution admission and every restart claim. It can include the process boot identity for diagnostics, but the complete value must be unique per claim.

## 5. Minimal Datly changes

### 5.1 Extend the stale-run read view

Update the canonical `dql/agently/run/run_stale.dql` and regenerate `pkg/agently/run/stale`.

Add predicates needed to select:

- `status = running`;
- interactive, unscheduled runs;
- root conversations only;
- expired lease;
- activity within `default.recovery.lookbackHours`, default 24 hours;
- newest activity first with a stable cursor and bounded page size.

Remove the current same-host restriction for restart recovery. Any pod using the shared Datly store may recover an expired root run.

This remains an existing Datly read component. Do not add a direct query in `app/store/data`.

### 5.2 Add an optional condition to the existing run PATCH

Extend the existing generated run sparse PATCH contract so one run mutation may include expected values:

```go
type RunPatchCondition struct {
    Status     string
    LeaseOwner *string
    Attempt    *int
}
```

Required semantics:

- Existing `PatchRuns` callers omit the condition and behave exactly as today.
- The stale view establishes expiry. A recovery claim matches the run ID, `running` status, observed lease-owner token, and observed attempt.
- The same conditional PATCH writes the new lease owner, lease expiry, heartbeat, and incremented attempt.
- Exactly one affected row means the claim succeeded.
- Zero affected rows means another owner/state change won; recovery stops without side effects.

Expose this through `app/store/data` using the same Datly run PATCH component. Do not implement the condition with handwritten SQL or a read-then-unconditional-write sequence.

If Datly cannot express this atomic conditional sparse update, stop at that capability gap. Do not expand the schema or introduce direct SQL as a workaround.

Lease timestamps are written as UTC, truncated to whole seconds. MySQL `DATETIME` carries no timezone, so UTC is the application contract; second precision avoids driver-dependent fractional precision differences across MySQL and SQLite. Timestamp equality is not used for fencing.

Every heartbeat rotates `lease_owner` while conditionally matching the previous token. Therefore a heartbeat that wins after a stale scan changes the token and makes the stale recovery claim match zero rows. The token, not timestamp equality, is the atomic renewal/claim fence.

## 6. Recovery algorithm

### 6.1 Scan and claim

Reuse the existing watchdog rather than introducing a coordinator.

1. On startup, scan immediately and continue on the existing watchdog interval so runs whose leases have not yet expired are not missed.
2. Read root interactive candidates from the extended stale-run Datly view.
3. For each candidate, load its current turn and reject scheduled, child, canceled, terminal, superseded, or ambiguous state.
4. Generate a new lease-owner token.
5. Conditional sparse-PATCH the same run:
   - preserve its ID and turn ID;
   - set the new lease owner and lease expiry;
   - set `last_heartbeat_at`;
   - increment `attempt` once.
6. Continue only if that PATCH claimed one row.

This is the only new admission primitive. Competing pods are resolved by the existing run row.

The competing-pod sequence is:

1. Pods A and B may read the same expired candidate.
2. Each generates a different lease-owner token.
3. Each submits the same existing run sparse PATCH with the observed `status`, rotating `lease_owner` token, and `attempt` as conditions. Expiry is established by the stale Datly view; timestamp equality is not part of the mutation fence.
4. Datly applies the condition and mutation atomically.
5. One PATCH affects one row and owns the run; the other affects zero rows and exits without reconciliation, binding, model, or tool work.
6. If the winning response is lost, that pod reads the run once in the background and continues only when its exact proposed token is present. It never issues an unconditional second claim.

Recovery scanning and claims run in the watchdog goroutine with bounded pages, bounded concurrency, and jitter. Startup does not wait for recovery, and request handlers never participate in claim contention.

### 6.2 Restore from the last known model/tool call

Use `run.iteration` and the persisted model/tool-call rows as the resume cursor. Load the root run steps ordered by iteration and durable call order, then find the last known call in the highest iteration. Use the existing binder for all transcript/history/last-turn projection; do not build a second recovery-specific history. Recovery does not invent a new checkpoint and does not execute a tool or model itself.

| Last durable state | Resume action |
| --- | --- |
| No call exists for the recorded iteration | Enter `runPlanLoop` at that iteration. |
| Model call has no `completed_at`, or is `thinking`/`streaming` | Treat the entire model attempt as uncommitted. Sparse-PATCH it as interrupted/canceled, keep its assistant message interim, exclude every tool directive emitted by that partial response from reconstructed history, and enter the loop at the same iteration. The normal model layer performs a fresh call and owns its retry policy. |
| Model call completed with no tool calls | Rebuild history from its durable assistant message and let the loop determine whether the turn is terminal or should continue. Do not call the model again for this iteration. |
| Model call completed with planned tools that have no `tool_call` row | Reconstruct the durable plan and execute only those never-started tool calls through the existing reactor/tool executor. Do not call the model again for this iteration. |
| Model call completed and some planned tools are already terminal | Reconstruct the durable plan, retain completed results, and execute only planned operations with no started `tool_call` row. |
| Model call completed and every emitted tool call has a durable terminal result | Rebuild history including those tool results and enter the next iteration. Do not replay the plan. |
| A tool result is durable but its terminal status/link projection is incomplete | Repair only the missing persisted status/link with the existing sparse PATCH, then enter the next iteration. Do not invoke the tool again. |
| A tool call is `running` without a durable result | Its external outcome is unknown. Do not replay it; block/fail the root turn with a typed safe error. |
| Approval/user input is pending | Preserve the wait; do not enter executable loop work. |
| A delegated child is unresolved | Block the root turn; do not recover or mutate the child. |

Completed model and tool calls remain untouched and are consumed by the existing binding/history code. Only an incomplete persisted record is minimally patched so the existing loop sees an unambiguous history.

The model completion boundary is authoritative: a partial streamed tool directive is not a tool to resume. It is abandoned with the incomplete model message and will be regenerated, changed, or omitted by the retried model call.

There is one safety exception. In the current executor, a `tool_call` row is persisted as `running` immediately before external tool execution. Therefore, if an allegedly incomplete model attempt already has a persisted started tool-call row, recovery must inspect it rather than merely hide it:

- terminal durable tool result: retain/reconcile the result;
- `running` with no durable result: block because the external outcome is unknown;
- no tool-call row: safely ignore the partial model-emitted directive and retry the model iteration.

For a completed model call, use the durable provider/model response payload to reconstruct the exact tool plan and match operations by tool-call/op ID:

- terminal tool-call row with durable result: skip execution and reuse the result;
- approval-queued/waiting operation: preserve the wait and skip execution;
- no tool-call row: the operation was planned but never started, so execute that operation only;
- `running` tool-call row without a durable result: do not execute it again because its outcome is ambiguous.

This means restart may resume inside an iteration at its tool phase. It does not restart the completed model phase.

### 6.3 Internal backdoor into the existing ReAct loop

Add one narrow, recovery-only internal entry point in `service/agent`:

```go
func (s *Service) resumeTurn(ctx context.Context, req resumeTurnRequest) error
```

It is deliberately unexported and is called only by the claimed-root watchdog path. It bypasses ordinary `Query`, intake, routing, new-turn admission, and `startTurn`; it does not bypass binding, the reactor, tool execution, retry policies, heartbeat, ownership checks, or finalization.

`resumeTurnRequest` contains only persisted identity/state needed to restart the loop:

```go
type resumeTurnRequest struct {
    ConversationID string
    TurnID         string
    LeaseOwner     string
}
```

`resumeTurn` loads identity/runtime fields through existing readers, then enters the existing loop through a small shared helper:

```go
type planLoopStart struct {
    Iteration int
    Phase     planLoopPhase // model or tools
    Plan      *execution.Plan
}

func (s *Service) runPlanLoopFrom(
    ctx context.Context,
    input *QueryInput,
    output *QueryOutput,
    start planLoopStart,
) error
```

The existing `runPlanLoop` remains the normal entry and delegates with the zero/default start:

```go
func (s *Service) runPlanLoop(ctx context.Context, input *QueryInput, output *QueryOutput) error {
    return s.runPlanLoopFrom(ctx, input, output, planLoopStart{})
}
```

The backdoor supplies a recovered iteration/phase; it does not fork or copy the loop. `runPlanLoopFrom` calls the existing `BuildBinding` path. The binder remains authoritative for:

- current/root turn and starter request;
- past and current-turn messages;
- steering and elicitation state;
- model/tool-result history and trace links;
- resource/attachment references;
- active skill history and projection rules;
- `History.Current`, `CurrentTurnID`, `LastResponse`, and trace state.

Run agent/model/provider/auth fields and the current same-ID agent definition are resolved through their existing readers before binding. The only recovery-specific lookup outside the binder is the durable run-step status needed to choose model phase versus tool phase. For a completed model with never-started tools, read its already-persisted response payload only to recover the exact planned operation IDs, names, and arguments; do not use it to reconstruct general conversation history.

The backdoor must not run intake, classify an agent, ingest uploads, create a starter message, queue a new turn, or call `startTurn`.

### 6.4 Resume the existing loop at the recovered phase

`resumeTurn` derives the boundary from persisted calls and chooses one of two paths inside the existing loop:

1. **Model phase:** an unfinished model attempt at iteration `N` is discarded as a whole, and `runPlanLoop` starts the model phase again at `N`.
2. **Tool phase:** a completed model attempt at iteration `N` is reconstructed from its durable response. The reactor executes only never-started planned tool calls, using the same `toolexec.ExecuteToolStep` path as normal execution. After all known tool results are durable, `runPlanLoop` continues at `N + 1`.

Expose one narrow adapter on the existing reactor service, for example:

```go
func (s *Service) ResumePlan(ctx context.Context, plan *execution.Plan, completed map[string]llm.ToolCall) ([]llm.ToolCall, error)
```

This is not a new execution primitive. It is a thin adapter over the reactor's existing response-to-plan parsing, pending-step launcher, and `toolexec.ExecuteToolStep` path. It filters the durable plan by operation ID, reuses completed results, and passes only never-started steps into those existing functions. It must not invoke the model. Keep it in the existing `service/reactor` package; do not create another executor.

The loop must rebuild binding and history through its existing code. Do not add a parallel prompt builder, transcript merger, or retry engine.

On backdoor entry:

1. Put the existing turn metadata into context.
2. Put the claimed run/lease token into execution context.
3. Start the existing heartbeat.
4. If the model completed, resume its pending tool plan and then continue the loop at the next iteration; otherwise restart that model iteration.
5. Use existing finalization and queue drain.

New model output receives a new model-message ID. The root conversation, turn, run, and starter-message IDs remain unchanged.

## 7. Lease checks during resumed execution

Heartbeat renewal uses the conditional run PATCH with the expected lease-owner token and writes a fresh token plus a UTC, second-precision expiry. An asynchronous read-back promotes the proposed token into the in-memory guard only when it became durable. A different token means ownership was lost; cancel local execution. Database/read failures do not extend the local conservative stop deadline. An independent local timer cancels execution at the conservative deadline even if the heartbeat goroutine stalls.

Do not add a Datly read before every model/tool dispatch. The existing heartbeat goroutine maintains an in-memory ownership guard containing the last successfully renewed token and a conservative local stop deadline. Model/tool dispatch checks that local guard only:

- token still matches the execution context;
- the last conditional renewal succeeded;
- the local stop deadline has not passed;
- the existing local cancellation context is still active.

The heartbeat itself is the authoritative database check. Replace its current unconditional run PATCH with a conditional PATCH; do not add another heartbeat call. Finalization adds the expected lease-owner condition to its existing run PATCH; do not add a pre-finalization read. Existing turn cancellation behavior remains unchanged in V1.

This handles dead-instance restart and competing recovery pods. It does not promise recovery from a live split-brain/partitioned owner or exactly-once external side effects.

### 7.1 No ordinary-response latency increase

Normal execution must retain the same number of synchronous persistence operations:

- Initial run admission already performs a run PATCH; generate and set the unique lease token in that same PATCH.
- Iteration updates already perform a run PATCH; carry the current token in that same update where needed.
- Heartbeat already runs asynchronously; make that existing PATCH conditional.
- Finalization already performs a run PATCH; add the condition to that same PATCH.
- Dispatch checks use only the in-memory lease guard and add no I/O.
- Stale scanning, claim contention, ambiguous-claim verification, and resume binding occur only in the background recovery path.

The conditional predicate may change database work inside an existing write, but it must not add a request-path round trip, lock wait across network/model/tool work, or synchronous recovery scan. Keep recovery concurrency separately bounded so it cannot exhaust the normal request worker pool or database connection pool.

### 7.2 Web transport behavior

- A browser that already owns an active turn reconnects EventSource after a server-process restart. Reconnect does not fetch transcript.
- Opening a CLI-created active conversation from history performs its normal one-time canonical hydration, then immediately subscribes to SSE.
- A React/chat remount that closes the previous context's subscription immediately transfers the hydrated active turn to a replacement SSE subscription; it does not wait for polling and does not fetch transcript.
- Terminal model/turn events settle the UI from SSE. Transcript hydration remains reserved for explicit navigation/opening history.

## 8. File-level change list

| File/area | Minimal change |
| --- | --- |
| `dql/agently/run/run_stale.dql` | Extend root/interactive/lease/lookback/page predicates. |
| `pkg/agently/run/stale/**` | Regenerate; do not hand-edit. |
| Canonical run write definition and `pkg/agently/run/write/**` | Add optional conditional sparse-PATCH predicate. |
| `app/store/data/service.go` | Expose conditional run PATCH through Datly. No SQL. |
| `service/agent/watchdog.go` | Claim and resume the same root run; remove successor-run plus empty-`Query` behavior. |
| `service/agent/resume.go` | Add the unexported recovery-only `resumeTurn` backdoor and reconstruction using existing readers/binding. |
| `service/agent/run_query.go` | Make existing `runPlanLoop` delegate to `runPlanLoopFrom`, which accepts an optional restored iteration/phase while keeping `BuildBinding` authoritative. |
| `service/agent/run_turn.go` | Make heartbeat conditional on the lease-owner token. |
| Existing `service/reactor` plan/stream implementation | Add a narrow `ResumePlan` method that executes only never-started steps through existing tool execution. |
| `../agently/ui/src/services/chatRuntime.js` | Reconnect SSE without transcript hydration and immediately reattach active hydrated turns after chat remount/conversation switch. |

No executor package is required for this first implementation because the existing loop stays in place. If later refactoring produces several executor concerns, use a dedicated package such as `service/agent/executor/{executor.go,request.go,result.go,lease.go}` rather than `executor_*.go` files in `service/agent`.

## 9. Implementation order

1. Extend the stale-run Datly view and tests.
2. Add conditional semantics to the existing run sparse PATCH and prove one-winner behavior on SQLite and MySQL.
3. Make normal and resumed heartbeat use unique lease-owner tokens and conditional renewal.
4. Add reactor `ResumePlan`, internal `resumeTurn`, and `runPlanLoopFrom` restored phase/iteration support.
5. Replace the watchdog's new-run/empty-`Query` path with the same-run internal backdoor.
6. Add the minimal interrupted model/tool reconciliation described above.
7. Run process-restart fault tests at model, tool, and finalization boundaries.

## 10. Required tests

- Stale view includes only expired root interactive runs inside the lookback.
- Two pods race to claim one run; one conditional PATCH wins and only that pod resumes.
- A lost claim response is resolved by reading the proposed token once; no duplicate/unconditional claim occurs.
- A source heartbeat racing a stale claim rotates the token and defeats that claim.
- The same conversation, turn, run, and starter IDs survive restart.
- Resume history and last-turn state come from `BuildBinding`; no recovery-specific transcript reconstruction exists.
- The recovery backdoor is unexported, called only after a successful root claim, and does not call ordinary `Query`, `startTurn`, intake, routing, or upload ingestion.
- Interrupted model generation re-enters the same logical iteration through `runPlanLoop`.
- Tool directives from a model call without `completed_at` are excluded from resumed history and are not dispatched.
- An incomplete model call with a persisted `running` tool-call row blocks unless a durable terminal result exists.
- A completed model call is not invoked again; only planned operations without a `tool_call` row execute.
- Mixed tool plan resumes only missing operations, reuses terminal results by op ID, and then advances the loop.
- Durable tool results appear in reconstructed history and the loop advances.
- Running tool with unknown outcome is not replayed.
- Approval/user wait and unresolved child do not resume.
- Wrong/expired lease token stops heartbeat and prevents new dispatch.
- Normal Query, model dispatch, tool dispatch, and finalization add zero synchronous store calls compared with the baseline.
- Recovery uses a separate bounded worker/semaphore and does not block server startup or request workers.
- Static operation-count review confirms no additional synchronous store/network call on ordinary Query, model dispatch, tool dispatch, or finalization; recovery and lease verification remain background work.
- Existing model and tool retry tests remain unchanged and pass.
- Existing normal Query, scheduled watchdog, queue, and sparse PATCH tests remain unchanged and pass.
- No schema or migration diff and no new direct SQL/database handle.
- MySQL smoke test uses `parseTime=true`, UTC location/session, and whole-second timestamps; it runs when `AGENTLY_TEST_MYSQL_DSN` is configured.
- UI-owned live restart: the open browser reconnects SSE, receives recovered events, and reaches terminal/Ready without transcript fetch.
- CLI-owned live restart: explicit history open hydrates the active turn once, immediately subscribes SSE (including after remount), receives recovered deltas/terminal event, and reaches Ready.
- Conversation-switch regression suite remains green.

## 11. Acceptance criteria

- Only the interrupted root turn is considered.
- Recovery resumes the existing ReAct loop rather than implementing another loop or retry policy.
- The backdoor composes existing binder, reactor, tool executor, sparse PATCH, heartbeat, and finalization primitives; it adds no alternative execution path.
- No new persistent structures.
- Every mutation uses an existing sparse PATCH component.
- The only query work is extending existing Datly predicates/views.
- Multi-pod claim contention has one winner.
- Competing-pod recovery adds no synchronous I/O to normal service responses and no additional ordinary request-path persistence calls.
- Unknown tool side effects are never replayed.
- No successor run/turn and no empty user query.
- Active-turn SSE reconnect performs no transcript fetch; history open may hydrate once and then becomes SSE-owned.
- No `executor_*.go` file sprawl.

That is the complete V1. Streaming journal work, child recovery, generic queue repair, new UI recovery state, recovery-resolution APIs, and full split-brain fencing are intentionally excluded.
