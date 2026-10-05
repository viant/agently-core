# Authentication & authorization

Agently supports local login, JWT bearer, OAuth 2.0 with PKCE in BFF
(backend-for-frontend) mode, and mixed per-route policies. All three propagate
the caller's identity via `context.Context` so tools and MCP servers run under
the right user without any parameter plumbing.

## Packages

| Path | Role |
|---|---|
| [service/auth/](../service/auth/) | HTTP handlers: `/v1/api/auth/*` — providers, login, logout, OAuth initiate/callback, OOB, session, me |
| [internal/auth/](../internal/auth/) | `context.Context` keys, token store, claim extractor |
| [internal/auth/token/](../internal/auth/token/) | Token lifecycle (validate, refresh, rotate) |
| [service/auth/session/](../service/auth/session/) | Session cookie, CSRF, idle TTL |
| [protocol/mcp/manager/auth_token.go](../protocol/mcp/manager/auth_token.go) | Injects per-request MCP auth token (see [doc/mcp-integration.md](mcp-integration.md)) |
| [service/policy/](../service/policy/) | Workspace-configured visibility authorization for reports, windows, starter prompts, and intake profiles |

## Modes

### Local
Username/password against a workspace `oauth/local.yaml` user table. Useful for dev, admin, and air-gapped installs. Session is a signed cookie.

### Bearer JWT
Caller sends `Authorization: Bearer <jwt>`. The service validates against a configured OIDC issuer (JWKs refreshed on cache miss). No session cookie needed.

### OAuth BFF (PKCE)
For browser UIs that must not hold access tokens:

1. Client hits `/v1/api/auth/oauth/initiate`. Server generates PKCE verifier + state, returns an authorization URL.
2. User authenticates with the IdP; IdP redirects to `/v1/api/auth/oauth/callback?code=...`.
3. Server swaps code for tokens, stores them server-side keyed by session id, sets an opaque session cookie on the browser.
4. Subsequent calls carry only the cookie; the server attaches the real token to outbound calls (MCP servers, downstream services).
5. Refresh happens transparently on expiry.

Never exposes access or refresh tokens to the browser.

### Mixed
Per-server / per-tool policy. Examples:
- MCP server A requires the user's ID-token (OIDC).
- MCP server B requires a service-account bearer.
- Some internal tools skip auth entirely.

Policy is declared on `mcp/<server>.yaml` (`auth: bff | bearer | id_token | none`) and honoured by the manager.

## Session + cookies

- Session cookie (`agently_session` by default, configurable) is HttpOnly, SameSite, Secure-when-HTTPS.
- CSRF: state-based on initiate, double-submit token on sensitive endpoints.
- Idle TTL + absolute TTL, both configurable. Refresh extends idle, never absolute.

## Token refresh

Inside any request, `internal/auth` checks token expiry before use and
refreshes if needed. The refreshed token is written back to the session and
attached to `ctx` so downstream calls use the fresh one — no caller action.

## Propagation guarantee

Every agent tool call that targets an MCP server runs through:

```
ctx (session-attached)
 └─ mgr.WithAuthTokenContext(ctx, server)     (selects per-server token / policy)
      └─ proxy.CallTool(ctx, ...)             (attaches MCP auth option)
```

Caller code never sees the raw token.

## Workspace config

- `oauth/providers.yaml` — IdP registrations (client id/secret, scopes, audiences).
- `oauth/local.yaml` — local users (dev/admin only).
- `oauth/policies.yaml` — per-route / per-server policies.

## Extensibility

- **New IdP**: add a provider entry; if non-OIDC, implement a claim extractor under `service/auth/providers/`.
- **New session backing**: satisfy `session.Store` (default: in-memory; production: Redis/Datly).
- **New auth mode**: add a case to the MCP auth policy parser + token injector.

## Related docs

- [doc/authorization-policy.md](authorization-policy.md) — external MCP visibility policy and legacy Forge compatibility.
- [doc/mcp-integration.md](mcp-integration.md) — how auth reaches external tools.
- [doc/sdk.md](sdk.md) — which SDK surfaces honour OAuth automatically.

## Session-import verification boundary

In a JWT-only workspace (`auth.jwt.enabled` with no configured OAuth client),
`POST /v1/api/auth/session` verifies supplied identity/access/bearer JWTs against
the configured JWT keys before writing a session or cookie. Missing, expired,
invalid, differently signed, and conflicting-subject credentials are rejected.
The session subject, email and scopes come from verified credentials; a caller's
`username` or opaque refresh token cannot supply identity or grant scopes. Both
auth handler implementations follow this rule. CLI commands stop when an
explicit flag/environment token is rejected instead of logging in as another
identity through a fallback flow.

This change does not establish verification of arbitrary raw OAuth imports.
The current OAuth/mixed session-import path retains its existing scope checks
and token handling; it does not apply the JWT-only verifier. In particular,
`handleCreateSession` does not currently consume the OAuth JWKS verifier built
by `oauthVerifierConfig`. That is a separate security boundary requiring review,
not evidence that every imported OAuth token is verified. The next bounded
integration should use the configured provider verifier for signed ID tokens,
validate its issuer/audience policy, and define an explicit validation mechanism
for opaque access-token imports. It must preserve authenticated callback/OOB
flows and must not infer trust from decoded claims or a client-supplied subject.
