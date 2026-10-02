# Turn queue canonical reader/writer

The existing `turnqueue/read/list/reader` and `turnqueue/write` contracts now
have real legacy/new evidence and private runtime linkage. They remain the sole
reader and writer for `turn_queue`; no case-specific persistence component was
added. The writer lifecycle contains only business defaults and UTC timestamps.
Binding, validation, Current reads, presence and DML remain generated/native.

`datlyv1/tests/turnqueue_e2e_test.go` executes independent SQLite fixtures through
the real legacy probe and generated runtime using `useCase{desc,input,expect}`.
Fifteen cases cover ordered reads, active empty/status predicates, empty reads,
zero sequence insertion, defaults, sparse status updates, forced queued status
when omitted/empty, supplied timestamps, nil/empty bodies, missing IDs, invalid
NULL sequence, DB rejection and a mixed late-failing batch. Successful responses
and stored reader results are compared. Separate tests prove caller-owned commit
and rollback, including that the caller transaction stays usable after invocation.

## Deliberate transaction correction

Legacy `Handler.updateByID` executes `db.ExecContext` outside the managed write
unit. In a mixed update plus rejected insert, the earlier update stays committed.
The generated writer owns the batch transaction and rolls back both operations.
The test explicitly asserts this difference instead of hiding it in normalization
or reintroducing direct SQL to mimic the bug.

The original Core source is unchanged. Application caller cutover still awaits
the full Datly 1.0 runtime migration. Lease claims, conversation-level sequencing
and tree deletion are separate operations that must reuse canonical components
with their original atomic guards; this pair's evidence is not proof of those
unmigrated operations. All application/Core changes remain uncommitted for review.
