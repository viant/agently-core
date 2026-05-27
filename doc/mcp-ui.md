# MCP UI implementation

Agently implements the MCP UI / MCP Apps extension as a layered system:

- `github.com/viant/mcp-ui` owns the typed extension contract
- `agently-core` produces tool/resource metadata and exposes the same-origin
  host bridge
- `agently/ui` renders MCP UI resources in chat bubbles and mediates guest
  actions

This doc explains the implementation, the boundaries between layers, and the
real execution paths in the current worktree.

## Scope

This doc covers:

- how Agently advertises `ui://...` resources on MCP tools
- how Agently resolves and serves MCP UI resources
- how the browser host renders those resources
- how guest `postMessage` actions flow back through approval-aware backend
  execution
- how compatibility fallback works when the peer does not negotiate UI support

This doc does not try to replace the extension SDK doc in
`github.com/viant/mcp-ui`. It focuses on Agently’s implementation of that
contract.

## Layers

| Layer | Responsibility | Main paths |
|---|---|---|
| Extension SDK | Typed capability, `_meta.ui`, `ui://...` builders, browser envelope schema | `../../mcp-ui/capabilities/`, `../../mcp-ui/meta/`, `../../mcp-ui/resource/`, `../../mcp-ui/appproto/`, `../../mcp-ui/compat/` |
| MCP producers | Attach MCP UI metadata to tools/resources and read exact `ui://...` resources | `../protocol/tool/adapter/mcp/`, `../protocol/ui/resource/`, `../protocol/mcp/expose/`, `../protocol/mcp/localclient/` |
| Same-origin host bridge | Narrow browser-facing HTTP endpoints for resource read and guest tool execution | `../sdk/handler_mcpui.go`, `../sdk/embedded_mcpui.go`, `../sdk/api/types.go` |
| Canonical execution path | Approval-aware guest tool execution and queue metadata | `../service/agent/guest_tool.go`, `../service/shared/toolexec/tool_executor.go`, `../protocol/tool/approvalqueue/` |
| Web host | Fetch resource, build iframe payload, validate guest messages, dispatch host actions | `../../agently/ui/src/services/mcpApps/`, `../../agently/ui/src/components/mcpApps/` |
| Chat projection | Project `uiResourceUri` into a dedicated MCP UI bubble row | `../../agently/ui/src/services/canonicalTranscript.js`, `../../agently/ui/src/components/chat/` |

## Core contract

Agently follows the typed MCP UI contract from `viant/mcp-ui`:

- tool metadata advertises `_meta.ui.resourceUri`
- the resource identity is an exact `ui://...` URI
- the host resolves the resource through `resources/read`
- the browser never speaks MCP directly
- guest actions use a small `postMessage` envelope contract

Important identifiers:

- MIME type: `text/html;profile=mcp-app`
- tool metadata field: `_meta.ui.resourceUri`
- guest methods:
  - `mcpui:host-ready`
  - `mcpui:message`
  - `mcpui:tools-call`
  - `mcpui:open-link`
  - `mcpui:tool-input`
  - `mcpui:tool-input-partial`
  - `mcpui:tool-result`
  - `mcpui:teardown`

## Server-side architecture

### 1. Tool metadata injection

Tool metadata is attached at the MCP adapter layer, not invented by the UI.

Relevant paths:

- `../protocol/tool/adapter/mcp/`
- `../protocol/mcp/expose/tool_handler.go`
- `../protocol/mcp/localclient/service_handler.go`

At `tools/list` time:

- the MCP handler lists tools normally
- if the tool definition or service provides `mcpuimeta.ToolUI`, that metadata
  is attached to the returned tool
- when UI capability is absent, Agently can optionally append an embedded HTML
  fallback, but only when the tool explicitly opts in

The capability/fallback decision is canonicalized in:

- `../../mcp-ui/compat/switch.go`
- `../protocol/mcp/uifallback/helper.go`

That means the fallback rule is exact:

- if UI capability is negotiated, use `_meta.ui.resourceUri`
- if UI capability is not negotiated, embedded fallback is allowed only when
  `_meta.ui.fallback = "embedded"`

There is no UI-side guessing.

### 2. Exact resource production

Resource production lives in `agently-core`, not in the browser.

Relevant paths:

- `../protocol/ui/resource/workspace.go`
- `../protocol/mcp/expose/tool_handler.go`
- `../protocol/mcp/localclient/service_handler.go`

The canonical resource shape published by agently-core today is:

- workspace-backed Forge window resources

The canonical workspace-backed implementation is:

- server scope: `agently.wk_default`
- URI pattern: `ui://agently.wk_default/view/<windowKey>`

`ReadWorkspaceViewResource`:

- loads the real workspace Forge window through
  `service/ui/window.LoadWorkspaceWindow`
- renders canonical HTML payload
- computes `contentHash`
- attaches typed `_meta.ui`
- optionally publishes:
  - `rendererUrl`
  - `sandbox`
  - `protocolVersion`

That keeps resource identity and resource contents server-owned.

### 3. Same-origin browser host endpoints

The browser host does not get direct MCP credentials or an MCP client.
Instead, Agently exposes a narrow same-origin bridge:

- `GET /v1/api/mcp-ui/resources/read?uri=...`
- `POST /v1/api/mcp-ui/tools/call`

Implementation:

- `../sdk/handler_mcpui.go`

Those endpoints are mounted by the SDK handler when configured with:

- `WithMCPUIResourceReader(...)`
- `WithMCPUIToolCaller(...)`

This is intentional:

- the browser only asks the host to read an exact `ui://...` resource
- the browser only asks the host to execute a guest-originated tool call
- all MCP transport, auth, approval, and bundle resolution remain server-side

### 4. Guest tool execution path

Guest tool execution is host-owned and approval-aware.

Relevant paths:

- `../sdk/embedded_mcpui.go`
- `../service/agent/guest_tool.go`
- `../service/shared/toolexec/tool_executor.go`
- `../protocol/tool/approvalqueue/state.go`
- `../sdk/api/types.go`

`POST /v1/api/mcp-ui/tools/call` ultimately flows through:

1. `backendClient.ExecuteMCPUIToolCall`
2. `agent.Service.RunGuestToolCall`
3. `toolexec.ExecuteToolStep`

Important properties of that path:

- it creates or reuses the real conversation context
- it persists a real turn for the guest call
- it applies tool-bundle approval metadata before execution
- queued approvals go through the existing approval queue
- the canonical producer-side source label is `guest_ui`

That `guest_ui` source is emitted in two places:

- returned tool-call output
- persisted approval queue metadata

So guest-originated actions are not a UI invention. They are canonical backend
state.

### 5. Resource read and embedded fallback in MCP server mode

When Agently itself is acting as an MCP server, the MCP handlers implement:

- `resources/list`
- `resources/read`
- `tools/list`
- `tools/call`

Relevant paths:

- `../protocol/mcp/expose/tool_handler.go`
- `../protocol/mcp/localclient/service_handler.go`

Both handlers:

- capture negotiated client capabilities during `initialize`
- serve real `resources/read` results for `ui://...`
- optionally append embedded fallback content when capability is absent and the
  tool explicitly opted in

That keeps the compatibility rule consistent between:

- Agently as an exposed MCP server
- Agently’s same-origin browser host

## Browser host architecture

The browser host lives in `agently/ui`, but the behavior is driven by the
server-owned contract above.

### 1. Resource loading and iframe construction

Relevant paths:

- `../../agently/ui/src/services/mcpApps/resourceLoader.js`
- `../../agently/ui/src/components/mcpApps/AppRenderer.jsx`
- `../../agently/ui/src/components/mcpApps/AppFrame.jsx`

Flow:

1. `AppRenderer` receives a `ui://...` URI
2. it calls `GET /v1/api/mcp-ui/resources/read?uri=...`
3. `resourceLoader` validates:
   - exact MCP UI MIME type
   - non-empty HTML text
4. it decides between:
   - `srcdoc` render
   - same-origin renderer route via `_meta.ui.rendererUrl`

The host default is strict:

- `sandbox = allow-scripts`
- nonce-based deny-by-default CSP for raw `srcdoc`

Optional relaxations come only from explicit typed metadata:

- `_meta.ui.sandbox`
- `_meta.ui.csp`
- `_meta.ui.cspPolicy`

### 2. Host-to-guest bootstrap

`AppRenderer` sends an exact `mcpui:host-ready` envelope to the iframe with:

- `windowId`
- `resourceUri`
- `allowedTools`
- `allowedToolBundles`
- `protocolVersion`

It can also send:

- `mcpui:tool-input`
- `mcpui:tool-input-partial`

The guest must echo the same `windowId` and `resourceUri` back on every host
action request. Agently does not use fuzzy attachment or inference.

### 3. Guest message acceptance rules

Relevant path:

- `../../agently/ui/src/services/mcpApps/hostAcceptance.js`

Guest messages are accepted only when all of these are true:

- exact `event.source` match to the mounted iframe window
- exact `windowId` match
- exact `resourceUri` match
- origin rules match the sandbox mode

For same-origin route-backed frames:

- `event.origin` must equal `window.location.origin`

For opaque-origin `srcdoc` frames:

- the host relies on the exact bound source window, not fuzzy origin text

### 4. Guest action dispatch

Relevant paths:

- `../../agently/ui/src/services/mcpApps/bridge.js`
- `../../agently/ui/src/components/mcpApps/AppRenderer.jsx`

Supported guest actions today:

- `mcpui:message`
  - appended to host-visible bubble state
- `mcpui:open-link`
  - host-mediated
  - MVP policy allows only absolute `https:` URLs
- `mcpui:tools-call`
  - validated against `_meta.ui.allowedTools`
  - executed through `POST /v1/api/mcp-ui/tools/call`
  - result returned as `mcpui:tool-result`

Open-link policy is enforced in the host bridge, not by the guest:

- `javascript:`
- `data:`
- relative URLs
- malformed URLs

are rejected deterministically.

## Chat rendering path

The browser host is only part of the story. Agently also projects MCP UI
resources into dedicated chat rows.

Relevant paths:

- `../../agently/ui/src/services/canonicalTranscript.js`
- `../../agently/ui/src/components/chat/IterationBlock.jsx`
- `../../agently/ui/src/components/chat/ChatFeedFromChatStore.jsx`
- `../../agently/ui/src/components/chat/MCPUIBubble.jsx`

Execution details already carry tool steps. When a tool step contains
`uiResourceUri`:

- the canonical transcript normalizer preserves it exactly
- the chat projection creates an `mcpui` render row
- `MCPUIBubble` renders `AppRenderer` with that exact URI

This is why a tool result can appear as:

- execution details
- plus a dedicated interactive bubble below it

without inventing a second backend concept.

## Workspace-backed Forge surfaces

Agently supports richer same-origin renderer-route MCP UI surfaces for real
workspace Forge windows.

Relevant paths:

- `../protocol/ui/resource/workspace.go`
- `../../agently/ui/src/components/mcpApps/MCPUIForgeWindowPage.jsx`
- `../../agently/ui/src/services/mcpApps/forgeGuestBridge.js`

The current workspace-backed flow is:

1. a tool advertises `ui://agently.wk_default/view/<windowKey>`
2. `resources/read` returns canonical HTML plus `_meta.ui.rendererUrl`
3. the browser loads `/mcp-ui/forge-window?...`
4. that route bootstraps the real Forge window renderer
5. guest actions still flow through the MCP UI host bridge

Important boundary:

- the iframe owns the MCP UI surface
- the Forge window definition still comes from the real workspace

Agently does not flatten Forge configuration into browser-only heuristics.

## Security model

The implementation follows a host-owned security model.

### Browser never owns MCP

The browser does not:

- open MCP transports
- call remote MCP servers directly
- invent approval policy

Instead it uses same-origin host endpoints and host-owned routing.

### Exact identity over heuristics

Agently uses exact identity throughout:

- exact `ui://...` resource identity
- exact `windowId`
- exact `resourceUri`
- exact bound iframe window identity

### Explicit policy surfaces

Policy is explicit and typed:

- `_meta.ui.allowedTools`
- `_meta.ui.allowedToolBundles`
- `_meta.ui.sandbox`
- `_meta.ui.cspPolicy`
- `_meta.ui.fallback`

The host does not infer these from HTML content.

### Approval remains canonical

Guest `tools/call` does not bypass existing approval behavior.

If a guest-requested tool is queue-gated, the canonical queue path is still
used, and the result status is surfaced back to the bubble as queued/completed
/failed.

## Actionable examples

### Example 1: expose a workspace-backed MCP UI tool

This example shows the producer-side steps in `agently-core`.

```go
package myservice

import (
	"context"

	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpuimeta "github.com/viant/mcp-ui/meta"

	uiresource "github.com/viant/agently-core/protocol/ui/resource"
)

func (s *Service) MCPUIToolUI(method string) (mcpuimeta.ToolUI, bool) {
	if method != "show_workspace_summary" {
		return mcpuimeta.ToolUI{}, false
	}
	return mcpuimeta.ToolUI{
		ResourceUri: uiresource.WorkspaceViewURI("summary"),
	}, true
}

func (s *Service) MCPReadResource(ctx context.Context, uri string) (*mcpschema.ReadResourceResult, error) {
	return uiresource.ReadWorkspaceResource(ctx, uri)
}
```

What this gives you:

- `tools/list` advertises `_meta.ui.resourceUri`
- `resources/read` resolves the exact `ui://...` URI
- the host can render the result without browser-side MCP logic

### Example 2: mount the same-origin host bridge

```go
mux := sdk.NewHandler(
	client,
	sdk.WithMCPUIResourceReader(client.ReadMCPUIResource),
	sdk.WithMCPUIToolCaller(client.ExecuteMCPUIToolCall),
)
```

That mounts:

- `GET /v1/api/mcp-ui/resources/read`
- `POST /v1/api/mcp-ui/tools/call`

### Example 3: handle a guest tool call from the browser

From the guest side, the iframe posts:

```json
{
  "jsonrpc": "2.0",
  "method": "mcpui:tools-call",
  "params": {
    "windowId": "mcpui-preview:ui://agently.wk_default/view/order",
    "resourceUri": "ui://agently.wk_default/view/order",
    "name": "system/os:getEnv",
    "arguments": { "name": "HOME" }
  }
}
```

The host:

1. validates exact source/origin/identity
2. checks `allowedTools`
3. calls `POST /v1/api/mcp-ui/tools/call`
4. returns `mcpui:tool-result`

The backend path:

- persists a real guest turn
- applies bundle-derived approval policy
- labels the source as `guest_ui`

### Example 4: preserve a tool step as an interactive bubble

If the canonical transcript contains:

```json
{
  "toolName": "mcpuiverify/show_widget",
  "status": "completed",
  "uiResourceUri": "ui://mcpuiverify/demo/verify_widget"
}
```

the UI projection path preserves that exact URI and renders a dedicated
`MCPUIBubble`.

There is no second lookup by tool name.

## Current implementation boundaries

Agently’s current implementation intentionally separates concerns:

- `mcp-ui` owns the extension SDK contract
- `agently-core` owns resource identity, capability handling, approval-aware
  execution, and exact same-origin bridge endpoints
- `agently/ui` owns browser rendering and host mediation
- Forge-specific window definitions remain workspace-owned

This means:

- no browser-side MCP client
- no UI-only invention of backend semantics
- no fallback matching of guest messages to the wrong bubble
- no approval bypass for guest tool execution

## Related docs

- [doc/mcp-integration.md](mcp-integration.md)
- [doc/tool-system.md](tool-system.md)
- [doc/streaming-events.md](streaming-events.md)
- [doc/approval.md](approval.md)
- [doc/workspace-system.md](workspace-system.md)
- [../../mcp-ui/README.md](../../mcp-ui/README.md)
