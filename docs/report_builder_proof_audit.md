# Report Builder Proof Audit

## Verified

### 1. Steward exposes reporting tools

Command:

```bash
./agently mcp list --api http://127.0.0.1:9191 --oob '~/.secret/awitas_dsp_ui.enc|blowfish://default' -s reporting
```

Observed tools include:

- `reporting:save_report`
- `reporting:get_report`
- `reporting:list_reports`
- `reporting:update_report`
- `reporting:export_report`

### 2. Live report persistence via MCP

Saved report artifact:

- artifact id: `89c4d5cf-f07c-4821-b615-df42c30e757f`
- artifact ref: `reportBuilder.savedReportPayload://report_codexLiveForecastingQ3`
- report id: `codexLiveForecastingQ3`

Verified operations:

- `reporting/save_report`
- `reporting/list_reports`
- `reporting/get_report`
- `reporting/update_report`

Representative proof files:

- `/tmp/report-save.json`
- `/tmp/report-list.json`
- `/tmp/report-get.json`
- `/tmp/report-update.json`

### 3. Live backend export with server-derived auth context

Submitted PDF export job:

- job id: `aaa64f0d-3d83-47ba-ad14-68d45d0ae585`
- format: `pdf`
- auth context ref: `actor=awitas_viant_devtest;access=true;id=true`

Completed PDF artifact:

- artifact id: `b4eee360-09f0-44c3-bc27-9e3ea3b80c24`
- content type: `application/pdf`
- artifact file: `/Users/awitas/go/src/github.com/viant-internal/steward_ai/deployment/steward/state/reporting/artifacts/b4eee360-09f0-44c3-bc27-9e3ea3b80c24.json`

Submitted XLSX export job:

- job id: `47c2ad7d-0d21-4a7b-9bc2-fa3bd9b947bd`
- format: `xlsx`
- auth context ref: `actor=awitas_viant_devtest;access=true;id=true`

Completed XLSX artifact:

- artifact id: `b5943d44-0f0c-4714-ad13-fee7f908f67d`
- content type: `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`
- artifact file: `/Users/awitas/go/src/github.com/viant-internal/steward_ai/deployment/steward/state/reporting/artifacts/b5943d44-0f0c-4714-ad13-fee7f908f67d.json`

### 4. Forecasting builder live prefill

Prompt:

- `open forecast builder for line 7288336`

Observed:

- builder opens successfully
- targeting is prefilled from the resolved line scope
- confirmation message is present in the conversation

Screenshot:

- `/Users/awitas/go/src/github.com/viant/agently/ui/test-results/forecasting-proof-harness-live-auth/chat-forecasting-window-line-prefill.png`

### 5. UI load of saved report file

Source file imported through the visible `Load report file` control:

- `/tmp/ui-import-proof/codex-live-report-file.json`

Observed:

- imported saved report payload is recognized
- imported report files section shows `1 record`
- saved report file summary is visible in the builder

Screenshot:

- `/tmp/ui-import-proof/import-probe.png`

Downloaded back through the visible UI button:

- `/tmp/ui-import-proof/downloaded-report-file.json`

Observed downloaded payload:

- `kind = reportBuilder.savedReportPayload`
- `title = Codex Live Forecasting Q3`
- `artifactRef = reportBuilder.savedReportPayload://report_codexLiveForecastingQ3`

### 6. Downloaded saved-report file import compatibility

Forge importer compatibility was extended so downloaded saved-report payloads
that carry `document` instead of `reportDocument` still build a reopen-ready
local get response.

Targeted guard:

```bash
node /Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderLocalImportDownloadedShape.test.js
```

### 7. UI load of richer authored saved-report payload

Imported fixture:

- `/tmp/ui-import-proof/capacity-location-saved-report-payload.json`

Observed:

- visible import succeeds with:
  - `Imported saved report payload Capacity Locations Top Markets Q3. Reopen in builder is ready.`
- richer saved payload creates:
  - an imported report file record
  - a derived reopen bundle
  - a current imported reopen artifact tracker

Screenshots:

- `/tmp/ui-import-proof/capacity-location-import-state.png`
- `/tmp/ui-import-proof/capacity-location-reopen-probe.png`
- `/tmp/ui-import-proof/capacity-location-reopen-probe-fixed.png`

### 8. Live top-level Presets / Export surface

Observed from the live Forecasting builder:

- `Presets` is visibly present and rendered in the result surface.
- screenshot:
  - `/tmp/ui-import-proof/presets-menu-proof.png`

Observed limitation:

- in the live line-prefill / runtime-action flow, the top-level `Export`
  button remains disabled even when result rows and drill actions are visible.
- backend export itself is already proven through MCP and artifact completion,
  so this is currently a UI state / affordance gap rather than a backend export
  capability gap.

### 9. Visible export controls from imported saved-report record

Imported fixture:

- `/tmp/ui-import-proof/capacity-location-saved-report-record.json`

Observed:

- the saved report file summary exposes visible export controls:
  - `Export snapshot`
  - `Inspect export`
- clicking `Export snapshot` submits a real UI request to:
  - `POST /v1/tools/reporting%3Aexport_report/execute`
- the submitted job is visible in backend export state:
  - job id: `af78de94-dc44-4f8c-b89c-15e5a9f13b33`
  - artifact ref: `reportBuilder.savedReportPayload://rbreport_capacity_q3_locations_top_markets`
  - format: `pdf`
  - status: `succeeded`
  - auth context ref: `actor=awitas_viant_devtest;access=true;id=true`
- screenshot:
  - `/tmp/ui-import-proof/capacity-location-saved-record-import.png`

This provides a browser-backed export proof path even though the plain live
Forecasting top toolbar `Export` button still remains disabled in the baseline
line-prefill runtime path.

### 10. Dead-code audit

A dependency graph pass over `forge/src/components/dashboard` found no clearly
unreferenced `reportBuilder*.js` / `reportBuilder*.jsx` source modules.

Current cleanup conclusion:

- there is no obvious orphaned Report Builder source file to delete outright
- remaining cleanup should focus on stale branches and overlapping flows inside
  active modules such as `ReportBuilder.jsx` and its import / reopen helpers

### 11. Tests

Passing targeted tests:

```bash
go test ./service/agent -run 'TestMaybeRunIntakeSidecar_OpenForecastBuilderForLineRoutesToBuilderAssist|TestMaybeRunIntakeSidecar_OpenForecastBuilderForLineNoRunPreservesNoRunScope|TestMaybeRunIntakeSidecar_OpenPerformanceBuilderForOrderRunsPrefilledWindowOpen'
go test ./app/executor -run 'TestBuilderBuild_DefaultReportingServiceWorkerProcessesQueuedExports|TestBuilderBuild_DefaultReportingServiceWorkerUsesFallbackIntervalWhenEnabled'
go test ./service/reporting ./app/executor -run 'TestForgeCSVExporter_ExportRendersCanonicalReportFill|TestForgeXLSXExporter_ExportRendersCanonicalReportFill|TestBuilderBuild_DefaultReportingServiceRunsForgeCSVExport|TestBuilderBuild_DefaultReportingServiceRunsForgeXLSXExport|TestBuilderBuild_DefaultReportingServiceWorkerProcessesQueuedExports|TestBuilderBuild_DefaultReportingServiceWorkerUsesFallbackIntervalWhenEnabled'
node /Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderSavedReportRecords.test.js
node /Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderLocalImportDownloadedShape.test.js
```

## Remaining

- Full UI-driven save-from-draft proof from the visible Report Builder surface.
- Explicit proof that clicking the visible `Reopen in builder` control promotes
  the reopened report into the full live builder session rather than only
  materializing the reopen bundle / diagnostics panels.
- Visible UI proof for an enabled top-level `Export` menu on the live builder
  surface, even though backend PDF / XLSX export is already proven.
- Deeper stale-branch cleanup inside active Report Builder modules.
- A full completion audit across every report-builder-related changed package, beyond the targeted proof and regression set above.
