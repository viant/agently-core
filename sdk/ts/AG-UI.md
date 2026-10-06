# AG-UI TypeScript transport

## Conversation transport

Outward SDK conversation interactions use AG-UI exclusively. Legacy protocol options and native conversation query/stream fallback paths have been removed. Configured BFF authentication, cookies, headers and injected networking remain in use. Workspace, report, layout and application services remain separate. `readConversationHistory`, `readApplicationState` and scoped `observeNativeEvents` support authorized application/history reads and background work; they never resubmit chat. Shared/read-only snapshots use a BFF read only after a protocol permission denial (403). This cutover does not establish complete UI parity.

`AgUiClient` uses exact dependencies `@ag-ui/client@1.0.1` and `@ag-ui/core@1.0.1` (protocol 1.0). Its `HttpAgent` owns POST streaming, schema enforcement, chunk normalization, lifecycle verification, and protocol reduction. `messages` and `state` retain standard message roles, multipart content, tool arguments/results, metadata, opaque continuation values and shared state. UI rows must be derived from this store; they must not reconstruct the next request.

```ts
import { AgUiClient } from './src/agui';

const client = new AgUiClient({ url: 'https://agent.example/run', threadId: 'thread-1' });
client.addMessage({ id: 'user-1', role: 'user', content: 'Hello' });
await client.run({ runId: 'run-1' }, {
  onMessagesChanged: ({ messages }) => render(messages),
  onRunErrorEvent: ({ event }) => showError(event.message),
});
```

`run` sends standard AG-UI inputs without Agently requirements. Supply the endpoint URL explicitly; there is no fallback to native endpoints. Configure credentials through `headers` or an injected `fetch`. `subscribe` exposes upstream callbacks for the full upstream event vocabulary, including unknown integration custom events. Protocol `RUN_ERROR` is reported through `onRunErrorEvent`; upstream does not reject its run promise for that terminal event. Transport/schema/sequence failures reject the promise and invoke upstream failure callbacks.

For an Agently endpoint, `runAgently({agentId, model}, parameters)` places execution selection inside `forwardedProps.agently` with `version: '1'`, `operation: 'chat'`, and `payload`. `discoverCapabilities` makes an explicit run with operation `capabilities` and requires a CUSTOM `agently.capabilities` event containing `{version:'1', capabilities: AgentCapabilities}`. Omitted standard capabilities mean unknown. The optional `capabilities.custom.agently` profile lists supported operations and execution controls; clients should enable selectors only when advertised. Discovery is opt-in because generic agents need not support it. Unknown custom versions remain visible to subscribers and are not interpreted.

`cancelRun(targetRunId, {runId}, subscriber?)` sends a separate version `1` `run.cancel` command on the configured endpoint, retaining its headers/fetch configuration. It works while the original stream is active and does not replace that stream’s protocol store. The command run ID is the durable request identity. Enable this extension only when the server advertises it. `agentlyCommandProps` builds other explicit versioned resource envelopes. `abortTransport` aborts the HTTP request. It does not claim backend execution cancellation. `pendingInterrupts` exposes a defensive copy of upstream interrupt state. `resume(responses, parameters)` requires an explicit resolved or cancelled response for every open interrupt and sends a new standard run. Unknown response IDs are rejected. Answering an expired interrupt fails; cancellation remains allowed. The optional fourth `resume` argument is an application response-schema validator; server validation remains authoritative. Tool registrations also accept `validateArguments(args, schema, call)` for application schema validation. Durable continuation belongs to the connected server.

`executeClientTools(registry)` explicitly executes pending frontend calls from the most recent successful run. Each registry entry contains a standard `tool` definition and an `execute(args, call)` handler. Handlers receive complete JSON arguments and return `{content, error?, metadata?}`; content supports text or ordered media parts. Applications validate arguments against their tool's parameters schema. Missing handlers, invalid JSON, invalid result content and handler failures reject; no tool call is silently answered. Each completed result is appended immediately, so retrying a failed batch does not repeat completed handlers. Calls with server results are excluded. Advertise the same definitions with `run({tools: registry.map(entry => entry.tool)})`, execute the pending handlers, then explicitly call `run` again to send the results in standard history. Execution and continuation are separate application decisions. An interrupt requires explicit `resume` responses and is never automatically resolved by a tool result.

Nested client tools use a standard interrupt with reason `agently.client_tool`, a `toolCallId`, and `metadata.agently = {version: '1', kind: 'client-tool'}`. Call `executeClientToolInterrupts(registry)` to explicitly execute only that profile and obtain per-interrupt resume responses containing `{content, error?}`. Combine any ordinary approval/input responses yourself and call `resume` explicitly. Unknown reasons, versions and workflow kinds are left unanswered. The helper preserves multipart results, does not fabricate an internal delegation result, and reuses completed handler outputs after a transport failure. Native turn-scoped tool IDs are opaque protocol aliases; applications must return the ID they received.

Upstream deliberately excludes `activity` messages from outgoing run inputs while preserving them in consumer history. Other roles, multipart media sources, metadata and opaque reasoning values round trip through upstream. `lastPostedInput` is a defensive copy of the original standard POST input; it does not itself prove server acceptance. `reconnectFull(subscriber?)` restores that original messages/state and re-POSTs the identical input, allowing a durable server to replay its full journal without duplicate deltas. It does not consume an arbitrary middle-of-run cursor. A physical-socket-drop test checks identical bodies and replaced partial history. The transport boundary suppresses only the pinned upstream reader-cancel teardown rejection; read failures still reject the run. Replay, background notifications and durable Agently resume require server support; consumers should check the connected server's capabilities before exposing them.

## Current backend and migration gate

The connected Agently backend has advanced beyond the original phase-one
adapter: it uses the pinned AG-UI 1.0 / `@ag-ui/client` 1.0.1 contract and a
Datly 1.0 durable journal, with scoped tool handoff, detached-run attribution,
goal/state and workspace/datasource/lookup/feed commands, and run resource
commands. This does not establish full goal/conformance/backend parity.
Approval/graph final integration remains in progress, and interactive MCP Apps
automatic app-scope resolution is still an architecture decision. See the
current plan and operation matrix for the authoritative backend status.

## Shell verification

Web/iOS/Android applications retain their existing presentation and workspace controls. Final cutover acceptance requires lossless history/state, tool and interrupt round trips, cancellation/recovery and actual shell parity checks. See the operation inventory in `ag-ui.md`; those acceptance gates remain distinct from SDK unit tests.

## Verification

`src/agui.test.ts` runs the actual upstream `HttpAgent` against a local HTTP SSE fixture. It splits frames across byte boundaries, including UTF-8 text, and verifies all 31 event types across meaningful ordered sequences: text/tool reduction and client handler/result continuation, all seven roles and five content parts with URL/data/file sources, every RFC 6902 operation and escaped pointers, reasoning/chunks/encrypted values, activity patches, subagent attribution and failures, raw/custom callbacks, success/interrupt/cancel/error outcomes, explicit interrupt resume and recovery. Expected state and message outcomes follow the pinned upstream reducer, including activity input exclusion. Capability discovery and execution envelopes remain opt-in.

Numeric JSON values follow the pinned upstream JavaScript Number representation. `validateArguments` and the optional `resume` response validator are synchronous hooks that must throw to reject invalid input; they do not replace authoritative server validation. Cached handler outputs live in this client instance, not a durable external-effect transaction.
