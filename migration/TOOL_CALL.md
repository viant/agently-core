# ToolCall migration

Canonical writer: datlyv1/dql/agently/toolcall/write/writer.dql, transcribed into
datlyv1/generated/agently/toolcall/write. Native generated current reads,
validation, sparse DML, deletion and transaction ownership handle persistence.
Application lifecycle code only retains attempt defaults, original UTF-8 error
sanitation and blank deletion identity acknowledgment.

DQL now retains transient responseOverflow with sqlx ignore metadata. Payload
pointer JSON tags preserve legacy explicit nulls in responses. Deletion uses
native markers and onDeleteNotFound=ignore; parent message/turn/payload rows
remain unchanged.

Thirty-seven data-driven fixture cases passed: 24 real legacy writer state and
response comparisons, two caller transaction cases, seven deletion comparisons
and four mixed delete/update/insert transaction cases. Coverage includes
explicit zero/null values, long ASCII/UTF-8 errors, composite op/attempt
uniqueness, logical fields, references and rollback on failure.

Focused log: /tmp/agently-toolcall-canonical-writer-tests.log (33.714s).
Full gate: /tmp/agently-toolcall-writer-endly.log (SUCCESS; 577.182s E2E). The writer
is verified and linked. Three reader drafts still require one canonical reader,
followed by one fact cube and root/application caller cutover.

Catalog at writer checkpoint: 29 fully verified contracts and 24 unlinked
drafts (53 total). Application cutover and root no-SQL audit remain
unproven. Core changes remain uncommitted for review.

Canonical reader source: datlyv1/dql/agently/toolcall/read/reader.dql. Destination:
datlyv1/generated/agently/toolcall/read. Three previous reader drafts are
removed. Required host modes preserve optional operation lookup, always-scoped
operation lookup, consistent by-turn ordering and full rows. Lookup projections
match the small legacy contracts and avoid fetching unrelated metadata. Public
and owner scope remains outside optional filters and cannot be replaced by URL
parameters. Optional by-op empty conversation follows legacy omitted-filter
behavior; the always-scoped mode remains distinct.

Twelve original reader comparisons, eighteen scope/filter/selector checks, two
HTTP host-override checks and historical timestamp execution pass. Full gate:
/tmp/agently-toolcall-reader-endly.log (SUCCESS; 104.695s E2E). Legacy probe compilation is
now shared once per test run; fixture databases and executable processes remain
independent for every case. The ToolCall fact cube and caller cutover remain.

Catalog: 30 fully verified contracts and 21 unlinked drafts (51 total). These are component counts, not application completion.

One ToolCall fact cube is now transcribed at dql/agently/toolcall/cube. It
provides record/retry counts, latency sum/average and complete nullable cost,
with conversation/tool/kind/status/operation dimensions. The explicit
ConsistentTurn filter preserves old by-turn relationship constraints; ordinary
fact counts retain the table grain. Seven original row-count comparisons, ten
native scope/filter/empty cases and two grouping/known-cost cases passed.
Full gate: /tmp/agently-toolcall-cube-endly.log (SUCCESS; 111.346s E2E). Root callers remain
legacy. Catalog: 31 fully verified and 21 drafts (52 total).
