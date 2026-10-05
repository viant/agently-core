# Native and application compatibility observation

The migrated web client's primary submitted agent work remains AG-UI. Native
mobile, CLI and scheduler clients can still start work using the existing native
API. The explicitly scoped application observer reads those independent native
turns and application notifications; it does not duplicate AG-UI execution or
silently replace the primary protocol stream.

## HTTP contract

`GET /v1/stream?conversationId=<native conversation>&compatibilityScope=native-and-application`

This is the existing authenticated BFF observer surface with a new explicit
scope. Its event shape and renderer enrichment remain the native streaming
shape. Omitting the scope retains existing mobile/CLI behavior. Other nonempty
scope values and missing conversation identities return 400. A missing
principal with authentication enabled returns 401. Native conversation visibility
is checked before subscription using `native.RequireVisibleConversation` and
its generated Datly visibility predicate; private foreign conversations return
403 while public/shared read visibility is preserved. AG-UI execution ownership
is not substituted for native conversation read authorization.

Only the native backend supplies this observer's explicit authorization and
provenance capabilities. Remote/generic adapters return 501. Independent
backends must never receive this scope or native conversation/extension data.

## Persisted execution provenance

The internal Datly reader is authored at
`dql/agui/provenance/read/reader.dql` with its parameterized SQL in
`sql/rows.sql`; generated artifacts are in
`internal/datly/agui/provenance/read`. It reads only existing `conversation`,
`turn` and `agui_run` tables. No schema or transcript format changes were made.

Classification is independent of subscribers, authentication subject, worker
leases, connection state and protocol terminal status:

- Any admitted `agui_run` matching the native conversation and `TurnID` proves
  AG-UI ownership. All such execution events are excluded, including after a
  protocol interrupt, disconnect, overflow, worker recovery and process restart.
  For persisted turns, globally unique native turn identity also detects isolated
  MCP proxy ownership under a synthetic protocol thread. The query deliberately
  checks across run principals so shared viewers receive
  the same execution classification as the owner.
- If an authorized conversation has a persisted native turn and no AG-UI
  admission, it is native-origin. Child conversations follow their persisted
  parent conversation/turn links; an AG-UI-owned ancestor excludes the child.
  Missing or cyclic ancestry remains unclassified.
- A missing native turn, failed/inconsistent provenance read or unscoped
  execution is unclassified. The observer emits `compatibility_reconcile` with
  conversation/turn identity, original event sequence, `status: unknown` and
  `patch.reason: execution_provenance_unavailable`. It contains no speculative
  delta, tool result, accepted input or failure detail. Repeated hints for the
  same turn are suppressed within a subscription, but each later event retries
  the unresolved read. Reconnect starts a new hint boundary.

AG-UI admission commits its server-selected native `TurnID` before native Query
can create or publish the turn. Initial web submissions cannot select an already
persisted native turn identity. Positive classification can therefore be cached
for the lifetime of an observer; absence alone is never cached as native
ownership before native persistence. Existing AG-UI run cleanup follows native
conversation-tree deletion, rather than expiring ownership rows independently
of retained native turns. Future retention or native-to-protocol handoff designs
must preserve this invariant or introduce explicit durable generation provenance.

`conversation_meta_updated` and goal update/clear/controller-scheduled
notifications are application events and are allowed independently of transport.
Turnless feed active/inactive notifications are also application events.
Turn-associated feed/execution events follow turn provenance. Foreign
conversation events are excluded. A native-origin raw `protocol_event` is not
used as a private protocol tunnel; it requests reconciliation.

## Web binding

The SDK exposes `observeNativeEvents` and an explicit `observeNativeWork` option
for AG-UI interaction mode. The production web singleton enables that option;
legacy SDK consumers retain their default route. The visible conversation keeps
observation open even when idle, so a later native mobile/CLI/scheduled turn is
not missed. This does not mark the conversation busy. Primary chat still uses
AG-UI; the scoped application channel cannot carry AG-UI-owned execution.
Unknown provenance triggers an authoritative bootstrap; shared readers denied
access to an owner's protocol journal use the existing authorized transcript
read. All observers and asynchronous reconciliation callbacks are fenced on
logout/account reset. Tests cover scoped URL/cookies, native deltas, suppressed
reconciliation hints, optional independence of primary AG-UI, shared-history
account fences, and idle view subscription. SDK tests pass 4 cases; runtime UI
tests pass 81 cases. Browser combined-observer verification is pending.

Combined browser check: the idle visible conversation received a real run issued
through the native query API by a separate test action simulating another client.
The scoped observer delivered its native control/model/text/terminal events and
the assistant rendered without a reload. Native streaming does not include the
originating user text, so the SDK now performs one bootstrap at native terminal
events to recover the authoritative user row. That final row-reconciliation
browser check remains pending; the scoped SDK tests cover terminal refresh.
The native query used in this check is an explicit test stimulus, not a fallback
from web composer submission.

The terminal reconciliation is now browser-verified: after a native-client test
submission into an idle visible conversation, both its exact user prompt and
assistant response appeared without reload. Artifacts: assembly
`output/playwright/agui-primary/native-background-rendered.png` and
`native-background.requests.txt`. The log includes explicitly scoped observer
GETs, AG-UI bootstrap commands, and the intentional native query test stimulus.
Full SDK suite passed 492 tests with 5 skips after integration
(`/tmp/agui-native-observer-full-sdk.log`); typecheck and production UI build pass.
This proves the deterministic native-client observation case, not every remaining
workspace/feed/approval/auth/public-backend acceptance gate.
