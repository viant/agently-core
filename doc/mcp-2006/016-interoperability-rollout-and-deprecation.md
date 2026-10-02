# 016 — Run Interoperability Rollout and Legacy Removal

Status: planned  
Parent workstream: WS-10  
Primary repositories: all participating repositories  
Depends on: [003](003-stateless-core-and-discovery.md) through [015](015-reporting-reconciliation.md)

## Outcome

Prove the July core and extension stack against independent hosts, roll it out
with measurable rollback points, and deprecate or remove legacy behavior only
after evidence—not merely elapsed time—shows it is safe.

## Interoperability matrix

Run the same MCP server and App bundle through:

1. the official Apps basic host and conformance fixtures;
2. Agently with Forge disabled;
3. Agently with Forge enabled;
4. at least two independently maintained production-capable Apps hosts;
5. a non-UI MCP host, verifying useful fallback tool results.

Record protocol/extension versions, feature results, deviations, artifacts, and
reproduction commands. Host-specific code in the server/App bundle is a failure
unless it is an explicitly negotiated optional profile.

An independent production-capable host is maintained outside Viant, supports
the pinned Apps lifecycle/security model, and yields reproducible evidence. If
fewer than two exist at the gate, one official plus one independent host may
proceed only with an architecture/security waiver; the missing cell stays open
and blocks claims of broad ecosystem interoperability.

## Deliverables

- automated core, Tasks, Apps, history, Forge, and reporting matrix;
- feature flags by protocol, extension, adapter, tenant, and tool;
- dashboards for negotiation, lifecycle failures, legacy use, restoration,
  security rejection, latency, and payload/cache budgets;
- canary stages and automatic/manual rollback thresholds;
- released-module CI with no unpublished local replacements;
- compatibility support table and operator/user migration guides;
- deprecation notices and removal decision records for old core versions,
  experimental Tasks, and private `mcpui:*` messages;
- archival reader strategy for retained historical transcripts.

Each library repository owns tagged-release CI. `agently-core` owns evidence
manifests and task 011's archival reader; Agently operations owns flags,
canaries, dashboards, and rollback; security owns sensitive removal approvals.

## Legacy removal gates

Private `mcpui:*` support may be removed only when all of the following hold:

- official Apps pass every required host/lifecycle/security scenario;
- usage is below the approved threshold for the approved observation window;
- remaining callers have an identified migration or explicit exception;
- rollback has been exercised in production-like conditions;
- retained transcripts remain readable through an archival adapter;
- release notes and support dates were published in advance.

The `mcp-ui` repository/module may remain as a thin compatibility release even
after new development stops. Deprecating its private protocol does not require
discarding reusable resource tooling.

Apply equivalent evidence gates—usage threshold/window, caller inventory,
migration, rollback rehearsal, archival readability where relevant, advance
notice, and owner approval—to old core versions, experimental Tasks, response-
payload URI discovery, `Experimental` UI capability, non-namespaced UI
metadata, URI-only IDs, and iframe Forge bridge startup. Record exact versions,
thresholds, windows, dates, and approvers in the parent before removal begins.

## Implementation steps

1. Materialize the matrix from tasks 001–015 in CI and staging.
2. Define SLOs, performance budgets, usage thresholds, and rollback triggers.
3. Publish tagged baselines for `mcp-protocol`, `mcp`, `mcp-ext`, `mcp-ui`,
   Forge, and Agently consumers.
4. Roll out discovery/core, then Tasks, Apps, history, Forge, and reporting in
   separately reversible stages, blocked by each README shared release gate.
5. Run independent-host and non-UI tests with preserved artifacts.
6. Observe the deprecation window and contact or block remaining legacy callers.
7. Remove only the behavior whose gates pass; keep adapters for failed gates.
8. Publish the final compatibility matrix and completion evidence.

## Tests

- every interoperability-matrix cell and negotiated version combination;
- upgrade, downgrade, mixed-version, canary abort, and rollback rehearsal;
- legacy adapter enabled/disabled with historical transcripts;
- no-UI fallback, Apps without Forge, and optional Forge profile;
- latency, memory, payload, cache, reconnect, and concurrent-view budgets;
- telemetry redaction and alert correctness;
- hostile-origin, cross-instance, authorization, negotiation, and fail-closed
  security fixtures across every applicable matrix cell;
- clean builds using tagged dependencies only.

## Acceptance criteria

- Required independent hosts run the same standards-based server/App bundle.
- A non-UI host receives useful tool behavior.
- Every rollout stage has tested detection and rollback.
- No legacy surface is removed without recorded gate evidence.
- The final support matrix identifies exact core/extension/module versions.

## Rollout and rollback

This task owns staged activation. Roll back the smallest failing layer and keep
new durable data readable. A rollback must not require destructive transcript,
view-state, or report migration.

## Completion evidence

- Interoperability matrix: _pending_
- Canary/rollback rehearsal: _pending_
- Tagged release graph: _pending_
- Legacy removal decision(s): _pending_
- Final support matrix: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
