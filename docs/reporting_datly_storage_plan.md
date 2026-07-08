# Plan: Reporting Storage Layer That Mimics Datly

Date: 2026-07-07

## Goal

Replace the current workspace-filesystem reporting persistence with a durable
DB-backed storage layer that follows the same design style already used by
Datly-backed entities in `agently-core`.

The target is:

- durable across deployments
- user scoped and gated
- compatible with the current `reporting` tool/service API
- able to store reports, export jobs, export artifacts, and audits
- safe by default against unnecessary report data leakage

## Why Datly-Like

`agently-core` already uses a recognizable Datly pattern for persisted
application entities:

- typed row structs with `sqlx` tags
- generated or declared query/read components
- custom write handlers for upsert / patch logic
- connector-based DB access

Examples:

- read component:
  [payload.go](/Users/awitas/go/src/github.com/viant/agently-core/pkg/agently/payload/payload.go:29)
- write component:
  [payload/write/index.go](/Users/awitas/go/src/github.com/viant/agently-core/pkg/agently/payload/write/index.go:12)
- write handler:
  [payload/write/handler.go](/Users/awitas/go/src/github.com/viant/agently-core/pkg/agently/payload/write/handler.go:15)

The reporting store should mimic that same shape instead of remaining a custom
filesystem-only side path.

## Current Gap

Today reporting persistence is implemented as a custom `app/store/reporting`
client with:

- `fs` backend
- `memory` backend

Store seam:

- [client.go](/Users/awitas/go/src/github.com/viant/agently-core/app/store/reporting/client.go:17)

Current filesystem store:

- [fs/store.go](/Users/awitas/go/src/github.com/viant/agently-core/app/store/reporting/fs/store.go:21)

That is fine for local development, but not for a deployment where workspace
state can be wiped or replaced.

## Design Principle

Do **not** replace the `reporting.Service` contract.

Keep:

- `reporting:save_report`
- `reporting:get_report`
- `reporting:list_reports`
- `reporting:update_report`
- `reporting:export_report`

Only replace the persistence implementation behind the current reporting store
interface.

## Recommended Architecture

### 1. Add a SQL-backed reporting store

Create a new package:

- `app/store/reporting/sql`

This package implements the existing `reporting.Client` interface and becomes
the production backend.

The filesystem backend remains:

- local fallback
- test fallback
- migration source

### 2. Mirror Datly package structure

Recommended layout:

```text
app/store/reporting/sql/
  store.go
  schema/
    report_shared_artifact.sql
    report_export_job.sql
    report_export_artifact.sql
    report_audit_event.sql

pkg/agently/reporting/
  artifact/
    report.go
    list.go
    get.go
  artifact/write/
    artifact.go
    input.go
    output.go
    handler.go
    index.go
  export_job/
    job.go
    list.go
  export_job/write/
    ...
```

This mirrors the existing Datly-style split:

- read views / query shapes
- write structs
- write handlers

### 3. Separate tables by lifecycle concern

Use four durable tables.

#### `report_shared_artifact`

Stores saved reports and published snapshots.

Main columns:

- `artifact_id`
- `artifact_ref`
- `owner_id`
- `owner_ref`
- `kind`
- `lifecycle`
- `version`
- `report_id`
- `title`
- `source_artifact_id`
- `base_artifact_ref`
- `policy_ref`
- `document_version`
- `report_document_json`
- `report_spec_json`
- `compile_state_json`
- `report_fill_json`
- `report_print_json`
- `saved_view_overlay_json`
- `metadata_json`
- `created_at`
- `updated_at`

#### `report_export_job`

Stores async export job lifecycle.

Main columns:

- `job_id`
- `artifact_ref`
- `owner_id`
- `conversation_id`
- `workspace_id`
- `auth_context_ref`
- `format`
- `scope`
- `status`
- `report_spec_json`
- `report_fill_json`
- `report_print_json`
- `metadata_json`
- `artifact_id`
- `error_text`
- `diagnostics_json`
- `submitted_at`
- `started_at`
- `completed_at`
- `retention_ttl_sec`

#### `report_export_artifact`

Stores completed export artifact metadata.

Main columns:

- `artifact_id`
- `job_id`
- `artifact_ref`
- `owner_id`
- `format`
- `content_type`
- `storage_kind`
- `blob_uri`
- `inline_data`
- `size_bytes`
- `created_at`
- `retention_ttl_sec`

#### `report_audit_event`

Stores audit records.

Main columns:

- `event_id`
- `event_type`
- `artifact_ref`
- `job_id`
- `artifact_id`
- `actor_id`
- `occurred_at`
- `metadata_json`

## User Gating

User scoping must remain strict and become query-level, not only app-level.

Current visibility logic is:

- [service.go](/Users/awitas/go/src/github.com/viant/agently-core/service/reporting/service.go:1722)

The SQL-backed design should enforce the same rule in every read/update query:

- `owner_id = effectiveActorID(ctx)`

Examples:

```sql
SELECT *
FROM report_shared_artifact
WHERE artifact_ref = :artifact_ref
  AND owner_id = :owner_id;
```

```sql
UPDATE report_shared_artifact
SET updated_at = CURRENT_TIMESTAMP,
    title = :title,
    report_spec_json = :report_spec_json
WHERE artifact_id = :artifact_id
  AND owner_id = :owner_id;
```

Required indexes:

- `(owner_id, artifact_ref)`
- `(owner_id, report_id)`
- `(owner_id, kind, lifecycle, updated_at)`
- `(owner_id, submitted_at)` for export jobs

## Safe Storage Defaults

This migration should improve durability **and** reduce leakage.

### Default rule

For ordinary saved reports:

- persist `reportDocument`
- persist `reportSpec`
- persist `compileState`
- persist `metadata`
- do **not** persist `reportFill` by default
- do **not** persist `reportPrint` by default

Persist `reportFill` / `reportPrint` only when the lifecycle requires a
snapshot:

- published snapshot
- export bundle
- explicit “save snapshot” behavior

Reason:

current saved reports can carry result rows, which is a bigger leakage risk than
the auth model itself.

## Datly-Like Read/Write Model

### Read path

Use Datly-style query components for:

- get report by `artifact_id`
- get report by `artifact_ref`
- list reports by owner / kind / lifecycle
- get export job
- list export jobs
- get export artifact
- list export artifacts

These should be simple and declarative.

### Write path

Use custom write handlers for:

- save report
- update report
- create export job
- transition export job status
- persist completed export artifact
- write audit event

Reason:

- patch semantics are easier to control
- owner scoping can be enforced centrally
- we can keep report-fill persistence conditional

This mirrors current Datly write handler patterns:

- [payload/write/handler.go](/Users/awitas/go/src/github.com/viant/agently-core/pkg/agently/payload/write/handler.go:32)
- [conversation/write/handler.go](/Users/awitas/go/src/github.com/viant/agently-core/pkg/agently/conversation/write/handler.go:35)

## DB Choice

### Production

Prefer:

- `Postgres`

Acceptable:

- `MySQL`

### Local dev

Allow:

- `sqlite`

Reason:

- sqlite is fine for local and CI
- Postgres/MySQL is safer for multi-node deployment durability

## Migration Path

### Phase 0. Schema and component scaffolding

Deliver:

- SQL DDL files
- SQL-backed reporting store implementation
- Datly-style row structs and query components
- write handlers for report and export entities

Acceptance:

- store satisfies the existing `reporting.Client` interface
- no reporting service API changes

### Phase 1. Dual-read migration tooling

Deliver:

- migration utility that reads:
  - `state/reporting/shared_artifacts/*.json`
  - `state/reporting/jobs/*.json`
  - `state/reporting/artifacts/*.json`
  - `state/reporting/audits/*.json`
- writes rows into SQL tables

Acceptance:

- migrated records remain accessible via current reporting tools
- owner scoping is preserved

### Phase 2. Safe-save policy

Deliver:

- default save path excludes `reportFill` and `reportPrint`
- snapshot/export paths still persist fill/print where needed

Acceptance:

- save/reopen report still works
- export still works
- saved report leakage is reduced

### Phase 3. Cutover

Deliver:

- config switch for reporting store backend:
  - `fs`
  - `sql`
- steward deployment uses `sql`

Acceptance:

- reports survive deployment/workspace recreation
- user scoping works exactly as before

### Phase 4. Filesystem retirement

Deliver:

- `fs` backend retained only for local dev/tests
- production no longer depends on workspace state for reports

## Config Plan

Add reporting store config under steward / runtime config, for example:

```yaml
default:
  reporting:
    enabled: true
    store:
      backend: sql
      connectorRef: agently
      driver: postgres
      tablePrefix: report_
```

Alternative for local:

```yaml
default:
  reporting:
    enabled: true
    store:
      backend: sql
      connectorRef: agently
      driver: sqlite
```

## What Not To Do

Do not:

- create a second persistence API outside `reporting.Service`
- let Forge write directly to DB
- skip owner scoping in SQL queries
- persist full `reportFill` for every ordinary save
- keep production durability tied to workspace filesystem state

## Recommended First Implementation Slice

Build this first:

1. `report_shared_artifact` SQL table
2. SQL implementation of:
   - `CreateSharedArtifact`
   - `GetSharedArtifact`
   - `ListSharedArtifacts`
   - `UpdateSharedArtifact`
3. config switch to choose `sql` backend
4. migration import from `state/reporting/shared_artifacts`

That gives the biggest product win fastest:

- saved reports become durable
- report reopen survives redeploy
- no need to finish export-job migration before proving the model

## Final Recommendation

Proceed with a Datly-like SQL reporting store.

The best design is:

- keep current `reporting.Service`
- add a SQL backend under the existing store seam
- mimic Datly’s read/query + write-handler structure
- enforce user gating in SQL
- store less report data by default
- migrate filesystem records once

That gets us durable report storage without changing the front-end or tool
contract.
