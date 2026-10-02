# 004 — Implement Multi-Round-Trip Requests

Status: planned  
Parent workstream: WS-2  
Primary repositories: `mcp`, `mcp-protocol`  
Depends on: [002](002-schema-packaging-and-generation.md), [003](003-stateless-core-and-discovery.md)

## Outcome

Implement stateless input-required flows in which a client collects elicitation
or other permitted responses and safely resumes the original operation on any
server instance.

## Security properties

`requestState` must be:

- integrity-protected;
- confidential when it contains request context, using authenticated encryption
  or an opaque server-side reference;
- tenant/user/audience bound;
- purpose and original-method bound;
- size bounded;
- expiry aware;
- single-use or replay bounded;
- safe to process on any server instance.

Client-provided state is never trusted merely because it decodes successfully.

## Deliverables

- typed `InputRequiredResult`, input request, response, and resume helpers;
- a request-state codec interface with encrypted and opaque-reference options;
- key rotation/version support;
- durable/reference store interface where opaque state is selected, plus a
  shared atomic replay/consumption store whenever single-use semantics are
  selected for either codec;
- shared key provisioning/rotation contract for all server instances;
- resume validation before business handler invocation;
- server API for issuing allowed input requests only during active processing;
- client orchestration for presenting input and resubmitting the original call;
- cancellation, expiry, invalid response, and partial-response behavior;
- trace/audit correlation across rounds without exposing state contents.
- July feature flag, typed disabled response, and outcome metrics for
  issued/resumed/expired/replayed/rejected states.

## Implementation steps

1. Confirm exact discriminators and schemas from task 001.
2. Define request-state claims and cryptographic/storage interface.
3. Bind state to principal, stable server audience (not process instance),
   method, expiry, and a canonical original-parameter digest that excludes the
   echoed state and collected input responses.
4. Return input-required result without retaining handler-local session state.
5. Validate echoed state and input responses on retry.
6. Enforce replay policy and consume single-use state atomically.
7. Resume the handler with reconstructed immutable context.
8. Add key rotation and operational diagnostics.

## Tests

- valid flow resumes on a different process instance;
- tampered state fails before tool execution;
- state replay follows the declared single-use/replay policy;
- expired state fails deterministically;
- state from another user, tenant, server, method, or parameter digest fails;
- old encryption key works during rotation window and is rejected afterward;
- oversized state/input response is rejected;
- unknown/already-satisfied input keys are handled per pinned spec;
- no secret state appears in logs, traces, or errors.
- concurrent single-use resumes on different instances have exactly one winner;
- cancellation is deterministic before and during resume;
- pre-July peers never receive July input-required/MRTR results;
- feature-disabled July requests return the pinned typed outcome.

## Acceptance criteria

- Multi-instance execution requires no sticky route or MCP session store.
- Security review approves state confidentiality, integrity, replay, and key
  lifecycle.
- Client and server fixtures match task 001.
- Metrics report issued/resumed/expired/replayed/rejected outcomes without
  sensitive payloads.

## Rollout and rollback

Enable only for July requests. If disabled, operations requiring input return a
typed unsupported result or use a separately supported legacy flow; never
silently downgrade an in-progress July request.

## Completion evidence

- Cross-instance test: _pending_
- Cryptographic review: _pending_
- Replay/expiry tests: _pending_
- Conformance fixture result: _pending_
- Disabled/legacy compatibility test: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
