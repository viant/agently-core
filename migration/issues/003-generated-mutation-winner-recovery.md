# Generated OAuth mutation cannot adopt a competing winner

Status: implemented locally after the user authorized backfilling the feature.
This is a local issue draft. No remote issue has been created.

## Reproduced behavior before backfill

The real legacy `CreateOrGetPending` writer checks affected rows. When creation
loses, it reloads by the unique flow hash and returns the stored winner with
`Created=false`. Guarded replacement similarly reloads after a CAS miss, with
one bounded retry when the row disappears or remains replaceable.

The generated writer passes four sequential legacy comparisons: creation,
pending adoption without DML, expired replacement and consumed replacement.
Its deterministic competing-insert test fails: a fixture-only SQLite trigger
stores a winning flow during INSERT and ignores the submitted insert. Legacy
reloads the winner. Datly commits that same stored winner, but returns its
unpersisted candidate with `Created=true`.

The trigger reproduces an affected-row loss deterministically; it is not proof
of concurrent-process scheduling or of a particular driver's unique-error path.
Both real runtimes execute against independent fixtures with the same trigger.

Reproduction from `agently-core-v1/datlyv1`:

```sh
go test ./tests -run 'TestLinkStateCreateAdoptSequentialEvidence/losing_insert' -count=1 -v
```

The test also compares persisted state through real typed reader components.
Before backfill it failed as a migration acceptance gate; this component remains
unlinked and excluded from the 15 verified components.

## Connected framework evidence

- Datly local base: `e4bdda213366c6ddff9ba3b186f3c227387b3a59`.
- Local framework identity fix: `127c7491c4ef48ffc2d16c478b2828cc87729fc5`.
- SQLX: `v0.26.1-0.20260928224516-716a37c8ca40`.
- `sql/dml/dml_execute.go:executeInsertStep` receives affected counts but uses
  them only for metrics, returning nil for a successful zero-row insert.
- Guarded update/delete convert a no-match outcome into `xhandler.Conflict`.
- `runtime/handler/writer/handler.go:FinalizeOutcome` calls the business hook
  after transaction completion. Its return is an error, not a recovery result.
- `runtime/handler/engine/outcome_finalizer.go:finalizeOutcomes` retains the
  original completion error even when the hook returns nil; it cannot turn a
  CAS miss or unique-error invocation into successful winner adoption.
- No bounded generated-writer retry/recovery policy was found in the connected
  spec, bootstrap, application or runtime surfaces.

## Required native capability

Expose trustworthy per-mutation affected-row/outcome evidence to business hooks
and support an explicit, bounded recovery decision using generated typed readers
and writers. Preserve the original request across attempts, reload fresh Current,
and let business hooks decide adoption versus eligible replacement/creation.
Do not invent a parallel predicate mechanism: retain SQLX Criteria and existing
Datly predicates for CAS conditions.

Recovery must distinguish successful creation from zero affected rows, relevant
unique-key losses from unrelated database failures, and CAS misses from invalid
input or authorization failures. Confirm transaction ownership before recovery:
never retry inside a failed transaction, swallow rollback/commit uncertainty,
replay unrelated mutations, or publish success for caller-pending work.

Acceptance must cover insertion races, replacement races, disappearance after
CAS loss, exhausted bounded retries, exact returned winner and Created ownership,
unrelated DB errors, rollback, caller transactions and deterministic regeneration.
Add genuine cross-connection/process concurrency evidence alongside deterministic
fixtures once the native contract is agreed.

Manual product SQL, manual writer handlers, generated-file edits, or a caller
read/check/write loop are not acceptable substitutes under this migration's scope.

## Implemented native resolution

Datly now reports actual row-operation counts/errors and exposes an optional
root `Recover(ctx,*Input,*Output,handler.MutationOutcome)` lifecycle hook.
The hook chooses preservation, corrected winner output, or one native request
replay with fresh Current/dependency binding. Existing mutation predicates and
SQLX Criteria remain the guard mechanism; xdatly has no added SQLX dependency.

Recovery requires one executed root record, one queued operation, one known
owned database unit, and confirmed completion. Caller-pending, uncertain,
partial/mixed, nested/batched/sibling work, extra diagnostics, validation and
unrelated DB errors are rejected. Driver-coded snapshot/deadlock contention
may recover after confirmed rollback. Original request facts and presence use
native Bindly replay or native shape cloning for trusted typed inputs.

The generated OAuth writer uses business hooks and a separately transcribed
mandatory-flow-hash reader to reload consumed/expired winners. Ten comparison
cases cover sequential creation/adoption/replacement, ignored losing insertion,
second-connection creation/replacement/deletion, adoption of the last expired
winner after exhausted retry, and an ignored insertion with no winner that must
remain an error. For second-connection cases legacy observes the same final
competitor state before its first read; native is raced after Current binding.
Those cases prove actual native transactional recovery, not identical concurrent
scheduling of both implementations. Multi-process scheduling remains a later
application acceptance gate.

Full Endly passes with 63 transcribed contracts, stable regeneration, application
tests and runtime build. The OAuth writer and recovery reader remain unlinked
and uncounted until remaining transport/caller/consumption gates pass.

Committed to the requested original Datly checkout on branch v1 as
`832049dfd8c02144b3b61ad87382f1976669187d`. Core uses `replace github.com/viant/datly => ../../datly`.
Existing Datly user work was preserved and excluded from the commit. Two unrelated
cube reload tests fail on both the backfilled tree and unchanged baseline;
affected mutation/generation checks and Agently Endly pass.
