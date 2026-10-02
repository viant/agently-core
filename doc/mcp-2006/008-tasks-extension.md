# 008 — Implement the Tasks Extension

Status: planned  
Parent workstream: WS-3 / official Tasks extension  
Primary repositories: `mcp-ext`, `mcp`, `agently-core`  
Depends on: [001](001-specification-pins-and-fixtures.md), [003](003-stateless-core-and-discovery.md), [004](004-multi-round-trip-requests.md), [006](006-authorization-and-transport-security.md), [007](007-mcp-ext-foundation.md)

## Outcome

Implement the official Tasks extension as a July-only, server-directed,
durable lifecycle, clearly separated from the non-wire-compatible experimental
Tasks API in `2025-11-25`.

## Compatibility rule

- July peers negotiate `io.modelcontextprotocol/tasks` per the pinned spec.
- A server returns a task only when the request declared support.
- Legacy `task` request augmentation does not opt into the new extension.
- `tasks/result` and `tasks/list` are not implemented on the July extension
  surface.
- `tasks/result` receives Method Not Found where required by the pinned spec.
- Product-specific task listing, if needed, is an owner-scoped ordinary tool or
  API and is not presented as the extension method.

## Deliverables

### Extension package

- official task/result discriminators and Task types;
- `tasks/get`, `tasks/update`, and `tasks/cancel` messages;
- state machine validation for working, input-required, completed, failed, and
  cancelled;
- extension capability helpers and server-directed creation policy;
- compatibility diagnostics for legacy requests.
- a versioned legacy-mode adapter preserving supported `2025-11-25`
  experimental Tasks behavior only for peers explicitly dispatched there;

### Runtime

- durable task store interface and at least one production implementation;
- atomic create-before-response behavior;
- owner/tenant/server/request binding;
- poll interval and TTL handling;
- durable result/error and outstanding input requests;
- `tasks/update` input-response validation;
- cooperative cancellation with ack-only response;
- cleanup/retention and idempotency.
- authenticated principal/tenant derivation from task 006, with an explicit
  anonymous-owner policy when authentication is intentionally absent;
- schema version, size limits, redaction, audit fields, and encrypted storage
  policy for durable inputs/results;
- feature flag and metrics for creation/state/expiry/cancel/rejection outcomes.

### Agently mapping

- map suitable existing async/goal operations to the extension without
  conflating their internal IDs with task IDs;
- retain conversation/audit correlation;
- keep the extension usable independently of Agently goal mode.

## Implementation steps

1. Generate/hand-code extension types from task 001's Tasks pin.
2. Implement and test the state machine.
3. Define durable store and ownership model.
4. Add task-producing tool-call wrapper after capability verification.
5. Implement get/update/cancel endpoints.
6. Implement durable input-required fulfillment through `tasks/update`; reuse
   task 004 validation primitives where applicable but never use an MRTR
   `requestState` token as the durable task identity.
7. Add legacy protocol dispatch and explicit incompatibility behavior.
8. Add retention sweeper and operational metrics.

## Tests

- no task is returned without per-request extension opt-in;
- task is durable and retrievable before creation response is sent;
- get returns current/terminal result or error correctly;
- update handles known, unknown, duplicate, and satisfied input keys;
- cancel acknowledges immediately and permits eventual terminal races per spec;
- TTL expiry and cleanup respect owner scope;
- task ID from another principal/tenant is rejected;
- duplicate/retried create is idempotent according to declared key;
- legacy `tasks/result/list` and task parameter follow pinned incompatibility
  rules;
- restart/multi-instance polling works.
- internal Agently IDs remain distinct from extension task IDs and goal mode is
  not required;
- durable payload size/redaction/audit controls and flag-disabled behavior;
- supported legacy-mode peers retain their versioned experimental behavior.

## Acceptance criteria

- Official Tasks fixtures and state transitions pass.
- No July task depends on an MCP session or sticky instance.
- Legacy and extension Task APIs cannot be accidentally mixed.
- Durable ownership and retention receive security/data review.
- Agently integration is optional and does not leak product types into
  `mcp-ext/tasks`.

## Rollout and rollback

Advertise Tasks only after durable storage and compatibility tests pass. A
rollback stops advertising the extension and returns ordinary tool results for
new calls; it must continue serving already-created tasks for their retention
window or provide an explicit migration/termination procedure.

## Completion evidence

- Extension conformance test: _pending_
- Durable restart test: _pending_
- Legacy incompatibility matrix: _pending_
- Agently adapter test: _pending_
- Durable data security review: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
