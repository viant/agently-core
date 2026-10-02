# 005 — Implement Subscriptions, Caching, and Tracing

Status: planned  
Parent workstream: WS-2  
Primary repositories: `mcp`, `mcp-protocol`  
Depends on: [001](001-specification-pins-and-fixtures.md), [002](002-schema-packaging-and-generation.md), [003](003-stateless-core-and-discovery.md), [006](006-authorization-and-transport-security.md)

## Outcome

Replace session-bound change delivery with the pinned July subscription model,
implement correct cache semantics for discovery/list/resource results, and
propagate standard trace context across MCP calls.

## Deliverables

### Subscriptions

- implement `subscriptions/listen` request/stream lifecycle from the pinned
  schema;
- define reconnection, durable resume cursor/token, cancellation, backpressure,
  replay bounds, and shutdown;
- provide an instance-independent event fan-out/replay interface and one
  production implementation so reconnect never depends on sticky routing;
- translate supported legacy list-changed notifications only inside the legacy
  adapter;
- revalidate authorization on initial listen, reconnect/resume, token expiry or
  credential change, and periodic policy checkpoints; never infer it from an
  old MCP session.

### Caching

- honor `ttlMs` and `cacheScope` on discovery, lists, and resource reads;
- define cache keys including server identity, protocol/extension versions,
  principal/tenant when required, method/name, and relevant request parameters;
- prevent user-scoped results from entering shared caches;
- expose local library invalidation and forced-refresh APIs (not new wire
  methods unless task 001's pin defines them);
- cap stale use and prevent cache stampedes.

### Tracing

- propagate the task-001 pinned W3C carriers—HTTP headers, JSON-RPC metadata,
  or both exactly as specified—for `traceparent`, `tracestate`, and `baggage`;
- validate and bound baggage;
- create spans for discovery, cache, subscription, tool, resource, extension,
  and retry flows;
- redact sensitive attributes and App/view state.

## Implementation steps

1. Confirm exact subscription, cache, trace fields, and carriers from task 001.
2. Introduce shared cache-policy and identity-key helpers.
3. Implement discovery/list/resource caches using explicit scope.
4. Implement subscription client/server with bounded buffers.
5. Add reconnect and cancellation behavior.
6. Add trace extraction/injection and span attributes.
7. Integrate metrics for hit/miss/stale/refresh and subscription health.
8. Add load and isolation tests.

## Tests

- discovery is not requested for every tool call inside a valid TTL;
- expired entries refresh and failures obey declared stale policy;
- private/user cache entries never cross principals or tenants;
- subscription reconnect works without `Mcp-Session-Id`;
- slow consumers trigger bounded backpressure behavior;
- cancelled subscriptions release goroutines/connections;
- legacy notifications do not leak into the July path;
- trace context crosses client, gateway, server, and downstream calls;
- invalid trace/baggage input is rejected or sanitized per policy.
- reconnect on another instance resumes without loss/duplication beyond the
  declared replay contract;
- cache stampede prevention, invalidation, forced refresh, and absent/zero TTL;
- trace redaction excludes tokens, App state, and sensitive parameters.

## Acceptance criteria

- Caching matches pinned `ttlMs`/`cacheScope` semantics.
- Subscription streams survive reconnect without sticky routing.
- The cache used by `mcp` and any gateway integration includes every required
  identity dimension; gateway implementation outside `mcp` is verified in 016.
- Load tests show bounded memory and connection use.
- Traces contain no tokens, raw App state, or sensitive resource parameters.

## Rollout and rollback

Roll out caches and subscriptions behind independent flags. Rollback may disable
caching/subscriptions but must not reintroduce session-bound behavior into July
requests. Polling fallback is allowed only if the pinned protocol permits it;
otherwise operators explicitly accept TTL-bounded staleness/no live change
delivery while the subscription flag is off.

## Completion evidence

- Cache isolation suite: _pending_
- Subscription load/reconnect suite: _pending_
- Trace propagation example: _pending_
- Operational dashboard: _pending_
- Cross-instance replay/fan-out test: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
