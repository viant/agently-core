# Turn migration

The canonical generated writer now retains the legacy insert-only creation-time
rule: a new row receives the current time even when a client supplies one.
Existing rows retain sparse updates and explicit null/zero presence through the
native writer. Application hooks retain creation-time and blank-deletion identity rules;
generated indexes, validation, DML and transaction ownership are unchanged.

`TestTurnWriterLegacyParity` contains fourteen data-driven real legacy/new cases.
It compares the legacy list projection, all physical stored columns, and writer
responses. Coverage includes required fields, invalid foreign references,
sparse changes, explicit null/zero/empty values, creation defaults,
mixed insert/update, invalid batch rollback and empty/null batches.
`TestTurnWriterCallerTransaction` verifies caller rollback and commit, including
observing the mutation while the transaction remains pending.

Focused checks passed in /tmp/agently-turn-writer-complete-parity.log (16.661s).
The reader and writer are selected in the nested Datly 1.0 host and fully
verified by /tmp/agently-turn-canonical-endly.log. Root/application callers
remain legacy.

The table now has one canonical reader and writer. The queued-count and controller-count drafts now share one Turn fact cube. Active selection
chooses the newest running or
waiting-for-user row; next-queued selection chooses the smallest nullable queue
sequence, then creation time and ID. List cursors retain creation-time/ID ties.
Deletion now uses the same writer through an explicit transient shouldDelete
marker and native onDeleteNotFound=ignore. Seven legacy deletion cases passed,
including repeated/missing/blank IDs and rollback after a late delete rejection.
Four mixed delete/update/insert tests passed for owned commit, insert rejection
rollback, caller rollback and caller commit. Deletion regeneration and full-suite
validation passed; conversation-tree caller integration is still outstanding.

Canonical reader consolidation passed 16 real legacy comparisons and four
scope/selector cases. The redundant active, by-ID, next-queued and queued-list
DQL/generated drafts are removed; the original legacy packages remain as the
application/probe baseline. The canonical reader requires a trusted host mode,
retains status/time/cursor predicates, and permits native fields/limit/offset
selectors. Queue discovery forces only id and queue_seq. SQLite interval tests
use bounds between records because legacy text timestamps and bound time values
have different representations at an exact lower boundary.

The previous full suite failed when a shared legacy probe edit was read before
its helper was appended; the completed probe now builds. Latest full Endly gate:
/tmp/agently-turn-canonical-endly.log. The gate passed (430.305s E2E; 473283ms workflow), including regeneration,
boundary checks and executable build. The catalog has 52 contracts: 24 fully
verified and 28 remaining drafts.

The fact cube now exposes RecordCount, QueuedCount and ControllerCount.
Conversation/status/origin dimensions and declared filters preserve the fact
grain; conditional counts return zero for empty sets. Eight real legacy count
comparisons and seven native aggregate/group/filter/authorization cases passed.
Only trusted host calls are allowed; request filters cannot grant this scope.
The two generated count-reader drafts were removed. Full cube Endly validation:
/tmp/agently-turn-cube-endly.log (SUCCESS; 450.493s E2E). Root callers remain legacy.

Catalog now contains 51 contracts: 25 fully verified and 26 unlinked drafts.
