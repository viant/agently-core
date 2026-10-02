# Run reader/writer checkpoint

The run table now has a privately linked canonical reader and writer for the
verified basic persistence slice. Twenty real legacy comparisons cover public,
own and other-owner visibility, trusted internal mode, ID/status/worker/
conversation/excluded-status predicates, server insert defaults, explicit
NULL/zero values, sparse patches, timestamps, no-op batches and validation.
Stored fields and successful response bodies are compared with the original
components. Two additional tests verify caller commit/rollback ownership.

The reader requires host-owned runaccess/internal and visibility/subject
providers. Public reads retain the effective_user_id rule; the host internal
mode matches the original DefaultPredicate override. Optional predicates cannot
bypass that scope. Business writer Init applies pending status, interactive
conversation kind, attempt one, iteration zero and creation time only to inserts.
All contracts, validators and mutation plumbing remain transcribed.

Scheduler list/total variants, deletion, its fact cube
and service caller cutover remain pending. No legacy persistence is removed.
All Agently/Core changes remain uncommitted for review.

## Canonical active/stale modes

The same reader now handles host-owned rows/active/stale modes. Maintenance
modes require trusted internal scope. Active mode selects the newest eligible
run after turn/conversation filtering; stale mode preserves running status,
heartbeat/lease/activity/worker/kind/root predicates and activity/creation/ID
ordering. Ten data-driven cases compare ordered full legacy results, and public
maintenance mode is rejected. The two obsolete generated source/packages have
been removed; the original baseline implementations remain untouched.

Two legacy no-predicate cases return no rows. Their SQL places AND status after
a possibly empty WHERE fragment. The canonical explicit WHERE corrects this
behavior. Corrected identities/order are asserted, and each complete row is
compared independently with the legacy run table reader. These cases are
documented corrections, rather than a claim of identical broken behavior.
Full Endly validation of the consolidated modes passed: 58/58 contracts,
regeneration and boundary checks, nested tests (308.798 seconds), runtime build.

## Conditional run patches

The generated row now retains the legacy logical Condition field; it is never a
SQL column. Business input Init validates owner-only versus status/attempt/owner
claims and the allowed-column rules, rejects missing targets and duplicate
guarded identities, then supplies typed per-row expectations to the existing
predicate.Handler interface. Its expression separates identities with OR and
AND-connects each row's expectations. Ordinary rows in a mixed batch retain
identity-only criteria. Native mutation_predicate owns execution-time guards,
transaction ordering and rollback. This does not introduce a second SQLX or
condition service API.

Sixteen data-driven cases pass, including complete field/body comparisons for
working legacy cases. Known single-row guard loss uses native recovery to
retain the legacy acknowledged no-op, with real rollback evidence. Three
independent connections change owner, status or attempt after Current lookup
and prove stale requests cannot overwrite winners. A mixed-batch guard loss
returns an error and rolls back prior work; it is not silently accepted.

Legacy mixed guarded/ordinary records panic when its executor combines distinct
Go record types. That failure is reproduced, while the single generated type
handles the corrected mixed batch. Exact parity is not claimed for that broken
path. Full Endly validation for conditional patches passed: 58/58 contracts,
regeneration/boundaries, all nested tests (543.119 seconds), runtime build.

## Canonical run lease operations

Run lease claim/release now reuse the writer and its single mutation predicate
group. Business input Init validates one existing target, owner and expiry,
freezes the default clock, and skips missing targets without inserting. Entity
Init limits lease writes to lease_owner and lease_until. Claim checks NULL
completed_at and available/expired/same-owner lease; release checks owner only.
The existing RunExpected business predicate carries these operation rules,
without a second predicate service or direct Go SQL. Known guard losses retain
the legacy false result via native recovery; the generated reader owns the
already-owned/unexpired fallback. MySQL-specific zero-affected-row behavior
has not been exercised against a live MySQL instance.

Eleven real legacy full-row comparisons pass, including completed targets,
expiry, same-owner renewal, missing targets and release. Four independent
connections change owner, complete or delete a target after Current lookup;
the guarded UPDATE preserves the competing state and never recreates rows.
All run regressions pass together (65.680 seconds). Full Endly validation for
run leases passed: 58/58 contracts, regeneration/boundaries, nested tests
(332.950 seconds), and runtime build. Scheduler service adapters and production execution
cutover remain pending.

## Scheduler run-list consolidation

Scheduler listings now use the canonical reader's trusted schedulerList mode.
They preserve schedule visibility/internal exclusion separately from ordinary
run effective-user visibility. Wildcard status/conversation/error inputs retain
legacy LIKE semantics; schedule IDs retain equality. Native selectors own
limit/offset and select the sixteen legacy fields without runtime worker, auth
or checkpoint overfetch. Ten full ordered legacy projection comparisons pass,
including owner separation, wildcard/case behavior, pagination and internal
schedule exclusion. The verified subject comes from a required provider; it is
not read from the caller's effectiveUserId query parameter.

The obsolete generated scheduler list package/source is removed. Per-schedule
reads, totals, run steps, deletion, its fact cube and caller adapters remain
pending. All run/scheduler-list regressions pass (78.131 seconds). Full Endly
validation of this consolidation is running.

## Per-schedule lookup and trusted due consolidation

The canonical reader now supports schedulerRuns and schedulerDue host modes.
Both preserve started_at/id ordering and the selected scheduler projection.
Schedule equality, excluded statuses and exact scheduled slots are standard
predicates. Public mode applies schedule visibility with the verified subject;
trusted due mode intentionally includes private/internal schedules. Eight full
ordered legacy comparisons pass, with an additional anonymous-private check.
Scheduled-slot fixtures use each writer path's actual timestamp storage.

The legacy since input belongs to group one, but its SQL only renders group
zero. A regression reproduces two legacy rows and asserts that the corrected
canonical since-turn predicate returns only the later row. Anonymous public
lookups no longer bypass private visibility through omitted effectiveUserId;
trusted internal access is explicit. These corrections are documented rather
than claimed as unchanged behavior. The duplicate generated scheduler run
lookup source/package is removed. Total/steps/cube/caller work remains pending.

The preceding scheduler-list full workflow failed once in the legacy worker
filter case. Four targeted executions and 30 cached legacy-probe executions
passed; no fixture-specific retry or suppression was added. The probe now
records legacy read status. A full gate rerun is still required; the earlier
failed gate remains recorded.
