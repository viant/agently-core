# Asana MCP

Agently is the client of Asana's official Streamable HTTP server:
`https://mcp.asana.com/v2/mcp`. Register an **Asana MCP** app in the
[Asana developer console](https://app.asana.com/0/my-apps), configure its
workspace distribution, and register the Agently HTTPS callback:
`https://<agently-host>/v1/api/auth/mcp/callback`.

This is separate from an External MCP app (Asana calling your server) and an
API app (ordinary Asana REST access). MCP tokens cannot be used with REST.

## Workspace configuration

`mcp/asana.yaml`:

```yaml
name: asana
transport:
  type: streamable
  url: https://mcp.asana.com/v2/mcp
auth:
  mode: oauth
  providerRef: asana
  clientRef: web
  resource: https://mcp.asana.com/v2/mcp
  tokenType: accessToken
eagerLink: true
toolsListVisibility: private
protectedResourceMetadataURL: https://mcp.asana.com/.well-known/oauth-protected-resource/v2
```

`oauth/providers/asana.yaml`:

```yaml
id: asana
issuer: https://app.asana.com
discoveryURL: https://app.asana.com/.well-known/oauth-authorization-server
defaultClient: web
clients:
  web:
    configURL: ${HOME}/.agently-oauth/asana.json
    confidential: true
    usePKCE: true
    scopes: []
tokenResponse:
  tokenURL: https://app.asana.com/-/oauth_token
  resource: https://mcp.asana.com/v2/mcp
  subjectPath: data.gid
```

The secret file, outside source control and readable only by the service user,
contains the registered client credentials and endpoints:

```json
{
  "clientID": "YOUR_CLIENT_ID",
  "clientSecret": "YOUR_CLIENT_SECRET",
  "authURL": "https://app.asana.com/-/oauth_authorize",
  "tokenURL": "https://app.asana.com/-/oauth_token",
  "authStyle": "params",
  "scopes": []
}
```

For deployment, mount this secret under the service user's home or replace
`configURL` with a managed SCY secret reference. Do not commit credentials.
Asana MCP scopes should be omitted; do not request API scopes or `openid`.
The live V2 protected-resource metadata advertises `https://mcp.asana.com/v2/mcp`.
Use that exact resource and the path-specific metadata URL shown above. The
origin-wide metadata describes `https://mcp.asana.com` and must not be used
to validate V2. This differs from the optional resource example in Asana's
integration guide; the live server metadata was checked on 2026-10-06.

Expose discovered tools through a bundle with `match: [{name: "asana:*"}]`
and add that bundle to the agent. No Asana-specific tool schemas are hardcoded;
the server's authenticated `tools/list` remains authoritative.

## Per-user connection

Each user needs an active cookie-backed Agently session, then uses the existing
MCP connection UI to authorize their own Asana account. The UI reads
`GET /v1/api/auth/mcp/asana/status`, initiates via
`POST /v1/api/auth/mcp/asana/initiate` with `X-Agently-Csrf`, and follows
the returned authorization URL. The callback uses the initiating session,
canonical user, single-use encrypted state, and PKCE. Tokens are encrypted
server-side and keyed by workspace, canonical user, and provider. Existing
refresh, disconnect, and provider kill switches apply.

Asana enforces the linked user's permissions. Distribution restricted to one
workspace permits only that workspace's users to authorize the app. Asana MCP
currently exposes all permitted MCP actions rather than granular OAuth scopes.

## Opaque OAuth provider support

`tokenResponse` is an explicit administrator-configured alternative for
providers whose access tokens are opaque and whose introspection response
does not supply the issuer, audience, and subject claims required by the
default validator. It applies only after the server's own authorization-code
exchange or refresh, with a confidential client and an exact pinned HTTPS
token endpoint and resource. Redirects from token requests are rejected.
The HTTPS response supplies the bearer token, future expiration, and provider
subject (Asana documents `data.gid`). Required scopes must be covered.

Refresh retains the original subject when omitted and rejects account changes;
an omitted scope retains the prior grant, while an explicitly returned scope
set is authoritative. The policy does not decode opaque token contents, accept
raw OOB/session imports, or grant an Agently login identity. Existing JWT and
RFC 7662 validation remains the default for other providers. A missing subject
in an initial exchange fails closed; verify the live MCP response before
considering deployment validated.

References: [Asana MCP integration](https://developers.asana.com/docs/integrating-with-asanas-mcp-server)
and [Asana OAuth](https://developers.asana.com/docs/oauth).
