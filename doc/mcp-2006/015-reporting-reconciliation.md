# 015 — Reconcile Reporting State and Portable Report Tools

Status: planned  
Parent workstream: WS-9  
Primary repositories: `forge`, `agently-core`, `agently`  
Depends on: [009](009-apps-server-and-mcp-ui-compatibility.md), [013](013-agently-forge-app-integration.md), [014](014-durable-view-state.md)

## Outcome

Unify report drafts with durable view identity while preserving the existing
saved-report SQL domain model. Expose portable MCP report tools so report
servers remain useful in generic MCP hosts without Forge UI.

## Ownership model

- unsaved report-builder state is a revisioned draft keyed by view UUID;
- saved reports retain their domain ID and authoritative SQL-backed lifecycle;
- a view may reference a saved report/version but does not become its owner;
- browser `reportBuilder.state` is migration input/cache, never authority;
- Forge UI actions are optional adapters over portable MCP tools.

## Entry decisions and ownership

Record the durable view-store/retention policy and supported opaque-App
`ui/report` actions before implementation. The report-serving MCP module owns
portable tool contracts and authorization; `agently-core` owns view-draft
persistence; `agently` owns host binding/migration; Forge owns optional UI
adapters. Name or create the report-server module before tool implementation.

## Deliverables

- draft schema integrated with task 014's ownership and revision controls;
- safe migration from current local report-builder keys with collision checks;
- explicit create/save/discard/reopen transitions between draft and report;
- versioned, namespaced `get`, `run`, `save`, and `export` tools with schemas,
  authorization, and saved-report IDs; draft reopen/fork/export uses authorized
  view-state APIs keyed by view UUID until a portable draft contract is approved;
- Apps UI metadata and optional Forge report/window adapters;
- live, restored, and offline behavior with stable error semantics;
- draft retention, deletion, redaction, and concurrent-edit policy;
- export path that does not require an iframe or Forge host.
- metrics for checkpoint/save/export outcomes, legacy-key use, migration,
  quarantine, and cross-owner rejection.

## Implementation steps

1. Inventory report SQL schema, builder storage, endpoints, and window actions.
2. Define draft-to-saved state transitions and authorization invariants.
3. Implement versioned draft persistence and guarded legacy-key import.
4. Implement portable report tools with complete structured results.
5. Bind Apps UI to those tools; bind Forge commands only as enrichment.
6. Add concurrency, discard, autosave/checkpoint, and retention behavior.
7. Migrate canary drafts and reconcile counts/hashes before broad rollout.

## Tests

- create draft, edit, save, reopen, fork, discard, and export;
- two report views in one conversation and identical local legacy keys;
- cross-conversation/tenant isolation and guessed draft/report IDs;
- save/open through another compliant MCP host without Forge;
- export through a non-UI host;
- concurrent edits, stale report version, and draft/report conflict;
- restored history with live server, offline server, and deleted report;
- legacy migration is idempotent and never overwrites a newer draft.
- draft size/rate bounds and secret/token redaction;
- flag-disabled rollback makes editors explicitly read-only/error and never
  silently falls back to local storage.

## Acceptance criteria

- Draft state follows view identity; saved report state follows report identity.
- Core report operations work without Agently or Forge.
- Forge adds window behavior without becoming the data authority.
- No report or draft leaks across conversations or tenants.
- Legacy local state is either safely imported or explicitly quarantined.

## Rollout and rollback

Import local drafts lazily with an audit marker and keep source data until the
reconciliation window closes. Rollback disables new draft writes and Forge
adapters, makes active drafts explicitly read-only, and leaves saved report
tools and readable revisions intact. Import an unattributed browser draft only
into the mounted authorized view; quarantine ambiguous data.

## Completion evidence

- State-transition test: _pending_
- Portable-host interoperability test: _pending_
- Legacy migration reconciliation: _pending_
- Isolation/concurrency report: _pending_
- Observability/legacy-usage dashboard: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
