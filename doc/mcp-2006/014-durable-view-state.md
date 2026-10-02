# 014 — Implement Durable Revisioned View State

Status: planned  
Parent workstream: WS-8  
Primary repositories: `agently-core`, `agently`, `forge`  
Depends on: [011](011-transcript-and-history-migration.md), [013](013-agently-forge-app-integration.md)

## Outcome

Persist view state so history navigation, reload, new tab, and authorized device
restore the intended view revision. Keep host shell state, standard App state,
and optional Forge state separate and enforce ownership, concurrency, retention,
and deletion centrally.

## State model

Each record includes view UUID, schema version, monotonic revision, owner and
tenant, conversation and tool-call IDs, resource identity, state class, content
hash, timestamps, retention class, and optimistic-concurrency token. Server-
issued task/resource IDs remain distinct from host-local view IDs.

State classes:

- host shell: placement, bounds, visibility, focus, and display mode;
- App: opaque, validated, size-limited checkpoint state;
- Forge profile: optional extension-owned state;
- report draft references, implemented by task 015.

## Entry decisions and repository ownership

Before schema implementation, record the durable store/retention policy,
server-issued versus host-local view ID policy, and whole-document CAS versus
typed-patch/lease conflict model in the parent proposal.

- `agently-core` owns the store interface, schemas, authorization, CAS,
  retention, and internal `Get`/`Checkpoint`/`Delete` service API;
- `agently` owns host lifecycle checkpoints, restore ordering, conflict UX, and
  browser-cache migration;
- `forge` owns serialization of shell/optional Forge state but cannot bypass
  `agently-core` authorization or revisions.

## Deliverables

- durable store interface and selected implementation;
- revisioned writes with compare-and-swap and idempotency keys;
- ownership/tenant checks on every read and mutation;
- multi-tab/device leases or explicit conflict resolution;
- checkpoint triggers, rate/size limits, schema migration, and redaction;
- retention, export, conversation deletion, account deletion, and audit policy;
- browser cache used only as a recoverable performance layer;
- restoration integration with transcript projection and Forge windows.
- a feature flag that disables writes while retaining read-only recovery;
- restore-source metrics: transcript, durable state, browser cache, or none.

## Implementation steps

1. Select the store and document consistency, availability, and retention.
2. Define schemas and migrations for each state class.
3. Implement authorized get/checkpoint/CAS/delete operations.
4. Add leases or conflict UI for concurrent editors and stale revisions.
5. Integrate lifecycle checkpoints at meaningful, bounded events.
6. Restore state after canonical transcript input/result initialization.
7. Connect conversation/account deletion and retention jobs.
8. Instrument conflicts, failures, size rejection, and restoration latency.

## Tests

- A→B→A, reload, new tab, and authorized second-device restoration;
- unauthorized user/tenant/conversation and guessed view UUID;
- concurrent edits, stale CAS, duplicate idempotency key, and lease expiry;
- schema upgrade/downgrade, oversized state, malformed state, and redaction;
- conversation deletion, account deletion, retention expiry, and export;
- browser cache loss with successful durable recovery;
- repeated same-resource views retain separate revision histories.
- resource version drift follows task 011's historical-resource policy;
- write-disabled recovery and legacy `sessionStorage` import or explicit
  abandonment behavior.

## Acceptance criteria

- Restored state is bound to the correct invocation and authorized principal.
- Concurrent writers cannot silently overwrite newer revisions.
- Durable correctness does not depend on browser local storage.
- Deletion and retention behavior is tested end to end.
- Host, App, Forge, and report state cannot overwrite one another.

## Rollout and rollback

Dual-write may be used only with reconciliation metrics and a bounded window.
Rollback stops checkpoints and reads the last compatible revision; never erase
newer records merely because an older runtime cannot interpret them. A
dual-write names both stores and reconciles revision/hash divergence. Opaque App
state receives structural, size, and secret-pattern controls only unless the
App supplies a declared redaction schema.

## Completion evidence

- Store decision record: _pending_
- Cross-device restoration test: _pending_
- Concurrency test: _pending_
- Retention/deletion audit: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
