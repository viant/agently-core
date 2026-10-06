# Backend completion audit, updated 2026-10-03

Scope: the current backend/SDK milestone in `ag-ui-complete-plan.md`.
Production web/mobile/CLI shells remain a later milestone. Parsing all 31
variants, or passing a mocked chat stream, does not complete this milestone.

## Current gating item and claim limits

| Priority | Finding | Exact source/evidence | Required closure |
| --- | --- | --- | --- |
| Closed 2026-10-03 | Interactive MCP Apps scoped binding | Approved design implemented following GPT-6 Astra consultation; current journal binding, native policy/approval, private receipts, retry suppression, renewal, assembled restart and builtin renderer proof pass. | Final combined verification recorded below; production shells remain later. |

The confirmed concurrent-history loss and child registration/recovery gaps were
fixed after approval. They remain mandatory regressions. Broader evidence limits
are listed below rather than converted into a full conformance claim.

## Correctness fixes completed during this audit

- Shared thread history now persists only messages the current producer changed
  against its last accepted local baseline. Other producers' fresh updates stay
  intact; different changes to the same identity conflict. Two real simultaneous
  HTTP POST/SSE streams with an actual Datly journal and MemoryBus preserve both
  histories and exact replay without another Query. Late journal-trigger failure
  rolls back shared history, run sequence/revision and the producer baseline.
- A trusted `CUSTOM agently.invocation` registration checkpoint commits before
  child dispatch without claiming it started. Restore validates immutable native
  parent/source identity; generic standard bridges cannot forge the framework
  ancestry namespace. Native child recovery reconciles canonical messages and
  results, starts only from execution evidence, and closes deepest-first before
  the root. Actual Datly fixtures cover registration-before-first-event, an open
  child journal while the native root succeeded, and reused child conversation
  metadata. Missing/queued/running children do not invent terminals; unchanged
  inspection does not append repeated snapshots.
- Schema-declared integer fields accept mathematical integers written as decimal
  or exponent JSON numbers. Generated models retain their public int64 API and
  normalize exactly within the schema safe range. Fractions, unsafe bounds and
  overflow reject; open Raw JSON metadata is untouched. Every generated integer
  object and nested usage/capability union has codec coverage. All four generated
  artifacts reproduce byte-for-byte.
- Root and recovery MemoryBus overflow now detach the protocol observer instead
  of committing a permanent native `RUN_ERROR`. The journal stays running; the
  stopped worker's bounded lease expires, and a later attachment restores
  authoritative native persistence without another Query. Actual Datly/native
  graph plus real MemoryBus tests prove this for initial and recovery observers.
- Queued runs inspect canonical native status and pending input. Model/stream
  boundaries cannot finish a queued turn. Polling detects `waiting_for_user`,
  which emits no canonical native terminal event, and produces the correct
  frontend-tool or approval handoff. The regression persists turns, message
  graphs, payloads, calls and approvals through native Datly components.
- Child reader overflow propagates observer loss to the journal owner and does
  not turn a successful native invocation return into a business error. It
  leaves a recoverable running journal; canonical native child reconciliation
  is now covered by the checkpoint regressions above.
- Detached native context already used `WithoutCancel`; a regression now proves
  observer overflow/exit leaves the native context alive and the detached
  journal running with its original `parentRunId`.
- Native `succeeded` status is recognized by protocol recovery.
- Ordered content metadata uses `UseNumber` both when mapping protocol parts
  and reloading persisted user content. Exact integers above 2^53 and long
  decimals survive native user/tool history binding.
- The reducer's public active text/tool lane views retain raw optional
  attribution for journal producers. Its separate retained ownership map
  preserves absent versus explicit empty IDs. A genuine native error with open
  text/tool lanes now has a regression for correct end-event attribution.

Relevant test files: `sdk/agui_observer_lifecycle_test.go`,
`sdk/agui_child_recovery_test.go`, `sdk/agui_parallel_http_test.go`,
`sdk/agui_message_updates_test.go`, `sdk/agui_concurrent_history_repro_test.go`,
`protocol/agui/translator_invocation_checkpoint_test.go`,
`protocol/agui/wire_integer_test.go`, `runtime/clienttool/content_test.go`, and
`service/agent/client_tool_content_test.go`.
The queue test uses a deterministic Query driver and actual Datly/native
persistence/bus. It is not a claim that the assembled scheduler/model pipeline
has independently passed every queue case.

## Current milestone acceptance

The parent run reports completed native 35/66 SDK fixture acceptance, including
the final compressed payload-reader/history restart fix, exact metadata clones,
and configured control/capability gates. Those results supersede the earlier
narrow evidence inventory. The full legacy MySQL bootstrap limitation remains
explicitly documented in `ag-ui-complete-plan.md`; targeted new-table MySQL
acceptance does not validate that unrelated legacy bootstrap.

Interactive MCP Apps app-instance scope resolution was approved, implemented,
and verified on 2026-10-03 as recorded below. Unbound or unauthorized proxy
requests still fail closed. The assembled backend was exercised through the pinned upstream
HttpAgent for chat, backend tools, frontend-tool continuation, approval/rejection,
goal/state resource operations, cancellation, and handoff restart/replay. The
actual CopilotKit 1.77.0 browser harness separately rendered chat, tool cards,
discovery, and fragmented Forge content without exposing authoring JSON. Browser
coverage does not extend to approval, resource, or restart UI interactions;
those proofs use the upstream protocol client. Runnable evidence is in the
assembly project's `dev/ag-ui/README.md` and its smoke scripts.
Production shell migration and operation inventory
remain governed by the parent's current milestone, rather than older phase-one
scope statements.

Recent bounded corrections also preserve inherited raw frontend definitions in
detached run inputs and apply the same rich-content boundary during live root
and child recovery. Native authoring checkpoints reconstruct raw byte offsets;
an unavailable/conflicting raw prefix defers fragments until a full snapshot
instead of exposing unprojected Forge JSON. Actual Datly/MemoryBus regressions
cover overflow followed by fragmented Forge recovery and a live child stream,
with no repeated Query. Raw tool parameters and metadata survive detached
admission, including exact integers above 2^53.

## Conformance claim limits

The Go consumer has 35 expectations generated by the actual pinned client,
including an accepted all-31 stream. The documented server validity policies
remain stricter on run identity, failed patches and activity object content;
chunk timestamps remain lossless in Go. See `ag-ui-reducer-conformance.md`.

The current server persists the complete raw input. The legacy execution tool
shim now retains raw metadata and the scoped session/pending-call path copies it
without floating-point conversion. Detached records preserve the original
standard tool definitions, rather than only a reconstructed model-facing subset.

This audit does not mark full backend conformance or application parity
complete. No commits were made.

## Latest verification

Full `go test -race ./sdk ./protocol/agui ./runtime/aguistate
./runtime/clienttool -count=1` passed after the causal checkpoint, child recovery,
integer codec and parallel HTTP fixes (SDK: 88.99 seconds). This precedes the
independently assigned compressed payload-reader restart fix; that change needs
its own stable generation/build and acceptance evidence before a final claim.

The final bounded rich/detached regression run passed under the race detector
(9.70 seconds). The complete payload contract suite passed after adopting exact
bytes and strict corrupt-gzip failures without weakening writer, selector,
authorization or rollback assertions.

The subsequent combined `go test -json ./... -count=1` completed with 177
passing packages and four failed tests in three packages. The saved result is
`/tmp/agui-final-go-20261002.jsonl`. Two manually constructed investigation
deletion fixtures lacked schema evidence for the four new protocol tables;
their explicit table inventory was corrected and the full `app/store/data`
suite subsequently passed (34.80 seconds). The detached goal continuation
fixture was corrected to share native conversation/goal/queue storage and to
compile its cold readers/writers before the unchanged three-second assertion
window. Eight race repetitions now pass while also checking a committed queued
turn and both goal notifications. The unchanged optional PDF renderer test also
timed out after 30 seconds and reproduced in isolation with the installed
bundled `pdftoppm`. This run is not a successful full-repository acceptance.

The renderer passes using a temporary Fontconfig file with the actual system
font directories; the bundled default additionally scans macOS asset directories.
The subsequent combined suite uses `FONTCONFIG_FILE=/tmp/agui-fontconfig-20261002.xml`
and `go test -json -p 4 ./... -count=1`, without skipping the renderer test.
Its result is saved in `/tmp/agui-combined-go-final-20261002.jsonl`.
That run completed with 179 passing packages; its sole failed package was
`service/agent`, where another notification fixture timed out before its
transition attempt. That fixture now precompiles its native readers and checks
errors before retaining the original three-second callback window. Five race
repetitions of both async goal cases pass, including the failed-transition
no-notification assertion (`/tmp/agui-async-goal-notifications-stable-repeat.log`).

The final combined core run **passed** after both fixture corrections, cached
usage isolation, Ollama terminal usage, scheduler binding, and all three new
public goal HTTP tests. The exact command was
`FONTCONFIG_FILE=/tmp/agui-fontconfig-20261002.xml go test -json -p 4 ./... -count=1`.
Result: 180 passing packages, 6,455 passing test/subtest records, 51 skipped
test/subtest records, zero failures; 215 packages have no tests. The authoritative
log is `/tmp/agui-combined-go-final-verified-20261002.jsonl`. This supersedes the
failed combined runs above; it does not close the pending MCP Apps architecture
decision or claim later production shell migration.

The subsequent assembly-wide Go run is **not green**: 10 packages pass, while
the legacy live-provider CLI query package has four failures (image reference
rejected by Chat API, disabled file listing, invalid-token expectation, and coder
startup with a missing reporting root). Its authoritative log is
`/tmp/agui-assembly-go-final-20261002.jsonl`. Read-only triage found no changes to
those assembly tests, CLI attachment conversion or reporting bootstrap relative
to HEAD. The legacy `image_file` emission and file-listing guard also predate
this adapter work; the precise auth/bootstrap causes remain unverified. These
failures must not be presented as an assembly-wide pass. The dedicated AG-UI
assembled proofs above exercise the current source with a local mock provider;
they do not claim legacy CLI or deployed authentication parity.

Conversation cleanup now has native Datly reader/writer manifest entries for
all four protocol tables. Focused integration checks cover root/child cleanup,
foreign identity collisions, active resource leases, expired lease fencing,
unclaimed admissions and rollback after a later native deletion failure. The
cleanup stays inside the existing authorized conversation-tree transaction.

Expanded assembled goal HTTP acceptance passed against a freshly rebuilt
backend with 74 official-schema events for cancellation and 72 for subscription
expiry. It checks identity, budget/controller fields, initial accounting,
objective/budget sparse updates and zero, pause/resume reasons, observer updates
through resume/complete/clear, exact mutation replay, conflicting input rejection,
and unchanged native transcript/shared state. Explicit null budget updates are
rejected by the current contract and leave the saved budget intact. The logs are
`/tmp/agui-goal-http-acceptance.log` and
`/tmp/agui-goal-http-expiry-acceptance.log`. These runs do not prove nonzero
accounting or scheduled controller execution.

The public two-runtime goal subscription crash regression subsequently passed
(60.59 seconds; `/tmp/agui-goal-http-takeover-test.log`). An owned child backend
is killed after admission; a second native runtime opens the same workspace and
waits for actual lease expiry. The identical accepted request replays its prefix,
observes a new native goal update after takeover, rejects foreign cancellation,
accepts owner cancellation and replays the exact terminal history. There is only
one `RUN_STARTED`. No journal status or lease timestamp is edited for this proof.

Public overflow acceptance uses actual MemoryBus overflow caused by committed
native goal updates. Three race repetitions pass
(`/tmp/agui-goal-http-overflow-race-proof.log`). Overflow terminates that resource
run with `GOAL_SUBSCRIPTION_ERROR`; identical input replays the exact error,
while a new subscription run receives authoritative state and live updates.
This terminal policy differs from reclaiming a still-running crashed observer.

The clean assembled accounting proof passes after provider-cache warmup without
any goal-management mutation: 15 tokens against budget 10 becomes
`budget_limited` and automatically reaches the independent subscription. A real
1.2-second provider delay also verifies at least one elapsed accounting second.
Both runs validate 28 wire events against the official schema
(`/tmp/agui-goal-automatic-accounting-proof.log`,
`/tmp/agui-goal-automatic-elapsed-proof.log`). The invocation wrapper preserves
configured provider callbacks, optional streaming/backoff and anchor support.
Ollama's terminal stream event now retains its final usage. Sequential and
concurrent finder/provider race regressions pass; provider/core/modelcall suites
also pass.

Scheduled model execution acceptance subsequently passed with 68 official-schema
events (`/tmp/agui-goal-scheduler-proof.log`). `backendClient.SetScheduler` now
binds the existing scheduler to the existing agent, and explicitly removes that
binding when configured with nil. A public goal with a two-second wake delay
produces a pending schedule and a real native scheduler model continuation
without another chat POST; accounting reaches 30 tokens and `budget_limited`.
The proof checks a succeeded native run with its actual schedule identity,
then verifies that a separate paused goal stays unchanged through a complete
35-second polling window. No new scheduler service or schema was introduced.

## MCP Apps final acceptance, 2026-10-03

The approved binding gate is closed. See `ag-ui-mcp-app-scope-plan.md` for the
implemented invariants, executed native and assembled proofs, and supported host
configuration limits. The complete core suite after the MCP implementation
passes 180 packages and 6,487 test/subtest records, with 52 skips and zero
failures (`/tmp/agui-mcp-combined-go-20261003.jsonl`). The long lease test was
separately executed despite its opt-in flag: 65.10 seconds, actual HTTP effect
count one, lease renewal and competing claim rejection. Source preserves scoped
identity, actor/tool policy, hidden host data and uncertain outcomes.

The official CopilotKit iframe renders the actual MCP resource and returns its
tool callback through the AG-UI forwarding adapter. Application network capture
contains three successful `/v1/ag-ui/run` POSTs, with empty model inputs and
`threadId == runId` on both proxy calls. Console has zero errors and the upstream
Lit/sandbox warnings. Shell typecheck/production build passes. The tested fixture
is trusted and credential-free; this does not establish production untrusted
iframe origin isolation. Broader legacy CLI failures remain documented above.

## Final backward compatibility evidence, 2026-10-03

The latest complete core run passes 180 packages, 6,487 test/subtest records,
52 skips and zero failures, with the documented Fontconfig configuration
(/tmp/agui-core-compat-final-20261003.jsonl). Existing SDK suites pass:
TypeScript 424 tests/5 opt-in skips plus typecheck/build, Swift 116 tests/4
opt-in skips, Android 124 tests/4 skips.

Authenticated Steward checks through the existing HTTP SDK pass against an
isolated metadata copy and fresh runtime. OOB uses PKCE and the deployment's
full scope set. Legacy session/identity, agents, conversations and web/iOS/Android
metadata targets pass; four AG-UI discovery/workspace streams validate all nine
events; legacy identity remains valid afterward. Evidence:
/tmp/agui-steward-sdk-oob-compat-20261003.log. The reusable helper is
../agently-ag-ui/dev/ag-ui/steward-compat/main.go. Credentials/tokens are never
printed; no original runtime/database was used, no live model was invoked and
no remote business mutation was performed. Native device UI and every remote
MCP operation remain outside this proof. The four broader legacy CLI failures
and full legacy MySQL bootstrap limitation described above remain explicit.
