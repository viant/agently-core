# Complete AG-UI implementation

Goal: implement and verify the full pinned AG-UI 1.0 contract. This is not
complete when every event merely parses, or when a small chat fixture passes.
The schema at `protocol/agui/schema-1.0.json` and matching upstream client
behavior are the versioned baseline. Existing phase-one code is the starting
point, not the acceptance boundary.

## Current milestone and later shells

The user clarified the sequence: complete the AG-UI-compatible Agently backend
and its extensions now; production web, mobile and CLI shells come later. SDK
protocol plumbing and conformance clients are part of the current validation
work, not a claim that those production shells have migrated. The upstream
CopilotKit shell remains an interoperability harness.

Retiring existing interaction routes is gated on the later shell migrations.
It is not safe to remove them while the existing clients still depend on them.

## Required evidence

### Agently capability gaps versus protocol mapping gaps

The user clarified that a "missing feature" means a capability Agently itself
lacks, not an unimplemented event adapter. The audit currently confirms three
new backend capability families, plus a partial media representation gap:

| Capability | Baseline Agently | Work |
| --- | --- | --- |
| Client-executed tools | No request-scoped external executor handoff/result continuation | Backfill runtime deferral and durable continuation; expose through AG-UI |
| Shared JSON state | Existing semantic feed patches are not a general shared JSON document or full RFC 6902 implementation | Backfill durable state, snapshots and atomic patching |
| Durable interaction replay | In-memory bus subscription with overflow notification; no durable ordered replay | Backfill journal, run identity, observer ownership and reconnect/restart recovery |
| Ordered multimodal content/provider handles | Attachments and several provider media paths exist; native message/tool history does not retain all ordered AG-UI part/source forms | Extend the existing content/history representation; preserve supported provider functionality |
| Elicitation, approvals, cancellation, queues | Existing runtime services | Map to AG-UI; preserve semantics and avoid duplicate execution |
| Reasoning and linked agents | Existing stream events and linked conversation execution | Map to standard spans/messages/subagent invocation attribution |
| Goals and scheduled continuation | Existing durable goal service, token/time accounting, pause/resume/status controls and scheduler | Preserve through versioned goal commands/resources and background subscription; not a missing backend goal engine |

This is a capability-family count, not a count of individual behaviors. Final
conformance still requires the following implementation and verification gates.

Goal parity is explicit: deterministic goal get/create/update/clear and
pause/resume operations, budget/accounting fields, status reasons, controller
schedule and wakeup behavior must cross the extension contract without routing
management through a model. Background goal updates require a subscription
lifecycle distinct from any completed foreground chat run.

| Requirement | Evidence required |
| --- | --- |
| All 31 event variants and all request/message/content/outcome fields | Lossless round trips, positive/negative schema fixtures, generated definition coverage |
| Ordering and attribution | Real stream tests for interleaving, message/tool boundaries, parent ownership, subagents, steps, reasoning and terminal exclusivity |
| Client tools | Actual client definition -> model call -> external execution -> result continuation, including several calls and duplicate delivery |
| Interrupt/resume | End interrupted run, identify pending input, validate answer, start a distinct run, continue the same logical turn without re-executing completed tools |
| State/activity/history | Snapshots plus all RFC 6902 operations, authoritative history projection, opaque metadata/content preservation, conflict/error handling |
| Multimodal | Text/image/audio/video/document source forms through SDK and appropriate existing backend/provider input mappings; no lossy stringification |
| Recovery and control | Disconnect, targeted cancel, duplicate admission, overflow, reconnect and restart recovery with explicit capability declarations |
| SDKs | TypeScript, Swift and Kotlin share fixtures; POST SSE fragmentation, UTF-8, schema/sequence errors and final protocol state are tested |
| External UI/backend | Upstream HttpAgent and CopilotKit consume real assembled-backend flows; existing passing mock-only checks are insufficient |
| Later shell/cutover gate | Preserve the existing operation inventory and extension mapping for later web/mobile/CLI migration; do not claim production shell parity from protocol conformance |

## Architecture decision requiring consultation

The current boundary only observes one `Query` invocation. Internal turn IDs are
derived from the initiating message, while AG-UI run IDs are supplied independently.
Current elicitation can wait in-process or return a deferred request; watchdog
recovery resumes an existing internal run. None provides a durable mapping of
several AG-UI runs to one interrupted logical turn. In-memory bookkeeping would
lose the mapping on restart and permit duplicate continuation/tool execution.

Proposed design:

1. Add durable protocol records in the existing conversation database, accessed
   through its existing storage/service architecture. An AG-UI run record owns
   external run/thread identity, parent-run attribution, internal turn binding,
   effective principal, status, continuation revision and pending interrupts.
   Protocol state and an ordered event journal use that binding. No separate
   database process or second public protocol is introduced.
2. Admit/claim a run and a resume revision atomically. A repeated request with
   the same accepted identity reattaches/replays its result; it never executes
   the tool or turn again. Reject conflicting input rather than overwriting it.
3. A client tool is a request-scoped tool definition. Do not register it globally
   or silently execute a same-named backend tool. Persist its planned call,
   arguments and expected result identity, end the protocol run at the handoff,
   then continue from submitted tool messages in a new protocol run.
4. Deferred elicitation/approval ends an AG-UI run with an interrupt outcome.
   Standard `resume` answers are validated against the pending request and
   atomically consumed. Reuse the existing ReAct loop/recovery checkpoints to
   resume the logical turn rather than routing a fabricated new user prompt.
5. Keep Agently's saved conversation history authoritative, as agreed. Build a
   lossless AG-UI message projection and retain external/internal aliases.
   Shared state is a distinct JSON document; activity and rich-content objects
   must not masquerade as assistant text. State revisions are persisted with
   run events so snapshots and replay describe the same accepted state.
6. Preserve disconnect-as-detach. Targeted cancellation explicitly addresses an
   admitted run and cancels its bound execution. Reconnect reads the journal
   through the agreed extension binding without inventing a new chat run.

The user approved this internal lifecycle/persistence change on 2026-10-02 and
explicitly required Datly 1.0 for every database operation. New components and
transactional orchestration use that existing native component runtime; direct
SQL is not an implementation substitute.

## Current backend milestone (2026-10-02)

The backend milestone now includes generated schemas for all 31 AG-UI 1.0 wire
events, pinned to `@ag-ui/core` and `@ag-ui/client` 1.0.1; a native durable
Datly 1.0 journal; scoped public tool IDs; frontend, root and nested client-tool
handoff; goal and shared-state resource commands; workspace, datasource, lookup
and feed operations; and `run.get`, `run.events.list` and `run.cancel`.
Detached runs carry `parentRunId`. Forge code-fence extraction happens at the
backend boundary, before legacy TypeScript client parsing. The official
CopilotKit shell is an interoperability harness for the assembled backend.
Production web, mobile and CLI shell migration remains later work.

This is a backend milestone, not a complete goal/conformance or backend-parity
claim. Approval and graph integration now have focused native persistence,
recovery, and assembled-client evidence; the complete core repository suite
passes after the final fixes. Interactive MCP
Apps automatic app-scope resolution was approved and implemented on 2026-10-03 as described in
[`ag-ui-mcp-app-scope-plan.md`](ag-ui-mcp-app-scope-plan.md). Explicit app
binding APIs and full host-result capture are present; preserve `_meta` and keep
captured host results out of model history. The local
`../mcp-protocol-ag-ui` replacement is currently required because the pinned
published DTO drops arbitrary `_meta` entries.

Verification evidence is deliberately bounded: MySQL 8.4 `newAGUI`-table
two-runtime lease/resume race checks and exact JSON behavior were verified.
The full legacy-schema bootstrap hit a preexisting invalid `op_id` index and
was not validated. Do not interpret that failure as a successful bootstrap.
No commits have been made; local builds may need the sibling worktree module
replacements to resolve the in-progress implementation.

| Area | Status |
| --- | --- |
| Phase-one adapter and basic assembled-backend chat/tool proof | Implemented; historical snapshot described in [`ag-ui-phase1.md`](ag-ui-phase1.md) |
| Pinned wire schema and generated definitions | All 31 event variants generated from AG-UI 1.0; schema round-trip coverage exists |
| Durable backend journal and resource operations | Implemented with Datly 1.0; focused MySQL 8.4 race/JSON evidence only |
| Client-tool handoff and detached parent attribution | Frontend/root/nested handoff and `parentRunId` implemented; assembled frontend continuation/restart and native graph/recovery regressions pass |
| Goal/state and workspace/data/lookup/feed commands | Implemented as resource commands; assembled goal fields/accounting/budget/scheduled execution, state patching, observer crash/replay/overflow and scoped controls verified; wider operation inventory remains open |
| Approval and graph integration | Focused durable receipt/claim, live-waiter, graph recovery and assembled approve/reject/effect replay checks pass; complete core suite passes |
| Interactive MCP Apps automatic app-scope resolver | Implemented and verified: scoped aliases, current authorization, native approvals, private receipts, replay/restart and builtin renderer |
| Official CopilotKit interoperability harness | Available for assembled-backend interoperability; production shells are later |
| Current AG-UI 1.0 backend/SDK milestone | Implemented and verified; evidence and explicit limits in the completion audit |
| Later route and production shell parity | Remains the later migration/cutover milestone |

No commits have been made for this milestone. Build instructions that consume
the implementation may need the sibling worktree replacements to remain
available until upstream module versions are published.

Final core gate: `go test -json -p 4 ./... -count=1` passes 180 packages with no
failures, using the documented temporary Fontconfig configuration to exercise
the optional installed PDF renderer. The assembly's dedicated AG-UI mock-backed
proofs pass; its broader legacy live-provider CLI suite still has four failures.
Exact results, conditions and limitations are recorded in
[`ag-ui-backend-completion-audit.md`](ag-ui-backend-completion-audit.md).
