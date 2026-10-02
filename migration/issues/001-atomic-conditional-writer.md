# Datly 1.0: generated conditional mutations with atomic guards and affected-row outcomes

Status: implemented locally after user authorization; generated/native SQLite tests pass.
SQLX Criteria published at `716a37c8ca40`; Datly extension in `datly-conditions`.
This is a local issue record; no remote GitHub issue was posted.
Repository: https://github.com/viant/datly
Examined version: `v1.0.1-0.20260928172836-5aad1bdd5494`
SDK: `github.com/viant/xdatly v1.0.1-0.20260927175016-ff38d5bca9b6`

## Required behavior

A generated writer must be able to atomically update a row only while declared
expected database values still match, and expose whether that guarded transition
won. Application code must not obtain a database handle, issue SQL, build a
parallel mutation DTO, or own the transaction.

This is necessary for cross-process claims, owner-fenced heartbeats/releases,
and replacement of previously observed expiring/consumed state. A transaction
that reads Previous and subsequently updates by identity alone does not preserve
these semantics under concurrent writers.

## Current capability and gap

Correction: v1 already had atomic `concurrency_token`/IfMatch execution, despite
stale documentation describing validation-only behavior. The remaining gap was
general additional compound/range/custom conditions on generated update/delete.

Implemented `mutation_predicate(view,group)` using the existing marker-aware
predicate compiler, no parallel DSL. SQLX owns Criteria and accepts it as an
update/delete option. Datly bridges SDK predicate results without adding an
xdatly dependency on SQLX. Required input retains native binding; optional absence
omits the condition. Guard misses return Conflict and managed rollback.

Related source and contract:

- `runtime/handler/writer/handler.go`: `Metadata`, `Field`, frame construction,
  concurrency-token validation, and queued mutation execution.
- `spec/column.go`: generated column control metadata.
- `llm/datly-writer/references/writer-contract.md`: "Explicit deletion and token
  validation" and the distinction between preflight validation and atomicity.
- `doc/legacy-to-v1-migration.md`: database operations must remain generated;
  framework gaps must not become raw-SQL application hooks.

## Minimal semantic examples

These SQL fragments describe required database behavior, not proposed DQL syntax
or permission to implement application SQL.

Owner-fenced update:

```sql
UPDATE job
SET lease_owner = :nextOwner, lease_until = :nextUntil
WHERE id = :id AND lease_owner = :expectedOwner;
```

A claim can additionally compare status and attempt while updating those same
columns. Expected values are controls and must not be confused with working SET
values or physical/composite primary keys.

Replacement of observed state:

```sql
UPDATE pending_state
SET state_key = :newKey, expires_at = :newExpiry, consumed_at = NULL
WHERE flow_key = :flowKey
  AND state_key = :observedKey
  AND (consumed_at IS NOT NULL OR expires_at <= :databaseNow);
```

Zero affected rows means a competitor won or the observed state changed. It must
not be reported as a successful acquisition, nor become an implicit insert-on-
miss. Callers may need to return or reload the winner through a generated reader.

## Implementation requirements

1. Author typed guard/transition policy in DQL and transcribe it into native
   component metadata. Agree the public authoring surface before adding syntax.
2. Keep complete identity, authorization scope, expected controls, and new
   working values distinct. Expected values must be frozen from request/captured
   evidence and parameterized in the actual UPDATE/DELETE condition.
3. Support string/numeric/date controls and explicit NULL semantics. Lease expiry
   conditions must preserve the application's database-clock contract.
4. Preserve sparse SET presence, generated validation, relation sequencing,
   shared transaction ownership, and outcome-aware finalization.
5. Expose an explicit affected-row/transition outcome through the typed generated
   response or supported hook outcome. Define no-change versus guard-miss
   semantics for each driver; do not equate all zero row counts blindly.
6. Define whether guard failure rolls back an atomic multi-row graph or is an
   explicitly declared non-acquired outcome. Dependent changes must not commit
   when their ownership fence failed.
7. Do not solve this by declaring expected status/owner controls as invented
   primary keys or by adding direct SQL to lifecycle hooks.

## Acceptance tests

Use data-driven `useCase{desc, input, expect}` tests of transcribed components,
with schema/seed SQL restricted to test fixtures.

- Two independent invocations/processes observe the same row; exactly one claim
  succeeds and the loser cannot overwrite the winner. Include a MySQL test,
  rather than relying solely on SQLite's locking behavior.
- An owner/attempt/status changes between Previous read and mutation; the stale
  invocation affects no row and reports non-acquisition/conflict accurately.
- The expected owner differs from the new owner on the same physical column.
- Omitted, empty, zero, and explicitly NULL expected controls are distinct.
- A heartbeat rotates ownership; a stale release/renewal cannot mutate it.
- Two replacers race on one observed expiring/consumed state; one wins and the
  other observes the winner without a second successful replacement.
- Guard failure in a declared atomic multi-row event rolls back all dependent
  changes; no commit-dependent publication occurs.
- Sparse updates preserve every omitted physical field.
- Caller-owned transactions retain pending ownership and correct completion
  evidence; generated Current and mutation share the native database unit.
- Merge regeneration preserves authored business predicates/codecs/hooks.

## Application migration impact

Generated-only migration cannot safely replace the existing fenced run/lease and
pending-state contracts until this is implemented and proven. Ordinary component
migration can resume after this issue is triaged, or after explicit direction to
continue independent slices while the framework feature is handled separately.

Publication: GitHub posting was not possible in this session: no GitHub CLI or
connector is available, and the available browser is not signed in. This file
is the complete issue body ready for publication.
