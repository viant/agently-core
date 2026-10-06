# Agently Core and AG-UI

AG-UI (Agent–User Interaction Protocol) is the target public interaction protocol for Agently Core and its web, iOS, and Android clients. Standard AG-UI messages and events cover shared functionality. Versioned Agently extensions cover every additional feature.

## Architecture decision

There will be one public interaction contract: **AG-UI with Agently extensions**. The existing query, conversation stream, and feature-specific client APIs must migrate into that contract. Maintaining a native Agently protocol alongside AG-UI is not the target architecture.

- Agently exposes standard AG-UI operations and its extension capabilities.
- TypeScript, Swift, and Kotlin SDKs consume that same contract and drive their existing UI presentation models.
- Those SDKs can connect to external AG-UI agents using the standard subset and any mutually supported extensions.
- Agently connected to its own clients must retain all current functionality. Full parity is an acceptance requirement, not an optional later outcome.
- Other AG-UI agents need only implement the standard subset to support basic interaction. Agently-specific UI features require the relevant advertised extensions.

Internal Go services, persistence models, and event buses can remain implementation details behind the protocol boundary. Their reuse does not require a second public protocol.

This document is a migration design, not an implemented protocol or a complete extension specification. Pin the AG-UI schema/SDK version before implementation, and define extension schemas against that version.

## Current contract

| Concern | Agently Core today | AG-UI expectation | Gap |
| --- | --- | --- | --- |
| Start a run | `POST /v1/agent/query` accepts `agent.QueryInput` and returns JSON after `client.Query` finishes. | A client such as `HttpAgent` POSTs `RunAgentInput` and reads events from that response. | Add a run endpoint that streams its response. |
| Receive events | `GET /v1/stream?conversationId=...` subscribes to conversation-scoped SSE. | A run produces its own ordered event stream. | Subscribe before starting execution, then filter and close at the run boundary. |
| Identity | Events carry `conversationId`, `turnId`, message IDs, and tool-call IDs. | `threadId` and `runId` identify the run; message and tool-call IDs correlate events. | Define stable ID mappings, including the first turn of a new conversation. |
| Lifecycle | `turn_started`, `turn_completed`, `turn_failed`, and `turn_canceled`. | `RUN_STARTED` followed by exactly one `RUN_FINISHED` or `RUN_ERROR`. | Translate terminal states and guarantee ordering. |
| Text | `text_delta` plus persisted `assistant` message events. | `TEXT_MESSAGE_START`, `TEXT_MESSAGE_CONTENT`, `TEXT_MESSAGE_END` (or compatible chunk events). | Track each message boundary and avoid duplicating final content after deltas. |
| Tools | Tool-call start, argument deltas, completion, waiting, failure, and cancellation events. | `TOOL_CALL_START`, `TOOL_CALL_ARGS`, `TOOL_CALL_END`, and `TOOL_CALL_RESULT`. | Distinguish completed arguments from completed execution and serialize results. |
| State and history | Agently persists a canonical transcript and has an SDK reducer. | Optional `MESSAGES_SNAPSHOT`, `STATE_SNAPSHOT`, and RFC 6902 `STATE_DELTA`. | Choose a state projection and history ownership model before claiming state synchronization. |
| Human input | Separate elicitation and tool-approval APIs. | Interrupt-aware runs can finish with an interrupt outcome and resume through a later `RunAgentInput`. | Map pending requests and resumes if interrupt interoperability is required. |

Relevant code: [`sdk/handler.go`](sdk/handler.go), [`sdk/handler_common.go`](sdk/handler_common.go), [`sdk/handler_messages.go`](sdk/handler_messages.go), [`service/agent/query.go`](service/agent/query.go), [`runtime/streaming/event.go`](runtime/streaming/event.go), [`runtime/streaming/memory.go`](runtime/streaming/memory.go), and [`sdk/canonical_reducer.go`](sdk/canonical_reducer.go).

## Contract audit: individual data and behavior gaps

Reviewed 2026-10-01 against the working tree based on commit `4a7551e89307b4e0a76245611e083f26bfb8a57c`. This is source inspection, not an executed interoperability test. Shell download of upstream sources failed DNS resolution; the upstream files below were retrieved through the web tool. No immutable upstream commit was established, so these `main` links must be pinned before implementation.

### Actual upstream contract

The review used the [machine-readable 1.0 schema](https://raw.githubusercontent.com/ag-ui-protocol/ag-ui/main/spec/1.0/schema.json), [generated TypeScript types](https://raw.githubusercontent.com/ag-ui-protocol/ag-ui/main/sdks/typescript/packages/core/src/generated/types.ts), [schema maintenance notes](https://raw.githubusercontent.com/ag-ui-protocol/ag-ui/main/spec/README.md), [HTTP client](https://raw.githubusercontent.com/ag-ui-protocol/ag-ui/main/sdks/typescript/packages/client/src/agent/http.ts), [agent lifecycle](https://raw.githubusercontent.com/ag-ui-protocol/ag-ui/main/sdks/typescript/packages/client/src/agent/agent.ts), [event verifier](https://raw.githubusercontent.com/ag-ui-protocol/ag-ui/main/sdks/typescript/packages/client/src/verify/verify.ts), and [event reducer](https://raw.githubusercontent.com/ag-ui-protocol/ag-ui/main/sdks/typescript/packages/client/src/apply/default.ts). The inspected [core package manifest](https://raw.githubusercontent.com/ag-ui-protocol/ag-ui/main/sdks/typescript/packages/core/package.json) reports package version `1.0.1` and protocol version `1.0`; this does not establish which package a particular external UI has installed.

Concrete corrections to the earlier outline:

- The schema requires `threadId`, `runId`, and `messages` on input; the other request properties are optional. Do not confuse SDK defaults with required wire fields.
- Exact schema objects reject undeclared properties. Place extension data at defined extension points, not beside standard fields. Optional does not generally mean nullable.
- The inspected outcome union includes `cancelled`; cancellation need not be misreported as success or execution failure.
- AG-UI already defines `AgentCapabilities`, including a `custom` map. Use this instead of borrowing an A2A AgentCard for UI capability data. Omitted capability means unknown.
- The schema lists 31 event variants and explicitly requires consumers to handle its event vocabulary. A producer need not emit every event on each run. Our SDKs cannot claim full 1.0 consumer support while implementing only text and tools.
- Schema validation is insufficient for sequence correctness. The reference client separately normalizes chunks, verifies lifecycle/ownership, and applies events. Its HTTP sender enforces input and strips unknown material before transmission.

These are version-specific findings. Overview pages previously consulted differ on empty deltas, required request fields, outcome variants, content types, and parent-run descriptions. Treat the chosen schema plus matching behavior as the baseline; record divergences instead of combining incompatible versions.

### Request field mapping

The current [`QueryInput`](service/agent/query.go) has 35 JSON-exposed fields. The following covers each of them; destinations marked extension are proposed Agently profile decisions.

| Agently input field(s) | Proposed destination / concrete issue |
| --- | --- |
| `conversationId` | Standard thread identity. Preallocate and authorize it before stream subscription; do not switch IDs after the client has started the run. |
| `messageId` | User message identity, currently also used as internal turn/run identity. Separate these: AG-UI run identity and the latest user message ID cannot be assumed identical, especially on resume. |
| `query`, `displayQuery` | Map the user-visible text into message history; preserve internal expansion separately. Serializing the expanded prompt as the user's message can leak implementation content and change later history. |
| `transcript` | Translate typed history rather than passing Agently's persisted turn/page hierarchy. Define ownership, deduplication by ID, and reconciliation with supplied messages. Do not append the whole client history as new user input. |
| `context` | Agently accepts a map; AG-UI context entries have description/value strings. Preserve typed UI context in a separately defined extension or shared-state schema instead of pretending the shapes match. |
| `tools` | Currently a backend tool-name allow-list. Keep it as an extension selector; AG-UI supplied tools represent client-provided definitions. They require separate dispatch and continuation. |
| `attachments`, `resourceURIs` | Translate supported media into content parts and preserve resource references through the artifact profile. A local AFS URI is not automatically fetchable by another UI. |
| `agentId`, `agent` | Define routing and authorized configuration operations in the extension. Do not accept unrestricted runtime configuration merely because an input can carry arbitrary forwarded data. |
| `userId` | Resolve the effective principal from authenticated transport. A forwarded field must not become identity authority. |
| `parentConversationId` | Preserve explicit conversation lineage in the extension. It is not an AG-UI parent run ID. |
| `conversationTitle` | Conversation metadata command/state; do not create a synthetic chat message containing the title. |
| `requestTime` | Define its ownership and timestamp encoding; do not use it as run identity or ordering authority. |
| `maxResponseSize`, `maxDocuments`, `includeFile`, `embeddingModel` | Retrieval/output controls in a typed execution extension. Preserve absent/default behavior. |
| `model`, `modelSource`, `modelPreferences`, `reasoningEffort`, `parallelToolCalls` | Execution profile fields. An external agent must explicitly support these controls; standard input does not guarantee their meaning. |
| `toolBundles`, `autoSelectTools`, `toolCallExposure` | Backend tool-policy extension. Distinguish selection, exposure to the model, and authorization. |
| `runtime` | Internal execution context needs a reviewed public projection; forwarding the entire object is not a portable contract. |
| `elicitationMode` | Reconcile synchronous waiting versus terminal interrupt/resume. A new run must route back to the correct outstanding request. |
| `autoSummarize`, `allowedChains`, `disableChains` | Runtime-policy extension; copying text or history does not preserve these policies. |
| `templateId`, `intentProfileId`, `scheduleId` | Versioned resource references with backend capability checks. Referenced configuration must exist and be authorized. |

AG-UI fields without a direct current QueryInput equivalent need explicit work: external `runId`, protocol declaration, `parentRunId`, shared `state`, and `resume`. `forwardedProps` provides a container, not implementations for these operations. A resume containing only tool results or interrupt answers must not be forced through the normal new-user-query path.

### Event and history issues

The current [`streaming.Event`](runtime/streaming/event.go) has 85 JSON fields and 41 distinct declared event values. These counts measure the inspected struct/constants, not every dynamic event or every API payload in the repository.

| Data point | Concrete migration requirement |
| --- | --- |
| `type` | Translate meaning and ordering, not just lowercase names to uppercase names. |
| `conversationId`, `streamId`, `turnId` | Maintain stream context and internal-to-external ID maps. Most content events do not carry the full run identity, and the inspected error shape lacks thread/run fields. |
| `messageId`, `assistantMessageId`, `userMessageId` | Establish one stable protocol message ID per semantic message and retain the internal aliases separately. |
| `parentMessageId` | Agently may use this for the starter user message. For a tool call, select its owning assistant message; do not copy the field solely because the name matches. |
| `content`, `contentMode`, `contentOffset` | Convert delta/snapshot/offset semantics. Appending an aggregate assistant snapshot after text deltas duplicates content. Replacing an already emitted prefix needs a defined reconciliation path. |
| `narration`, `narrationSource` | Separate runtime progress, assistant commentary, and reasoning. Agently can replace interim narration with the final message at the same ID; a naive append-only translation would concatenate them. |
| `toolCallId`, `toolMessageId`, `arguments`, `responsePayload` | Preserve call identity, argument text, and result message identity separately. Parsed argument maps cannot reproduce the original argument stream. Structured results require intentional serialization/media mapping. |
| Tool completion | Model argument completion and actual tool execution completion occur at different points. Emit call-end at argument completion, then result when execution settles. |
| Tool failure | A failed tool may be recoverable by the agent. Do not terminate the whole run for every tool error. The inspected result event and persisted tool message expose different error fields; define the live/history representation explicitly. |
| `status`, `error`, `statusReason` | Distinguish queued, waiting, canceled, failed, and completed. Use the selected standard outcome where it applies; keep queue/wait details in the extension. |
| `createdAt`, `startedAt`, `completedAt` | Agently uses time strings/objects; standard event timestamp is numeric. Use epoch milliseconds for SDK compatibility, retain interval timing under documented metadata, and omit absent optional values. |
| `eventSeq`, `queueSeq` | Neither is automatically an SSE resume cursor. Queue sequence currently derives from Unix nanoseconds; sending it as a JavaScript number can lose precision. Define opaque/string cursors, ordering scope, and replay retention. |
| `patch` | Current semantic maps and feed operations are not automatically RFC 6902 patches. Reuse requires a specified state root and full patch semantics. |
| `renderedContent` | Keep plain assistant text usable; attach versioned rich presentation metadata or publish a linked activity/state object. Generic UIs will not acquire a Forge renderer from the payload alone. |
| `pageId`, `iteration`, model-step data | These support Agently's execution presentation. Preserve them as typed extension metadata; synthetic page IDs must not replace message IDs. |
| Linked conversations | A child conversation ID is reusable across invocations. Derive a distinct invocation identity for subagent attribution, and choose separate child runs versus nested subagent events deliberately. |
| Usage | Current model/turn/conversation totals need a run-scoped projection with provider/model attribution. Do not sum repeated absolute snapshots or add reasoning/cache components twice. Embedding accounting needs an extension where the standard token record has no equivalent. |
| `skillName`, goal/planner/feed fields | Preserve typed extension resources/events, linked to the originating run, tool, and message where relevant. |
| History snapshots | Current canonical transcript is organized into turns/pages. Build protocol messages independently of rendering, including roles, tool arguments/results, metadata, media, and opaque continuation values. |

Specific source checks: [`service/reactor/service_stream.go`](service/reactor/service_stream.go) publishes planned calls with a turn parent; [`sdk/ts/src/chatStore/reducer.ts`](sdk/ts/src/chatStore/reducer.ts) accumulates text per page and does not accumulate tool-argument deltas; [`sdk/ts/src/feedPatch.ts`](sdk/ts/src/feedPatch.ts) implements a limited feed patcher, not full state-patch validation. Existing reducers cannot serve as lossless AG-UI protocol stores unchanged.

The reference reducer preserves some client-owned history during snapshot reconciliation and associates tool calls with assistant messages. Our parity tests must compare resulting protocol state, not just whether bubbles look similar. The client also treats chunk expansion and event verification as separate stages; all three of our SDKs need equivalent behavior.

### Lifecycle cases that change runtime design

1. Preset answers return before normal turn startup; queued requests return before execution. Neither `Query` return nor one selected bus event can universally define protocol completion.
2. Agently accepts steering during a live turn. Represent steering as an extension command targeting that run, with independent command completion; do not inject a second run-start into the active stream.
3. Standard interrupt continuation ends one run and begins another. Current elicitation resolution can wake a waiter, while tool approval can execute the tool directly and synthesize a result under the original turn. Preserve the logical turn while tracking multiple protocol runs and avoiding duplicate execution.
4. A client tool result normally arrives in subsequent input. Preserve argument and result history even when no new user message exists.
5. The reference verifier rejects ordinary events after run completion until another run starts. Feeds, workspace updates, and scheduled activity therefore need an explicit subscription/control lifecycle. A permanent conversation stream cannot simply be relabeled as a chat run.
6. The inspected standard client aborts its HTTP request. Agently currently detaches the query from request cancellation. Specify whether a disconnect detaches observation or cancels work, and provide a deterministic targeted cancel operation.

### Workspace UI: actual contracts and gaps

Workspace UI is an application runtime, with both desired state and acknowledged actions. It is much more than rendered chat content. The following comes from implemented types and handlers; the MCP Apps/Forge virtual-window document is marked planned and is not treated as completed functionality.

| Contract / individual fields | Current code and behavior | AG-UI migration decision needed |
| --- | --- | --- |
| Layout: `schemaVersion`, `layoutRevision`, `workspaceId`, `catalogRevisions`, `layout` | [`service/workspace/layout.go`](service/workspace/layout.go): workspace-wide navigation, applications, panels, menus, and catalog revisions. | Define workspace-scoped fetch/cache/invalidation outside any particular chat. Do not assign global layout to whichever thread happened to connect first. |
| Target selection: platform/form factor and request capability values | [`adapter/http/ui/handler.go`](adapter/http/ui/handler.go), SDK target-context helpers. | Preserve target-aware window selection. Standard agent capabilities describe the agent; they do not replace client renderer/device capabilities. |
| Window metadata, permission result, assigned resources | Handler loads workspace or built-in window metadata, applies permission logic, and merges selected dependencies. | Define a typed window resource and permission operation. Preserve transitive datasource/dialog/model/schema resolution and window-owned overrides. |
| Styles/themes: content-addressed URLs | [`sdk/ts/src/client.ts`](sdk/ts/src/client.ts) explicitly validates legacy style URL paths. | Replace legacy URL assumptions with the negotiated extension asset binding on web and native clients. |
| Workspace object: `version`, `revision`, `objectId`, `conversationId`, `kind` | [`protocol/ui/workspace/object.go`](protocol/ui/workspace/object.go). | Keep schema version distinct from object revision. Scope identity to conversation and object; establish stale-update rejection. |
| `origin`, `lastActivatedBy`: message/tool/turn/call IDs | Same descriptor; origin comes from execution context. | Keep trusted provenance. Client-submitted state must not rewrite the originating tool or turn. |
| `content.renderer`, `windowKey`, `windowId`, `parameters` | Renderer currently identifies a Forge window. Window definition and instance IDs are different. | Declare renderer/version support; preserve both IDs. Unknown renderer needs an explicit unavailable/fallback presentation. |
| `placement.preferred`, `allowed`, `initialWorkspaceMode` | Workspace/inline placement preferences and constraints. | Extension-owned placement semantics; these are not standard message roles or state-patch operations. |
| `lifecycle.state`, `restorePolicy`, `refreshPolicy`, timestamps | Descriptor starts opening and only adopts acknowledged lifecycle. | Keep desired state and client-applied state distinct; publishing a snapshot does not prove a window opened. |
| `stateRef`, `navigation`, `capabilities` | Descriptor carries a workspace state reference, labels/icons, and supported object actions. | Preserve resolvable state references and object-level permissions separately from backend feature capability flags. |
| Command transport: `clientId`, command `id`, `ok`, `result`, `error`, `timeoutMs` | [`UIBridgeRpcClient.kt`](sdk/android/src/main/java/com/viant/agentlysdk/UIBridgeRpcClient.kt): `ui.hello`, `ui.poll`, `ui.response`, `ui.snapshot` at `/v1/ui/rpc`. | Replace public JSON-RPC with typed AG-UI extension delivery and acknowledgments. Preserve correlation, timeout, duplicate-ack handling, and explicit errors. |
| Attached-client freshness / selection | [`service/ui/window/registry/registry.go`](service/ui/window/registry/registry.go): namespace/client registration, last poll, snapshot freshness, conversation matching. | Define attachment leases, reconnect, and preferred-client routing. Two devices must not both execute one targeted command. |
| Snapshot: selected window/tab and windows | Registry also stores forms, view state, presentation, region, modal/minimized flags, comparison context. | Keep shared conversation resources separate from per-device focus/layout. Define ownership before allowing bidirectional patches. |
| Datasource live state: `input`, `filter`, `control`, `form`, `selection`, `collection`, `collectionInfo`, `metrics`, `formStatus` | Registry snapshots expose state needed by agent UI tools. | Preserve typed datasource namespaces and safe snapshots. Agent state and renderer state need explicit partitioning. |
| UI events: `seq`, `at`, conversation/client/window IDs, `kind`, `actor`, `detail` | [`registry/events.go`](service/ui/window/registry/events.go): ordered event records, with conversation events retained independently of transient snapshots. | Define cursor lifetime, scope, deduplication, and read access; never interpret sequence as global across restarts. |
| Fetch: datasource `id`, `inputs`, cache hints; result `rows`, `dataInfo`, `metrics`, cache metadata | [`sdk/api/datasource.go`](sdk/api/datasource.go). | Deterministic extension request/result, pagination and caching preserved. Fetching data must not start an LLM turn. |
| Lookup registry: token formats, named inputs/outputs, dialog/window references | Same API. | Keep composer token storage/display/model forms and binding rules intact; plain tool JSON Schema cannot encode all existing UI behavior. |
| Feed patches: `dataSourceRef`, `op`, `path`, `value` | [`protocol/tool/service/ui/feed/service.go`](protocol/tool/service/ui/feed/service.go): preview-only add/replace/remove operations. | Define a dedicated preview command or translate to a precise state subtree; never equate a preview edit with a persisted business mutation. |
| Reports: schema/grammar, scope/id, sequence, mode, target, source, reset version, datasource assemblies | [`sdk/rendering/types.go`](sdk/rendering/types.go). | Preserve progressive assembly/reset semantics and report/data identity. A custom activity can carry content, but does not supply a report engine. |
| Restore: tool request/result payloads and workspace descriptors | [`sdk/ts/src/workspaceRestore.ts`](sdk/ts/src/workspaceRestore.ts) reconstructs windows from completed tool history and forms. | Export explicit durable workspace snapshots or preserve sufficient typed history. Never re-execute historical open/edit commands simply to render history. |
| Remote window providers and proxied datasource calls | [`service/workspace/metadata.go`](service/workspace/metadata.go) registers provider/window-specific fetch routes. | Define the boundary: clients use the unified extension; backend-to-provider connectivity is an implementation detail. Preserve provider/catalog revision and resource binding. |

#### The acknowledgment deadlock to avoid

[`ui/view.open`](protocol/tool/service/ui/view/service.go) sends `ui.window.open` through `bridge.UICommand` and waits for a reply. It can then send `ui.data.fetch` and wait again. The open result may remain `opening` when the client has navigated but not finished protected metadata loading.

A naive migration fails as follows: the agent run waits for the UI acknowledgment; the UI submits an ordinary second query; Agently queues it behind the active turn; that turn cannot finish until the queued acknowledgment runs. The extension dispatcher must service acknowledgments and workspace operations independently of the chat-turn queue.

Proposed wire behavior (extension design, not a built-in AG-UI operation):

1. The SDK attaches a particular client/device and advertises renderer capabilities through an extension control exchange.
2. A run-scoped custom command carries a command ID, target client, operation, parameters, and deadline; standalone commands use the separately specified control/subscription lifecycle.
3. The targeted renderer applies the command and submits a correlated acknowledgment through the same AG-UI extension contract. It does not create a user message, invoke an LLM, or join the chat-turn queue.
4. The waiting runtime operation completes only after the acknowledgment is accepted. State/report updates are versioned independently of command completion.
5. Restore consumes snapshots; replayed notification history never automatically repeats an acknowledged side effect.

Keeping `/v1/ui/rpc` as a required public client connection would violate the single-protocol target. Wrapping its unmodified JSON-RPC envelope in `CUSTOM` would also leave two contracts to implement. Reuse its internal dispatcher/hub if useful, but publish one typed extension contract and migrate every SDK to it.

### Capabilities and discovery: narrower promise

Use the inspected standard `AgentCapabilities` categories for identity, transport, tools, output, state, multi-agent, reasoning, multimodal, execution, and human interaction. Place the Agently extension manifest in its `custom` space. The core agent model's `Capabilities.ModelArtifactGeneration` and workspace capability flags cannot be copied wholesale into that shape; compute the descriptor from enabled services, permissions, and actual protocol support.

The reference `AbstractAgent` exposes an optional `getCapabilities()` hook. The inspected `HttpAgent` does not implement a universal discovery HTTP endpoint, and its base persistent `connect()` path is unimplemented. Thus the data schema is reusable, but endpoint/bootstrap discovery and workspace subscriptions still need a documented binding. An unmodified external UI can run standard chat against a configured URL; it does not automatically discover or render our entire application shell.

### Cross-platform acceptance cases

Source inspection confirms current Swift and Kotlin stream transports open GET requests; TypeScript uses EventSource. All need POST-stream support and protocol event models. Android's current `eventSeq` is an `Int`, while the server's is `int64`; define new cursor encoding without inheriting that mismatch.

Required shared fixtures and end-to-end scenarios:

- New/existing threads, preset answers, queued turns, replayed request IDs, and tool-only continuation.
- Multiple messages and interleaved tool arguments; parent-message association; aggregate-content deduplication; metadata and opaque values survive a round trip.
- All selected-version events, chunk expansion, malformed known fields, unknown future fields, absent versus null, and numeric precision.
- Interrupted/resumed runs, cancellation versus disconnect, recoverable tool failure, usage without double counting, and child invocation attribution.
- Two devices on one thread, targeted open/focus/form commands, stale client, timeout, duplicate/late acknowledgment, and disconnect during open.
- Window opening versus ready versus failed; permission denial; platform variants; resource dependency closure; revision and style-cache changes.
- Datasource fetch/cache and lookup bindings; preview-only feed edits; invalid JSON Patch; concurrent user edits; report reset/sequence behavior.
- Restore without side effects; post-run feed changes; control operations while a chat is active; no calls to the retired UI RPC or native feature routes.

The source audit establishes concrete work, not full conformance. Still outstanding before coding: pin matching upstream artifacts, validate complete fixtures against the actual schema, settle control/subscription transport semantics, and complete the consuming web/mobile application audit beyond the targeted MCP UI and workspace paths inspected below. The previous small adapter estimate remains withdrawn.

## Spaces-style workspace in Agently

Clarified scope: bring the whole Spaces-style product model into Agently's UI workspace, including Pages, files, interactive artifacts, organization, sharing, and agents working alongside content. This is broader than displaying an MCP widget or connecting to an existing ChatGPT account.

The official [Space overview](https://learn.chatgpt.com/docs/space) describes persistent work organized into spaces and pages, with files and other artifacts. [Pages](https://learn.chatgpt.com/docs/space/pages) has editable blocks and nested pages. [Collaboration](https://learn.chatgpt.com/docs/space/collaboration) and [agents in Space](https://learn.chatgpt.com/docs/space/agents) add access and agent-assisted editing. These are product capabilities, not an AG-UI resource schema we can import unchanged. No public portable whole-Spaces wire contract was established by the reviewed documentation.

### Reuse versus missing primitives

| Required behavior | Existing Agently foundation | Concrete gap / proposed contract |
| --- | --- | --- |
| Multiple named Spaces | Deployment workspace metadata, layout applications, navigation. | Add durable logical Space IDs, titles, ownership, membership, and lifecycle. A workspace root path is configuration/storage location, not a portable Space ID. |
| Persistent resource hierarchy | Workspace resource loaders and conversation-owned `workspace.Object`. | Add Space membership, resource kinds, parent/order, links, move/trash/restore, and pagination. A resource must survive the conversation or window that created it. |
| Editable Page documents | Text/Markdown rendering and structured reports. | Add first-class Page and Block records with stable IDs, ordered content, metadata, revision guards, edit operations, and receipts. Execution `pageId` currently means an execution page, not a document Page. |
| Human and agent editing | Agent tools, elicitation, and UI actions. | Route both through the same document mutation service, with actor provenance and conflict detection. Rendering generated Markdown is not collaborative editing. |
| Concurrent editing | Event streams and object/report revisions. | Choose operation-based collaboration semantics and persistence. Generic state patches do not provide text merge, cursor/presence, undo, or offline reconciliation. |
| Files, images, reports, presentations, spreadsheets | Resource tools, uploaded/generated files, rich reports, exports. | Use a common artifact descriptor, binary/content revision, viewer capability, and permission model. Existing export support does not establish interactive editor parity for every format. |
| Interactive visualizations and apps | Forge windows, report renderers, existing MCP UI host. | Define durable content and independent view instances; implement the standard Apps lifecycle where interoperable widgets are required. |
| Chat alongside a selected resource | Conversations, UI snapshots, intent profiles, scoped tool context. | Bind a thread/run to Space/resource/selection IDs and observed document revision. Scope the model's context instead of injecting the entire Space. |
| Comments, mentions, attribution | Runtime origins and conversation messages. | Add anchored comment threads, stable selection/block anchors, mention targets, resolution, and attribution. Agent mentions need an explicit dispatch rule; a mention in ordinary content is not permission to execute. |
| Resource access and sharing | Existing authorization and permitted-view services; conversation visibility. | Define Space/resource ACLs and inheritance. Viewing a window does not grant edit/share/export permission on its backing document. Linked resources retain independent access. |
| Search and references | Resource search, lookups, file browser, conversation search. | Add authorization-filtered Space/page/block indexing and typed references. Moving a Page must preserve reference identity. |
| Instructions, tasks, recurring work | Skills, intent profiles, goals, schedules. | Bind instructions and task definitions to resources; preserve explicit activation and authorization. A document saying “daily” must not itself create a schedule. |
| Web and mobile restore | Per-platform SDKs and workspace restore helpers. | Reopen views against current durable resource revisions, preserving per-device focus/placement separately from shared content. Verify renderer support on each platform. |

The inspected [`workspace.Object`](protocol/ui/workspace/object.go) requires conversation ownership, constructs its object ID from a window ID, and derives its state reference from conversation plus window. [`sdk/canonical_workspace.go`](sdk/canonical_workspace.go) projects these descriptors from acknowledged tool results into message attachments. Reuse that projection for provenance and opening resources, but add a durable resource layer underneath it; do not make a Space or Page an alias for a chat attachment.

### Proposed minimum resource model

These are proposed extension resources, not AG-UI standard objects:

- **Space:** stable ID, title, owner/workspace identity, revision, membership policy, resource references, timestamps, trash state.
- **Resource:** stable ID, Space ID, kind, optional parent, ordering, title, content reference, revision, source/origin, permission reference, and renderer hints.
- **Page:** resource ID and ordered Blocks. Each Block has an ID, kind, content, metadata, and concurrency guard. Keep document content revision separate from layout and binary asset revisions.
- **View instance:** instance/client IDs, resource ID and opened revision, placement/focus, renderer capability, and acknowledged lifecycle. Multiple views can display one resource.
- **Edit:** operation ID, resource ID, expected revision or block guard, semantic operations, actor, and an applied/rejected receipt with the new revision. Define atomicity explicitly.
- **Reference / comment / task:** independent identities pointing to resource/block/selection anchors, with explicit access and execution policies.

All lifecycle, read, edit, search, share, and subscription operations must be part of the documented AG-UI extension profile. The server invokes resource services deterministically; agents use controlled tools over those same services. Standard AG-UI messages handle the conversation alongside the resource. Extension snapshots and notifications handle durable workspace data, without turning document reads into model turns.

### What can be reused from the Pages integration available here

The installed Pages connector exposes useful design examples: separate Space and Page IDs; canonical blocks with hashes; sequence guards; semantic insert/patch/move/delete operations; per-operation receipts; reference resolution; sharing operations; and artifact embeds. Those are connector contracts visible in this session, not evidence of an openly deployable Pages backend for Agently. No user Pages were read or modified for this audit.

We can adopt equivalent principles with our own versioned schemas. A future connector to actual ChatGPT Spaces would additionally need an authorized supported API, ID mapping, attachment/reference transfer, permission mapping, and conflict-aware synchronization. AG-UI alone does not supply that integration, and it is not required to build the requested Spaces-style workspace in Agently.

## MCP UI / MCP Apps compatibility audit

The read-only audit also inspected the sibling web application at `../agently` (HEAD `c8c5682c83590d8fa1336ff82cd7243738f307ba`) and the local `../mcp-ui` checkout. The web package points at `../../agently-core-v1/sdk/ts`; therefore this review does not assume it builds against the current `agently-core/sdk/ts` working tree. Verify deployment dependency resolution before claiming cross-repository parity.

| Surface | Observed implementation | Compatibility consequence |
| --- | --- | --- |
| Resource identification | `ui://` resource URIs and `text/html;profile=mcp-app` recognition exist. | Useful shared surface, but correct MIME/URI alone does not prove Apps compatibility. |
| Guest handshake/messages | [`appproto.js`](../agently/ui/src/services/mcpApps/appproto.js) validates `{version, method, params}` with `mcpui:host-ready`, `mcpui:tools-call`, etc. | The inspected host does not speak the standard Apps initialization and JSON-RPC request/response contract on this path. An unmodified standard App will not become compatible by receiving the same HTML. |
| Tool callbacks | [`bridge.js`](../agently/ui/src/services/mcpApps/bridge.js) POSTs the dedicated MCP UI tool endpoint and constructs a new result envelope. | Migrate the network leg into the AG-UI extension dispatcher, retaining the approval-aware guest-tool path. |
| Tool result fidelity | That bridge sets `_meta` to an empty object and exposes its backend wrapper as structured content; [`MCPUIToolCallOutput`](sdk/api/types.go) has no full original MCP result envelope. | Preserve original ordinary content, structured content, hidden metadata, and error semantics through a typed host-only payload. Do not dump hidden app data into model-visible messages or state. |
| Workspace HTML | [`workspace_forge.html.tmpl`](protocol/ui/resource/workspace_forge.html.tmpl) produces a structural summary. Rich rendering relies on private `rendererUrl` metadata. | Another host may display the summary but does not gain the full interactive Forge window. Export a portable bundle or provide an explicitly supported renderer integration. |
| Rich Forge guest | [`MCPUIForgeWindowPage.jsx`](../agently/ui/src/components/mcpApps/MCPUIForgeWindowPage.jsx) fetches same-origin authenticated window metadata and installs the private guest bridge. | This relies on Agently routes, credentials, and runtime. It is not a standalone portable MCP App as currently implemented. |
| Same-origin and CSP policy | [`resourceLoader.js`](../agently/ui/src/services/mcpApps/resourceLoader.js) and workspace resource metadata use private sandbox/renderer/CSP fields. | Map to the pinned standard host resource policy and broker operations; an external host need not honor these private fields or supply Agently cookies. |
| Window/App identity | The Forge page derives an instance window ID from `windowKey`. | Define resource ID, App instance ID, and Forge window ID separately. Test two instances of the same resource across conversations and devices. |
| Native clients | Native SDK workspace/Forge support exists; the targeted iOS/Android app searches did not establish an equivalent standard Apps host. | Treat portable WebView/AppBridge hosting as unverified platform work, not automatic reuse of native Forge rendering. |
| Official migration plans | Tasks [009](doc/mcp-2006/009-apps-server-and-mcp-ui-compatibility.md), [010](doc/mcp-2006/010-agently-apps-host.md), and [013](doc/mcp-2006/013-agently-forge-app-integration.md) are marked planned, with conformance evidence pending. | Source inspection agrees that the existing private bridge needs migration. Plans and a directory named `mcpApps` are not implementation proof. |

The [MCP Apps overview](https://apps.extensions.modelcontextprotocol.io/api/documents/overview.html) defines a host/guest bridge over postMessage, with initialization, tool/resource access, context, and display behavior. [OpenAI's UI guidance](https://developers.openai.com/plugins/build/chatgpt-ui) uses that shared standard and treats OpenAI-only capabilities as additional extensions. [OpenAI plugin extensions](https://developers.openai.com/plugins/build/extensions) cover surfaces such as sidebars, conversation panels, and file viewers; those do not constitute the Pages/Spaces document model.

### Single-protocol boundary and portable embedded apps

The target remains one public interaction protocol between Agently clients and the Agently backend: AG-UI plus published extensions. Embedded app code can be isolated behind a local standard MCP Apps host bridge, while the host maps permitted tool/resource operations onto that one backend contract. Backend connections to MCP servers are similarly implementation boundaries.

This distinction is essential: unmodified MCP Apps require their own specified iframe-to-host messages. If “one protocol” is interpreted as forbidding even that local component bridge, standard MCP Apps compatibility is impossible without rewriting each guest. Do not claim that AG-UI itself implements the Apps bridge or the Spaces editor. Preserve one backend contract and state component-host compatibility explicitly.

Required proof: run a standard upstream App in the Agently host, run an exported Agently App in a standard host, verify the full result envelope and approval flow, test repeated-resource instance isolation and teardown, and exercise equivalent behavior on supported native platforms. No such live conformance run was performed in this documentation audit.

## Extension contract

Use standard AG-UI semantics wherever they apply. For additional functionality, define a documented Agently extension profile using AG-UI extension points. Proposed conventions below are design choices, not claims that AG-UI standardizes these operations:

- Client extension commands use a namespaced envelope in `RunAgentInput.forwardedProps`, for example under `agently`, with an extension version, operation name, request ID, target IDs, and a typed payload.
- Server extension notifications and command results use `CUSTOM` events with names such as `agently.turn.queued`. Their values carry a versioned schema and the necessary correlation and resource IDs.
- Ordinary text, tool results, state, and run boundaries retain their standard event types. Do not wrap all native events in `CUSTOM` and leave clients to implement the old protocol inside it.
- Capability discovery advertises supported extension operations and versions through the same contract. Define a discovery request that works before a conversation is created; do not assume an external AG-UI agent understands it.
- Management commands dispatch directly to the appropriate service. They must not depend on an LLM deciding to invoke a tool.
- Specify command validation, authorization, idempotency, error responses, and completion boundaries. Distinguish the command's run ID from the run or resource it targets.
- Define how long-lived subscriptions carry conversation, workspace, scheduler, and feed updates outside a chat run. A subscription must not keep a completed chat run artificially open or confuse an update about another run with its own lifecycle.
- Specify upload/download and large-payload transfer as part of the extension profile. Transport choices must be explicit; clients must not silently depend on legacy file APIs. If capability-issued asset URLs are used, document them as the data transfer binding of this profile.

The extension points provide envelopes, not the semantics for these features. A concrete operation catalog and schemas are required before declaring the unified protocol complete.

## Feature coverage

| Feature family | Target contract | Required migration work |
| --- | --- | --- |
| Chat, streaming, tool activity | Standard run, message, and tool events | Stable identities, message boundaries, ordered argument deltas, results, and error handling. |
| Client-executed tools | Standard tool definitions, calls, and result messages | Handler registration, argument assembly, execution, and continuation with results; tool display alone is insufficient. |
| Conversation history and shared state | Standard message/state snapshots and deltas, plus management extensions | History ownership, list/create/update/delete operations, pagination, restore, compaction, and pruning. |
| Elicitation and approvals | Standard interrupt/resume semantics where supported by the pinned version; extensions for additional behavior | Pending request identity, structured answers, decisions, edits, expiry, and idempotent resume. |
| Turn queues, steering, cancellation | Agently command and notification extensions plus standard run boundaries | Queue inspection/reordering/editing/removal, steering, cancel semantics, and target run correlation. |
| Goals and schedules | Agently command, state, and notification extensions | Creation, updates, status, execution, and subscriptions independent of foreground chat. |
| Async tools and live feeds | Standard tool results plus Agently progress/feed extensions | Waiting state, updates after a chat run, feed patches, subscription lifecycle, and recovery. |
| Workspace, agents, models, tools, skills, and templates | Agently discovery and management extensions; standard events where applicable | Configuration, selection, resource operations, capability visibility, and runtime overrides. |
| Files, attachments, generated artifacts, and payloads | Standard content forms where supported; Agently transfer and artifact extensions | Upload/download, resource identity, retention, and rich result metadata. |
| Reporting, datasources, lookups, rich content, and windows | Agently typed commands, state, and events | Preserve report rendering, exports, datasource operations, workspace restoration, and UI/window interactions. |
| Linked agents, execution traces, reasoning, and usage | Standard events/metadata where supported; Agently extensions for additional detail | Attribution, hierarchy, detailed execution presentation, payload references, and accounting. |
| Authentication and authorization | Defined authenticated transport and extension capability rules | Consistent principal handling, credentials, access checks, and feature visibility on every platform. |

This table groups the known feature families; it is not an exhaustive API inventory. Audit all registered routes and all SDK methods, including optional handlers, and assign each user-facing operation a standard or extension mapping. Infrastructure protocols such as MCP and A2A remain outside the scope of replacing the UI interaction contract.

## Reuse of existing specifications and Agently primitives

One interaction protocol can carry resources defined by several reusable data specifications. Reusing an agent or skill document format does not require the UI to speak another interaction protocol.

| Primitive | Reusable specification or model | Proposed use |
| --- | --- | --- |
| Portable skill package | [Agent Skills](https://agentskills.io/specification): `SKILL.md`, frontmatter, instructions, and supporting resources | Keep the existing skill package format; list, fetch, and activate skills through Agently AG-UI extensions. Validate exported packages against the chosen specification version. |
| Agent identity and advertised abilities | AG-UI `AgentCapabilities` and its typed categories | Use AG-UI’s own descriptor and its custom capability space. Define the discovery binding explicitly; A2A AgentCard reuse is unnecessary for this UI contract. |
| Portable agent/workflow definition | [Open Agent Specification](https://oracle.github.io/agent-spec/index.html) | Evaluate as an optional import/export format for overlapping agent, model, tool, and workflow concepts. Runtime compatibility requires an explicit semantic mapping; the format alone does not guarantee identical execution. |
| Agently agent definition | [`protocol/agent.Agent`](protocol/agent/agent.go) | Reuse existing runtime configuration internally and expose versioned public descriptors/configuration resources through the extension profile. Separate public catalog data from privileged runtime configuration. |
| Intent profile | [`protocol/intent.Profile`](protocol/intent/profile.go) | Preserve Agently's instructions, applicability, model selection, tool bundles, evidence contract, templates, bootstrap, knowledge, and execution restrictions in a documented extension schema. No direct equivalent covering this full model was established in the reviewed AG-UI, Agent Skills, or Agent Spec material. |
| Tools and structured inputs | Standard AG-UI client tool definitions and JSON Schema | Reuse schema-based inputs. Backend tool catalogs and bundles remain extension resources; do not advertise all backend tools as client-executed tools. |
| Goals, schedules, feeds, templates, reports, and workspace/window resources | Existing Agently domain models | Derive versioned extension schemas from the current models after auditing which fields and behaviors belong in the public contract. Share the schemas and fixtures across TypeScript, Swift, Kotlin, and Go. |

The existing [`protocol/skill`](protocol/skill) parser and portability tests already provide a foundation for skill reuse. Its internal metadata map accepts broader values than Agent Skills' documented string-valued metadata map, so portable export needs validation rather than assuming every internal representation is compliant.

A2A remains a separate backend interoperability concern. Its AgentCard is not the UI discovery contract. An A2A `AgentSkill` is an advertised ability; an Agent Skills `SKILL.md` package contains instructions and resources.

To make extensions portable, publish their schemas, versions, operation semantics, examples, and conformance fixtures. A capability declares which operations a backend implements; it must not merely identify the backend as Agently. Prefer standard semantics where available, and require explicit support for extensions that affect execution rather than silently ignoring them.

## SDK and UI design

Migrate [`sdk/ts`](sdk/ts), [`sdk/ios`](sdk/ios), and [`sdk/android`](sdk/android) to the same AG-UI and extension schemas. Backend selection changes endpoint, credentials, and capabilities, not the public protocol used by the client.

Maintain lossless protocol messages and shared state separately from their visual projection. The existing TypeScript [`chatStore`](sdk/ts/src/chatStore) models Agently execution pages and is not a complete AG-UI message store: text deltas accumulate into pages, and its current tool delta handler does not accumulate argument content. Reconstructing the next AG-UI input solely from rendered rows would lose information. Preserve message roles, tool arguments/results, metadata, and supported opaque continuation fields, then project into the existing UI model.

Each SDK must support POST streaming, standard event reduction, the extension registry, capability handling, thread/history ownership, client tool execution, interrupts, and explicit cancellation behavior. Closing an HTTP stream and cancelling backend work are separate operations whose relationship must be specified.

Generic AG-UI agents must remain usable when Agently extensions are absent. Unsupported extension features should be unavailable in the UI; clients must not fall back to native Agently endpoints to obtain them.

## Lifecycle and recovery requirements

The existing [`Query`](service/agent/run_query.go) has multiple completion paths. A normal query runs a turn, a queued query returns before that turn executes, and a workspace-intake preset answer can return before the normal turn lifecycle starts. The protocol boundary must handle each explicitly:

- Subscribe before execution can publish relevant events and establish correlation before accepting concurrent work.
- Preserve the client-supplied AG-UI run identity while mapping internal turn and message IDs separately.
- Do not finish a queued run merely because `Query` returned.
- Complete preset-answer runs without waiting for a terminal turn event that their path does not emit.
- Close message and tool-call boundaries correctly, and emit exactly one terminal event for an accepted run on success, failure, or interrupt according to the selected schema.
- Define cancellation, transport disconnect, retry, subscription overflow, snapshot recovery, and replay behavior. The existing in-memory bus is not a durable replay log.
- Keep targeted management operations and background notifications from completing or mutating the wrong foreground run.

## Scope and delivery

### Agreed implementation sequence (2026-10-02)

Deliver this proposal in two milestones:

1. **Adapter and interoperability proof.** Implement the AG-UI streaming boundary,
   start the SDK migration, publish the initial extension profile, and run an
   independent upstream UI against the assembled Agently backend. Keep Agently
   authoritative for saved history in this milestone. This proves the contract;
   it does not establish full feature parity or retire the existing clients.
2. **Unified public protocol and full proposal.** Complete the operation inventory,
   extension schemas, web/iOS/Android migration and feature parity described below.
   Make AG-UI plus Agently extensions the main public interaction contract and
   remove superseded native interaction endpoints after the acceptance gates pass.

Internal runtime-to-wire translation may remain at the boundary. Maintaining two
public protocols permanently is not the target. Consult the user before substantial
architecture changes. Current implementation details and limits are tracked in
[`doc/ag-ui-phase1.md`](doc/ag-ui-phase1.md).

The earlier estimate of seven server areas, five TypeScript client areas, and 10–15 files covered a basic adapter alongside the existing API. **That estimate does not apply to the required single-protocol migration with full functionality across web and mobile.** A defensible file or effort estimate needs the operation inventory and extension schemas first.

Deliver the migration in these stages:

1. Inventory every current UI/SDK operation and build a parity matrix for web, iOS, and Android. Pin AG-UI versions and mark standard versus extension mappings.
2. Define shared extension schemas, capability discovery, commands, background subscriptions, binary transfers, identity rules, and errors.
3. Implement the server protocol boundary and deterministic dispatch to existing services, covering all query lifecycle paths.
4. Migrate all three SDKs to standard AG-UI plus the same extension profile, with lossless protocol state and existing UI projections.
5. Wire web and mobile feature flows to the migrated SDKs and complete the parity matrix.
6. Validate standard interoperability and complete Agently feature parity, then cut over clients and retire the superseded public interaction routes. Any temporary compatibility bridge must have a removal gate; it is not a supported second protocol in the final design.

Acceptance requires:

- An independent standard AG-UI client can run Agently's standard chat/tool flows.
- Agently web, iOS, and Android clients can run an external standard AG-UI agent without Agently-specific dependencies for those flows.
- Every existing Agently feature has an implemented mapping and passes parity checks on the platforms that currently support it.
- Shared protocol fixtures validate all SDKs against the same schemas and event sequences, including interleaved messages/tools, client tool round trips, interrupt/resume, queued and preset runs, snapshots, reconnect, and background updates.
- End-to-end tests show that migrated clients make no calls to the retired native interaction APIs.

## References

- [AG-UI core architecture and standard HTTP client](https://docs.ag-ui.com/concepts/architecture)
- [AG-UI event types, including custom events](https://docs.ag-ui.com/concepts/events)
- [AG-UI message and history contract](https://docs.ag-ui.com/concepts/messages)
- [AG-UI tools and client tool results](https://docs.ag-ui.com/concepts/tools)
- [AG-UI state snapshots and deltas](https://docs.ag-ui.com/concepts/state)
- [AG-UI interrupt and resume contract](https://docs.ag-ui.com/concepts/interrupts)
