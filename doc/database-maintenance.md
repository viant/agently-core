# Database Maintenance

Database maintenance provides bounded retention and orphan-repair primitives
for SQLite and MySQL. `agently-core` owns candidate selection, transactional
revalidation, mutation, and lease fencing. The Agently application owns the
periodic worker and its environment configuration.

This maintenance path complements the user-authorized hard-delete operations
described in [conversation-deletion.md](conversation-deletion.md) and
[schedule-deletion.md](schedule-deletion.md).

## Key files

| File | Role |
|---|---|
| [`service_delete_maintenance.go`](../app/store/data/service_delete_maintenance.go) | Interactive and scheduled-fallback conversation graph revalidation |
| [`service_delete_maintenance_candidates.go`](../app/store/data/service_delete_maintenance_candidates.go) | Bounded interactive and scheduled-fallback root selection |
| [`service_delete_scheduled_maintenance.go`](../app/store/data/service_delete_scheduled_maintenance.go) | Current and legacy scheduler-run retention |
| [`service_technical_maintenance.go`](../app/store/data/service_technical_maintenance.go) | Report runtime metadata and expired-session retention |
| [`service_orphan_maintenance.go`](../app/store/data/service_orphan_maintenance.go) | Static safe-delete, safe-detach, and report-only orphan rules |
| [`service_maintenance_lease.go`](../app/store/data/service_maintenance_lease.go) | Database lease acquisition, renewal, fencing, release, and stale-row cleanup |

## Data service contract

The maintenance API separates listing from mutation:

1. `List*Candidates` returns bounded, keyset-paged identifiers and timestamps.
   It does not load message bodies, report JSON, export inline data, prompts, or
   other large payload columns.
2. `Maintain*` evaluates one candidate again in a transaction.
3. Dry-run mode reports the result without mutation.
4. Delete mode requires a valid `MaintenanceLease`; the transaction locks and
   validates its fencing token before changing data.

Candidates may disappear or become recent between listing and processing. Such
rows are returned as skipped or no longer eligible, not treated as failures.

## Interactive conversation retention

Interactive maintenance accepts only root conversations. It builds the same
graph as manual deletion: descendants through conversation parents,
parent-turn relationships, and `message.linked_conversation_id` are included
recursively. The graph is capped at 10,000 conversations.

The complete graph must:

- classify as interactive rather than scheduled;
- have known latest activity at or before the cutoff;
- have no protected inbound graph reference or user schedule;
- have no live run, live internal goal-wakeup schedule, or active report export.

This is system retention, not user-authorized deletion. It includes ownerless
legacy roots and does not require historical owner values to agree across the
graph. Owner metadata returned with a candidate is diagnostic only. Manual
conversation deletion continues to require ownership of every graph node.

Delete mode locks the graph, rechecks these conditions, and uses the ordinary
conversation deletion order. Empty legacy conversation statuses and known
active-looking statuses are not sufficient to block deletion by themselves;
current run lease and heartbeat evidence determines whether a worker is live.

## Scheduled-run retention

Scheduled maintenance has two complementary paths. The primary path starts
from `run` rows attached to a schedule and also supports legacy `schedule_run`
rows when that table exists. The run and all of its contained conversation
graph are evaluated as one candidate. The schedule itself is retained for
future occurrences.

The scheduled-conversation fallback starts from an old root graph that
classifies as scheduled but has no current `run` row and no existing legacy
`schedule_run` row anywhere in the graph. It handles historical conversation
shells whose run metadata has already disappeared. A stale `schedule_run_id`
marker alone does not count as a run. The fallback does not require historical
owner metadata because it is system retention rather than user-authorized
deletion. Delete mode locks and revalidates the complete graph, including the
absence of run rows; a newly found run returns `run_present` and leaves the
graph untouched. The associated schedule is retained.

This is system retention, not a user request. Historical owner fields are kept
for diagnostics but are not an authorization boundary. Safety instead comes
from structural containment: the graph must not be explicitly attached to a
different schedule or a different persisted scheduled run. Internal schedules,
missing schedules, recent activity, live runs, and active report exports are
not eligible.

## Technical retention

Technical maintenance processes small, independently revalidated records:

| Kind | Age source and conditions |
|---|---|
| `report_run` | `updated_at`; recent report context, export job, artifact, or audit dependencies defer deletion |
| `report_export_job` | completed, started, or submitted time; positive job/artifact `retention_ttl_sec` values are honored and recent artifacts or audit events defer deletion |
| `report_audit_event` | `occurred_at` |
| `session` | `expires_at`; only unclassified sessions whose expiry is older than the retention cutoff are eligible |

Report rows are classified as interactive, scheduled, or unclassified from
their associated conversation. Sessions belong only to the unclassified scope.
Persisted status is deliberately not an eligibility condition: a technical row
left `queued` or `running` for the entire retention period is stale data, not
proof that a worker still owns it.

Deleting a report run or export job also removes its database-resident export
artifacts and associated audit events in dependency order. It does not delete
physical objects from external storage.

`report_shared_artifact` is intentionally absent from this API. Saved reports
are durable user data and must not be removed by conversation or technical
retention.

## Orphan maintenance

Every orphan rule has a static action that callers cannot override:

- `safe-delete` removes a child row whose required parent is absent, including
  unused `call_payload` rows after every supported reference is checked;
- `safe-detach` preserves a row and clears a broken optional reference;
- `report-only` records an anomaly without changing data.

Required-parent cleanup covers conversation runtime rows such as goals, turns,
queues, messages, calls, generated files, execution claims, export artifacts,
and report contexts. Optional references across conversations, messages, turns,
runs, schedules, payloads, report runtime rows, and generated files are
detached. An old `investigation` is safe-deleted when `conversation_id` is NULL,
empty, or references a missing conversation. Its age is determined by
`investigation.created`, and the configured orphan grace period applies.

The report-only rules for `report_export_job.missing_report_run` and
`report_export_job.missing_artifact` provide diagnostics because those
relationships are not sufficient evidence that the job can be removed.

The following historical pseudo-orphan rules are intentionally disabled and do
not produce candidates:

- `report_audit_event.missing_job` and `.missing_artifact`: these columns are
  optional event context, not ownership-defining foreign keys. Audit rows are
  handled by `occurred_at` retention instead.
- `report_shared_artifact.missing_source`: `source_artifact_id` is a logical
  source identity such as `report_<reportID>`, not a foreign key to
  `report_shared_artifact.artifact_id`. The old rule was report-only; it must
  never be restored as a delete action because it matches valid saved reports.

Execute mode locks the current maintenance lease, locks the candidate where
supported, reruns the exact predicate and age test, and applies only the
rule-defined action.

## Distributed coordination

`maintenance_lease` is the cross-instance coordination table. Acquisition
atomically inserts a missing lease or replaces an expired one using database
time. Every acquisition receives a new random token. Renew, release, stale-row
cleanup, and every destructive maintenance transaction require the matching
key, owner, token, and an unexpired lease.

MySQL uses serializable transactions and row locks. SQLite operations also pass
through the process-local write gate to reduce `SQLITE_BUSY` contention. The
application worker currently uses the key `conversation_cleanup`, a two-minute
TTL, and renews every 30 seconds. Lease rows expired for more than seven days
are removed by the active lease holder; the retention is intentionally fixed.

The schema must be deployed before maintenance is enabled. Core does not create
or migrate `maintenance_lease` at runtime.

## Performance diagnostics

### Deletion graph reader A/B switch

`AGENTLY_DELETE_GRAPH_READER=legacy|compact` selects the conversation reader
used for deletion graph discovery, row locking and inbound topology checks.
Unset or empty means `compact`. Set `legacy` explicitly to use the previous
graph reader. Any other value fails the graph operation before
its transaction starts. The choice is pinned for the complete operation,
including nested schedule cascades and revalidation.

`compact` uses the separate, generated, internal
`dql/conversation/graph/read` component. Its SQL selects only `id`,
`created_by_user_id`, `status`, `schedule_run_id` and the raw creation timestamp,
with no transcript joins or content columns. It supports ID, parent-ID and
parent-turn-ID batches and has no pagination that could truncate the graph.
Linked-message edges, ownership, liveness, activity, references and graph size
limits are still checked by the same deletion orchestrators. MySQL row locks
and the single transaction for an entire tree are preserved; SQLite keeps its
existing transaction/write-gate behavior.

This does not switch conversation lists/transcripts, retention candidate or
schedule-root selection, payload handling, or scheduler execution. General
conversation readers remain unchanged. Use the same built
binary, fixture and cleanup settings for A/B tests; change this environment
variable and restart the dedicated test process. Return to `legacy` to disable
the compact path without rebuilding. A full cleanup speedup must be measured
end-to-end, not inferred from the smaller graph SELECT alone.

### Private deletion metadata and child batching

Deletion planning reuses identities and payload-reference columns from reads
already performed within the same preparation phase. It does not reuse that
snapshot for explicit post-lock revalidation. Dangling run IDs remain included
in the lock/check set, and model/tool run links remain scoped by turn.

Separate generated internal `conversation/cleanup/read` and `run/cleanup/read`
components return only nine/eleven metadata columns. They require a trusted
host capability and exactly one bounded identity predicate. The adapters split
input IDs into at most 400 binds per read; results have no pagination truncating
descendants. Raw nullable timestamps, execution-run filtering and row-lock
options are preserved. These readers are used only by deletion/maintenance,
not conversation execution, scheduler execution, public lists or transcripts.

Private `conversation/children/delete` deletes message, model-call, tool-call
and generated-file keys with one parameterized SQL per nonempty portion of at
most 400 keys. It flushes earlier queued operations and uses the parent's
transaction. There are no intermediate commits, pre-reads of full records,
independent transactions, retries or silent fallbacks. Missing keys remain
no-ops. Public writers and their normal insert/update paths are unchanged.
The existing diagnostic flag optionally records `children_bulk_delete` table,
key/affected counts and duration, never bodies or SQL arguments. Affected rows
are not committed until the enclosing graph operation succeeds.

These optimizations are always part of the private deletion path; the older
graph/payload A/B switches do not disable them. They preserve lease fencing,
saved-report/shared-payload protections and one transaction per complete tree.
The [five-pair verification report](../script/mysql/cleanup_benchmark/OPTIMIZATION_RESULTS_20261008.md)
documents scope, timings, actual SQL counts and rollback tests.

Graph discovery and deletion planning reads project identities and fields
needed for authorization, reference guards and liveness, rather than message
bodies or report documents. These working reader selectors do not project a
writer's independent `CurrentWriter` input view.

Payload cleanup therefore uses a private, generated `payload/delete` component
whose current-state SQL and Go type contain only `id`, never `inline_body` or
other payload contents. Both graph payload deletion and `call_payload.unused`
use it. Ordinary payload insert/update, compression, storage and transcript
reads retain their existing components. The private component accepts 1–400
distinct, explicitly delete-marked identities and requires trusted `payloadaccess` access;
it is not a public HTTP deletion endpoint. All ten inbound-reference guards
remain on the DELETE itself, within the caller's transaction. An absent key
at the current-state read is a no-op; a later guarded DELETE conflict is not
ignored and still rolls back the operation, including any earlier chunks.
Graph deletion checks references in sequential chunks of at most 400 IDs,
projecting only `id, referenced` with an explicit page limit equal to the chunk
size. Shared or missing rows are skipped; the remaining rows use one private
writer invocation per nonempty chunk. This bounds component invocations, not
necessarily the number of SQL DELETE statements generated by Datly. All chunks
use the graph's single transaction: there are no intermediate commits and locks
remain held until the graph commits or rolls back. Context cancellation or any
chunk error aborts the operation. The `call_payload.unused` orphan path continues
to submit one candidate per managed operation.

`AGENTLY_DELETE_PAYLOAD_MODE=row|bulk` switches **only** payload deletion.
The default is `bulk`; set `row` explicitly to use the generated private writer.
`bulk` uses the separate, hand-authored internal `PayloadBulkDelete` component and executes
one parameterized `DELETE FROM call_payload WHERE id IN (?,...)` per nonempty
portion of at most 400 persisted keys. Its SQL is stored in
`internal/datly/payload/delete/sql/bulk_delete.sql`; it retains the same ten
`NOT EXISTS` reference guards as the row writer. The shared key-only pre-read
and complete request validation remain in both modes. It flushes earlier
buffered reference removals, then uses Datly's transaction SQL capability on
the `agently` connector. It never opens or commits an independent transaction.
An affected-row mismatch is a conflict and rolls back the whole operation,
including previously executed portions and related investigation deletions.
Missing keys at the pre-read remain no-ops. There is no silent fallback to row.

The mode is read and validated before the enclosing deletion transaction,
then pinned in its context for all nested graph and portion invocations.
An invalid setting fails without mutation. Change the environment and restart
the process to switch modes; setting `row` restores the previous implementation
without rebuilding. Neither mode changes ordinary payload insert/update,
transcript reads, scheduler execution, retention eligibility, saved reports,
schema/indexes, or external file management. Orphan payloads still execute one
candidate/transaction at a time, so bulk does not combine multiple orphan
candidates or multiple trees into one transaction.

With both `AGENTLY_DEBUG_CONVERSATION_DELETE=1` and
`AGENTLY_DEBUG_CONVERSATION_DELETE_DETAILS=1`, the `payload_delete_batches` phase
reports candidate, reference-batch, writer-batch, submitted, shared-skipped and
missing counts, plus `mode`, successful `delete_statements` and `affected`.
Bulk additionally reports `payload_bulk_delete` for each attempted SQL execution
with expected/affected counts and success/error. `submitted` counts rows passed
to writers; `affected` describes successfully executed statements, **not**
committed deletions. Later failures can roll them all back. These phases use
the opt-in detailed diagnostic flag, disabled by default; no payload contents
or SQL arguments are logged.

An orphan's transactional recheck renders only its selected rule and exact
record predicate; full candidate scans still include all enabled rules.
Dependent mutations share a generated writer invocation only when their owner
and expected-reference guards match. Writer batches remain bounded at 400 rows;
graph lock batches retain their existing 500-row bound. Transactional fencing,
post-lock rechecks and foreign-key deletion order are unchanged.

`AGENTLY_DEBUG_CONVERSATION_DELETE=1` keeps the compact manual-deletion logs.
Set `AGENTLY_DEBUG_CONVERSATION_DELETE_DETAILS=1` as well to enable phase and
component timings for manual deletion and maintenance. Each detailed outer
operation has a trace ID and a
final component-invocation count. This is not a SQL-query or affected-row count;
one component can execute multiple statements. The outer completion includes
managed commit/rollback. Inputs, payloads and SQL parameters are not logged.

For a repeatable, isolated SQLite timing fixture, run:

```bash
AGENTLY_TEST_CLEANUP_PERFORMANCE=1 go test ./app/store/data \
  -run '^TestCleanupPerformanceFixture$' -count=1 -v
```

It reports cold and warmed operation durations separately for roots with small
and large message sets, reporting dependencies and unused orphan payloads.
The message fixtures also report allocated bytes. Runtime construction and
fixture seeding are excluded. No wall-clock threshold is enforced in CI;
fewer component invocations do not guarantee lower latency for every fixture.

The [MySQL benchmark guide](../script/mysql/cleanup_benchmark/README.md) contains
the isolated fixture, measured before/after results and opt-in verification
commands. Do not run benchmark seeding against an application database.

## Extension rules

When adding a table or relationship:

1. Decide whether the data is conversation-owned, independently retained,
   durable user data, or externally managed.
2. Candidate scans must select only identifiers and timestamps.
3. Add a bounded keyset cursor and a deterministic age source.
4. Revalidate under a transaction immediately before mutation.
5. Require and fence every destructive system-maintenance operation with the
   maintenance lease.
6. Prefer safe detach for optional references. Use report-only whenever a
   missing reference does not prove ownership or invalidity.
7. Never add `report_shared_artifact` to generic retention or infer ownership
   from `source_artifact_id`.
8. Keep physical artifact deletion outside this layer until storage ownership
   is explicit.

The application-level modes, defaults, logs, and rollout procedure are
documented in Agently's `doc/database-cleanup.md`.
