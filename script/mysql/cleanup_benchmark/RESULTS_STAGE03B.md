# Cleanup 3B — bounded payload transactions

Date: 2026-10-22. Synthetic local data only; no production access or deployment.
Stage 3A is committed separately as `35b0184e`; its analysis and measurements
are in `RESULTS_STAGE03.md`.

## Transaction boundaries

Only maintenance changes its atomicity. Manual deletion continues to use the
original strict, authorized, all-or-nothing path, including its locking reads.
Conversation execution, the scheduler executor, dependencies and schema are
unchanged.

For plans with more than 400 combined AG-UI events / ordinary payload IDs:

1. Qualify/discover/plan once, then finish that read-only transaction.
2. Delete AG-UI events in deterministic units of at most 400 IDs. Each unit
   uses a transaction-owned, parameterized bulk DELETE, guarded by event kind,
   protocol run and conversation scope. Missing/re-scoped events are no-ops.
3. In one metadata transaction, recheck external graph/schedule/report
   references without locking SELECTs or repeating age qualification. Atomically
   detach cyclic edges and delete dependent metadata, children before parents
   and the root as the last conversation.
4. Delete ordinary payloads in units of at most 400 IDs using the existing
   unreferenced-payload guards. New shared references preserve their payloads.

Smaller plans retain one transaction. The coordinator is ordinary Go outside
the managed transaction; each unit obtains fresh standalone lease evidence and
checks cancellation. A confirmed retryable rollback retries only that unit,
not any predecessor. Unknown commit outcomes never replay automatically.

This deliberately conservative split preserves all discovery anchors until
the metadata transaction commits, including linked-message-only cycles and
run-only metadata. It does **not** promise 400 total affected rows per
transaction or a fixed transaction duration: the metadata unit remains atomic
and can still be large. SQL ID batches remain bounded; no per-row replacement
of the existing compact graph reader or ordinary-payload bulk path was made.

## Partial progress and restart

`Partial`, `CommittedBatches` and `AffectedRows` propagate through the native
store, public maintenance facade, policy and worker, even with an error.
These conversation-unit counts include only confirmed, explicit DML (not rolled-back work or FK
cascades); zero-row units and the read-only planning commit are not counted.
Post-commit errors retain confirmed progress without replaying it. `Deleted`
does not claim whole-tree completion after a failed later unit. The worker's
`mutated` count now also includes candidates with confirmed conversation-unit
DML, including a worker whose peer finishes the remaining metadata.
The new aggregate unit counters do not retrofit explicit-row accounting into
the existing orphan/technical policies; those retain their existing counters.

| Interruption point | Durable remainder / next pass |
| --- | --- |
| Event unit fails or lease/cancellation stops progress | Root, runs and discovery edges remain. The next pass qualifies the root again; reactivation can stop further deletion. |
| Metadata unit fails | Detaches and metadata deletes roll back together. Previously committed event deletions remain; descendants stay discoverable. |
| Ordinary-payload unit fails after metadata commit | Unreferenced payloads are handled by the existing `call_payload.unused` age-gated rule; shared references remain protected. |
| Concurrent worker already removed the whole tree | Missing events and absent metadata are no-ops; each worker reports only its own confirmed DML. |
| New external graph/report/schedule reference | The metadata boundary check / existing write guards refuse unsafe deletion; committed event progress is reported as partial. |

Current MySQL protocol payloads additionally have `ON DELETE CASCADE` from
their run; unplanned late events may be removed by that existing FK. Cascades
are not included in explicit affected-row counts. No orphan rule was broadened:
ReportOnly and SafeDetach classifications retain their existing behavior.

## Deliberately slow large-tree MySQL test

The identical test fixture runs against archived 3A and the working 3B through
the pre-existing maintenance API: 1001 AG-UI payloads, a BEFORE DELETE trigger
sleeping 5 ms per event, warm dry-run, FK checks enabled. Timers cover the
maintenance call; transaction lifetime comes from Performance Schema, not
COMMIT-statement latency. Schema/seed/compile are outside those timers.

| Observation | Stage 3A | Stage 3B |
| --- | ---: | ---: |
| Whole maintenance call | 19.820 s | 6.414 s |
| Maximum committed transaction lifetime | 19.794 s | 2.388 s |
| All observed committed transactions | 2 | 10 |

The maximum observed transaction lifetime fell 87.9%. Transaction counts also
include standalone lease/read transactions; they are not the DML-unit counter.
The trigger is artificial and the host is shared: this test proves the split,
not production throughput or a general 67% cleanup speedup. Both variants
deleted the root, run and all 1001 payloads. Raw local evidence:
`LOCAL_LOGS/stage03_mysql_units_v2.log`.

## Full V2 non-regression series

The full seeded V2 test uses the same method as `RESULTS_STAGE03.md`: Go
1.25.8, MySQL 8.0.43/schema 42, pinned container image and 4 GiB limit,
compact graph reader, ordinary-payload bulk mode, 30-day cutoffs and batch
10000. Scheduler execution and detailed SQL/deletion tracing are disabled.
Every measurement uses a freshly created owned loopback MySQL on 3308 and
application on 9292. Schema/seed/compile and regressions are outside the
measured cleanup pass; no Go tests or builds run concurrently with the series.

Archived 3A (`35b0184e`) and working 3B are rotated in alternating order.
Because of host variability the comparison is extended to six runs of each.
Dependency and tracked fixture hashes must remain unchanged. Local evidence:
`LOCAL_LOGS/stage03_units.gwalCX/`.

Main round labels are `1, 2, 3, 4r, 5, 6`. Original round 4 was excluded
before the replacement result was known: extending the local shell runner
while one copy was active caused a post-verification shell parse error.
Its cleanup/33 assertions had passed (3A 53.162 s, 3B 48.781 s), and its owned
container was removed. Both variants were repeated under `4r` without further
runner edits, rather than selecting a faster individual measurement.

| Variant | Six cleanup-pass times (seconds) | Median | Range |
| --- | --- | ---: | ---: |
| Archived 3A | 49.117, 57.686, 56.222, 51.142, 57.016, 52.504 | 54.363 s | 49.117–57.686 s |
| Stage 3B | 47.575, 54.627, 49.387, 42.035, 52.317, 53.060 | 50.852 s | 42.035–54.627 s |

The observed 3B median is 6.5% lower than the fresh paired 3A median and
28.1% lower than the earlier controlled baseline median of 70.711 s. It is
1.8% higher than the previous 3A-series median of 49.972 s, still within
the plan's 5% non-regression allowance. Both the throughput and large-tree
transaction-duration gates pass. The spread and the change between 3A
series illustrate shared-host variability; these are local observations,
not a production SLA or proof that all timing differences are causal.

Every selected run passed all 33 assertions: 113 eligible/deleted, zero
cleanup failures or partial results, and protected data retained. 3B recorded
90 conversation-unit commits and 4727 explicit affected rows. Its `mutated`
count is 113, versus the old 23, because confirmed tree DML is now included;
this reporting correction is not a change to which records qualify.

V2 contains large aggregate BLOB volume, but its individual deletion plans do
not cross the 400-ID split threshold: 90 tree DML commits remain 90. Therefore
V2 establishes full-data correctness and ordinary-path non-regression; it is
not evidence of shorter large-tree transactions. The separate 1001-event test
above and 401-event/payload failure tests exercise that behavior.

An additional end-to-end application smoke test added 1001 old AG-UI events
to one V2 tree. The built CLI application completed its cleanup pass in
41.602 s with 113 eligible/deleted, zero failures/partial results, 95 committed
conversation units and 5728 explicit affected rows; all 33 assertions passed.
This confirms that the private unit component is reachable through the actual
application runtime, not only the native-store test harness. This modified
fixture is excluded from the main timing medians. Local evidence:
`LOCAL_LOGS/stage03_units.gwalCX/stage3b/run-app-units/`.

## Verification

- 399/400/401 boundaries; full rollback for small plans, partial committed
  counts for large plans; error/lease loss/cancellation between units.
- Current-unit retry after confirmed rollback; no replay on unknown or known
  committed outcomes; no replay of an earlier committed 400-event unit.
- Reactivated root between passes; parent/parent-turn/incoming/outgoing links
  added between units; linked-message-only cycle rollback/restart.
- Concurrent overlapping cleanup, protocol-event scope changes, newly shared
  ordinary payloads, scheduled-run partial failure and retry.
- SQLite app/store/data suite and native runtime; MySQL compact/legacy graph
  readers, current/legacy root retention, manual 401-payload row/bulk rollback.
- Original MySQL heartbeat reproduction: a deliberately slow 10-second DELETE
  allowed nine lease renewals without the old lease-row lock. Takeover,
  disappearing records, new shared references, guard rollback and an actual
  InnoDB deadlock followed by a successful retry also passed.
- Race checks for tree/transaction/worker packages, targeted go vet and diff
  inspection. macOS linker LC_DYSYMTAB warnings did not fail the race tests.

No local credentials, raw logs, test database dumps or unrelated files are
part of this change. Code rollback cannot restore already deleted records.
