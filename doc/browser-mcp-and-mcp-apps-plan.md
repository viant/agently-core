# Generic browser MCP and official MCP Apps integration

## Objective and ownership

Complete the generic browser-side MCP configuration and execution integration,
then make Agently's MCP App host compatible with the current official stable MCP
Apps extension. MCP Apps UI must remain separate from workspace UI. No
Mechanize-specific tool implementations, server names, extension IDs, bank data,
credentials, or application workflows belong in this implementation.

Implementation owner: a GPT-6.1 Sol sub-agent. Root continues the separate local
Mechanize connection/login work. Preserve existing dirty files and generated
`.agently` directories. Do not deploy services, edit live workspace configuration,
load credential stores, or inspect private installation/bank files.

## Current authoritative baseline

The preceding source implementation added:

- MCP configuration: `executionLocation`, `browserTransport`, optional
  `browserTools` name patterns.
- Backend MCP manager refusal of browser execution before credential/cookie
  construction; no fallback to a backend HTTP or stdio connection.
- Authenticated ephemeral browser catalogs bound to user, owned conversation,
  AG-UI thread, configured server, connection generation and schema hash.
- Existing server-tool aliases and agent/tool-bundle selection, without a new
  tool namespace or a universal execute wrapper.
- Browser-local discovery/execution through an injectable transport factory and
  a configurable Chrome extension adapter.
- Durable AG-UI call/result correlation, parent continuation, connection teardown
  and unknown-call nonreplay.
- Agently's generic client opt-in. Forge renders the resulting standard tool
  events and has no separate MCP executor.

Reported validation passed: Go MCP config/manager, catalog, clienttool and SDK
suites excluding `Live`; agent selection tests; SDK TypeScript typecheck; 540 TS
tests with 5 skipped; Agently singleton smoke test. Reinspect these changes and
their coverage rather than treating the report as proof of the new Apps work.

Relevant paths:

- `protocol/mcp/config/{config.go,browser.go}`
- `protocol/mcp/manager/{manager.go,provider_repo.go,browser.go}`
- `service/browsermcp/`, `service/agent/{binding.go,tools.go}`
- `runtime/clienttool/session.go`
- `sdk/browser_mcp.go`, AG-UI durable handlers and interrupt/result processing
- `sdk/ts/src/{browserMCP.ts,aguiConversationTransport.ts,aguiSession.ts,client.ts}`
- sibling `agently/ui/src/services/agentlyClient.js`

## Protocol baseline and compatibility audit

Verify the latest **stable** specification before choosing a pin; distinguish
stable from draft. The preceding audit found stable `2026-01-26` and official
`@modelcontextprotocol/ext-apps` version `2.0.3`, with compatible 1.x/2.x wire
protocols. Do not upgrade a dependency solely because its version is newer.

Primary references:

- [Official MCP Apps repository](https://github.com/modelcontextprotocol/ext-apps)
- [Stable Apps specification](https://github.com/modelcontextprotocol/ext-apps/blob/main/specification/2026-01-26/apps.mdx)
- [Host, view and server architecture](https://apps.extensions.modelcontextprotocol.io/api/documents/overview.html)
- [Official postMessage transport](https://apps.extensions.modelcontextprotocol.io/api/classes/message-transport.PostMessageTransport.html)

Existing `viant/mcp-ui` has typed metadata/resources, but its `appproto` defines
custom `mcpui:*` envelopes. Agently already has an official `AppBridge` renderer
alongside a legacy renderer. Inspect actual code and SDK constructor signatures
before identifying defects. Existing plain-HTML acceptance, CSP permissiveness,
visibility enforcement and lifecycle behavior need explicit review.

## Required architectural separation

| Layer | Owns | Must not own |
| --- | --- | --- |
| MCP browser transport | Config-selected MCP discovery/calls and originating conversation correlation | UI rendering, workspace navigation, native credentials |
| MCP Apps host | Tool-associated `ui://` resources, sandboxed app instances, official host/view protocol | Workspace screen state or arbitrary workspace window execution |
| Workspace UI / Forge | Workspace screens, saved definitions, navigation and workspace state | Implicit authorization for app-originated MCP calls |
| Agent/tool execution | Configured server selection, agent/bundle policy, durable pending calls and outcomes | Trust in caller-supplied schemas as privilege authority |

MCP Apps is optional UI enhancement. Tools must remain independently callable
through normal MCP. A tool without UI metadata must keep working.

Workspace-backed rendering can remain an explicitly selected adapter if useful,
but it must not be the default definition of an MCP App. The normative app host
must not require a Forge window key, saved workspace screen, workspace navigation
state or an Agently-specific resource URI. Remote server-owned app resources
must also work.

## Work package 1: finish and verify generic browser execution

1. Reinspect the current public config schema. Keep extension ID, port name,
   transport type and deadlines configurable; retain transport-factory injection
   for other implementations.
2. Confirm no browser descriptor contains bearer tokens, cookie values, OAuth
   resources, private key paths, stdio commands or backend transport fallbacks.
3. Confirm configured server/name policy and normal agent/bundle selection are
   applied after authenticated ephemeral catalog registration. Catalog schemas
   and descriptions are metadata, not new privilege authority.
4. Preserve normal server-tool names and individual schemas in `tools/list`.
   Apply scope/visibility filtering; do not replace the catalog with one generic
   execute tool or hardcoded application methods.
5. Bind calls/results to original user, conversation, thread, server, catalog
   generation, connection and call ID. A different page/conversation cannot adopt
   a pending call or its result. Unknown execution must not be retried after
   navigation, disconnection, cancellation or expiry.
6. Verify client continuation and parent run IDs across multiple calls, failed
   calls, interrupted streams, duplicate callbacks and stale catalogs.
7. Keep Forge on standard tool event projections. Add an injected relay only if
   actual code requires it; never a datasource-to-local-tool bypass.

## Work package 2: official MCP Apps contract

1. Create a method/capability matrix against the stable official specification:
   mandatory behavior, optional advertised behavior and explicit unsupported
   behavior. Implement the mandatory host/view lifecycle.
2. Use official JSON-RPC `ui/initialize`, initialized notification, tool input,
   partial input/result delivery, and acknowledged resource teardown. Negotiate
   versions/capabilities; do not infer readiness from arbitrary messages.
3. Use `io.modelcontextprotocol/ui` negotiation and `_meta.ui.resourceUri`.
   Enforce exact `ui://` resource identity and
   `text/html;profile=mcp-app` MIME for the normative renderer.
4. Preserve resource-owned metadata, CSP and sandbox requirements. Reject
   undeclared network/frame/resource access. Do not globally allow eval, blob or
   inline execution to make a resource appear to work; use the spec/SDK's actual
   sandbox/CSP mechanism and test legitimate scripts through it.
5. Enforce `_meta.ui.visibility` for model discovery and app calls. App-only
   visibility must not become authorization to read credentials or bypass the
   normal tool policy.
6. Separate host/view handshake from outer client/server MCP initialization.
   Do not leak iframe capabilities or identity into the MCP server connection.
7. Validate `event.source`, expected iframe instance, lifecycle state, method,
   request ID, resource/server binding and message size. Opaque iframe origins
   require correct source-window checks, not an arbitrary-origin allowlist.
8. Validate app-originated tool/resource/message/open-link actions through the
   actual host policy. Preserve canonical guest provenance, real conversation
   ownership, approval queue, and tool-bundle enforcement.
9. Handle cancellation, malformed frames, navigation, teardown acknowledgement
   timeout and duplicate initialization without replay or stale iframe authority.
10. Keep credentials and local Vault UI out of app HTML, resources, tool input,
    results, `_meta`, model context and diagnostic messages.

## Work package 3: isolated UI ownership and legacy compatibility

1. Introduce/finish dedicated MCP App instance state keyed to the originating
   tool call, configured server and exact resource. Do not reuse workspace window
   IDs as authorization identities.
2. Resolve server-owned resources through the selected MCP execution location.
   Browser-local resources need an authorized browser route, not a hidden backend
   HTTP request to the user's laptop. If the selected server does not advertise
   resource support, report unavailable without synthesizing HTML.
3. Keep workspace rendering/navigation APIs unchanged. Add regression tests that
   app initialization, teardown and guest messages do not mutate workspace state.
4. Keep custom `mcpui:*` only behind an explicit compatibility adapter with a
   documented version/selection rule. Do not silently translate unknown messages
   or pretend legacy fallback negotiated the official protocol.
5. Normal Apps UI should use official SDK behavior where available. Keep Go
   metadata/runtime interfaces generic; do not port the entire JS SDK to Go.
6. Update sibling `viant/mcp-ui` only where typed official contract support is
   needed, preserving its separation from core MCP schema and transport/runtime.
   Preserve pre-existing dirty `go.mod` and `go.sum` changes.

## Validation and evidence

- Config tests: browser/server mode separation, invalid transport parameters,
  secret-bearing fields rejected, no backend auth/cookie/resource setup.
- Catalog tests: ownership, thread mismatch, schema/name substitution, collisions,
  stale config/generation, auth change, revoke and expiry.
- Execution tests: actual configured browser catalog -> existing agent/bundle
  selection -> original AG-UI pending call -> fake local MCP server -> correlated
  result -> durable continuation. Include two conversations and unknown nonreplay.
- Official interoperability: test a small generic MCP App using the actual
  official SDK against the implemented host. Verify initialization, result
  delivery, allowed app tool call and teardown; use no Mechanize-specific fixture.
- UI boundary tests: wrong iframe, pre-handshake calls, nested/unknown payloads,
  URI/server substitution, forbidden methods/domains, visibility and legacy opt-in.
- Workspace isolation tests: existing Forge/workspace views still work; arbitrary
  MCP App works without a saved workspace screen; no state/navigation crossover.
- Run relevant Go package suites, TypeScript typecheck, SDK/frontend suites, and
  source-only Agently smoke tests. Keep live tests/configuration out of scope.
- Record exact commands/results and skipped/unverified coverage. Green unit
  tests do not prove deployed browser or banking automation.

## Deliverables and completion criteria

1. Generic browser execution selected through MCP configuration, documented with
   placeholders rather than personal extension IDs or credentials.
2. Normal permitted tool catalog and call/result lifecycle proven through the
   configured browser path, without application-specific tool logic.
3. Official MCP Apps compatibility matrix and implemented mandatory lifecycle,
   resource/visibility/security behavior verified by official SDK interoperability.
4. MCP Apps UI independent of workspace UI, with explicit legacy compatibility
   and workspace regression coverage.
5. Updated integration documentation, source changes and test evidence ready for
   review; remaining deployment qualifications stated plainly.

Do not mark this plan complete based on intent, compilation alone, custom legacy
messages, a diagnostic-only bridge or a workspace-window-only demo. After the
framework work is finished, return a concise handoff so root can prioritize the
separate mBank runtime deployment, private sign-in, history reconciliation and
payment preparation.

## Implementation handoff (2026-10-07)

The generic browser execution implementation has been retained and extended
without product-specific methods. Official Apps now have a dedicated host
transport/lifecycle and a separate-origin sandbox entry. Outer negotiation uses
`extensions`, not the legacy `experimental` slot. The actual existing SDK 1.7.5
`PostMessageTransport(target, target)` signature was verified valid; it was not
misreported as a constructor bug. `AppBridge` owns the official handshake and
schemas; the host transport adds source/origin, lifecycle, message budget,
nonreplay, and teardown authority checks.

Resource reads enforce exact identity and profile MIME; resource-owned CSP
metadata supplies only declared origins. Inline bundled scripts/styles use the
stable spec's declared defaults; eval and blob allowances were removed. A generic
browser resource route revalidates the authenticated catalog and uses only the
original browser connection. Browser app instances are ephemeral, local, keyed
to their original call/catalog/connection and never restored as workspace windows.
Remote app tools keep canonical native guest provenance, agent/bundle policy and
approval behavior. Visibility is enforced for model discovery, browser catalogs,
app policy, and native dispatch. Legacy `mcpui:*` is explicit
`mcpui:1.0.0` adapter selection, separate from normative Apps.

The capability matrix and sandbox deployment contract are in
[mcp-integration.md](mcp-integration.md#official-mcp-apps-host). Mandatory
host/view lifecycle is implemented. Optional browser **guest** tool execution is
explicitly unsupported and not advertised; no local policy/approval bypass was
introduced. Optional links, app messaging, logging, downloads, model context
updates, non-inline display modes and device permissions are also not advertised.

Changed source includes:

- `runtime/mcpapps/visibility.go`, registry visibility and native dispatch gates,
  `service/agent` model/app policy, and MCP client initialization/fallback helpers.
- Browser catalog metadata/resources in `service/browsermcp` and local app
  lifecycle/resource APIs in `sdk/ts/src/browserMCP.ts` and `client.ts`.
- Sibling Agently `standardHost.js`, `standardDocument.js`, `sandboxProxy.js`,
  `StandardAppRenderer.jsx`, browser app feed, explicit legacy selection,
  `mcp-app-sandbox.html`, and build entry.
- Sibling `mcp-ui` official `extensions` capability helpers, typed visibility and
  resource CSP metadata. Pre-existing module files were preserved; testing used
  a temporary modfile because the supplied module requires a newer Go directive.

Validation completed on source (no live configuration or bank files used):

| Command / scope | Result |
| --- | --- |
| `go test ./service/browsermcp ./protocol/mcp/clienthandler ./runtime/mcpapps ./internal/tool/registry ./service/agent -skip Live -count=1` | Passed; registry and agent full suites included |
| `go test ./service/browsermcp ./protocol/mcp/clienthandler ./runtime/mcpapps ./protocol/mcp/expose ./protocol/mcp/uifallback ./sdk -skip Live -count=1` | Passed; full SDK suite 117.6 seconds |
| `go test ./internal/tool/registry -run MCPVisibility -count=1` and `go test ./service/agent -run MCPAppsVisibility -count=1` | Passed; native/app audience enforcement and configured model/app policy separation |
| SDK `npm run typecheck` | Passed |
| SDK `npm test` | 541 passed, 5 skipped; 50 suites passed, 2 skipped |
| Agently UI Apps/workspace/chat regression selection | 15 suites, 103 tests passed |
| `vite build --outDir /tmp/agently-apps-build --emptyOutDir` | Passed; no deployment output written |
| `MCP_APPS_CHROME_EXECUTABLE='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' node scripts/proof-mcp-apps.mjs` | Passed on two isolated origins using actual official App SDK: handshake, partial/full input, host result, app tool call, CSP blocking eval/network/host DOM access, acknowledged teardown |
| Sibling `mcp-ui`: `go test -modfile=/tmp/agently-mcpui-test.mod -mod=mod ./...` | All five packages passed; original go.mod/go.sum retained |

Remaining consuming-runtime qualifications: deploy only the sandbox entry and
its dependencies on a dedicated origin and select its URL; verify the actual
browser extension's capability/resource support; verify deployed same-server
app approval and browser teardown behavior. No deployment, commit, push, live
configuration edit or private runtime action was performed. This source handoff
does not certify deployed banking automation or add optional browser guest tool
execution.
