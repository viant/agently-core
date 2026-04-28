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

- [doc/mcp-integration.md](mcp-integration.md) — how auth reaches external tools.
- [doc/sdk.md](sdk.md) — which SDK surfaces honour OAuth automatically.
