# 013 — Integrate MCP Apps as Forge Virtual Windows

Status: planned  
Parent workstream: WS-7  
Primary repositories: `mcp-ext`, `agently`, `agently-core`, `forge`  
Depends on: [007](007-mcp-ext-foundation.md), [010](010-agently-apps-host.md), [011](011-transcript-and-history-migration.md), [012](012-forge-runtime-and-virtual-window-api.md)

## Outcome

Let a standards-compliant MCP App participate in Forge window management as an
opaque virtual view, while keeping optional Forge features in a separately
negotiated profile. The same server and UI bundle must continue to work in a
generic Apps host.

## Deliverables

- Agently parent adapter from Apps instances to Forge virtual windows;
- stable mapping among conversation, tool call, view UUID, and Forge window ID;
- inline, hosted-window, and top-level placement policies;
- an optional reverse-DNS Forge extension profile with explicit version and
  feature negotiation;
- mediated window/report commands and typed unsupported-capability responses;
- live versus historical/offline action policy;
- cleanup across iframe, AppBridge, parent adapter, and Forge window lifecycle;
- no same-origin DOM dependency and no shared global client/window identity.
- parent-only queue ownership: embedded App routes never start the main Forge
  bridge, and parent/iframe cannot consume one command queue.

## Entry decisions

Before implementation, record the Forge profile identifier/governance, the
server-issued versus host-local view ID policy, and the exact `ui/window` and
`ui/report` actions meaningful for opaque Apps in the parent proposal.
Historical live-only actions return typed unavailable outcomes here; durable
queued state changes are deferred to task 014.

## Architecture boundary

The App iframe speaks only AppBridge to its parent. The authenticated parent
maps approved requests into Forge commands. Standard Apps semantics remain
usable when Forge is absent; Forge metadata is optional and ignored safely by
other hosts.

## Implementation steps

1. Define placement selection from tool metadata and host policy.
2. Implement identity mapping and parent-side virtual-window registration.
3. Add the optional Forge profile to `mcp-ext/forge` with capability checks.
4. Route approved display/window/report actions through the parent adapter.
5. Define historical/offline behavior for actions that require a live server.
6. Coordinate lifecycle, focus, navigation, and teardown state machines.
7. Add diagnostics and telemetry without exposing iframe content or secrets.

## Tests

- the same App works in Agently without Forge enrichment;
- Forge profile absent, partially supported, incompatible, and fully supported;
- inline/hosted/top-level placement and user-policy override;
- two conversations and two same-resource instances remain isolated;
- malicious iframe cannot call Forge directly or target another window;
- embedded routes do not start a Forge bridge and parent/iframe cannot share or
  race one queue identity;
- live-only action from restored history returns a stable typed outcome;
- iframe crash, Forge window close, navigation, and concurrent teardown.
- official Apps basic-host comparison and no measurable Forge-profile overhead
  when the profile was not negotiated.

## Acceptance criteria

- Generic Apps interoperability is preserved with Forge enabled or disabled.
- Forge commands are authorized and executed only by the parent adapter.
- Optional profile negotiation adds no requirement to standard Apps servers.
- Closing any layer deterministically releases all related registrations.
- Unsupported capabilities degrade without breaking the App result.
- Ambiguous or partial profile negotiation fails closed to standard Apps.

## Rollout and rollback

Gate Forge placement separately from Apps rendering and enable it per tenant or
tool. Rollback returns Apps to inline/standard host presentation and removes
Forge registrations without invalidating transcript snapshots.

## Completion evidence

- Generic-host comparison: _pending_
- Forge-profile negotiation test: _pending_
- Isolation/threat-model test: _pending_
- Lifecycle cleanup test: _pending_
- Parent-only bridge ownership test: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
