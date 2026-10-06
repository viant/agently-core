# AG-UI streaming phase (historical)

This document records the original phase-one implementation and is retained as
historical context. Its capability list and gaps below describe that phase and
are not the authoritative current backend inventory. See
[`ag-ui-complete-plan.md`](ag-ui-complete-plan.md) and
[`ag-ui-operation-matrix.md`](ag-ui-operation-matrix.md) for current status.

Implementation of the first streaming slice of [`ag-ui.md`](../ag-ui.md).
The target remains a single public interaction protocol. Existing routes remain
temporarily available while feature parity is incomplete; this is not a claim
that the final migration is complete.

The agreed delivery has two milestones: first this adapter/interoperability
proof, then the full proposal's client and feature migration with retirement of
the old public protocol. Internal event translation may remain after cutover.

## Decisions and pins

- User decision: Agently owns persisted history in this phase. Only the last
  user message in a chat request is submitted. Earlier messages are the client
  view and are not imported or reconciled into storage.
- Protocol 1.0, upstream TypeScript core/client 1.0.1. The immutable schema and
  provenance are in [`protocol/agui`](../protocol/agui/SCHEMA.md).
- External UI: published CopilotKit 1.76.0 `CopilotChat` and upstream `HttpAgent`,
  hosted in [`examples/ag-ui-shell`](../examples/ag-ui-shell/README.md).
  This is a local integration harness using upstream components, not AG-UI Dojo
  or a replacement production Agently UI.
- Internal agent services, persistence and event bus remain unchanged. The
  adapter translates at the HTTP boundary. Substantial runtime changes require
  consultation before implementation.

## HTTP contract

`POST /v1/ag-ui/run` receives schema-valid `RunAgentInput` and returns SSE with
JSON `data` frames. Authentication uses the same middleware as the other SDK
routes. No identity is accepted through forwarded properties. An existing
thread must belong to the effective user before its events can be subscribed.

`threadId` is the persisted conversation ID. The newest user message ID is the
existing internal turn ID. The external `runId` is preserved independently in
AG-UI lifecycle events. Subscribe and emit `RUN_STARTED` before invoking the
agent. Filter the stream to both thread and turn. Queued queries remain open
until their own terminal event; preset responses are completed without waiting
for a nonexistent normal turn lifecycle.

Text snapshots reconcile with previously emitted deltas without duplication.
Tool argument completion and tool execution/result completion are distinct.
Persisted tool payload references are resolved by the backend before standard
tool results are emitted. Cancellation maps to the `cancelled` outcome. Each
observed run ends once; bus closure/overflow becomes `RUN_ERROR` rather than
silently claiming success. SSE comments keep idle/queued streams alive.

Disconnect detaches observation, preserving existing backend execution
semantics. It is not a targeted cancel operation. There is no durable replay or
automatic retry; duplicate active submissions and already persisted newest
message IDs are rejected. A new run ID is required by callers for each run.
Replay/idempotency across restarts and detached-run reattachment remain later
work. Text replacement that cannot be represented as an append fails explicitly
rather than leaving a generic client with a silently incorrect transcript.

## Agently extension version 1

Selectors are optional; a standard client can omit the extension and use backend
defaults. This is the only initial client extension envelope:

```json
{"forwardedProps":{"agently":{"version":"1","operation":"chat","payload":{"agentId":"simple","model":"local_mock"}}}}
```

`operation: "capabilities"` runs deterministic discovery without invoking a
model or creating a conversation. Empty messages are valid for discovery.
It returns `CUSTOM` named `agently.capabilities` with
`{"version":"1","capabilities":{"custom":{"agently":{...}}}}` between
normal run boundaries. The profile advertises `chat` and `capabilities`, agent
and model selection, `history: "server"`, `disconnect: "detach"`, and
`replay: false`. Unknown operations/versions are rejected, not ignored.

Typed `agently.queue` and `agently.progress` events supplement ordinary run,
text and tool events. Queue sequence values are decimal strings to avoid losing
precision in JavaScript. They are not replay cursors. Extension request IDs are
correlation metadata only, not durable idempotency keys.

## Scope and migration gate

| Surface | This phase | Remaining work |
| --- | --- | --- |
| Go HTTP producer | Chat, text, backend tools, queued/preset lifecycle, discovery | Client tools, interrupt continuation, targeted cancel, replay |
| TypeScript consumer | Upstream protocol store, POST streaming, lossless standard message/state reduction, extension helpers | Wire existing Agently UI projections and all feature flows |
| External UI | Independent CopilotKit shell, selectors and capabilities | Production auth/deployment and full extension rendering |
| Swift/Kotlin | Existing clients retained | Same protocol consumer and extension migration |
| Management/workspace/files | Existing routes retained | Operation inventory, schemas, dispatch, subscriptions, transfer binding |

The producer explicitly rejects nonempty client tools, context/shared state,
resume/parent-run inputs and non-text new user messages in this phase. It does
not claim interrupt, multimodal, shared-state or full Agently feature parity.
The TypeScript consumer can use supported standard features of other external
AG-UI agents; backend capability limits are separate from consumer behavior.

Cutover/removal gate: finish the proposal's operation parity matrix and shared
fixtures for web/iOS/Android; demonstrate all supported clients using only
AG-UI plus agreed extensions; then remove the superseded interaction routes.

## Verification

```sh
go test ./protocol/agui ./sdk
cd sdk/ts
npm ci
npm test
npm run typecheck
npm run build
```

Go tests cover lifecycle ordering, correlation, queued/preset returns, failures,
cancellation, authorization and payload hydration. TypeScript tests exercise
the official `HttpAgent` over HTTP, including fragmented UTF-8 SSE, standard
text/tools/state, schema fixtures, custom events and invalid event ordering.
The independent shell has its own build and browser smoke checks.

For assembled-backend checks use the sibling `agently-ag-ui` checkout's
`dev/ag-ui` fixture. It starts the actual Agently assembly with an isolated
workspace and a deterministic local model. It is separate from the shell's
JavaScript-only transport fixture, which by itself does not prove backend
interoperability.
