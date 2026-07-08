# Report Builder Persistence And Export Plan

## Goal

Implement seamless report persistence and backend-driven export for Report Builder,
with:

- persistence and export orchestration owned by `agently-core`
- Forge responsible for report authoring/runtime/export request construction
- steward consuming the capability through assigned MCP tools
- backend export execution running under the current authenticated
  conversation/session context

## Current Evidence

### agently-core already has a reporting service

Relevant files:

- `/Users/awitas/go/src/github.com/viant/agently-core/service/reporting/service.go`
- `/Users/awitas/go/src/github.com/viant/agently-core/service/reporting/types.go`
- `/Users/awitas/go/src/github.com/viant/agently-core/service/reporting/worker.go`
- `/Users/awitas/go/src/github.com/viant/agently-core/app/store/reporting/*`

Existing reporting MCP/tool surface already supports:

- `submit_export`
- `get_export_status`
- `list_export_jobs`
- `list_export_artifacts`
- `get_artifact`
- `share_artifact`
- `transition_artifact`
- `get_shared_artifact`
- `list_shared_artifacts`

This means `agently-core` already owns:

- export job orchestration
- artifact persistence
- shared artifact persistence
- worker-based background execution

### Forge already builds canonical export payloads

Relevant files:

- `/Users/awitas/go/src/github.com/viant/forge/src/reporting/reportExportRequestModel.js`
- `/Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderRuntimePreview.js`
- `/Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/useReportBuilderExportExecution.js`

Forge already constructs a canonical `reportExportRequest` with:

- `target.format`
- `source`
- `reportSpec`
- `reportFill`
- `reportPrint`

Supported formats already include:

- `pdf`
- `csv`
- `xlsx`
- `html`

### Forge backend export contracts exist

Relevant files:

- `/Users/awitas/go/src/github.com/viant/forge/backend/reporting/export/model.go`
- `/Users/awitas/go/src/github.com/viant/forge/backend/reporting/export/pdf/*`
- `/Users/awitas/go/src/github.com/viant/forge/backend/reporting/export/xlsx/*`

The Forge backend already validates and renders:

- PDF using `reportPrint`
- XLSX using table-oriented export contracts

### Missing capability

What does not yet exist as a clean reusable cross-agent capability:

- a first-class MCP-backed report store in `agently-core`
- a canonical `save_report` / `get_report` / `list_reports` / `update_report` tool surface
- a canonical `export_report` tool that binds export execution to current
  auth/conversation context

## Architecture Direction

### Layer ownership

#### Forge

Forge should continue to own:

- report authoring UI
- `reportDocument`, `reportSpec`, `reportFill`, `reportPrint`
- export request construction
- local draft/saved-view UX

Forge should not own:

- durable cross-agent report storage
- export job storage
- auth-context replay logic for backend MCP-backed refetch

#### agently-core

`agently-core` should own:

- durable report persistence
- report store tool contracts
- export orchestration
- auth-context-backed export execution
- job/artifact persistence

#### steward

Steward should:

- receive assigned MCP tools from `agently-core`
- consume them
- not implement storage or export orchestration itself

## Target MCP Tool Surface

Add a report-store/tool surface in `agently-core`, ideally as part of the
existing reporting service or a sibling report service.

### Required tools

- `save_report`
- `get_report`
- `list_reports`
- `update_report`
- `export_report`

### Optional later tools

- `archive_report`
- `delete_report`
- `publish_report`

## Canonical Stored Report Record

Suggested stored report shape:

```json
{
  "reportId": "report_123",
  "workspaceId": "steward",
  "ownerId": "user_abc",
  "conversationId": "conv_456",
  "title": "Audience forecast review",
  "kind": "reportBuilder.savedReportPayload",
  "version": 7,
  "reportDocument": {},
  "reportSpec": {},
  "compileState": {},
  "metadata": {
    "agentId": "steward",
    "templateId": "forecast_review",
    "sourceArtifactKind": "dashboard.reportBuilder",
    "tags": ["forecast", "audience"]
  },
  "createdAt": "...",
  "updatedAt": "..."
}
```

Notes:

- store canonical reporting payloads, not UI-only state
- keep version for optimistic concurrency
- keep conversation/workspace linkage for seamless reopen/export

## Export Job Record

Suggested export job shape:

```json
{
  "jobId": "job_123",
  "reportId": "report_123",
  "artifactRef": "reportBuilder.savedReportPayload://report_123",
  "ownerId": "user_abc",
  "conversationId": "conv_456",
  "workspaceId": "steward",
  "format": "pdf",
  "status": "queued",
  "request": {},
  "authContextRef": "authctx_789",
  "metadata": {
    "requestedBy": "reportBuilder",
    "agentId": "steward",
    "locale": "en-US",
    "timezone": "Europe/Warsaw"
  },
  "submittedAt": "...",
  "startedAt": "...",
  "completedAt": "..."
}
```

## Auth-Context Propagation

### Requirement

Backend export must run with the same current user/conversation auth context
that the frontend already has, because the backend may need to call MCP/data
tools to resolve or refresh report data similarly to the frontend path.

### Rules

- do not pass raw cookies or raw bearer tokens from the browser
- do not rely on client-supplied user IDs as authority
- derive current auth/session context on the server
- persist a server-issued auth-context reference on the export job

### Proposed model

1. User triggers export in Forge.
2. Forge submits `export_report` through `agently-core`.
3. `agently-core` resolves:
   - current authenticated principal
   - current conversation/thread/workspace
   - current auth/session replay handle
4. `agently-core` stores the export job with an internal `authContextRef`.
5. Worker rehydrates auth from that reference.
6. Worker performs any MCP-backed refetch or export steps under that user
   context.

### UX requirement

This must be seamless to the user:

- no separate auth chooser
- no manual token/cookie field
- export just works from the active conversation

## reportExportRequest Contract Change

Current Forge export requests already carry:

- `target`
- `source`
- `reportSpec`
- `reportFill`
- `reportPrint`

To support backend PDF/XLSX generation with extra context, add:

- optional `metadata` object on `reportExportRequest`

Suggested contents:

- title/subtitle overrides
- locale/timezone
- workspace/agent identity
- conversation/thread ID
- render hints
- backend auth-context hint/ref

Do not put raw credentials in this field.

## UI Direction In Forge

### Presets

Use `Presets` instead of chart-only naming because the preset action can change:

- table/query shape
- chart

### Export

Use an `Export` menu with:

- `PDF`
- `XLSX`

Keep `Save report file` separate from export.

### Chart-view noise

Reduce chart-view chip noise:

- hide `Chart view`
- hide explicit chart type chip
- hide `page rows` on chart view
- keep only useful counts like measures/breakdowns/filters

## Suggested Implementation Sequence

1. Extend `agently-core` reporting types with:
   - report record type
   - report store interface
   - `export_report` request metadata/auth-context support
2. Add MCP tool methods:
   - `save_report`
   - `get_report`
   - `list_reports`
   - `update_report`
   - `export_report`
3. Bind export jobs to server-derived auth/conversation context.
4. Update Forge export request model/schema to allow `metadata`.
5. Update Forge Report Builder UI:
   - `Presets`
   - `Export` menu with `PDF` and `XLSX`
   - quieter chart-view meta strip
6. Assign new report tools to steward configuration.
7. Add tests and proof runs.

## Proof Expectations

Minimum end-to-end proof should include:

- save report through `agently-core`
- list/get/update report through `agently-core`
- export PDF through backend orchestration
- export XLSX through backend orchestration
- proof that export executes using current conversation/auth context

## Claude Delegation Prompt Seed

Use this prompt with Claude:

> Design and implement seamless report persistence and backend-driven export for
> Report Builder. Reuse existing Forge report/export contracts wherever
> possible. Put durable report storage and export orchestration in
> `agently-core`, not steward-only code. Add MCP-backed tools for `save_report`,
> `get_report`, `list_reports`, `update_report`, and `export_report`. Ensure
> backend export runs under the current user/conversation auth context without
> requiring the client to pass raw cookies or tokens. Extend the export request
> contract to support optional backend metadata for PDF generation. Update Forge
> UI to use `Presets`, an `Export` menu with `PDF` and `XLSX`, and a reduced
> chart-view meta strip. Include tests, proof artifacts, and migration notes.

