# MCP Apps authorization through the durable AG-UI journal

Status: approved and implemented on 2026-10-03. GPT-6 Astra consultation findings
were incorporated; native, assembled-backend and builtin renderer acceptance pass.

The official MCP Apps middleware sends an isolated proxy run whose threadId is
its runId. Its published request contains serverHash, optional serverId, method,
and params. Those fields do not identify the original authorized conversation
or the particular rendered app. A configured server may serve several apps and
conversations. The backend must not infer app authority from a server hash.

## Proposed binding

Reuse the existing authenticated protocol journal as the durable app registry.
Each backend-produced MCP Apps activity receives an app-instance identity. Its
public serverId becomes an opaque app-scoped alias containing the activity's
scoped journal identity; its metadata retains the configured native server ID,
resource URI, version, and app-instance identity. These identifiers contain no
credentials. The original configured server hash remains unchanged.

When the builtin renderer sends __proxiedMCPRequest with that serverId, the
backend resolves the activity through the current principal's authorized run
and journal. It requires an actual backend-issued MCP Apps activity, verifies
its version, serverHash, and app binding, then uses the native configured server
ID and original conversation for canonical resource/tool authorization. Caller
URLs, headers, identities, and credentials never become configuration.

The ordinary AG-UI request/response shapes and builtin activity vocabulary stay
the same. The public alias is an implementation-specific server identifier,
which the published middleware already permits. Generic shells can ignore the
activity when they lack an MCP Apps renderer.

Resource reads are limited to the registered app resource. Tool calls retain
existing per-tool policy and approval. The original app scope does not grant
permission to invoke every tool on that server. Notifications and ping require
real configured host callbacks; absent callbacks are declared unsupported.

No new database tables or direct SQL are required. All binding lookup uses the
existing Datly-backed run/journal API. Revocation follows original conversation
visibility and explicit app policy; deleting/invalidating its run makes the
binding unavailable. The host result, structuredContent and _meta stay outside
model history and shared state.

## Validation before advertising

Prove isolated proxy runs cannot use foreign-principal journals, altered
serverHash, non-app events, removed app bindings, unauthorized resources/tools,
or caller-supplied native server IDs. Test two app instances on the same server,
restart lookup, the pinned official MCP Apps renderer, and approval continuation.
Until those pass, proxy capability is not advertised.

## Verified implementation

Aliases resolve the exact issued event and the current activity projection under
the current principal. Removed activities, invalidated origin runs, foreign
principals, changed native configurations and non-app events are rejected.
Child apps additionally require the trusted, immutable invocation ancestry and
retain their original child conversation independently from journal coordinates.
The current native agent's configured tool surface and approval metadata govern
every call; client tool bundles and native server IDs cannot replace them.

The dispatch fence precedes effects. Exact host receipts live in private run
pending data and terminal host results; they never enter native model messages
or shared state. Recovery with a receipt returns it; a dispatched call without
a receipt fails with an uncertain outcome instead of repeating its effect.
Registry, tool executor, MCP session/auth replay and HTTP redirects all honor
the scoped no-retry marker. A real 65-second HTTP call proved lease renewal,
competing-worker rejection and one remote effect. Native Datly parent/approval
records and canonical operation identities are used for approve/reject/resume.

The installed upstream middleware normally executes MCP directly. The external
shell uses an explicit AG-UI forwarding adapter, stripping inherited model
inputs and assigning isolated proxy threads. The published CopilotKit renderer
and its iframe handshake/tool callback were exercised against the assembled
backend, with only `/v1/ag-ui/run` application requests. Process restart preserves
aliases, completed resource receipts, pending approvals and byte-identical replay.

Default native bindings support configured HTTP/SSE endpoints. Notifications
remain unsupported without a real configured callback; STDIO needs an explicitly
trusted integration. The development harness uses the upstream renderer's
default iframe sandbox and records its browser warning; production untrusted-app
origin isolation is part of the later shell milestone, not proven by this fixture.

Reproducible assembly scripts: `dev/ag-ui/mcp-app-smoke.mjs --approval` and
`dev/ag-ui/mcp-app-restart-smoke.mjs prepare|resume <owned-file>`.
Evidence: `/tmp/agui-mcp-assembled-proof-20261003.log`,
`/tmp/agui-mcp-restart-prepare-20261003.log`,
`/tmp/agui-mcp-restart-resume-20261003.log`, and
`examples/ag-ui-shell/output/playwright/browser-mcp-app-isolated-20261003.png`.
