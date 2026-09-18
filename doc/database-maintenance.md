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
