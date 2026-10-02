# 003 — Implement Stateless Core Discovery and HTTP Routing

Status: planned  
Parent workstream: WS-2  
Primary repository: `mcp`  
Depends on: [001](001-specification-pins-and-fixtures.md), [002](002-schema-packaging-and-generation.md)

## Outcome

Implement the July stateless client/server path, including `server/discover`,
per-request metadata, and routable HTTP headers, while retaining isolated
legacy handshake/session behavior for supported older protocol versions.

## Baseline

The current client calls `initialize`, caches capabilities, and repeats the
handshake on reconnect. Server handlers retain initialization state. Current
HTTP middleware handles `MCP-Protocol-Version` but not the complete July
routing contract.

## Design constraints

- JSON-RPC 2.0 remains the message envelope.
- July request handling must not read mutable handler-global client capability
  or identity state.
- Any July request may execute on any server instance.
- Legacy and July state must be isolated by explicit protocol dispatch.
- The iframe `ui/initialize` lifecycle is unrelated and remains in task 010.

## Deliverables

### Request context

- decode and validate protocol version, client info, client capabilities, and
  extension map from every July request as specified;
- expose immutable request-scoped accessors;
- prevent fallback to cached legacy initialization state.

### Discovery

- implement client and server `server/discover`;
- include server identity, core capabilities, extensions, and cache metadata;
- add cache key/scope behavior consumed by task 005;
- provide explicit refresh/invalidation APIs.

### HTTP routing

- emit and validate `MCP-Protocol-Version`;
- emit and validate `Mcp-Method` and operation-specific `Mcp-Name`;
- reject header/body mismatch before method dispatch;
- ensure proxy-safe normalization without accepting ambiguous duplicate headers;
- distinguish legacy session headers from July requests.

### Version dispatcher

- define the pinned bootstrap advertisement/selection mechanism used before a
  July-only `server/discover` call can be made;
- require explicit mutually supported selection and prohibit silent downgrade;
- route July requests to stateless handlers;
- route supported older versions to the existing handshake/session adapter;
- return deterministic errors for unknown or unsupported versions;
- keep version choice observable.

### Extension composition

- expose transport-neutral registry hooks owned by `mcp`; application-level
  composition may use `mcp-ext`, but `mcp` does not import it;
- construct and validate per-request client metadata on both client and server;
- bind exact method/header names to task 001's pin and mark the task blocked if
  the pin differs materially from the planning names.

## Implementation steps

1. Introduce protocol-version dispatch before existing handler state access.
2. Define immutable July request context.
3. Implement `server/discover` handler and client cache interface.
4. Add request header construction and server validation.
5. Split legacy initialization/session logic behind a versioned adapter.
6. Remove July reliance on `ensureInitialized` and reconnect initialization.
7. Add instrumentation and feature flags.
8. Run mixed-version compatibility suites.

## Tests

- July tool call succeeds without `initialize`;
- two sequential calls are served by different instances with no shared MCP
  session;
- per-request capabilities differ correctly between calls;
- header/body method and name mismatches are rejected;
- duplicate/ambiguous routing headers are rejected;
- discovery caching does not cross user/tenant scope;
- legacy initialize flow still works only on a supported old version;
- July request cannot read prior legacy client state;
- unknown version fails deterministically.
- mutually supported version selection, explicit downgrade, and July-disabled
  rejection are deterministic and observable;
- a July request carrying `Mcp-Session-Id` follows the task-001 pinned policy;
- discovery invalidation observes a server capability change.

## Acceptance criteria

- No July handler depends on `Mcp-Session-Id` or mutable initialized-client
  fields.
- Gateways can route using validated headers without parsing the body.
- Discovery results match task 001 fixtures.
- The default version remains unchanged until task 016.
- Metrics distinguish July, legacy, downgrade, and rejection paths.
- July remains disabled for browser-reachable deployments until task 006 passes.

## Rollout and rollback

Ship behind a July protocol feature flag. Rollback disables July advertisement
and leaves the existing legacy path available. Do not share partial state
between the two paths. When disabled, a July request receives the pinned typed
unsupported/version response and is never silently executed as legacy.

## Completion evidence

- Stateless integration test: _pending_
- Multi-instance test: _pending_
- Header conformance test: _pending_
- Legacy compatibility test: _pending_
- Flag-off/downgrade test: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
