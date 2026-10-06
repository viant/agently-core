# Durable approval coordination for the migrated web UI

The existing per-item inbox endpoint remains
`POST /v1/tool-approvals/{id}/decision`. Native-origin decisions keep their existing
behavior. An AG-UI-owned chat approval is intercepted before the legacy decision
path can execute, synthesize another prompt, finish the turn, or resume native
work independently of the protocol.

## Command and result

`approval.decide` is an independent resource run on `/v1/ag-ui/run`, with no model
query. Its versioned envelope has operation/request identity and this payload:

```json
{
  "originalRunId": "original-protocol-run",
  "approvalId": "native-approval-id",
  "answer": {
    "interruptId": "native-approval-id",
    "status": "resolved",
    "payload": {"action": "approve"}
  }
}
```

The outer thread must be the original native conversation; the original run is
read under the effective BFF principal. The approval must occur in that run's
persisted interrupt set and match its original native turn, tool operation and
message graph. The command has its own durable journal and lease. The inbox
wrapper derives a stable command identity from the original target and genuine
answer, so an identical retry observes the same decision command.

The terminal command result and inbox JSON response have the same shape:

```json
{
  "status": "ok",
  "outcome": {
    "approvalId": "native-approval-id",
    "action": "approve",
    "status": "executed",
    "result": "the actual native tool result"
  },
  "protocol": {
    "version": "1",
    "threadId": "native-conversation",
    "originalRunId": "original-protocol-run",
    "commandRunId": "independent-decision-command",
    "continuationRunId": "successor-if-admitted",
    "remainingInterruptIds": []
  }
}
```

The real outcome retains existing native identity, decision, tool, error and
expiry fields. It can also carry the same optional protocol references for the
existing inbox outcome-discovery channel. References in a completed command are
point-in-time data: exact replay does not rewrite them with later state. The web
coordinator bootstraps after a decision or trusted application hint to discover
the current continuation. A failed real effect remains a failed outcome; protocol
references are not discarded by the legacy HTTP failure-only response path.

## Effect and continuation safety

The coordinator reuses `aguiApplyApproval`, the existing `aguiDecision` claim and
`aguiOutcome` receipt. The existing Datly approval claim owns principal/status,
updated-at and deadline comparison before external execution. A decision without
a completed outcome is uncertain and never automatically reset or repeated.
Completed receipt verification checks the exact canonical answer, current queue
scope, original native tool/message identity, completed tool/message result and
terminal queue state. A completed pre-expiry answer remains replayable after
expiry only with this proof; ordinary unanswered deadlines and schema validation
remain in force. An expired unanswered decision uses a server-verified timeout
transition, not a fabricated user approval.

Each per-item decision takes effect immediately. Once every original interrupt
has a genuine completed answer, the coordinator builds the complete standard
resume array and admits one deterministic successor through the existing
prior-revision CAS. The resumed worker consumes those receipts without running
the tool effects again. Mixed elicitation and frontend-tool interrupts remain
unanswered until their real responses arrive; the server can add already-complete
approval receipts to a genuine mixed standard resume, but does not manufacture
missing responses.

The successor preserves the full standard message graph, including activities,
opaque encrypted reasoning, multipart content, metadata, subagent attribution
and explicitly empty optional values. A nullable persisted thread state stays
nullable: standard input omits `state:null` and selects current native server
state rather than substituting an empty document. Original immutable backend
execution controls are restored through a bounded principal/thread/turn-validated
lineage traversal. Existing permitted model/agent selection and generated
`context.agui` remain intact; a client cannot replace backend tools, bundles,
reasoning controls, context or attachment references on resume.

A restricted native-turn/tool-bound translator method reconciles authoritative
receipt snapshots. Generic standard-event producers remain unable to forge
Agently presentation or invocation metadata. No raw native event tunnel or full
MCP host envelope is added to model messages.

## Restart recovery and notifications

The existing agent `Watchdog.Start` lifecycle invokes the injected protocol
reconciler independently of the native stale-run sweep, including its initial
pass and periodic passes. Assembly wires the backend after the listener binds.
There is no handler-owned polling goroutine or browser-only recovery dependency.

The private Datly recovery reader keyset-pages opaque durable run keys, at most
50 candidates per pass. It considers interrupted parents and admitted or
lease-expired successors/decision commands. Bounded concurrency, per-item time
budgets, restored saved principal/security and lifecycle-bound worker dispatch
keep stalled items from starving later ready records. Discovery uses a literal
SQL `LIMIT 50`: the selected Datly runtime cannot parse a parameterized LIMIT in
its source-projection subquery. Authoring and generated output were updated and
verified against the native SQLite runtime.

Native watchdog suppression is narrowly about approval recovery responsibility.
Unanswered/uncertain approval boundaries and continuation admission/dispatch
belong to this coordinator. Initial protocol-origin native workers and already
started native continuation execution retain existing native lease recovery;
protocol ancestry or successor existence alone does not suppress it.

After the completed native decision receipt commits, and after a fresh successor
admission commits, the backend publishes existing application
`conversation_meta_updated` with only `patch: {aguiUpdated: true}`. The explicitly
scoped compatibility observer permits that hint regardless of transport. It
carries no private input, receipt or run identity. A notification failure does
not overwrite a committed effect or result.

## MCP proxy inbox and recovery

An isolated `__proxiedMCPRequest` approval belongs to a synthetic protocol thread
and a canonical native guest turn. The inbox discovers that immutable ownership
through a private principal-scoped Datly native-turn reader. It resolves the
original registered activity alias and rechecks current app/tool authorization;
a supplied endpoint hash, observer presence or iframe input cannot create host
authority.

A proxy decision adds `originalThreadId` to the command payload. The outer thread
remains the authorized native conversation. Its result references add
`kind: "mcp-app"` and `nativeConversationId`; `protocol.threadId` identifies the
synthetic proxy thread, while `outcome.conversationId` identifies the native
conversation. Owned `run.get` exposes optional `resumedByRunId` for exact successor
discovery after a lost response or observer reconnection.

The coordinator admits a genuine proxy successor before executing the native
decision. Inbox and direct standard continuation compete on the same original
revision CAS. An existing successor must have the same immutable proxy request,
native identity and genuine answer. An admitted successor is dispatched; an
in-flight successor is observed while its native outcome commits. Neither path
runs the chat worker or repeats a claimed effect. Approve, reject, cancel and
trusted unanswered timeout use the existing native receipt mechanism. The
original JSON-RPC settles only through that successor's actual full host result
or genuine error.

The full host recorder persists the result in private proxy pending state before
completion. Recovery checks the current binding, native turn/operation and exact
accepted answer against the completed native receipt. A crash after both commits
but before the terminal protocol event recovers the same full host result without
another effect. A timeout decision command can also recover after its native
receipt commits but before its response, using only the verified canonical
timeout answer. A captured host effect without a complete native outcome remains
uncertain and fails closed; no cancellation, successful receipt or repeated tool
call is invented. GPT-6 Astra reviewed this boundary and root accepted it as
explicit safe refusal: automatic successful reconciliation of incomplete native
outcomes is intentionally unsupported, and eventual completion is not promised.
Initial and already-started chat model workers retain
native recovery; genuine proxy approval phases belong to the proxy recorder
recovery path.

Full `_meta`, multipart host content and `structuredContent` remain on the
owner-authorized MCP host channel. Canonical transcript data stays unchanged.
Standard bootstrap messages exclude exact native `host_request` turns. Historical
origin-less guest turns are recognized only by the existing server marker
(starting message ID equals turn ID; same-ID assistant `host_request` message with
`interim: 1`), never text matching. The canonical serializer reports that origin
additively, allowing the web chat view to omit guest rows while the host receives
its full result.

## Evidence

Focused tests cover real Datly effect receipts, per-item inbox operation, HTTP
command journals/exact replay/foreign denial, mixed unanswered interrupts,
completed receipt replay after expiry, timeout routing, uncertain-effect refusal,
direct-admission coordination, competing watchdog CAS, crash after the final
receipt and after successor admission, native runtime restart, keyset progress,
post-commit hints, immutable execution controls and translator trust boundaries.
The chat configured-service fixtures deliberately lack a model iteration/provider;
their resumed engine returns a real configuration error after consuming the real
tool receipt, rather than fabricating a successful model response.

The production shell's saved decision fixture recovered through the rebuilt
watchdog and completed without another decision POST. Parent browser evidence
records the real continuation separately from unit fixtures. Full production
parity is not implied by backend tests. Parent production-browser evidence separately proves approve with one effect and full iframe result, reject
with zero effects, and live/reloaded chat isolation.

Logs: `/tmp/agui-approval-focused-gate.log`,
`/tmp/agui-successor-lossless-tests.log`, `/tmp/agui-successor-runtime-tests.log`,
`/tmp/agui-approval-protocol-store-gate.log`, and
`/tmp/agui-approval-backend-gate.log`, `/tmp/agui-proxy-approval-tests.log`,
`/tmp/agui-proxy-expanded-gate.log`, `/tmp/agui-proxy-race-gate.log`, and
`/tmp/agui-proxy-protocol-store-agent-gate.log`,
`/tmp/agui-proxy-full-protocol-store-gate.log`,
`/tmp/agui-proxy-timeout-recovery-gate.log`, and `/tmp/agui-proxy-final-gate.log`
(all passed; the fast suite skips the opt-in long lease fixture). The separately
enabled `AGENTLY_TEST_LONG_MCP_APPS=1` HTTP lease test also passed 66.6 seconds in
`/tmp/agui-mcp-long-lease-final.log`.

Datly 1.0 read sources are `dql/agui/recovery/read`,
`dql/agui/provenance/read` and `dql/agui/native_turn/read`. Generated internal
components are linked through the existing application policy. Turn-run discovery reuses the existing
`dql/agui/turn/read` component. No tables, persistence formats or transcript
schemas were changed. Stock `transcribe get` used the selected v1 checkout
`ea77f67ffe83257808e1e300b8d5aaae53705b1b` and temporary current-schema SQLite
fixtures. Live MySQL deployment verification remains separate.
