Implement seamless report persistence and export for Report Builder, with
storage and export orchestration owned by `agently-core`, while keeping Forge
responsible for report authoring/runtime models and steward consuming the
capability through assigned MCP tools.

Use the current codebase as authoritative. Do not redesign from scratch when an
existing seam already exists.

Current evidence:

- `agently-core/service/reporting/service.go` already exposes reporting tools
  for export jobs, artifacts, and shared artifacts.
- `agently-core/service/reporting/types.go` already has canonical export job
  and artifact types.
- `agently-core/app/store/reporting/*` already provides reporting persistence.
- `forge/src/reporting/reportExportRequestModel.js` already builds canonical
  `reportExportRequest`.
- `forge/backend/reporting/export/model.go` already validates PDF/XLSX/CSV/HTML
  export requests.
- `forge/backend/reporting/export/pdf/*` and `forge/backend/reporting/export/xlsx/*`
  already exist.

What to implement:

1. Add a first-class MCP-backed report store in `agently-core` with tools:
   - `save_report`
   - `get_report`
   - `list_reports`
   - `update_report`
   - `export_report`

2. Keep Forge responsible for:
   - building `reportDocument`
   - building `reportSpec`
   - building `reportFill`
   - building `reportPrint`
   - building export requests

3. Ensure backend export runs under the current authenticated
   conversation/session context:
   - do not require the UI to pass raw tokens/cookies
   - derive auth context on the server
   - persist a server-side auth context reference on export jobs
   - allow backend MCP/data fetching to execute as the current user

4. Extend the export contract to support optional backend metadata for PDF
   generation:
   - add optional `metadata` to the canonical report export request
   - make it available in `agently-core` reporting and Forge backend/export
   - do not store raw credentials there

5. Update Forge UI:
   - use `Presets` instead of chart-only naming
   - use `Export` menu with `PDF` and `XLSX`
   - reduce chart-view meta strip noise

6. Update steward config/tool assignment only as a consumer:
   - steward should get assigned the new MCP tools
   - steward should not own storage implementation

Required deliverables:

- code changes across `agently-core`, Forge, and steward config if needed
- MCP tool schemas
- stored report record shape
- export job record shape
- auth-context propagation design
- tests
- proof artifacts
- short migration notes

Guardrails:

- do not put persistence logic in steward-only code
- reuse existing Forge report/export models where possible
- prefer minimal new abstractions
- preserve existing export job/artifact flow in `agently-core`

Please return:

1. A concise implementation plan tied to real files.
2. The code changes.
3. Tests added/updated.
4. Notes on any remaining gaps or follow-up work.

