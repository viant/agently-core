# 010 — Implement the Agently MCP Apps Host

Status: planned  
Parent workstream: WS-5  
Primary repositories: `agently`, `agently-core`  
Depends on: [003](003-stateless-core-and-discovery.md), [006](006-authorization-and-transport-security.md), [009](009-apps-server-and-mcp-ui-compatibility.md)

## Outcome

Implement the official Apps host lifecycle in Agently with a sandboxed iframe,
an authenticated parent-side broker, strict instance isolation, and a temporary
adapter for existing `mcpui:*` guests.

## Deliverables

- Apps capability negotiation and `ui/initialize`, distinct from core MCP
  initialization;
- sandboxed iframe creation from negotiated `ui://` resources;
- pinned sandbox-proxy readiness and resource-delivery lifecycle for web hosts;
- official AppBridge JSON-RPC proxy with source, origin, instance, method, and
  payload validation;
- ordered lifecycle: zero or more partial-input updates, exactly one complete
  input, complete result or cancellation, and teardown acknowledgement/timeout;
- brokered app tool/resource calls through normal authorization and approval;
- link, context, display-mode, and size request handling;
- untrusted app-context updates with provenance, role, size, and rate limits;
- unique identity keyed by conversation, tool call, and view UUID—not resource
  URI alone;
- feature-flagged private-protocol adapter and usage telemetry.
- app-only tool filtering: hidden from model discovery, restricted to the
  originating server/App instance, and rejected cross-server;
- bounds for message/state size, request rate, outstanding request count, and
  bridge queues with backpressure.

## Entry decision

Use the upstream AppBridge package where feasible. Before implementation,
record in the parent proposal whether it is adopted; an independent router
requires a documented reason for every divergence and equivalent fixtures.

## Security boundary

The iframe is never trusted with host credentials or direct Forge access. The
parent owns MCP sessions, approvals, persistence, navigation, and virtual-window
commands. CSP and sandbox policy are derived from validated metadata, with
permissions denied unless negotiated and approved.

## Implementation steps

1. Add Apps negotiation to the MCP client path without changing legacy clients.
2. Implement resource loading, MIME/CSP checks, and sandbox construction.
3. Adopt/wrap the selected AppBridge router and per-instance capability table.
4. Implement the lifecycle state machine and deterministic teardown.
5. Route app-initiated tools/resources through authorization and approval.
6. Add context/display/link/size mediation and audit events.
7. Wrap the old `mcpui:*` bridge behind a separately selectable adapter.
8. Add diagnostics for lifecycle violations without logging sensitive payloads.

## Tests

- upstream Apps example bundles and lifecycle fixtures;
- partial/complete input and result/cancellation ordering permutations;
- duplicate complete input, late message, teardown timeout, and iframe reload;
- wrong source, wrong origin, wrong view UUID, cross-instance replay, oversized
  message, unknown method, and malformed JSON-RPC;
- approval allow/deny and permission revocation during an active view;
- app-only tool hiding, same-server success, and cross-server rejection;
- sandbox-proxy readiness/resource ordering and bounded outstanding requests;
- CSP/sandbox enforcement without requiring iframe same-origin access;
- two views using the same resource URI remain isolated;
- legacy adapter on/off and non-Apps fallback.
- stateless July MCP calls and per-view `ui/initialize` operate independently in
  one end-to-end scenario.

## Acceptance criteria

- An upstream-compatible Apps bundle works without private Viant messages.
- The parent is the sole authority for privileged operations.
- Cross-conversation and cross-instance messages are rejected and audited.
- Teardown leaves no listener, bridge registration, or retained credential.
- Legacy behavior can be disabled independently from official Apps.
- A missing server view ID receives an explicitly host-local UUID that is never
  represented as server-issued.

## Rollout and rollback

Gate official Apps and the legacy adapter separately. Start with internal
servers, then canary external Apps. Rollback disables official negotiation and
requests normal teardown before the timeout destroys active Apps instances;
stored tool results and text/structured fallback remain readable.

## Completion evidence

- Lifecycle conformance report: _pending_
- Threat-model/negative-test report: _pending_
- Resource-leak test: _pending_
- Legacy adapter telemetry: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
