# ModelCall migration

The canonical transcribed writer now handles insert, sparse update and deletion.
Native current-row reads and sparse DML replace the legacy manual SELECT *,
INSERT and dynamic UPDATE. Authored application Go only retains the legacy
blank deletion identity acknowledgment rule. Deletion uses native markers and
onDeleteNotFound=ignore; message, turn and payload parent rows are preserved.

Thirty-five real fixture cases pass: twenty legacy writer state/response
comparisons, seven legacy deletion comparisons, two caller transaction cases,
two first/second update failure cases and four mixed mutation transactions.
Legacy raw *sql.DB writes partially commit before a later failure; the generated
writer rolls the entire mutation back. This intentional correction is asserted.

Legacy modernc SQLite timestamp strings remain readable through Datly's
6507cebb compatibility fix. The driver accepts Time.String text without changing
its default write format. Timestamp fields remain in complete parity checks.

Focused evidence: /tmp/agently-modelcall-canonical-writer-tests.log (46.491s).
Full Endly gate: /tmp/agently-modelcall-writer-endly.log (SUCCESS; 497.513s E2E). The writer
is verified and linked. A canonical table reader, one ModelCall fact cube,
and root/application caller cutover remain outstanding.

Catalog at the writer checkpoint: 26 fully verified contracts and 25
unlinked drafts (51 total). These counts do not establish application
cutover or a completed root no-SQL audit.

Canonical reader source: datlyv1/dql/agently/modelcall/read/reader.dql.
Destination: datlyv1/generated/agently/modelcall/read. It supports ordinary
fact rows and trusted transcript mode, which preserves assistant-role selection.
Public/owner scope stays outside optional predicate groups. Native fields, limit
and offset selectors are declared and tested. Four real transcript comparisons
cover all physical columns; seventeen native scope/predicate/selector cases and
two HTTP host-input override cases pass. Historical SQLite timestamp text is
included in the actual native reader path. Payload enrichment remains owned by
conversation composition; this scalar reader does not claim to replace it.

Full reader gate: /tmp/agently-modelcall-reader-endly.log (SUCCESS; 518.188s E2E). One ModelCall
fact cube and application caller cutover remain. Catalog now has 52 contracts:
27 fully verified and 25 unlinked drafts.

Fact cube source: datlyv1/dql/agently/modelcall/cube/reader.dql. Destination:
datlyv1/generated/agently/modelcall/cube. One cube exposes record count, nine
token sums and cost, with conversation/provider/model/model-kind/status/role
dimensions. Cost is NULL when any contributing value is unknown, preserving
legacy usage completeness. Role derivation preserves router, explicit modes,
sidecar agents and react fallback. No authored Go handler or hook is needed.

Nine original usage/model grouping comparisons and twelve native scope/filter/
empty cases pass. Required host internal/subject bindings remain outside request
filters. Full cube gate: /tmp/agently-modelcall-cube-endly.log (SUCCESS; 532.823s E2E).
Existing conversation composition and root callers still need to be redirected
as part of caller cutover; adding this cube does not claim that cutover.

Catalog now has 53 contracts: 28 fully verified and 25 unlinked drafts. These are artifact counts, not application completion.
