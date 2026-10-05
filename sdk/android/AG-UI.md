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

The additive `com.viant.agentlysdk.agui` package speaks the pinned AG-UI 1.0 schema over POST SSE. `AgUiClient` accepts an explicit endpoint URL through `EndpointConfig`; authentication, cookies and custom transports use its existing configuration hooks. Standard external agents do not depend on Agently endpoints. Existing native clients remain available until the complete web/iOS/Android feature-parity gate in the repository's `ag-ui.md` is satisfied.

```kotlin
val client = AgUiClient(EndpointConfig("https://agent.example/run"))
val input = AgUiRunInput.create(
    threadId = "thread-1", runId = UUID.randomUUID().toString(),
    messages = listOf(AgUiMessage.user("user-1", JsonPrimitive("Hello")))
)
var latest: AgUiSnapshot? = null
client.run(input).collect { update ->
    latest = update.snapshot
    // Project snapshot.messages/state into UI; inspect event for lifecycle.
}
val next = latest!!.nextInput(UUID.randomUUID().toString())
```

JSON trees retain numeric tokens and every opaque field. Messages preserve all roles, content parts (text/image/audio/video/document and data/URL/provider-file sources), metadata, tool arguments/results, encrypted continuation values and unknown fields. The protocol store remains authoritative; UI rows must never reconstruct history. Known events validate against the bundled pinned schema while tolerating unknown fields. Unknown event types remain observable through `sourceEvent`/`event`.

All 31 event variants are handled: per-subagent text/tool/reasoning chunk lanes, lifecycle and ownership verification, full message reduction, shared-state and activity RFC 6902 patches, snapshots/history reconciliation, encrypted values and subagent lifecycles. Raw/custom/step/span/subagent events remain observable without fabricating chat messages. Terminal snapshots retain the entire outcome, interrupts, usage and result. Updates contain an immutable snapshot after each normalized event.

`nextInput` retains the full store while omitting activity messages from outgoing history by default, matching upstream HttpAgent. Set `includeActivityMessages = true` for schema-permitted activity inputs. It retains prior tools/context/forwarded properties unless overridden, replaces run identity and current history/state, and never copies old resume answers into a new run. Null shared-state snapshots remain in the store; their optional input state field is omitted because the schema forbids input null.

`AgUiClientTool(definition, validateArguments, execute)` registers a standard Tool definition and an authorized suspend handler. Advertise those same definitions in `AgUiRunInput.tools`. `AgUiClientToolDispatcher.executeClientTools(snapshot, tools)` executes only pending calls from a successful terminal run and returns typed messages for `snapshot.toolResultsInput`. It preserves ordered media, error text and metadata, excludes already answered calls, validates arguments/results, and retains successful outputs across retries. A changed function/arguments value for an already completed identity fails closed. Handler failures leave that call unanswered; handlers own external-effect idempotence. `clearCompleted()` releases outputs after durable continuation acceptance, never just after disconnect.

`executeClientToolInterrupts(snapshot, tools)` explicitly handles only reason `agently.client_tool`, version `1`, kind `client-tool`. It returns resume entries with `{content,error?}` and preserves result metadata. Approval, other human input and unknown profile versions remain for the caller; combine their explicit responses before `nextInput`.

`nextInput(..., resume, responseValidator)` requires exactly one response per open interrupt, rejecting unknown/duplicate IDs, missing payloads and invalid advertised schemas. Expired interrupts require cancellation. The built-in evaluator supports the pinned vocabulary plus local `$defs`, `anyOf`, type unions, string/array bounds and numeric bounds. Unsupported keywords/references fail closed. Inject `(JsonElement, JsonElement) -> Unit` for broader application JSON Schema validation; registrations accept the same hook. Server validation and authorization remain authoritative.

Cancelling collection aborts only HTTP. `AgUiClient.cancelRun(threadId, targetRunId, commandRunId): Flow<AgUiUpdate>` submits an independent version `1` `run.cancel` command on the configured endpoint while the original stream remains open. Its acknowledgement is a command result, not proof inferred from disconnect. `AgentlyAgUiExtensions.cancelInput` constructs the typed request; `command(operation, requestId, payload, existing)` builds other explicit resource envelopes. Enable extensions only when advertised by the server.

`AgentlyAgUiExtensions.forwardedProps` builds version `1` execution/discovery envelopes. Supported `CUSTOM agently.capabilities` values `{version:"1", capabilities:...}` validate and appear in snapshots; discovery is an explicit run that generic agents need not support. Other extension names/versions remain observable. Protocol `RUN_ERROR` is a terminal event and completes the stream normally; consumers must inspect it. Transport, JSON/schema, sequence and patch errors fail the stream. No native-route fallback occurs.

After a partial transport drop, reconnect by re-POSTing the identical accepted input from `snapshot.input`. Each `run(input)` creates a fresh verifier/store, consumes the full durable journal from RUN_STARTED and replaces state through authoritative snapshots. Do not reconnect with `nextInput` or append replay deltas to the previous projection. The disconnect/full-replay tests verify identical POST bodies, multimedia history and opaque continuations without duplicated text. Generic middle-of-run Last-Event-ID segments require checkpointed open message/tool/reasoning/step/subagent trackers; this API does not claim that checkpoint resume support.

## Verification and fixture synchronization

Run `./gradlew testDebugUnitTest` with JDK 17 and an Android SDK compatible with the existing project configuration. New tests include an actual local MockWebServer POST SSE connection with seven-byte throttling, UTF-8/CR/LF splits, exact header/input verification, errors and missing terminals. The shared conformance fixture covers all 31 raw variants, with expected 45 normalized events and 10 complete messages produced by actual upstream `@ag-ui/client@1.0.1`; every raw event was validated by official core 1.0.1. Tests compare full messages and their order, all six RFC 6902 operations, large numeric tokens, owner/lifecycle failures, interrupts and client tool continuations.

Canonical fixture: `protocol/agui/testdata/conformance.json`. Native copy: `src/test/resources/agui/conformance.json`. Bundled schema: `src/main/resources/agui/schema-1.0.json`. `pinnedMirrorsMatchCanonicalRepositoryFixtures` compares both copies byte-for-byte with canonical repository sources. Copy updated canonical files into both native SDK resources before rerunning tests; do not regenerate separate expected projections.

Set `AGENTLY_AGUI_LIVE_URL` to a running local assembled endpoint to test capability discovery and standard streamed chat through OkHttp with fresh threads. Device/UI integration, authenticated deployment and every Agently feature flow still need the broader migration parity checks; consumer conformance alone does not establish those flows.

The action tests also execute authorized handlers and submit multipart results through actual OkHttp/MockWebServer POST SSE, exercise cached outputs and strict human-response coverage, and submit cancellation while a separate original HTTP call remains open. The standard tool roundtrip has no Agently forwarded properties. SDK protocol tests do not establish production Android UI or deployed authentication parity.

The default AG-UI OkHttp fallback retains a per-client session cookie jar with normal domain/path/secure/expiry rules. Injected clients keep their own cookie policy; an explicit application Cookie header takes precedence. The opt-in live handler/continuation/replay test supplies a generated test-only anonymous header because the loopback HTTP backend emits a Secure cookie. This proves configured same-scope continuation, not deployed HTTPS authentication. Secure-cookie policy tests do not establish an end-to-end TLS deployment.

The native suites also consume all 35 cases from `protocol/agui/testdata/reducer-semantics.json`, comparing complete ordered messages, shared state and normalized events for accepted sequences and requiring every rejected sequence to fail. A byte-equality guard protects the native mirror. The cases include owner/history retention, reopens, snapshot reconciliation, activity replacement, independent reasoning lifecycles, opaque empty IDs and separator-safe steps. The consumer accepts one corrective RUN_ERROR after RUN_FINISHED to match pinned HttpAgent1.0.1; any event after RUN_ERROR is rejected. This does not relax the Agently producer's single-terminal journal policy. Wait for stream collection to complete before dispatching tools or constructing a continuation from its final snapshot.

`integer-semantics.json` adds 66 shared raw-JSON cases for timestamp, token usage and execution-capability integers. Integral decimal/exponent literals such as `1.0` and `1e3` are accepted; fractional/out-of-safe-range values and negative nonnegative counters are rejected exactly. Opaque unbounded numeric tokens remain intact. Raw JSON strings in the fixture prevent fixture generation from erasing the lexical forms, and native mirrors are checked byte-for-byte.

## Native conversation coordinator

AgentlyClient uses the SDK-owned AG-UI coordinator without a protocol or transport-mode parameter.
The default is `AG_UI`; `LEGACY` remains explicit. The coordinator routes query, canonical transcript/live-state
bootstrap, tracking, explicit cancellation, interrupt resume and approval decisions
through the SDK-owned coordinator without automatic legacy fallback. It reuses the
configured `/v1/ag-ui/run` BFF route, headers and injected OkHttp session/cookie policy.

`ConversationStreamSnapshot` retains renderer fields and adds the canonical DTO,
original canonical JSON, separately authorized host activities, unavailable activity
IDs, run references and exact client/native message aliases. Protocol message bodies
come from `AgUiStore`; the native adapter adds no protocol reducer or text matching.
Scoped `native-and-application` notifications reconcile other authorized clients.

Call `resetConversationTransport()` before changing authenticated identity.
Reset/navigation detach transport without cancelling execution. Cancellation uses
an explicit durable command. Network loss does not synthesize turn completion or
failure; uncertain mutations are never automatically repeated. A fresh reconcile
waits behind an older read, then performs another command. The assembly retains its
attachments, native Forge workspace, report/feed consumers and theme/layout renderers.
Unit tests/APK assembly do not establish Steward simulator parity: actual authenticated
starter workflows, interactions, styling and restart checks remain required.
