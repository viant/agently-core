# 012 — Upgrade Forge Dependencies and Runtime APIs

Status: planned  
Parent workstream: WS-7A  
Primary repository: `forge`  
Depends on: [003](003-stateless-core-and-discovery.md), [007](007-mcp-ext-foundation.md)

## Outcome

Align Forge with released July-capable MCP modules and expose a generic virtual
window lifecycle plus scoped bridge API. Registering a view must not imply
iframe creation or give iframe code direct access to Forge internals.

## Deliverables

- tagged `mcp`/`mcp-protocol`/`mcp-ext` dependency alignment, with pseudo
  versions confined to development branches;
- virtual-window registration, snapshot, restore, focus, update, and removal;
- separation of host shell state from opaque App and Forge-extension state;
- bridge registration scoped by owner, conversation, window, and view UUID;
- parent-only bridge access with startup queueing and deterministic teardown;
- authenticated parent handshake/handle binding selected by the parent
  proposal's first-party authentication decision; iframe origin alone is not
  sufficient authority;
- backend bridge compilation and integration tests under selected MVS versions;
- capability errors for unsupported window operations.
- compatibility with the selected retained legacy MCP backend-bridge mode until
  task 016 records that mode's removal gate.

## API constraints

`WindowContent` is one possible presentation, not the registration model.
Virtual windows may represent Apps, reports, or future extensions. Forge owns
layout and shell state; the application owns only its opaque, size-limited
state. Route changes cannot silently install a global bridge. Forge APIs use an
opaque owner/scope key; Agently may populate it from a conversation ID.

## Implementation steps

1. Inventory Forge's MCP dependency graph and resolve current version skew.
2. Publish or select compatible tagged baseline releases.
3. Define the virtual-window and scoped-bridge interfaces with stable IDs.
4. Refactor registration away from implicit iframe/content construction.
5. Implement startup queueing, readiness, teardown, and listener cleanup.
6. Enforce parent-only access and ownership at every command boundary.
7. Compile and test frontend/backend bridge paths against the same dependency
   matrix used by Agently.

## Tests

- register, snapshot, restore, focus, update, and remove each window type;
- route transition without automatic iframe or bridge installation;
- command before ready, duplicate registration, reconnect, and teardown race;
- wrong owner/conversation/window/view UUID and stale handle;
- two Apps with the same resource remain isolated;
- dependency graph and MVS drift checks using released modules only;
- listener, goroutine, and browser-handler leak tests.
- unsupported operation errors, oversized opaque state, and queue count/size
  bounds;
- retained legacy backend-bridge compatibility;
- unauthenticated/stale/iframe-issued parent handle rejection.

## Acceptance criteria

- A generic opaque view can participate in Forge layout without Forge knowing
  the Apps protocol.
- Only the authenticated parent adapter can invoke Forge window commands.
- Registration and rendering are separate operations.
- Teardown revokes handles and clears queued commands/listeners.
- Forge and Agently agree on released MCP module versions.
- Frontend virtual-window/bridge contracts and Go backend version interfaces
  are separately identified as task-013 inputs.

## Rollout and rollback

Introduce the API alongside current windows and migrate one view type first.
Rollback selects the old registration adapter; dependency downgrades require
the compatibility matrix to pass and must not rewrite persisted view state.

## Completion evidence

- Tagged dependency graph: _pending_
- Virtual-window API test: _pending_
- Scoped-bridge security test: _pending_
- Leak/race report: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
