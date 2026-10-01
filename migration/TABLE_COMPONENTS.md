# Canonical table ownership

The current inventory is [table-component-map.json](table-component-map.json).
It classifies all 62 contracts from stock-emitted root reader output and writer
input view tags. Physical tables and computed snapshots are listed separately.
The current namespace is `dql/` plus `internal/datly/`; typed compositions live
in `internal/store/`. Six fact cubes cover message, model call, run, approval
queue, tool call, and turn. Input/output type settings and emitted structs match
for every contract; detailed presence/null/zero validation is a separate audit.

Stock `datly transcribe` owns generated runtime artifacts. Authored lifecycle
hooks are preserved during regeneration. Business cases reuse canonical
predicates, selectors, mutations, and managed units; root-table coverage does
not itself establish caller parity or prove that every fact grain is covered.

## Historical consolidation notes

These milestones retain their original counts and pending work at the time.

### Canonical table components

The user's target is one generated reader and one generated writer per physical
table, plus one cube per fact table. Business cases select predicates, selectors
and native mutation options; they do not create another persistence handler.
All contracts and plumbing remain owned by `datly transcribe`.

`table-component-map.json` inventories current root ownership from emitted
reader output and writer input view tags. Nested joins do not establish the root
table. Four computed message graphs still need explicit ownership review.

#### Reuse and consolidation

- Reuse `turn_queue`'s existing reader/writer for queue list, insert, sparse status
  transitions and deletion. Preserve ordering and atomic claim/lease guards.
- Consolidate `turn`'s seven readers into the table reader and its fact cube;
  queued/active/status filters use predicates and count/grouping queries use
  cube measures where supported by the connected runtime.
- Conversation get/list now share the canonical reader, with trusted host mode,
  verified visibility input and root-only list selectors. Transcript remains a composed graph
  with message/turn/call relations, preserving execution inclusion and pagination.
- Keep tree deletion atomic across table writers. Collect dependency identities
  through generated readers; generated writers own mutation order, markers,
  criteria, affected-row checks and rollback. Do not copy the legacy SQL loop.
- Consolidate pending/expired OAuth lookup and CAS/create/adopt behavior through
  the canonical table reader/writer after transport and caller parity passes.
- Keep selector proxying, authorization predicates, presence, ownership and CAS
  conditions explicit. Consolidation cannot relax them or overfetch by default.

#### First implemented consolidation

Goal now has one reader and one writer. The writer accepts its explicit transient
`shouldDelete` marker and native `onDeleteNotFound=ignore` policy. Normal write
output omits the false marker. The separate generated deletion package/source
and linkage were removed after insert/update/delete legacy parity passed.
Missing/mixed deletion identities are included in the data-driven comparisons.

#### Session and schedule progress

Session now has one reader and one batch writer, including explicit deletion.
Schedule now has one reader for public and trusted internal scope, and one
writer with legacy business defaults and change detection. Schedule deletion
also uses that writer; lease conditions still need to be folded into it; its callers
remain on legacy until the complete behavior passes.

#### Fact cubes

No current migration source declares a cube yet. Candidate fact grains evidenced
by the schema are model call (`model_call.message_id`, tokens/cost/latency), tool call
(`tool_call.message_id`, cost/latency/outcome), run (`run.id`, usage/cost/status) and turn
(`turn.id`, status/queue/elapsed behavior). Validate each grain, nullable measure
semantics and authorized grouping keys before generating its sole cube.
Queue, export and audit tables require separate grain/measure classification;
do not assume every entity table is a fact or blindly duplicate existing cubes.

Compare real legacy/new results for every consolidated case using
`useCase{desc,input,expect}`, including empty/NULL/zero inputs, scoped selectors,
guarded transitions, missing deletion, races, caller transactions and rollback.
Application caller cutover and the final whole-codebase no-SQL audit remain
required. All Agently/Core changes stay uncommitted for the user's final review.

#### Run fact cube consolidation

Run now has a canonical reader, writer, and fact cube. Scheduler totals use the
same count measure; the duplicate total-reader draft was removed after eight
real legacy comparisons passed. The cube also supports token/cost SUM measures
and selectable schedule/status/kind/owner dimensions, with nullable sums checked
in SQLite. Scope remains supplied by trusted host providers: three request-filter
override cases prove the subject, report mode and internal permission cannot be
replaced by cube input. The cube is privately linked, with no MCP tool exposure.
Root scheduler callers still await the application cutover.

Catalog: 31 fully verified contracts and 21 unlinked drafts (52 total).
These are component counts, not application migration completion percentages.
