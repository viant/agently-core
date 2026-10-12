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

An output artifact policy can opt a screenshot tool into bounded model previews
and context retention with `role: screenshot`. The role is saved in the owned
artifact manifest; image MIME types and filenames do not classify an image.

```yaml
outputArtifacts:
  capture_window:
    name: screenshot.png
    role: screenshot
```

`resources/readImage` keeps the received artifact unchanged. Classified screenshot
previews preserve aspect ratio, never upscale, and fit within a 2048-pixel longest
edge and 2500 patches of 32×32 pixels. `detail: original` explicitly reads the
stored image bytes. Ordinary images retain the existing 2048×768 preview default.
Classified screenshots use provider `detail: high`, including uploaded image IDs;
ordinary images keep provider-default detail.

Active prompt history retains the newest two unique classified screenshots by
default. Older screenshot bytes are omitted from model input while their owned
artifact references and durable transcript remain available. Reading an older
artifact explicitly makes it current again. Workspace defaults can configure
`projection.screenshotRetention.maxImages` (1–64), or disable this projection with
`projection.screenshotRetention.enabled: false`. Uploaded images without a trusted
screenshot role remain unaffected.

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

See the [dated compatibility audit](mcp-compatibility-audit.md) for authoritative
versions, tested surfaces and remaining host/release gates.

## MCP Tasks extension adapter

`github.com/viant/mcp-tasks` owns the generic stable 2026-07-28 Tasks contract.
Agently's adapter is `sdk.MCPTasksBackend`, configured with the existing durable
AG-UI store and native ownership/status/resume/cancel controls. The adapter does
not schedule tools or replace async execution. Opaque task aliases and control
receipts are journaled in protocol-only ledgers in the existing store. They have
no native turn, are excluded from AG-UI execution recovery, and cannot be resumed
through the ordinary AG-UI endpoint; the captured native execution stays
canonical. On reconstruction, aliases resolve the same owned run and follow
validated native continuation links instead of repeating work.
Existing async run/status/cancel tool pairs, operation selectors, wait/detach
and skill fork remain supported native APIs. MCP Tasks adapts these executions
for standard clients; it does not deprecate their tools or change their modes.

`protocol/mcp/expose.WithTasks` is an explicit transport decorator. Supply an
already-authorized normal handler, a backend, the real owner resolver, and a
trusted read-only capture of the native execution admitted by the ordinary tool
path. Non-negotiated `tools/call` delegates unchanged. Negotiated calls remain
ordinary results unless capture returns an existing durable native reference.
Never reconstruct that authority from caller arguments or an unverified response
ID. The owner/capture callbacks must apply the same exported tool policy and
principal checks as normal dispatch; there is no self-approval shortcut.

`sdk.MCPTasksAGUIControls` delegates resume to existing AG-UI admission/preflight
and scoped cancellation to the existing native cancellation route. The standard
input response transports an ordinary elicitation result; the original approval
schema and approval path remain authoritative. Partial answers are durably
collected before one native resume. A dispatched response with an uncertain
outcome is not replayed. Task cancellation acknowledgement records control intent
and remains distinct from terminal native cancellation.

Fork and detach retain their existing meaning. Capture a waiting fork's native
parent execution when its contract waits for the child result. Capture the
actual existing detached child when its task should outlive its parent. Preserve
conversation/turn/run/invocation/operation links in the trusted reference.
Cancelling an HTTP connection or polling context never cancels these executions.
AG-UI continues projecting the original native events; Tasks is another owned
observation/control protocol, not another event or execution owner.

The default `MCPTasksAGUISnapshot` reads existing journal status/interrupts and
requires a complete original MCP result to report completion. It refuses to
convert a mere queue receipt, arbitrary model response or bare native RUN_ERROR
into a fabricated final MCP result. Native errors without a canonical MCP
outcome require an explicit host Snapshot projection. A host whose detached async operation uses a different canonical result
store must supply its own `Snapshot` callback and authorize its exact operation
and causal relationship. Unrepresentable frontend-only interrupts are rejected;
this adapter does not silently grant browser-tool execution.

The optional transport decorator and Tasks subscription transport are
implemented. Automatic registration inside the closed `viant/mcp` handler switch
and application-specific capture/policy configuration remain integration steps.
Core pins the published base Tasks module; the new subscription exports require
publishing and pinning the subscription revision (or an explicit local workspace
replacement during development). No runtime config, endpoint,
UI, scheduler, credentials or deployment is changed by this source adapter.

Source validation covers native-store alias reconstruction and owner/tenant
isolation, detached child observation after parent completion, original MCP
result/tool-error preservation, partial inputs and causal continuation, unknown
resume nonreplay, TTL, standard JSON-RPC polling, normal-call preservation, and
a real existing live AG-UI waiter waking without restarting its query. Existing
fork/detach/async/AG-UI regression suites remain applicable; deployment and the
host's exact native operation capture still need end-to-end qualification.


### Task subscriptions through native AG-UI events

The adapter implements `tasks.ChangeSource` in `sdk/mcp_tasks_subscription.go`.
`MCPTasksAGUIControls` attaches subscriptions to the existing native execution
bus through `subscribeNativeEvents`; it subscribes before the initial full-state
snapshot. Only invalidation hints are forwarded internally. The Tasks stream
re-reads the owned canonical run/journal after each hint, on a bounded timer, and
on reconnect, covering dropped/overflowed hints and other-process commits. Native
AG-UI progress/logging payloads never become Tasks notifications.

Enable the actual transport in the MCP handler factory using
`expose.WithTaskSubscriptions(notifier, taskrpc.SubscriptionOptions{...})` on
`expose.WithTasks`. The notifier must be the request/session transport notifier.
For stateless HTTP, `taskrpc.HTTPHandler` supplies a request-scoped SSE notifier.
The source adapter makes no subscription claim or acknowledgement when this
transport option is absent. Authentication and exported-tool capture still use
the host's real policy; subscription IDs or caller task IDs are not authority.

`subscriptions/listen` accepts a bounded set of owned `notifications.taskIds`,
acknowledges the exact set first, then sends `notifications/tasks` with the same
complete detailed state that `tasks/get` would return. Every message carries
the originating string/integer subscription request ID in `_meta` (the typed
HTTP client generates positive integers). Input-required states
are checked against the subscribing client's ordinary input capabilities.
Foreign owners/tenants and mixed unsupported task/base filters are rejected
before any acknowledgement or task payload is sent. Reauthorization continues
through canonical reads throughout the stream.

A terminal task notification contains the original result/error, and the stream
ends gracefully once all selected tasks are terminal or its bounded lifetime
expires. Cancelling the SSE request, closing an observer, or cancelling a stdio
subscription stops observation only. Explicit `tasks/cancel` still invokes the
existing native scoped cancellation route; its acknowledgement is not a claim
that execution stopped. Fork parents and detached children keep their existing
native scope and lifetime.

The generic typed HTTP client `Subscribe` validates acknowledgement ordering,
subscription metadata, task IDs, notification type and full state. Reconnect is
bounded, uses a fresh subscription ID and canonical snapshot, and never repeats
a tool call, input response or cancellation. HTTP frame budgets are independent
of total stream lifetime. Malformed frames, owner/policy errors and callback
failures are not retried. No second scheduler, execution owner or AG-UI event
projection is created.

Source tests include a real HTTP Tasks stream with native AG-UI event wakeups,
detached-child completion after parent completion, missing-hint reconciliation,
observer disconnect, cancellation terminal state, and tenant/owner rejection.
The wakeup test uses a one-hour reconcile timer and still receives the terminal
state within one second, proving that the existing native event bus drives it.

### Browser setup timing diagnostics

The browser SDK records bounded User Timing measures named
`agently.browserMCP.metadata`, `agently.browserMCP.initialize`,
`agently.browserMCP.catalog`, `agently.browserMCP.register`, and
`agently.browserMCP.setup`. Each name retains its latest measurement only.
Measures contain durations, not credentials, arguments, tool schemas, origins,
or conversation identities. Instrumentation failure never changes execution.

Inspect these with browser developer tools or `performance.getEntriesByName`.
`setup` covers one setup attempt, including metadata lookup. A reused connection
updates `setup` and `metadata` without another initialize/catalog/register pass;
those stage values may therefore describe the earlier cold connection. They
exclude model generation and subsequent tool execution. Failed attempts are
also timed, so confirm successful setup before reporting normal latency.

No workspace browser-MCP descriptor means no extension connection or catalog
registration. A configured browser server uses the browser-provided catalog;
it must never fall back to opening that server from the backend.

### Browser tool terminal results and deadlines

The browser SDK returns a bounded terminal error for a transport timeout or
loss, retaining the original call ID and catalog metadata. An in-flight call
has an **unknown** effect outcome; a queued call that never reached the port is
`notDispatched`. Repeated identical IDs share or return the cached result;
conflicting IDs and late replies never dispatch another input. Socket loss
blocks new sends and app resource access, while preserving a still-valid
catalog only long enough to report original results. Explicit close, account
reset, catalog revocation, expiry, and ownership checks remain enforced.

New deferred client calls also carry a server-owned immutable deadline in the
existing pending-call journal. It uses the configured tool timeout when
available (`ToolCallTimeoutSec`, the scoped tool timeout, or
`AGENTLY_TOOLCALL_TIMEOUT`), otherwise the existing three-minute default, capped
by the parent deadline. The existing watchdog runs at startup and every 60
seconds by default. Timeout delivery therefore includes up to a sweep interval
plus bounded discovery, backlog, and completion work; this is not an exact
nine-second guarantee.

A due call is completed through a deterministic successor and the existing
prior-revision compare-and-swap. The client result and timeout producer compete
for that same continuation. The winner commits one original terminal result;
restart recovery adopts it rather than replaying browser input. Timeout results
state that the effect is unknown. They are persisted before attempting model
continuation, so a later missing browser bundle or model configuration cannot
erase the timeout receipt. No revoked browser capability is recreated.

Human approvals and calls with later deadlines remain pending on the successor
without being answered. Legacy pending calls without a saved deadline receive
an explicit **three-minute recovery grace**, measured from the first verified
recovery observation that commits successfully. This does not claim to recover
an original admission timeout. The existing Datly-owned journal atomically
consumes the prior revision CAS and publishes a replacement pending wait with
immutable `recoveryObservedAt` and `deadlineAt`; there is no intermediate missing
pending run. A lost commit reply, restart, or later sweep adopts those exact
values without extending the grace. A normal result winning the prior CAS
prevents migration; a result arriving during the grace can resolve the same
original call IDs. The watchdog's normal sweep/backlog latency still applies.
No tool or model is executed by the recovery-grace journal itself.

Cross-conversation delegated dependency graphs remain with their existing
dependency coordinator and are excluded from this timeout producer. These are
explicit recovery limits, not evidence that every browser workflow is
production-stable.
