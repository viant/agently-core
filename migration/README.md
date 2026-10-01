# Datly migration checkpoint

Migration work is in `agently-core-v1`, branch `v1`. The user authorized
Core and Agently migration/cleanup commits are on `v1`. October 1 live verification is active; completion remains unproven.
The original checkout remains separate.

## Current root layout

One Core module contains 65 explicitly typed DQL contracts (62 ordinary contracts and three base readers):

- `dql/`: canonical contract sources.
- `internal/datly/<entity>/{read,write,cube}`: stock-generated artifacts and authored lifecycle hooks.
- `internal/store/`: typed adapters and managed business compositions.
- `internal/datly/{host,link,queryselectors,codec,predicate,dbtime,invariant}`: private host/support capabilities.
- `scripts/datly/`, `tools/schema/`, `cmd/datly/`, and `e2e/datly/`: authoring and verification tools.

[components.json](../components.json), [table ownership](table-component-map.json),
[package relocation](package-relocation-map.json), and
[explicit contract shapes](root-contract-shape-validation.json) are the current inventories.
Table ownership comes from emitted root view tags; nested joins and computed
snapshots do not establish another physical-table component.

## Current verification status (October 1)

Core `ecdd1962` and Agently `b70ab461` are committed. All 62 contracts transcribe and regenerate 827 artifacts unchanged. The current full Core suite fails, including a generated message writer alias error and reporting cube failures. Live UI/CLI conversations and original-version transcript/database comparisons are pending. See [current acceptance](final-acceptance-audit.json).

## Historical validation scope (September 30)

Core and Agently full Go suites and builds pass against published Datly
`a59b17988b72`, unified xdatly v1 and published SQLX, without local framework
replacements. Full [Endly acceptance](published-endly-final-validation.json)
passes 62/62 transcription, byte regeneration of 823 artifacts, boundary audit,
all contract/store/native tests, and the linked command build.
[Core acceptance](published-core-final-validation.json) includes real application
HTTP and embedded SDK tests. Final MySQL Data and independent-host scheduler
lease checks pass. OAuth token/state MySQL contention and provider-width checks also pass.
Public IdP discovery/JWKS verification passes. Token exchange and live scheduler
authorization with secrets are deferred by the user.
See [final requirements](final-acceptance-audit.json) for remaining gates.

Before the namespace move, actual SDK1 application tests passed across Go,
TypeScript (413 tests), Swift (91), and Android (97), including JSON/auth/SSE,
report lifecycle/adoption, export retry, audit/artifact access, and queue edits.
MySQL reporting checks passed independent-store contention, late rollback,
current reads under a caller snapshot, and caller-owned commit/rollback.
See [reporting/SDK/MySQL evidence](reporting-sdk-mysql-validation.json),
[application HTTP evidence](application-http-validation.json), and
[mobile evidence](mobile-sdk-server-validation.json). Those results establish
behavior before relocation. Fresh [root namespace caller gates](root-layout-caller-validation.json)
also pass full Go SDK/server/reporting callers, TypeScript413, Swift91, Android97,
and MySQL reporting contention/caller transactions.

Published-dependency [OAuth MySQL acceptance](oauth-mysql-acceptance-validation.json)
passes provider-width/metadata, independent-pool refresh lease/CAS winner checks,
and single-use state creation/consume/replay guards.

Authorization behavior is preserved. The known direct conversation detail-read
ownership gap is a follow-up after merge to main, per the user's instruction.
Generated component coverage alone is not a public caller compatibility claim.

## Tooling

```sh
(cd e2e/datly && endly -r=build)
(cd e2e/datly && endly -r=transcribe)
(cd e2e/datly && endly -r=regeneration)
(cd e2e/datly && endly -r=run)
(cd e2e/datly && endly -r=core)
```

Endly acceptance builds, invokes operation-specific transcription through the
pinned native Datly CLI, compares regeneration bytes, and runs native Go tests.
The separate `endly -r=validate` diagnostic currently fails on native writer
planning for `delete_not_found`; it is not reported as a passing acceptance gate. The three base readers use the narrow existing-public-API
Go task under `e2e/datly/authoring` for canonical input and named resource authority.
There are no committed Python authoring or verification wrappers. See
[tooling instructions](../scripts/datly/README.md).

## Historical milestones

The following checkpoints preserve earlier counts, paths, and limitations at
that time. They are historical evidence, not the current runtime state.

### Agently Core Datly 1.0 migration checkpoint

Work is isolated in `agently-core-v1`, branch `datly_1`, based on local main
`44b8887846c22dfd47f7d788c34ea12ceaa5c5ae`. Core changes are uncommitted for
review. The original `agently-core` checkout is left alone.

#### Current coverage

58 contracts are verified and linked: the original 42-contract legacy catalog
plus reader/writer pairs for `tool_execution_claim`,
`conversation_report_context`, `report_run`, `report_export_job`,
`report_export_artifact`, `schedule_run`, `investigation` and
`report_audit_event`. All 21 physical conversation-deletion tables now have
canonical mappings; the original catalog alone did not cover them. Other
direct product SQL and caller cutover work remains; see
[coverage audit](delete-table-coverage-audit.json).
The root `go.mod` now pins local Datly 1.0, SQLX and unified xdatly v1, but
legacy root source still imports removed SDK0 APIs, so the root build is
currently incomplete. See [root cutover evidence](root-sdk1-cutover-validation.json).
The application still uses SDK0 persistence; SDK1 caller cutover, public
Go/TypeScript/Kotlin/Swift SDK compatibility, direct product SQL removal, final
original-Core backfill and hygiene remain pending.
The [cross-platform SDK contract](../doc/datly-sdk-contract.md) separates
Datly's private generated components from the public Go/TypeScript/Swift/Kotlin
wire surface and records the current validation baseline.

[components.json](../datlyv1/components.json),
[remaining-work.json](remaining-work.json) and
[table-component-map.json](table-component-map.json) are the current inventories.
Older component checkpoint documents retain historical counts.

Message has one canonical reader, one writer and one fact cube:

- Writer: 34 checks cover legacy storage/output parity, per-turn sequencing,
  supplied NULL/zero, caller transactions, MEDIUMTEXT truncation, deletion and
  cascade rollback.
- Reader: 45 checks cover scalar and transcript lookups, 29 persisted fields,
  narration/progress filters, gzip hydration, selectors and private relation
  scope. Host access and mode cannot be replaced by HTTP query values.
- Cube: 11 checks cover legacy pending counts, four native fact measures,
  ownership scope and nullable grouped dimensions.

The seven original Message reader/count drafts are retired. Their capabilities
are represented by these two generated components. See
[Message writer evidence](message-writer-validation.json) and
[reader/cube evidence](message-reader-cube-validation.json).

Tool approval queue now also has one reader, one writer and one fact cube.
Its 72 checks cover legacy writer/list/outcome/count behavior, mixed transactions,
idempotent deletion, personal ownership and selector forwarding. The full Endly
workflow passed, including 45/45 transcription, regeneration, all tests and build.
See [approval evidence](approval-validation.json).

#### Toolchain and boundaries

- Datly `v1.1.1-0.20260929192034-37431d0bac20`; local replace `../../datly`.
  Local `v1` is one commit ahead of the user-pushed `origin/v1` at `e3c62bb5434579ac9ca571eae754701f9072c4c7`.
- SQLX `v0.26.1-0.20260929151803-0df7f08c4926`; local replace `../../sqlx`.
- SDK1 `github.com/viant/xdatly v1.0.1-0.20260927175016-ff38d5bca9b6`.
- Authoring authority: Datly 1.0 reader/writer skills and native DQL grammar.

The nested module isolates SDK1; `migration/legacyprobe` executes original SDK0
contracts in separate processes against independent fixture databases.
Every reader/writer contract and its support comes from stock operation-based
`datly transcribe`. Application Go is limited to business hooks, predicates and
codecs. Schema/seed/observation SQL is confined to fixture tooling/tests.

The public client SDKs are a separate cutover surface: Go `sdk`, TypeScript
`sdk/ts`, Android `sdk/android`, and iOS `sdk/ios`. The current Agently UI
dependency and Android sibling-source setting point at the original
`agently-core` checkout; the iOS local package likewise defaults to an app
package link populated from original Core. These consumers must be switched to
this checkout and verified against migrated server contracts before the SDK
upgrade can be called complete.

#### Verification

Historical commands below used the former layout. Current verification runs from `e2e/datly`:

```sh
endly
### Individual tasks:
endly -t=transcribe
endly -t=regeneration
endly -t=verify
endly -t=build
```

The workflow transcribes all discovered DQL, checks byte-stable regeneration and
contract boundaries, runs the full nested-module suite, and builds the linked
host. Component parity does not establish root/application caller cutover.
The native OAuth store adapter now composes the generated link-state reader and
writer without direct SQL. Its seven data-driven lifecycle cases and the full
Endly workflow pass; see [adapter evidence](oauth-store-adapter-validation.json).
The root auth service still uses SDK0 and has not been switched to this adapter.
Forge shared-artifact CRUD now has a typed Datly 1.0 adapter. Eleven data-driven
cases compare it with the original reporting SQL store, including create-only,
update-only, strict delete and owner isolation. Datly exposes a typed strict-delete
not-found error for the adapter; see [Forge store evidence](forge-store-adapter-validation.json).
The root reporting store still uses SDK0 and direct SQL for other report tables.
The turn_queue application adapter now offers a single-call batch writer. Four
data-driven cases compare SDK0 and SDK1, including a late failure where native
Datly rolls back the batch and SDK0 leaves a partial update; see
[queue adapter evidence](turnqueue-store-adapter-validation.json).
The private queue-reorder custom component now composes the generated turn and
turn_queue writers under one managed transaction. Seven data-driven cases cover
success, stale input, late-failure rollback across both tables and caller-owned
commit/rollback. SDK0's separate queue patches leave a partial update on the
same late failure. Two independent SQLite callers produced exactly one successful
reorder in ten repeated runs; see [reorder evidence](queue-reorder-validation.json).
Root caller wiring and MySQL contention validation remain pending.
`tool_execution_claim` is the first table added after auditing conversation-tree
deletion. Its generated reader/writer pass 20 cases, including legacy DELETE
parity, guarded deletion, host trust and caller transactions; see
[claim evidence](tool-execution-claim-validation.json).
`conversation_report_context` now has one generated owner-scoped reader and one
CAS writer. A typed adapter matches the original SQL store in ten Get/Put cases;
additional tests cover guarded delete, caller transactions and two-connection
revision races. The full Endly gate passes 46/46 transcription and byte-stable
regeneration of 619 artifacts; see [report context evidence](report-context-validation.json).
Six deletion tables remain unmapped.
`report_run` now has one generated reader/writer and a typed adapter. Nineteen
cases cover full 26-field legacy read parity, create/update errors, revision
CAS, completed snapshot immutability, narrow adoption, caller transactions and
two-connection races. Full Endly passes 48/48 transcription and byte-stable
regeneration of 646 artifacts; see [report run evidence](report-run-validation.json).
The private adoption component now composes the run and context writers under
one managed transaction. Nine data-driven cases cover the legacy adoption
contract, late second-write rollback, caller-owned transactions, and repeated
SQLite contention; see [adoption evidence](report-adoption-validation.json).
Root caller wiring remains pending.
`report_export_job` and `report_export_artifact` now each have one generated
reader/writer pair. The job pair passes 26 cases for legacy read/create/claim/fail
parity and guarded native transitions. The artifact pair passes 23 cases,
including legacy Put/Get/List parity and a generated parent-job lookup in its
business hook. The full Endly gate passes 52/52 transcription and byte-stable
regeneration of 700 artifacts; see [job evidence](report-job-validation.json)
and [artifact evidence](report-artifact-validation.json).
The private run-export submit and completion components now compose the
generated run, job and artifact contracts under managed transactions. Ten
submit and six completion use cases compare with the SDK0 reporting store;
late completion failure rolls back artifact insertion. The full Endly gate
passes after host linkage; see
[export workflow evidence](report-export-workflow-validation.json).
Root reporting callers and application-level contention retry remain pending.
The `schedule_run` reader/writer now passes 15 deletion, scope and transaction
cases. Its disposable SQLite schema mirrors the existing Platform/Steward
Skeema table; public Core DDL and entities were left untouched. The full Endly
gate passes 54/54 transcription and 727 byte-stable artifacts; see
[schedule-run evidence](schedule-run-validation.json) and
[schema provenance](schedule-run-schema-validation.json). The disposable schema
also includes Skeema's `investigation` table for its reader/writer pair. The
Skeema checkout is a separate internal project used read-only; its remote was
unreachable during the freshness check, and the local
Platform and Steward copies matched each other.
The `investigation` reader/writer passes 15 legacy deletion, native guard,
reader-scope and caller-transaction cases. The full Endly workflow passes 56/56
transcription, 754 byte-stable artifacts, nested tests and host build; see
[investigation evidence](investigation-validation.json). Only the disposable
Endly fixture received the missing table definition.
The `report_audit_event` reader/writer passes 22 legacy deletion, guarded
append, reader-scope and caller-transaction cases. The existing Core and Endly
schema already contained this table. Full Endly passes 58/58 transcription,
781 byte-stable artifacts, nested tests and host build; see
[audit evidence](report-audit-validation.json). The legacy duplicate-event
skip policy still needs caller wiring through the generated reader before
append. The writer business hook now allocates UUIDs for audit-sink events whose
ID is absent; the 58-contract Endly gate passed again after this change.
The Agently app now links local Go, TypeScript, Android sibling-source, and iOS
SDK code from this V1 checkout. Standalone SDK suites and iOS/Android app tests
pass; the UI builds, but its full test suite and the independent Forge Android
suite have failures. See [client cutover evidence](client-sdk-cutover-validation.json).

#### Next work

OAuth link-state root caller/transport integration remains. Full Endly
transcription, regeneration, nested tests and host build pass. Scheduler live
IdP verification still needs the requested credential path. Once components and callers are migrated,
rescan the original Core and backfill its latest changes, remove direct product
SQL, and review package naming/hygiene.

Agently app integration uses a local Core replace; its earlier binary build and
focused Core/app unit checks passed. The app still links SDK0 persistence.
See [app validation](app-local-core-validation.json) and the newer
[client SDK baseline](client-sdk-cutover-validation.json).
