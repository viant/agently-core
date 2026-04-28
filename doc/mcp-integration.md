# MCP integration

Agently-core is an MCP client, an MCP host (serving internal tools), and an MCP
server (via `mcpserver/`). This doc covers how MCP clients are managed,
authenticated, and routed from the tool registry.

## Layers

| Layer | Path |
|---|---|
| Manager (lifecycle + per-conversation clients) | [protocol/mcp/manager/](../protocol/mcp/manager/) |
| Proxy (name normalisation, retry, reconnect) | [protocol/mcp/proxy/](../protocol/mcp/proxy/) |
| Auth bridge (BFF / bearer / ID-token / OAuth) | [protocol/mcp/manager/auth_token.go](../protocol/mcp/manager/auth_token.go), [internal/auth/](../internal/auth/) |
| Config (servers, scopes, headers) | [protocol/mcp/config/](../protocol/mcp/config/), `workspace/repository/mcp/` |
| Resource / prompt / tool exposure | [protocol/mcp/expose/](../protocol/mcp/expose/) |
| In-process MCP bus (internal "servers") | [mcp/internal/](../mcp/internal/) |
| Outward-facing MCP server | [mcpserver/](../mcpserver/) |

## Per-conversation clients

`Manager.Get(ctx, convID, server)` returns a live MCP client scoped to a single
conversation. That means:

- OAuth tokens and cookies stay per-user.
- A client is reconnected automatically on transport error.
- Tool calls honour the conversation's auth context without the caller plumbing tokens manually.

`Manager.Touch(convID, server)` extends the client's idle TTL after each call.

## Auth propagation

Agently's rule is **auth rides `context.Context`, never a parameter**. The registry does this for you:

1. [internal/tool/registry/registry.go:712](../internal/tool/registry/registry.go) calls `mgr.WithAuthTokenContext(ctx, server)` — looks up the server's auth policy.
2. [internal/auth/context.go](../internal/auth/context.go) pulls the per-user token/ID-token off the request session and injects it under the MCP auth key.
3. [protocol/mcp/proxy/proxy.go:26](../protocol/mcp/proxy/proxy.go) `CallTool` pulls the token back out and attaches it as `mcpclient.WithAuthToken(...)`.

Supported modes (selected per MCP server in YAML):

- `bff` — backend-for-frontend; session cookie → server-side token swap.
- `bearer` — forward the user's bearer token verbatim.
- `id_token` — use OIDC ID-token instead of access token (some servers require this).
- `mixed` — per-tool override (e.g. public tools skip auth).

## Resource + prompt exposure

Internal services publish MCP `resources/*` and `prompts/*` via
[protocol/mcp/expose/](../protocol/mcp/expose/). Client-side discovery is in
[protocol/tool/service/resources/mcp.go](../protocol/tool/service/resources/mcp.go).

## Extensibility

- **New MCP server**: add YAML under `<workspace>/mcp/<name>.yaml` with `uri`, `auth`, and optional headers.
- **New auth mode**: add a case to `WithAuthTokenContext` + a parser in `protocol/mcp/config/`.
- **Expose internal state as MCP resources**: implement `expose.Provider`.

## Related docs

- [doc/tool-system.md](tool-system.md)
- [doc/auth-system.md](auth-system.md)
