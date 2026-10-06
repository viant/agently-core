# External AG-UI test shell

This independent local React app uses the published **CopilotKit 1.77.0** `CopilotChat` from `@copilotkit/react-core/v2`, and upstream `HttpAgent` **1.0.1**. The package pins the same AG-UI core/client versions used by CopilotKit; `package-lock.json` pins transitive dependencies. Tool calls use CopilotKit's built-in default tool card, registered with `useDefaultRenderTool()`. There is no Agently SDK dependency or custom chat implementation.

The npm registry's latest versions were rechecked on October 2, 2026:
CopilotKit 1.77.0 and AG-UI core/client 1.0.1. Unsupported activity types,
including `agently.rendered-content`, have no registered renderer in this
harness and render nothing; the backend's safe text fallback remains visible.
The installed 1.77.0 `useRenderActivityMessage` hook was exercised directly with
an unknown activity and returned `null` without exposing its content.

Node 20.19+ or 22.12+ is required by Vite. From this directory:

```sh
npm ci
cp .env.example .env.local
# Edit AGENTLY_BACKEND_URL to the origin of your running Agently backend.
npm run dev
```

Open the printed local URL. The shell POSTs standard `RunAgentInput` to `/v1/ag-ui/run` and reads SSE using upstream event normalization, verification, and reduction. Agent ID and model are optional; empty fields use backend defaults. Each chat request adds `forwardedProps.agently = {version: "1", operation: "chat", payload: {agentId?, model?}}`. Capability discovery uses a separate HttpAgent/thread and `operation: "capabilities"`; `CUSTOM` events named `agently.capabilities` are displayed in the extension panel.

## Deterministic transport/UI fixture

In one terminal run `npm run mock`, and in another run `npm run dev` with the default backend URL. Send any chat message and use Discover capabilities. The fixture streams a clearly labeled reply and returns fixture capability data; it does not test Agently execution, authentication, tools, persistence, or feature parity. Stop the fixture before launching a real backend on port 8080, or configure a different backend origin.

## Authentication and proxy

Both Vite dev and preview proxy `/v1/ag-ui` to the server-only `AGENTLY_BACKEND_URL`. Browser requests use same-origin credentials; the proxy forwards cookies and Authorization headers and rewrites response cookie domains/paths to the shell origin. No backend API key is configured or exposed by this app. Authenticate using your backend's approved flow, and arrange for its session cookie to be valid on the shell's origin. This proxy does not automatically transfer cookies stored for another hostname; Secure cookies need HTTPS. The shell does not invent a login endpoint or proxy unrelated API routes.

Only configure a trusted backend. The direct-agent provider uses CopilotKit's documented `agents__unsafe_dev_only`, intended for development/prototyping. This local shell is not a production deployment or authentication gateway. Static `dist` alone has no proxy; use `npm run preview` locally after building.

```sh
npm run build
npm run preview
npm ls @ag-ui/core @ag-ui/client @copilotkit/react-core
```

Primary upstream references: [self-managed and local agents](https://docs.copilotkit.ai/strands/backend/self-managed-agents), [React SPA](https://docs.copilotkit.ai/react-spa), [AG-UI HTTP client](https://docs.ag-ui.com/sdk/js/client/http-agent). CopilotKit's current v2 chat lives in react-core, so the legacy react-ui package is deliberately unnecessary.

Verified here: dependency compatibility, TypeScript production build (upstream large-chunk warnings), fixture streams consumed by the pinned upstream HttpAgent, and browser chat/configuration/capability discovery using the actual upstream UI. The actual assembled Agently Go service was also exercised through the upstream browser UI using a deterministic local OpenAI-compatible model: two successful chat runs reused one thread, submitted the prior message history, and rendered one assistant reply each. A real backend `system/os/getEnv` tool execution also appeared in the upstream tool card with its fixture result and final text. Capability discovery advertised server-owned history and detach-on-disconnect. This verifies the real Go/runtime path with a local model fixture; authenticated-session flows and production model providers were not exercised. The inspector is disabled to prevent its notification CDN and telemetry requests.

MCP Apps contract fixture: run `MCP_APP_PORT=18243 node ../../../agently-ag-ui/dev/ag-ui/mcp-app-fixture.mjs`, then `node mcp-app-contract.mjs`. This invokes published `@ag-ui/mcp-apps-middleware@0.1.1` against a real loopback Streamable HTTP MCP server and validates isolated proxy event IDs plus resource/tool host envelope fidelity for two calls. It is an upstream contract check, not assembled backend authorization proof. The renderer is the builtin CopilotKit 1.77.0 MCP Apps renderer. Its proxy requests pass through the shell HttpAgent without receiving a chat-operation override.

Actual assembled backend checks are in `../../../agently-ag-ui/dev/ag-ui/mcp-app-smoke.mjs`; set `AGENTLY_AGUI_URL` and `MCP_APP_AGENT` for an isolated configured workspace. The published middleware executes MCP HTTP itself when installed in front of an agent. Consequently it must not be installed in the browser to bypass the backend's app bindings and approvals. The builtin renderer sends the published proxy request shape to the configured backend HttpAgent directly.
