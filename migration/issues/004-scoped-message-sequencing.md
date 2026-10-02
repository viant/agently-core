# Missing feature: native per-turn message sequence allocation

Status: implemented and verified in Datly 7af4012d082777f6fc573afc497257213761a1da.
SQLX owns the new io/sequence primitive; local SQLX changes await user review/push.
Existing Core migration work remains uncommitted on datly_1.

## Required behavior

The original message writer has a string `id` primary key and a separate numeric
`sequence`, unique within `turn_id`. For a new message with a nonempty turn and
no caller-provided sequence, it allocates the next value for that turn. It never
resequences updates or repairs explicitly supplied sequences. Its collision
path retries automatic insert allocation up to ten times.

Example: t1 has sequence 2 and t2 has sequence 100. Inserting an automatic
message for t1 must allocate 3, independently of t2. Same-batch supplied values
and concurrent writers must not collide. Caller-owned transactions, rollback,
original Has evidence, zero/explicit values and truthful mutation outcomes must
retain native ownership.

## Reproduced connected-build gap

Authority: Datly v1 6507cebbc1d3bd71c9a01d1d9ea1eb624923b462.

1. With the actual message shape (string id PK, numeric sequence, unique
   turn_id/sequence), public `sequencer.Service.Allocate(..., "Sequence")` fails:
   `sequence selection for messages matched 0 numeric columns; specify
   SequenceField or SequenceColumn`. The caller already supplies SequenceField
   through Datly's selector option; the field is not a native numeric identity.
2. With sequence marked as a composite numeric identity, t1 receives 101 rather
   than 3. Selection honors the numeric field but remains table-wide.

The current sequencer API takes table, destination and selector, with no tuple
partition declaration. Its reservation key is catalog/schema/name. The writer
contract's “scoped sequencer” means invocation ownership, not per-turn numbering.

Evidence: ../evidence/scoped-message-sequence-probe.go and
../evidence/scoped-message-sequence-results.json. The probe performs only
in-memory fixture SQL and calls the real public Datly allocator.

## Extension requirements

Add a declarative/native partitioned sequence capability for a non-identity
column, with turn identity bound from typed entity fields. Keep reservation,
allocation, known collision handling and transaction ownership in the framework.
Use typed driver/constraint information rather than application error strings.
Do not add raw DB/session access to application hooks or another process-global
MAX(sequence) counter. Specific API/tag design remains to be reviewed.

Acceptance must include two independent turns, same-batch supplied and generated
values, explicit zero/nil/empty-turn behavior, updates preserving original
sequence, actual independent-connection races, rollback and caller transactions,
known collision outcomes and relevant SQLite/MySQL behavior. Generated contracts
and Endly regeneration must remain authoritative.

## Current migration checkpoint

ToolCall cube full Endly passed (111.346s E2E; 159124ms workflow). There are
31 verified contracts and 21 unlinked drafts. Application SDK1/caller cutover,
no-SQL root audit, backfill and hygiene still remain. Do not claim completion or
create the final datly_1 branch yet.

## Implemented contract and evidence

`sequence_scope(view.sequence, view.turn_id)` lowers into generated sequenceScope
metadata. Inserts allocate only omitted values; explicit zero/NULL and updates
are preserved. SQLX counters and source mutations share the caller/root
transaction. SQLite and live MySQL independent-connection tests passed. MySQL
requires ledger provisioning outside business transactions.

Generated-writer tests cover two turns, reserved supplied batches, scoped races,
caller commit/rollback, forced external automatic collision, ten-attempt
exhaustion, explicit-value failure without repair, concurrent primary-key winner
preservation and regeneration. Existing native identity sequencers and ordinary
Recover hooks retain their prior contracts. The broader Core Endly suite passed
after correcting a guard that initially affected OAuth recovery.

Documentation: /Users/awitas/go/src/github.com/viant/datly/doc/scoped-sequences.md
and SQLX io/sequence/README.md. Message component migration still remains; this
feature resolves the capability gap without claiming the full migration is done.
