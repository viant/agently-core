# Cleanup stage 4 — measurement-gated optimization

Date: 2026-10-22. Baseline: `41dd2fb6` (3B). Synthetic local MySQL only;
no production access, deployment, dependency, schema or index changes.
Working plan: `.LOCAL/_DELETE_FIX/plan_v04.md`.

## Method and decision gate

Keep a change only with at least 5% lower full-V2 median and wins in at least
5/6 alternating AB/BA pairs; extend an ambiguous result to 12 pairs (9 wins).
An unproven or insufficient hotspot is skipped, not an excuse to change code.
Historical stage-3 timings are not the control for this experiment.

Frozen 3B binary SHA-256:
`2c4b404d336d99d53b3acf80510bcb896beaa800504f67c2fdb728d58fd3857d`.
4A binary SHA-256:
`f7e5fa6b14f6c0fef592ae7d7af552d8c3a628081da7a299c2b89a7982a4f9e3`.
Local evidence: `LOCAL_LOGS/stage04_series.gioi4A/`; the baseline includes
archived core sources, build modfiles, integrity hashes and the executable.
Local runners: `.LOCAL/_DELETE_FIX/{benchmark_stage04,pairs_stage04,test_stage04_mysql}.sh`.

Each run imports schema 42 and the same compatible V2 seed into a new owned
MySQL 8.0.43 container (pinned image, 4 GiB limit, loopback3308). The real
application runs on 9292, scheduler execution disabled, compact graph and bulk
ordinary-payload modes, 30-day retention and batch10000. Compilation, schema,
seeding, correctness checks, profiling and regression tests are outside timed
cleanup passes. No builds/tests run concurrently with the timed pair series.
The existing MySQL on 3307 is never used or stopped.

## Stage 0 — current baseline and diagnostics

Six fresh 3B passes: **49.868, 45.652, 133.855, 46.299, 44.623, 43.345 s**;
median **45.9755 s**. All 33 V2 checks pass, deleted/mutated113, failed0,
partial0, committed_batches90, explicit affected_rows4727. The 133.855 s
outlier is retained: host load was high, not a reason to cherry-pick samples.
Module/schema/fixture/workspace/runner integrity comparison passed.

Separate diagnostic pass (45.453 s, excluded from medians) records 4360
component calls, 4388 prepared SELECTs and 2930 prepared DELETEs. Selected
aggregate component wall times (nested, **not additive SQL execution times**):

| Operation | Calls | Aggregate time |
| --- | ---: | ---: |
| Old AG-UI event writer | 15 | 9.855 s |
| Ordinary payload bulk | 106 | 2.343 s |
| Existing child bulk | 253 | 2.023 s |
| Native execution-run DELETE writer | 84 | 0.964 s |
| Protocol-run DELETE writer | 15 | 0.963 s |
| Turn writer | 84 | 0.843 s |

Prepared statements have NULL SQL_TEXT in Performance Schema history and are
not reliably attributed to per-table digests. General log proves SQL counts;
component timers include runtime overhead, not just server SQL. Transaction
lifetime is not COMMIT latency. No claim of a precise per-table SQL-time split.

A separate diagnostic-overlay binary profiles startup plus one full pass
(40.076 s, excluded from all medians). CPU sample total19.51 s; allocations
2678.49 MiB, dominated by parser/runtime/catalog work. These measurements are
diagnostics, not a causal speed comparison.

## Stage 4A — scoped event bulk also for small maintenance plans

The 9.855 s writer cost warrants an isolated experiment. Maintenance parents
supply their existing transaction SQL capability. The mutator's first step
uses the **same embedded kind/run/conversation-scoped SQL as 3B event units**,
with sorted native event IDs, at most400 per statement, and actual affected
rows recorded through the existing confirmed-transaction accounting.

There is no new transaction, commit, retry loop or flush anchor. Events are
the first mutation, before any buffered metadata work; a later metadata error
must roll them back. Large-plan boundaries remain 3B's. Manual deletion still
uses the canonical strict writer even if a SQL capability is accidentally
present. Scheduled-maintenance changes concern deletion only, not scheduler
execution. Existing compact readers and other bulk paths are untouched.

### Verification

- Unit cases0/1/399/400/401/1001: batch bounds, deterministic ordering, scope,
  absent rows, actual counts, error/rollback and unchanged manual writer.
- New MySQL0/1/399/400 regression: a late conversation-delete trigger proves
  events already disappeared inside the transaction, then aborts. Events,
  protocol run and root all return after rollback; retry is one committed
  batch with exactly N+2 explicit deleted rows. A foreign thread using the
  same public protocol-run identity remains untouched.
- Existing SQLite and MySQL regressions: boundaries399/400/401, large1001,
  cycles/overlap/new references/moved events, partial progress, lease loss,
  cancellation, confirmed rollback retry, unknown commit and manual rollback.
- Real heartbeat regression: 10-second DELETE, nine successful renewals;
  no maintenance_lease lock held by cleanup, no waiting heartbeat.
- Full `go test ./app/store/data` and targeted store packages PASS; race for
  cleanup/AG-UI store tests and all conversationtree/maintenancetx/
  maintenancebatch tests PASS; targeted `go vet` and `git diff --check` PASS.
  macOS race linking emits LC_DYSYMTAB warnings but tests execute and pass.

### Alternating full-V2 comparison

| Pair | Order | Frozen 3B | 4A | Reduction |
| --- | --- | ---: | ---: | ---: |
| 1 | A/B | 34.483 s | 27.829 s | 19.30% |
| 2 | B/A | 33.344 s | 28.469 s | 14.62% |
| 3 | A/B | 32.865 s | 28.380 s | 13.65% |
| 4 | B/A | 33.461 s | 27.944 s | 16.49% |
| 5 | A/B | 34.107 s | 27.059 s | 20.66% |
| 6 | B/A | 33.969 s | 28.685 s | 15.56% |
| Median | | **33.715 s** | **28.162 s** | **16.47%** |

**Accept 4A:** lower median by5.553 s (16.47%), wins6/6, comfortably passing
both gates. No ambiguous-result extension or discarded samples. All12 runs
pass33/33 with deleted/mutated113, failed0, partial0, committed_batches90 and
explicit affected_rows4727; remaining1 conversation/1 run/12 payloads,
11 schedules and the saved report are preserved.

Since4A is the only accepted code change, this fresh paired comparison also
measures the final cumulative result against the frozen3B binary. No later
code optimization is mixed into these measurements. The earlier unpaired
45.9755 s baseline and diagnostic/profile runs are **not** used to claim the
speedup. This shared-host synthetic result is not a production SLA; workloads
with few protocol events may benefit less.

### Final diagnostic and integrity check

A separate 4A diagnostic pass completes in 28.129 s and passes all 33 checks
with the same 113 deletions, 90 committed batches and 4727 explicit affected
rows. It is excluded from the paired timing comparison. General-log statement
counts confirm the intended reduction:

| Prepared SQL / component | Frozen 3B diagnostic | 4A diagnostic |
| --- | ---: | ---: |
| DELETE statements | 2930 | 1192 |
| DELETE from call_payload | 1942 | 204 |
| SELECT statements | 4388 | 4356 |
| Component calls | 4360 | 4345 |
| Canonical AG-UI event writer calls | 15 | 0 |

Thus 1738 DELETE statements disappear without changing confirmed row or batch
counts. SELECT totals also include lease renewal and writer-internal reads;
they are not evidence of a new read-cache optimization. Remaining native-run,
protocol-run and turn writers take 0.732, 0.648 and 0.634 s respectively in
this diagnostic (component wall times, not isolated SQL time).

Final integrity hashes match for both repositories' module files, schema,
fixture, verification SQL, workspace configuration and runner. No application
source, dependencies, schema or indexes were changed. All owned test containers
were removed; the pre-existing MySQL on port 3307 remains untouched.

## Stages 4B / 4C / 4D — evidence before changes

4B: turn/run writers individually cost less than one second; queues and
approvals have no DELETE calls in V2. The native run's separate fresh identity
lookup and run-evidence boundary checks are not counted as removable writer
cost: the plan requires preserving guards. Bulk for another table has no
demonstrated >=5% full-pass benefit on this fixture. No speculative change.

4C: full SQL text reveals549 repeated SELECT occurrences across the *whole*
pass:139 connection-version queries,118 lease queries,121 schedule queries,
90 report-context queries,66 run/legacy queries,10 report-run queries and5
report-artifact queries. This is **not** evidence of549 reusable reads within
one preparation attempt. Lease and cross-unit boundary evidence must be fresh;
reference checks and canonical mutation readers are not interchangeable with
the existing private compact cache. The seven allowlisted lightweight routes
already reuse exact inputs per preparation attempt. No further adequately
costed, safe duplicate was established; no cache broadening.

4D: MutationReport accounts for about2.52 MiB cumulative allocation, about
0.094% of the diagnostic profile, and has no attributed CPU samples. Sampling
does not prove zero cost, but provides no hotspot justifying journal changes.
Datly and its mutation-evidence contract remain untouched.
