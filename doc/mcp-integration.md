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

## Passing uploaded assets to tools

Compatible tools can receive an original or exported scratchpad URI in their
existing file-input field, for example:

```json
{"sourceURL":"scratchpad://artifact/a123"}
```

The receiving tool resolves the reference using its authenticated identity and
accessible storage. Artifacts are user-owned and reusable across that user's
conversations; conversation associations do not impose another artifact ACL.
Existing MCP client/credential isolation and scratchpad ownership checks apply.

Passing a URI does not transfer bytes to an arbitrary remote server. Direct
handoff requires a scratchpad-aware AFS resolver and backing-storage access;
other consumers need their own file transport. The resource feature does not
introduce a universal MCP upload adapter. Reading or presenting the file to the
LLM is unnecessary before forwarding it.

See [resources.md](resources.md) for upload, export, and handoff examples.

## Extensibility

- **New MCP server**: add YAML under `<workspace>/mcp/<name>.yaml` with `uri`, `auth`, and optional headers.
- **New auth mode**: add a case to `WithAuthTokenContext` + a parser in `protocol/mcp/config/`.
- **Expose internal state as MCP resources**: implement `expose.Provider`.

## Related docs

- [doc/asana-mcp.md](asana-mcp.md) — Asana setup and opaque OAuth token responses.
- [doc/tool-system.md](tool-system.md)
- [doc/auth-system.md](auth-system.md)

## Browser execution location

A configured MCP server can execute in the originating web browser while keeping
its ordinary `server-tool` names and agent/tool-bundle selection. This route uses
standard MCP initialize/tools/list/tools/call and durable AG-UI frontend-tool
correlation; MCP Apps is an optional UI layer, not the execution transport.

```yaml
name: local-device
executionLocation: browser
browserTransport:
  type: chrome-extension
  extensionId: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa # Replace with the installed ID.
  portName: browser-mcp-v1             # Public port published by the extension.
  requestTimeoutMs: 9000
browserTools: ["safe_*"]                    # Optional configured name policy.
```

The example selects a transport, not product-specific framework tool code.
`executionLocation` defaults to `server`. Browser configs must not contain
server transports or auth configuration. Their provider returns before setting
up token/cookie stores or expanding credential resources. Manager.Get refuses
server-side construction and backend discovery omits these servers. There is no
HTTP, stdio, OAuth, cookie or server-execution fallback for browser calls.

A generic web shell enables `AgentlyClient({ ..., browserMCP: {} })`. Agently's
web client enables that generic support. A custom shell can inject
`browserMCP.transportFactory`; the factory receives only public addressing and
conversation/thread/connection IDs. The default Chrome extension adapter uses
the configured extension ID and port name and waits for local approval. No
workspace credentials, native-host name or Mechanize tool implementation is
embedded in the SDK or Forge. Forge-backed chat views render the same standard
tool events/results; they do not gain a direct datasource-to-local-tool bypass.

Authenticated workspace metadata exposes only public browser descriptors. The
browser discovers the local catalog and registers it under its authenticated
user, owned native conversation, exact AG-UI thread, configured server and fresh
connection ID. The server checks the configured name policy, assigns a catalog
ID/hash, and retains an immutable bounded snapshot. Replacing a connection or
catalog invalidates the old generation. These schemas/descriptions are metadata,
not proof of a local broker's identity or a grant of native authority. The local
extension and canonical native executor remain responsible for that authority.

Only declarations matching the current registered name/schema/metadata can
enter AG-UI. They pass through normal agent and tool-bundle selection; registering
a catalog does not add every local tool to an agent. Backend collisions remain
errors. Per-conversation catalog definitions are not cached in shared tool
surfaces. Pending tool results retain their original call IDs and exact catalog/
connection provenance; substitution, foreign-thread replay and catalog changes
are rejected.

The SDK executes completed frontend calls through the originating connection,
then continues the existing durable AG-UI run with its original parent run ID.
It handles only the standard pending-call and explicit `agently.client_tool`
interrupt profiles. Human approvals and unrelated interrupts retain their
existing UI flow. Calls are dispatched once: a lost local reply remains
uncertain and is not transparently replayed. Auth reset, conversation view
teardown, origin/thread change and disconnect close local connections and revoke
catalogs. Before each local call the host rechecks the authenticated catalog.
MCP result `_meta` stays out of model content.

Current bounds: 64 catalog tools, 256 KiB registration, 32 active catalogs per
user, 256 process-wide, a 15-minute ephemeral catalog lifetime, 256 call IDs per
connection, 32 continuation rounds and a serialized queue of 16 requests. The
extension independently enforces its own approval lifetime and command budgets.
Catalogs do not survive server restart. Long-lived native trust preferences and
persistent device enrollment are separate from this ephemeral relay.

### Standards boundary

The MCP tool schemas and initialize/tools/list/tools/call results remain standard
MCP. `executionLocation`, `browserTransport`, catalog-registration endpoints and
the extension approval/request/result envelope are Agently transport extensions;
they are not official MCP configuration fields. This transport stays separate
from the optional official Apps UI extension below. AG-UI carries the standard frontend tool-call/result correlation, while
MCP Apps adds sandboxed resources and a separate host/view protocol.

## Official MCP Apps host

Agently's normative renderer uses the stable **2026-01-26** extension and the
existing pinned `@modelcontextprotocol/ext-apps` **1.7.5** SDK. The current official
npm stable release was verified as 2.0.3; the wire contract does not require an
upgrade. [Stable specification](https://github.com/modelcontextprotocol/ext-apps/blob/main/specification/2026-01-26/apps.mdx),
[official SDK](https://github.com/modelcontextprotocol/ext-apps).

Outer MCP initialization advertises `extensions["io.modelcontextprotocol/ui"]`
with `mimeTypes: ["text/html;profile=mcp-app"]`. This is independent of the
iframe's `ui/initialize` and `ui/notifications/initialized` handshake. Tool
`_meta.ui.resourceUri` selects one exact `ui://` resource from its own configured
server. Plain `text/html`, substituted/ambiguous resources, oversized HTML and
CSP fragments are rejected.

Web hosting requires a separate sandbox origin. Build produces
`mcp-app-sandbox.html` plus its two script dependencies; serve those files on a
dedicated HTTPS origin with no workspace routes, credentials, authenticated
cookies, or other application content. The host selects it using
`VITE_MCP_APPS_SANDBOX_URL=https://sandbox.example/mcp-app-sandbox.html`.
HTTP loopback is allowed for isolated development. Missing or same-origin
configuration produces an unavailable app; the ordinary tool result remains
usable. This change does not configure or deploy that origin.

The outer proxy has `allow-scripts allow-same-origin` and a different origin from
the host. Its inner View has `allow-scripts` and an opaque origin. The host checks
the exact proxy window/origin, and the proxy checks the host and exact inner
window/opaque origin. The proxy only forwards JSON-RPC and loads one resource.
Resource CSP uses the official bundled inline-script/style defaults and only
declared network/resource/frame/base origins. Eval, blob scripts, object embeds,
forms and undeclared origins are blocked. Camera/microphone/geolocation/clipboard
permissions are not granted. Sandbox resource navigation revokes the instance.

App instances are keyed to their original tool call, server, resource and current
connection. They do not require a saved workspace screen, Forge window ID, or
workspace navigation state. Official Apps do not run workspace APIs. The old
custom `mcpui:*` envelope renderer is a separate adapter, selected only by
`VITE_MCP_UI_LEGACY_ADAPTER=mcpui:1.0.0`; embedded fallback also requires explicit
legacy metadata. The module release is `viant/mcp-ui` v0.2.x, while its custom
legacy wire envelope version is 1.0.0.

| Method/capability | Remote configured server | Browser configured server |
| --- | --- | --- |
| `ui/initialize`, initialized notification | Official SDK negotiation; exactly once | Same |
| Sandbox ready/resource ready | Different-origin proxy and opaque View | Same |
| Full/partial tool input and tool result | Delivered after initialized | Same; result retained only in local host state |
| `ping` | Official host connection health | Same |
| `tools/call` / `serverTools` | Same-server, current app visibility plus agent/bundle policy and canonical approval path | Not advertised; rejected, preventing local approval bypass |
| `resources/read` / `serverResources` | Exact bound resource on the configured server | Exact bound resource on the original browser connection, after authenticated catalog ownership/config revalidation; requires advertised resources |
| Size changes | Bounded inline height | Same |
| App-requested teardown / `ui/resource-teardown` | Authority revoked immediately, acknowledgement awaited up to 1 second | Same |
| Open links, app messages, logging, downloads, model context updates, display-mode changes, app-exposed tools | Not advertised; unavailable | Same |
| Browser permissions | Not granted | Same |

`_meta.ui.visibility` defaults to model and app. Model discovery excludes
app-only tools; app calls reject model-only tools. Empty or malformed audiences
fail closed. Visibility narrows the current configured tool policy; it cannot
grant access to a native admin/secret tool, another server or another conversation.
Normal MCP tools remain individual tools with their own schemas.

Browser resource reads use no backend HTTP fallback. The original browser host
owns their ephemeral app records and closes them on conversation teardown,
connection loss, identity reset or catalog expiry. They are not restored as
executable apps from historical workspace state. Browser app-originated tool
execution is an explicitly unsupported optional capability; model-originated
browser tool execution still follows normal durable approval/agent selection.

### Source validation

The source-only browser proof is `agently/ui/scripts/proof-mcp-apps.mjs`.
It starts two ephemeral localhost servers and a fresh headless browser context,
loads a generic app built with the actual pinned official SDK, checks input/result
and tool calls, proves CSP/host-DOM isolation, and awaits teardown. It does not
connect to a deployed backend or read runtime configuration. Run with
`node scripts/proof-mcp-apps.mjs`; an existing browser binary can be selected with
`MCP_APPS_CHROME_EXECUTABLE=/path/to/browser`.

Deployment of the separate sandbox origin and extension-specific support for
`initialize` capability parameters / `resources/read` must be verified by the
consuming runtime. Source tests are not deployed browser or application proof.
