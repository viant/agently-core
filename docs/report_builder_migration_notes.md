# Report Builder Migration Notes

1. Enable reporting in the workspace runtime.

```yaml
default:
  reporting:
    enabled: true
```

2. Async export processing now starts automatically whenever reporting is enabled.

- If `default.reporting.queueIntervalMs` is omitted or `0`, `agently-core` now uses a fallback worker interval of `250ms`.
- `default.reporting.queueBatchLimit` remains optional; when omitted, the reporting worker uses its built-in default batch size.

3. Steward should consume reporting through assigned MCP tools, not through steward-only persistence code.

- Required tool surface:
  - `reporting:save_report`
  - `reporting:get_report`
  - `reporting:list_reports`
  - `reporting:update_report`
  - `reporting:export_report`

4. Backend export auth context is derived server-side.

- UI callers should not pass raw bearer tokens or cookies.
- Queued export jobs persist a derived `authContextRef` such as:
  - `actor=<user>;access=true;id=true`

5. Canonical export payloads must keep `reportFill.datasets[*].provenance.requestHash` in sync with the serialized dataset request.

- Forge already computes this with the same `fnv1a` hash used by backend validation.
- Hand-authored fixtures or tests that bypass Forge helpers must keep that hash aligned or XLSX / CSV export validation will fail.
