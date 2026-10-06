# Agently live presentation contract

This preserves the existing web renderer's execution pages, tool rows, Tool Feeds, planner and commentary behavior while the official AG-UI reducer remains the message/state authority. It is not a raw native-event transport and does not change the canonical transcript or persistence format.

`metadata.agently.presentation` version `1` is a closed scalar projection of native identities, execution role/phase/mode, model/provider labels, timestamps, usage counts, and opaque payload references. Its producer copies an explicit allowlist, excluding native Patch, request/response bodies, authentication context, full MCP host receipts and `_meta`, and Forge authoring JSON. The existing RUN_STARTED `identityVersion/nativeTurnId` remains compatible. The schema is in `protocol/agui/extensions/presentation-v1.schema.json`; SDK consumers use `readAgentlyPresentation` rather than echoing arbitrary metadata into view models.

The generic runtime ProtocolEvent/EmitStandard bridge rejects the native presentation namespace, its identity/control activities and custom events, including attempts embedded in message snapshots. The legacy native identity namespace is reserved too. Trusted native producers remain separate; user-identity emission checks the translator's native run scope. Arbitrary non-Agently custom events remain observable and supported.

Versioned activities describe native turn lifecycle (`agently.turn`), feed lifecycle (`agently.feed`), planner state (`agently.planner`), planned tool names/IDs (`agently.tools-planned`), sanitized commentary (`agently.narration`) and explicit user identity (`agently.user-identity`). `agently.tool` describes actual executor start/wait/completion even after the model's standard TOOL_CALL_END closed its argument lane; effect timing survives restoration and an approval wait does not claim execution began. Planned calls never become standard pending tool executions. Protocol RUN_STARTED does not establish that a queued native turn has begun. Tool waiting and linked-conversation references use the same scoped tool IDs as standard TOOL events. Aggregate usage uses explicit model_call, turn, or conversation scope; aggregate snapshots do not enter model-only token totals.

Protocol and native user identities differ. Initial protocol users receive trusted run/turn/client-message references; observed native model parents produce a scalar identity activity. Bootstrap checks the recorded principal/thread/turn/client-message tuple and joins the canonical turn's `startedByMessageId`. It preserves the protocol multipart body and the original canonical transcript while avoiding a second logical user row. It never matches by text or treats caller metadata as ownership authority.

Feed removal is distinct from observer stream completion. Independent subscriptions
include reduced `active` and `activationKnown`. Historical feed rows do not establish
activity. Missing or incomparable activation is `active: null,
activationKnown: false`; no stream completion, expiry or retained data invents an
inactive/active transition. Foreign conversations and other feed IDs cannot change
the subscription or trigger refresh. Shared chat messages/state remain unchanged.

Completed-observer recovery is now implemented. Production's optional
`FeedActivationJournalReader` returns a complete authenticated principal/thread
snapshot from `agui_event` joined to its owning `agui_run`, including every run
status and checking both event/run identities. The private generated Datly reader
is authored in `dql/agui/feed_journal/read` and linked at
`internal/datly/agui/feed_journal/read`. The `feedfacts` management operation reads
thread messages and all 256-row `(run_key, sequence)` keyset pages within one
managed serializable transaction. Run enumeration is only pagination; run age,
status and key order never establish lifecycle order. A 16,384-row operational
bound, timeout, failed page or invalid identity returns an error without returning
a selected prefix as an authoritative snapshot. The subscription cannot publish
`activationKnown: true` from incomplete recovery.

Recovered and buffered live facts use one partial-order reducer. Newer original
`activationAt` wins; native presentation `createdAt` is a legacy original-time
fallback only for actual native facts, never inherited refresh snapshots. Event
emission time and `time.Now()` are not substitutes. Opposing independent equal-time
or undated facts remain stably unknown regardless of scan order; another equal
candidate cannot clear that ambiguity. Exact duplicate original facts coalesce.
Validated original journal sequence can order facts from the same original run
with equal original times, including two undated facts. A dated and an undated fact
remain incomparable. Older buffered live events cannot overwrite newer recovered
facts.

`activationSource: {runId, sequence}` is optional provenance and preserves the
original journal position through copies. The server validates that reference
against an actual original event in the same complete owner/thread snapshot and
checks the original boolean/time before using sequence order. An inherited
snapshot cannot make its own newer position an original transition. Invalid,
foreign or missing references lose sequence authority. Legacy snapshots retain
safe timestamp/unknown defaults. The legacy bool-only `AGUIFeedActivationReader`
cannot establish this order and is not consulted for durable recovery.

An actual live publication also records optional typed `activationFact` with its
version, feed ID, explicit boolean, original time when available, and exact source
position. The top-level activation remains the reduced view, which can be unknown.
This prevents a conflicting/undated fact from disappearing merely because the
current view is unknown. Initial and inherited refresh snapshots do not fabricate
this original-fact marker. `FeedActivationSnapshot`, `FeedActivationFact` and
`FeedActivationSource` are validated presentation schema definitions; SDK readers
validate the optional fields but render only the reduced view under the trusted
Agently connection profile. They do not promote the stored fact into browser
authority or flatten it into model text.

This closes the demonstrated completed-observer preservation gap: an explicit
inactive fact saved in an old observer journal survives a new subscription and an
actual runtime restart, even without a thread feed activity. This is recovery of
already-recorded lifecycle facts. Native feed specifications and historical tool
payload reconstruction are unchanged. Transitions that occur with no observer
and are never recorded anywhere remain unknown; complete offline lifecycle
capture would require a separately reviewed producer. This change introduces no
new producer, synthetic expiry, persistence table, model query or domain behavior.

Focused tests verify schema-valid standard events, scalar metadata boundaries, exact zero/false values, source attribution for nested runs, restore/replay preservation, no synthetic planned tool calls, feed active/inactive without assistant IDs, nullable unknown activation, same-journal/cold-thread activation recovery, unchanged chat state, narration authoring sanitation, and a real Datly user row whose ID differs from the protocol user. SDK readers preserve known fields and leave unknown versions/events observable through the normal subscriber path. Production browser appearance and complete projection integration remain separate acceptance gates.

The feed-specific gates cover all observer statuses, principal/thread/feed
isolation, multi-page reads, original-time precedence, inherited/source validation,
scan-order independent ambiguity, buffered live merge, unknown snapshots retaining
original facts, incomplete-scan refusal, unchanged messages/state, actual restart,
and a later explicit transition after the old run is terminal. Logs:
`/tmp/agui-feed-final-backend-gate.log`, `/tmp/agui-feed-protocol-gate.log`,
`/tmp/agui-feed-bound-gate.log`, `/tmp/agui-feed-full-protocol-store-gate.log`,
`/tmp/agui-feed-live-race-gate.log`, `/tmp/agui-feed-sdk-reader-gate.log`, and
`/tmp/agui-feed-sdk-typecheck.log` (consult terminal status). Datly 1.0 checkout
`ea77f67ffe83257808e1e300b8d5aaae53705b1b` was selected via the authoring skill;
stock `transcribe get` produced the seven reader artifacts using a disposable
current-schema SQLite fixture. Actual SQLite component behavior is verified;
live MySQL deployment validation is separate.
