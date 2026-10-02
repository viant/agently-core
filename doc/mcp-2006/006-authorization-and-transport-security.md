# 006 — Harden Authorization and HTTP Transport Security

Status: planned  
Parent workstream: WS-2 / security gate  
Primary repository: `mcp`  
Depends on: [001](001-specification-pins-and-fixtures.md), [003](003-stateless-core-and-discovery.md)

## Outcome

Implement the pinned July authorization requirements and protect remote/browser-
reachable MCP HTTP endpoints against issuer mix-up, DNS rebinding, hostile
origins, ambiguous proxy headers, and legacy-session confusion.

## Deliverables

### Authorization

- validate authorization-response `iss` according to the pinned SEP;
- bind dynamically registered credentials to the authorization-server issuer;
- re-register rather than reuse credentials after issuer migration;
- declare correct OpenID Connect application type;
- implement documented refresh-token request/use behavior;
- implement scope accumulation and step-up semantics;
- implement the pinned well-known discovery suffix rules;
- maintain resource/audience binding and existing protected-resource metadata.

### HTTP endpoint hardening

- explicit listen-address policy with safe localhost development defaults;
- Origin validation for browser-capable requests;
- Host validation and DNS-rebinding protection;
- trusted-proxy allowlist before honoring forwarded headers;
- duplicate/ambiguous header rejection;
- content type, request size, timeout, and concurrency limits;
- explicit disposition of `Mcp-Session-Id` on July requests;
- TLS requirements and production configuration guidance.
- apply Origin/Host/proxy/header/limit hardening to both July and every retained
  legacy HTTP endpoint; bind July `Mcp-Session-Id` behavior to task 001's pin.

### Audit and secrets

- structured authorization failure categories without leaking tokens/codes;
- credential-store migration and deletion behavior;
- issuer/client/resource correlation in audit logs using non-secret identifiers;
- redaction tests for headers, query parameters, and OAuth responses.

## Implementation steps

1. Build a requirement matrix from the pinned authorization SEPs.
2. Refactor credential keys to include issuer and resource where required.
3. Add issuer validation before code/token acceptance.
4. Implement application type, refresh, discovery, and step-up changes.
5. Add endpoint Host/Origin/proxy validation before MCP dispatch.
6. Separate development-local and production-remote defaults.
7. Add security test servers for malicious issuer/origin/host cases.
8. Document deployment and reverse-proxy requirements.
9. Produce a threat model covering issuer mix-up, browser endpoints, proxies,
   retained legacy routes, and credential migration/rollback.

## Tests

- missing/wrong/mixed-up issuer fails closed;
- credentials registered for issuer A are never sent to issuer B;
- discovery redirect/migration cannot reuse old credentials silently;
- application type, refresh-token request/use, and well-known suffix precedence
  match the pinned authorization SEP matrix;
- step-up retains only permitted accumulated scopes;
- malicious Origin and DNS-rebinding Host are rejected;
- untrusted forwarded headers do not override public origin/client identity;
- duplicate routing/auth headers are rejected;
- July request with legacy session header follows documented policy;
- request-size/timeout/concurrency limits work under load;
- logs and traces contain no bearer token, authorization code, refresh token,
  client secret, or raw App context.
- retained legacy endpoints enforce the same Host/Origin/proxy/header/limit
  protections;
- rollback either dual-reads issuer/resource-scoped keys safely or forces
  reauthorization; it never resumes unscoped credential reuse.

## Acceptance criteria

- Security review maps every pinned authorization SEP to code and tests.
- Default remote deployment fails closed when origin/host policy is absent.
- Local development remains usable on explicit loopback bindings.
- Existing supported OAuth flows pass compatibility tests.
- Rollback cannot bypass issuer or endpoint validation.

## Rollout and rollback

Stage credential-key migration with a bounded dual-read that accepts legacy
records only after validating issuer/resource, then rewrite them to scoped keys.
If rollback cannot read scoped keys safely, force reauthorization or disable
the affected endpoint. Security validation is not feature-flagged off in
production. Production remote mode enforces TLS and fails closed; documented
plaintext is limited to explicit loopback development.

## Completion evidence

- SEP requirement matrix: _pending_
- Security test suite: _pending_
- Threat-model review: _pending_
- Deployment guide: _pending_
- Credential migration/rollback test: _pending_
- Legacy endpoint hardening test: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
