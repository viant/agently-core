# Native AG-UI consumer

## Default conversation transport

AG-UI is the outward SDK conversation interaction path. Protocol selectors and legacy conversation transports are removed. The configured BFF authentication, cookies, headers and injected networking remain in use. Supporting workspace, report, layout and application APIs remain available. Dedicated readConversationHistory/readApplicationState helpers provide authorized native read-only history without a query or stream fallback; SDK checks do not establish full product UI parity.


The backend has advanced beyond the original phase-one adapter: the current
milestone uses the pinned AG-UI 1.0/1.0.1 contract, a Datly 1.0 durable
journal, scoped client-tool handoff, detached-run attribution, goal/state and
workspace/data/lookup/feed commands, plus run resource commands. This is not a
complete goal/conformance/backend parity claim. Approval/graph final integration
and interactive MCP Apps app-scope architecture remain open; production shell
migration is later. See the core repository's current plan and operation matrix.

The additive `AgUiClient` speaks the pinned AG-UI 1.0 schema over POST SSE. It accepts an explicit URL and configured URLSession/headers, so standard external agents do not depend on Agently endpoints. The existing native client remains available until the complete web/iOS/Android feature-parity gate in the repository's `ag-ui.md` is satisfied.

```swift
let client = AgUiClient(endpoint: URL(string: "https://agent.example/run")!)
let input = try AgUiRunInput(
    threadId: "thread-1", runId: UUID().uuidString,
    messages: [.user(id: "user-1", content: .string("Hello"))]
)
var latest: AgUiSnapshot?
for try await update in client.run(input) {
    latest = update.snapshot
    // Project update.snapshot.messages/state into UI; inspect update.event for lifecycle.
}
let next = try latest!.nextInput(runId: UUID().uuidString)
```

`AgUiValue` retains arbitrary JSON and exact numeric tokens. Messages preserve all roles, content parts (text/image/audio/video/document and data/URL/provider-file sources), metadata, tool arguments/results, encrypted continuation values and unknown fields. The store owns the protocol representation; UI rows must never reconstruct history. Unknown event types remain observable through `sourceEvent`/`event`; known events validate against the bundled pinned schema, tolerating unknown fields without dropping them.

All 31 event variants are handled: text/tool/reasoning chunks normalize by per-subagent lanes; lifecycle verification checks message/tool/reasoning/step/subagent boundaries and ownership; the reducer applies text, tool results beside their assistant owner, shared state, activity patches/snapshots, history reconciliation and encrypted values. Terminal snapshots preserve outcomes, interrupts, usage and result fields through `terminalEvent`. Snapshot updates occur after each normalized event. Raw/custom events and step/span/subagent events remain directly observable without fabricating chat messages.

`nextInput` preserves the full conversation in the snapshot while omitting activity messages from outgoing history by default, matching upstream HttpAgent. Set `includeActivityMessages: true` for schema-permitted activity inputs. It retains prior tools/context/forwarded properties unless overridden, replaces run identity and current history/state, and never carries old resume answers into a new run. A null shared-state snapshot remains in the store; because optional input state cannot be null, its next input omits that field.

`AgUiClientTool(definition:validateArguments:execute:)` registers a standard Tool definition and an authorized async handler. Advertise those same definitions through `AgUiRunInput.tools`. `AgUiClientToolDispatcher.executeClientTools(snapshot:tools:)` executes only pending calls from a successful terminal run and returns typed messages for `snapshot.toolResultsInput(runId:results:)`. It preserves ordered media, error text and metadata, excludes already answered calls, validates arguments and result messages, and retains successful outputs across retries. A changed function/arguments value for an already completed identity fails closed. Handler failures leave that call unanswered; handlers own external-effect idempotence. `clearCompleted()` releases retained outputs after durable continuation acceptance, never just after a disconnect.

`executeClientToolInterrupts(snapshot:tools:)` explicitly handles only `reason: "agently.client_tool"` with version `1`, kind `client-tool`. It returns resume entries with `{content,error?}` and preserves result metadata. Approval, other human input and unknown profile versions remain for the caller; combine their explicit responses before `nextInput`.

`nextInput(..., resume:, responseValidator:)` requires exactly one response for every open interrupt, rejecting unknown/duplicate IDs, missing payloads and invalid advertised response schemas; expired interrupts require cancellation. The built-in schema evaluator supports the pinned vocabulary plus local `$defs`, `anyOf`, type unions, string/array bounds and numeric bounds. It rejects unsupported keywords/references instead of silently accepting them. Inject `AgUiResponseValidator` for a broader application JSON Schema implementation; client-tool registrations accept the same validator hook. Server validation and authorization remain authoritative.

Cancelling the consuming task aborts only HTTP. `AgUiClient.cancelRun(threadId:targetRunId:commandRunId:) throws -> AsyncThrowingStream<AgUiUpdate, Error>` submits an independent version `1` `run.cancel` command on the configured endpoint while the original stream may remain open. Its acknowledgement is a command result, not proof inferred from disconnect. `AgentlyAgUiExtensions.cancelInput` constructs the typed request, and `command(operation:requestId:payload:existing:)` builds other explicit resource envelopes. Enable server extensions only when advertised by the connected server.

`AgentlyAgUiExtensions.forwardedProps` builds the version `1` execution/discovery envelope. A supported `CUSTOM agently.capabilities` value `{version:"1", capabilities:...}` is validated and retained in snapshots. Discovery remains an explicit run; external agents need not implement it. Other extension versions and names stay observable. Protocol `RUN_ERROR` is a terminal event and completes the stream normally; consumers must inspect it. Transport, JSON/schema, sequence and patch errors fail the stream. No native-route fallback occurs.

After a partial transport drop, reconnect by re-POSTing the identical accepted input from `snapshot.input`. Each `run(input)` creates a fresh verifier/store, consumes the full durable journal from RUN_STARTED and replaces state through authoritative snapshots. Do not reconnect with `nextInput` or append replay deltas to the previous projection. The disconnect/full-replay tests verify identical POST bodies, multimedia history and opaque continuations without duplicated text. Generic middle-of-run Last-Event-ID segments require checkpointed open message/tool/reasoning/step/subagent trackers; this API does not claim that checkpoint resume support.

## Verification and fixture synchronization

`swift test` runs the native reducers/schema/lifecycle/continuation and URLSession transport tests. The common fixture covers all 31 raw event variants; expected 45 normalized events and 10 complete messages were produced by actual upstream `@ag-ui/client@1.0.1`, with every raw event checked by `@ag-ui/core@1.0.1` EventSchema. Tests compare both full messages and their order, and exercise arbitrary numeric tokens, all six RFC 6902 operations, byte/UTF-8/CR/LF splits, multiline SSE, ownership failures and terminal outcomes.

The canonical fixture is `protocol/agui/testdata/conformance.json`; the native copy is `Tests/AgentlySDKTests/Fixtures/agui-conformance.json`. The bundled schema is `Sources/AgentlySDK/Resources/AGUI/schema-1.0.json`. `testPinnedMirrorsMatchCanonicalRepositoryFixtures` fails if either differs byte-for-byte from the canonical repository sources. When the canonical fixture or schema changes, copy those files into both native SDK resource locations before rerunning their tests; do not regenerate separate expected projections.

Set `AGENTLY_AGUI_LIVE_URL` to a running local assembled endpoint to run actual capability discovery and standard streamed chat through URLSession. This live test uses fresh threads and no legacy endpoints. Device/UI integration, authenticated deployment and all Agently feature flows still need the broader migration parity checks; consumer conformance alone does not establish those flows.

The action tests additionally execute authorized handlers and submit their multipart results through URLSession, verify cached output reuse and human-response coverage, and send cancellation on a separate POST while the original transport remains open. The standard tool roundtrip sends no Agently forwarded properties. These are SDK protocol tests, not production mobile UI or deployed authentication parity.

The opt-in live handler/continuation/replay test uses a generated explicit fixture anonymous-cookie header to keep the same principal on loopback HTTP. The backend emits a Secure cookie, whose policy remains intact. This validates configured credential propagation and actual native turn continuation; it does not prove default HTTPS cookie-jar or deployed authentication interoperability.

The native suites also consume all 35 cases from `protocol/agui/testdata/reducer-semantics.json`, comparing complete ordered messages, shared state and normalized events for accepted sequences and requiring every rejected sequence to fail. A byte-equality guard protects the native mirror. The cases include owner/history retention, reopens, snapshot reconciliation, activity replacement, independent reasoning lifecycles, opaque empty IDs and separator-safe steps. The consumer accepts one corrective RUN_ERROR after RUN_FINISHED to match pinned HttpAgent1.0.1; any event after RUN_ERROR is rejected. This does not relax the Agently producer's single-terminal journal policy. Wait for stream collection to complete before dispatching tools or constructing a continuation from its final snapshot.

`integer-semantics.json` adds 66 shared raw-JSON cases for timestamp, token usage and execution-capability integers. Integral decimal/exponent literals such as `1.0` and `1e3` are accepted; fractional/out-of-safe-range values and negative nonnegative counters are rejected exactly. Opaque unbounded numeric tokens remain intact. Raw JSON strings in the fixture prevent fixture generation from erasing the lexical forms, and native mirrors are checked byte-for-byte.

## Native conversation integration

AgentlyClient uses the SDK-owned AG-UI coordinator without a protocol or transport-mode parameter.

Canonical `conversation.bootstrap` results retain the complete native transcript, feeds, protocol run references, projection quality and state. Authorized host activities remain separate from model-visible messages. `AgUiPresentationProjector` reads the standard reducer's complete graph into existing presentation DTOs; it does not accumulate a second protocol history or deduplicate by text. Admission uses the owned protocol run/native turn identity. Navigation detaches views while submitted work continues. Transport drops replay the same immutable input once; restored runs attach through `run.attach` without chat resubmission. Interrupt boundaries remain waiting; committed approval/elicitation decisions reconcile fresh canonical state and discover successor runs. A scoped native-and-application availability subscription observes other clients' work.

`AgUiConversationTransportTests` covers bootstrap preservation, shared BFF authentication, exact admission ownership, replay identity, attach without resubmission, view detach, interrupted completion, fresh reconciliation and account fences. These tests and an app build establish integration readiness; actual simulator starter, workspace, report, feed and theme acceptance must still be verified against real services.
