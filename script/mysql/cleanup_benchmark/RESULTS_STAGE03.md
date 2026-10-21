# Cleanup 2P / 3A — local verification

Dates: 2026-10-21–22. Synthetic local data only; no production mutations.

## Scope

Stage 3A qualifies a conversation tree using its root's
`COALESCE(last_activity, updated_at, created_at)`. A qualified old root also
qualifies recent/live descendants and internal execution/protocol activity.
Run-only scheduled retention still checks the execution's own age/liveness.
External references, shared ordinary payloads, schedules and saved reports
remain protected. Manual deletion retains its owner/liveness validation,
locking reads and all-or-nothing transaction.

The implementation keeps compact key-only projections, bounded SQL batches of
400 IDs, dependency order and one transaction per tree. It removes repeated
discovery, reuses private planning reads only within an attempt and avoids
cloning the mutation journal twice per payload batch when diagnostic details
are disabled. Each worker pass fixes one clock for policy cutoffs.

## Method

- Baseline: `bc1952ff`; stage 1: `631b6839`; stage 2: `ebb1a325`.
- Same application source, Go 1.25.8, dependency files and build flags for all
  variants; archived core revisions, not worktree resets.
- MySQL 8.0.43, schema 42, FK checks on, 4 GiB container limit, 3 GiB tmpfs;
  pinned image `sha256:6f19538dd7d235eca1d38163bdb4124d5b52a7bcae4ed20c8dd35af64dbd1434`.
- Fresh owned container and V2 seed for each run, loopback ports 3308/9292.
  The unrelated existing MySQL instance on 3307 was not used or stopped.
- V2: 93 conversations, 276 runs, 3286 payloads; about 416 MiB declared
  payload bytes. Expected: one conversation, one run, 12 payloads, all
  11 schedules and one saved report remain. 33 SQL assertions per run.
- Compact graph reader, bulk payload mode, all retention cutoffs 30 days,
  batch 10000, scheduler execution disabled. Timings cover the whole cleanup
  pass, not compilation, schema installation, seeding or regression tests.
- Main timings have detailed deletion traces disabled. Diagnostic passes
  are separate and are not included in the medians. Prepared statement
  counts require the diagnostic general log; digest counts alone are incomplete.
- The shared host was not exclusively reserved. Repeat/rotate variants and
  report ranges rather than interpreting a single run as a production SLA.

Local raw evidence: `LOCAL_LOGS/stage03_series.k0F0VL/`; disposable runner:
`.LOCAL/_DELETE_FIX/benchmark_stage03.sh`. Neither logs nor credentials are
included in this report's commit. Core/application dependency files and
tracked fixture hashes are checked before/after the series.

## Initial 2P series (three rotated runs)

| Variant | Runs (s) | Median (s) |
| --- | --- | ---: |
| Baseline | 68.093, 57.153, 45.574 | 57.153 |
| Stage 1 | 47.981, 48.529, 52.071 | 48.529 |
| Stage 2 | 47.457, 67.038, 51.060 | 51.060 |

All nine main runs and three separate diagnostic passes passed 33/33
assertions with 113 eligible/deleted operations and zero cleanup errors.
These controlled repetitions did not reproduce the old single-run ordering
39.616 → 64.704 → 77.027 seconds. They do not establish the exact cause of
those historical measurements: dependency/host conditions were not captured
in an equivalent way then. The measurable repeated-read overhead below is
independent evidence, not an assertion that the historical difference was
entirely random noise.

## Diagnostics before 3A

Separate diagnostic pass totals (nested timings overlap; do not add columns):

| Variant | Observed component calls | Discoveries / 90 trees | Planning (s) | Mutation orchestration (s) | Run-evidence refreshes |
| --- | ---: | ---: | ---: | ---: | ---: |
| Baseline | 8096 | 180 | 13.711 | 14.796 | 156 |
| Stage 1 | 7870 | 180 | 14.878 | 16.070 | 156 |
| Stage 2 | 7408 | 180 | 14.809 | 16.249 | 156 |

The existing diagnostics do not expose a separate native journal Flush/
finalization timer. Request/transaction duration includes it; component and
mutation timings are not a substitute for an isolated Flush measurement.
For example, baseline main run 1 spent 0.872 s executing explicit COMMIT
statements, whereas 67.395 s was the summed *whole transaction lifetime*;
the latter must not be described as COMMIT latency.

## Extended 3A comparison

The comparison was extended to six main runs because of the observed spread.
Additional variants were interleaved; every run used a newly seeded database.

| Variant | Six main runs (s) | Median (s) | Range (s) |
| --- | --- | ---: | --- |
| Baseline | 68.093, 57.153, 45.574, 83.900, 76.856, 73.328 | 70.711 | 45.574–83.900 |
| Stage 2 | 47.457, 67.038, 51.060, 83.641, 65.262, 70.816 | 66.150 | 47.457–83.641 |
| Stage 3A | 43.399, 63.793, 53.340, 50.209, 49.396, 49.735 | 49.972 | 43.399–63.793 |

The stage 3A median is 29.3% below baseline and 24.5% below stage 2,
meeting both planned 5% gates. Every main run passed 33/33 assertions,
with 113 eligible/deleted operations and zero cleanup errors. This is a
local empirical gate, not a statistical confidence bound or a production SLA.

The isolated journal-copy variant (stage 2 plus only that optimization) took
58.671, 82.204 and 70.142 s, median 70.142 s. It does **not** demonstrate a
repeatable elapsed-time gain on this host. Its unit test does establish that
optional per-chunk diagnostic accounting no longer clones the mutation journal
when detailed diagnostics are off. Do not attribute the full 3A gain to that
optimization or interpret the isolated variant as proving a regression.

Separate final SQL-diagnostic runs confirm the structural reduction:

| Observation | Stage 2 | Stage 3A |
| --- | ---: | ---: |
| Observed component calls | 7408 | 4360 |
| Graph discoveries / 90 trees | 180 | 90 |
| Delete plans | 90 | 90 |
| Run-evidence refreshes | 156 | 90 |
| Executed prepared SELECTs (general log) | 7442 | 4388 |
| Executed prepared DELETEs (general log) | 2930 | 2930 |

Component calls fell 41.1%, prepared SELECT executions 41.0%, while DELETE
executions were unchanged. Both diagnostic runs passed 33/33 assertions with
zero cleanup errors. Their elapsed times are excluded from the main comparison;
the diagnostic instrumentation and concurrent verification make those times
unsuitable for attributing latency to individual SQL phases. Heartbeat UPDATE
counts may differ with pass duration.

## Correctness verification

- SQLite app/store/data regression suite, native maintenance tests,
  conversation/tree/transaction unit tests and maintenance worker tests.
- Root activity COALESCE precedence/fallback, old root + recent child/run,
  recent root + old run, active internal protocol/export, closed parent cycle,
  missing run markers, fractional and mixed timestamp pagination.
- MySQL actual heartbeat during a 10-second deletion (nine successful
  renewals; no lease-row lock held by deletion), takeover/write wait,
  disappearing rows, new shared payload references and confirmed deadlock
  rollback/retry with fresh lease/plan. Cleanup SQL: READ COMMITTED and no
  locking SELECTs. Manual deletion is intentionally unchanged.
- Race tests for conversationtree, maintenancetx and service/maintenance;
  targeted go vet and diff inspection.
- Manual 401-payload row/bulk rollback and actual MySQL current/legacy root
  retention checks run separately from measured passes.

This document records local verification, not authorization to deploy. Code
rollback cannot restore deleted data. Stage 3B is a separate change and commit.
