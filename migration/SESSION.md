# Canonical session reader and writer

Session now uses one transcribed reader and one transcribed batch writer. The
writer accepts ordinary entities and explicit `shouldDelete` entities in the
same transaction; `onDeleteNotFound=ignore` retains idempotent leaf deletion.
The authored lifecycle hook preserves creation time defaults and legacy update
replacement fields and skips those defaults for delete requests. Generated code
owns identity matching, presence, mutation ordering and transactions.

Eleven real legacy write/read comparisons and six real legacy delete comparisons
use the same new writer. Legacy writes take a scalar and return a scalar; the
new canonical writer takes/returns a collection. The parity fixture wraps and
unwraps the singleton explicitly. The existing API caller still needs that
shape adaptation during runtime cutover; public API compatibility is not yet
claimed. Deletion acknowledgments remain a caller adaptation from transformed
request identities. The obsolete generated delete component has been removed;
the original legacy implementation remains the comparison baseline.

Additional data-driven checks cover mixed update/delete/insert commit,
late insertion failure rolling back updates and deletions, and caller-owned
commit/rollback. Test SQL only owns fixtures and observes stored state.
Application/Core changes remain uncommitted for review.
