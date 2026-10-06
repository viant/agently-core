# Web UI migration to AG-UI

Status: web migration accepted, 2026-10-04. AG-UI is the production web UI's
primary interaction protocol, with Agently extensions and existing BFF
application services preserving the native experience. Mobile UI migration is
a subsequent stage; existing mobile SDK compatibility is verified below.

## Current acceptance status

| Requirement | Current evidence |
| --- | --- |
| Native primary chat, follow-up and reload | Production-web flows verified through AG-UI; native transcript remains authoritative. Concurrent initialization revision conflicts are fixed: simultaneous clients on an existing thread received RUN_STARTED in 106/149 ms, with normal serialized native execution. Agent/model selection survives first submit, remount, follow-up and cold reload. |
| Independent public backend in the same shell | Two real LangGraph Dojo turns through authenticated BFF; no native history/context/extensions in outgoing messages; system context hidden. |
| BFF session lifecycle | Real browser logout shows the sign-in screen; auth/me, backend discovery and an attempted AG-UI run return 401. Sign in opens the configured Viant login form. A new authorized OOB-backed BFF session restores 200 responses and the native shell. |
| Background native client work | Idle visible web conversation receives native work and reconciles its canonical user row. A fresh official AG-UI client run also appears in the mounted production UI without reload; post-commit notification/reconciliation regressions pass. |
| Cancellation | Real BFF cancel returned true; AG-UI cancelled outcome and cancelled row survive reload. |
| Chat approval continuation/recovery | Saved completed decision recovered after server restart without another decision submission. |
| Standard MCP Apps | Rendering, full host result, isolated tool RPC and origin sandbox verified. Real iframe → inbox approval → genuine successor result passes with one tool effect; rejection has zero effects. Native chat isolation passes live/reload. Timeout, restart, competing-decision, revoked-binding, and lease-duration regressions pass. Incomplete native receipts remain explicitly uncertain with no automatic repeat. |
| Reports and workspace/tool feed | Progressive report verified. Earlier DOM-only feed checks were insufficient. Inline rendering and detached, docked/rail, and promoted-workspace visibility/update/reload checks now pass. Recorded lifecycle recovery and unknown-status cache preservation pass regression tests and Astra review; unrecorded native transitions are not inferred. |
| Existing mobile SDKs | Swift: 112 unit passes plus 2 live passes. Android: 120 unit passes per debug/release variant plus 2 live passes in each. Opt-in/device limitations documented separately. |
| Attachments | Actual web upload, staging promotion, AG-UI chat reference, canonical reload, and same-origin file retrieval pass for a synthetic text file. |
| Automation management | Existing Automation window, agent lookup, disabled draft save, list, and reopen after browser reload verified. Timer execution and authenticated visibility are separate from this local UI check. |
| Goals | Existing editor/create, feed resume/pause, objective edit/save, single-form cold reload, and clear pass in the browser. Session principal and feed-context conversation identity are preserved. |
| Queue/steering and final regressions | Repeated-identical steering passes terminal/reload browser checks with exact persisted IDs and one row per submit. Queue up/down changes live order and survives reload. Saving uses one coherent editor identity/content snapshot; the full 698-character edited request matches reload and actual model input exactly, with one execution and successful completion. Three shared-Forge failures were present in the pre-migration baseline. |

This table is the current gate summary. The entries below retain development
evidence and previously discovered defects; older failures subsequently fixed
are not outstanding merely because their investigation is recorded here.

Latest consolidation: the TypeScript SDK suite passes 520 tests with five opt-in
skips, and typecheck passes. The web suite passes 1,080 tests; the same three
pre-migration shared-Forge metadata/permission tests fail. Logs:
`/tmp/agui-sdk-final-queue-freshness-suite.log`, `/tmp/agui-sdk-queue-final-typecheck.log`,
and `/tmp/agui-web-final-editor-identity-suite.log`. These web results include the
final coherent Queue editor fix.

Queue Move/Edit now await `reconcileAgUiConversation` before refreshing view
datasources. Unlike a coalesced refresh, reconciliation waits for an older
in-flight read and then reads again after the command has committed. The
transport regression distinguishes old `[a,b]` ordering from committed `[b,a]`
ordering, and real handler tests verify the datasource refresh waits for that
result. This closes the post-command stale-read race without optimistic ordering.

Final Queue backend checks pass: generated Datly reader membership excludes
completed/canceled native turns and mismatched conversation/turn references;
up/down moves preserve atomic native/queue ordering and caller-owned transaction
commit/rollback. The generic reader retains its existing default behavior.
Canonical enrichment exports current positions as decimal `queueSequence`
strings, preserves legacy `queueSeq`, and emits the existing scoped availability
hint only after a successful Move. Logs:
`/tmp/agui-queue-reorder-membership-gate.log` (3.830s) and
`/tmp/agui-canonical-queue-projection-tests.log` (1.472s).

The final concurrency repeat found a separate rollback issue: decoding saved
projection JSON into existing maps retained an uncommitted text-start lane.
Rollback now reconstructs the projection from a zero value. A deterministic
first-text CAS collision checks one execution, one committed start and successful
completion. Five-repeat startup/projection checks pass (5.410s), and the final
race selection passes (6.840s): `/tmp/agui-queue-canonical-startup-repeat.log`
and `/tmp/agui-queue-startup-race-final.log`. Final assembly build:
`/tmp/agui-web-queue-final-20261004`. A final live simultaneous-client check on
that binary also passed: RUN_STARTED in 266/263 ms, success in 792/1,217 ms,
with no worker conflict (`concurrent-final-one.log` and
`concurrent-final-two.log` under assembly `output/playwright/agui-presentation`).

Final Queue browser evidence is in assembly
`output/playwright/agui-presentation/`: `queue-final-up-live.txt`,
`queue-final-up-reload.txt`, `queue-final-down-live.txt`, and
`queue-final-down-reload.txt` show the same native IDs changing order and
persisting it across reload. A later edit exposed refresh changing selection
while the editor retained another turn. Save now takes ID and content from one
coherent submitted or bound form snapshot; incomplete identity refuses to write.
Regression tests deliberately combine editor B with selection/form A.

The fixture retained its original preview-bound Queue definition to verify the
legacy editor normalization. Native turn
`1857c859-2370-41f5-a5c0-edd094747102`, protocol run
`3d059ad6-6bdb-4a5d-a0f6-8cab1652f692`, saved and reloaded all 698 characters,
then completed successfully at 2026-10-04T17:29:22.012Z. Root independently
compared `queue-full-editor-patch-body.txt`,
`queue-full-editor-reload-value.txt`, and the captured actual model input in
`queue-full-editor-execution-proof.json`: exact equality and one execution.
`queue-coherent-editor-reloaded.png` shows the intended native turn selected
without an error toast.

After the rollback correction, the scoped SDK recovery, approval, MCP proxy,
feed, shared-message, durable-run, startup and Queue regressions pass in 24.313s
(`/tmp/agui-queue-final-backend-regressions.log`). Full aguistate, AG-UI protocol,
extension, store and Datly manage package tests also pass
(`/tmp/agui-queue-final-protocol-store-regressions.log`). This is a scoped SDK
selection, not a claim that every repository package was tested.

Completion review checked the current implementation, this acceptance matrix,
browser artifacts, exact queued execution input, and test outputs. A separate
GPT-6.1 Sol source/evidence audit found no additional migration gap. Both working
trees remain on branch `ag-ui`. Test credentials were removed and the final
authenticated and native browser fixture servers were stopped after their runs
completed; fixture workspaces and evidence remain available. No deployment,
upstream certification/outreach, or mobile UI migration is claimed.

Concurrent-start evidence is in assembly
`output/playwright/agui-presentation/concurrent-fresh-start-fast.log` and
`concurrent-fresh-start-delayed.log`. Both requests started at
2026-10-04T16:17:44.393Z. The first completed in 499 ms; the second entered the
native queue in 194 ms and began native execution in 492 ms. Its later fixture
delay is intentional, not the previous lease-recovery delay. The earlier
conflict evidence remains historical evidence of the fixed defect.

Final BFF lifecycle evidence (isolated Steward shadow, 20531) is under assembly
`output/playwright/agui-primary/`: `bff-logout-check.txt`,
`bff-unauthorized-run-check.txt`, `bff-login-navigation-check.txt`, and
`bff-session-restored-check.txt`. Logout was invoked through the actual user
menu. Login navigation was checked without submitting credentials in the form;
session creation used the previously authorized OOB PKCE helper and real BFF
session endpoint. No access tokens or session-cookie values are in these
artifacts. This checks browser session behavior, not every identity-provider
interactive login variant.

## Required outcome

Steering and independent-client evidence: assembly
`output/playwright/agui-presentation/steering-fixed-identities.txt` records three
distinct persisted message/request IDs for one initial message and two identical
steering submissions. `steering-fixed-reload.png` retains exactly those three
user rows. `independent-client-fixed-result.txt` records successful run
`23af7db0-7d86-4f2d-a2a9-69412f219312`; the mounted production UI received its
third-client marker without reload (`independent-client-visible-without-reload.png`).

That fresh official HttpAgent was initialized with only the newest user message.
Its local reducer retains the position of IDs it already knows, so its final
message array placed that user ahead of restored history. A separate wire probe
confirmed Agently's actual MESSAGES_SNAPSHOT orders previous history before the
new user (`output/playwright/agui-primary/history-order-wire-check.txt`). Native
web rendering uses its canonical conversation projection and is correctly
ordered. The discovery proof is not a claim that every reference-client local
merge strategy reorders partial initial history to match the server snapshot.

Attachment browser evidence: conversation `4573ab1d-4201-4100-a04d-6d38fd7408e6`
uploaded `agui-web-attachment-check.txt` through the existing dialog. Native file
promotion completed before the AG-UI chat request; the versioned Agently payload
contained the conversation-scoped file URI. The filename survived reload, and
file retrieval returned the original synthetic bytes. Assembly artifacts under
`output/playwright/agui-primary/`: `attachment-run-request.json`,
`attachment-reloaded.png`, and `attachment-download-check.txt`. The deterministic
model supplies transport/presentation evidence, not a claim about model document
comprehension or every media format.

Automation UI evidence: the existing Automation workspace and agent lookup saved
disabled draft `17dce2fc-2093-46c6-ad4d-62275fee6b2a`, then listed it after browser
reload and reopening the workspace. `automation-draft-saved.json` records
`enabled:false`; `automation-draft-list.png` and `automation-draft-reloaded.png`
show the existing table. No timer was enabled or fired. This used the local
anonymous fixture; its existing scheduler intentionally normalizes anonymous
private inserts to public (`TestHandler_BatchUpdateAnonymousPrivateInsertBecomesPublic`),
so this check does not establish authenticated private visibility behavior.

The existing production web UI uses AG-UI as its primary agent interaction
protocol while preserving its layout and current functionality: conversation
history and controls, execution/tool feed, workspace windows, Forge reports,
MCP Apps, approvals, goals, scheduling and attachments. A second, independent,
publicly known AG-UI backend must work in the same UI for the capabilities it
supports. An Agently-only mock or the external CopilotKit harness does not prove
that the production UI is portable.

Existing BFF authentication is mandatory. Browser requests remain authenticated
to the BFF with the existing session. Downstream credentials stay server-side;
Agently session cookies must never be forwarded to an independent backend.

## Design decision after GPT-6 Astra consultation

1. Keep the portable AG-UI reducer/client separate from versioned Agently
   extensions and the web SDK compatibility facade. Preserve existing renderers
   and project protocol state into their view models without a visual redesign.
2. Standard AG-UI messages, state and lifecycle are authoritative for live agent
   interaction. Rich Agently presentation requires explicit metadata/activities;
   do not flatten multipart messages, host results or rich content into text.
3. Commands and observers use independent protocol runs, never model prompts.
   Preserve durable request identity, distinguish transport disconnect from
   execution cancellation, and report uncertain effects rather than reexecute.
4. The user explicitly permits other mechanisms for preserving application
   functionality. Existing BFF application services can remain for management,
   reporting, UI bridge control, authentication, assets and binary transfer.
   This is not a requirement to tunnel every HTTP endpoint through AG-UI.
   Primary chat must not silently fall back to the legacy query/stream routes.
5. Bootstrap, reload and observing work started by another client are required.
   Use authorized history/bootstrap and active-run discovery/attachment;
   an in-memory original POST alone is not enough. Replaying must not execute a
   submitted turn or tool effect twice.
6. Independent backends are selected through trusted BFF configuration with
   separate credentials and capability profiles. Generic connections must not
   receive Agently commands or private workspace state. Unsupported controls are
   unavailable explicitly, rather than appearing to succeed.
   Foreign standard-backend metadata must not be treated as authority to invoke
   local Agently workspace/MCP/report services; extension readers and host actions
   are scoped to the selected trusted connection profile.

## Acceptance gates

Latest goal UI proof uses the real authored Forge toolbar. Adding the trusted
conversation ID to root/sub feed contexts makes its actual goal handlers target
the owning conversation (12 feed-context tests pass). Resume showed active;
Pause showed paused; saving the edited objective survived reload with one goal
form; Clear returned 204 and removed the form. Artifacts in assembly
`output/playwright/agui-primary/`: `goal-toolbar-resumed.json`,
`goal-toolbar-edited-reloaded.png`, `goal-toolbar-reload-check.txt`, and
`goal-cleared-check.txt`.

Detached feed proof uses an independent fixture at 20561 with the same authored
synthetic feed configured `presentation.target: overlay`. Real tool calls render
and update the table. Source CSS repairs put the overlay above shell chrome and
give the existing Forge flex hierarchy a definite size; native Close and reopen
work. Reload restored one overlay and one updated row with an 80px visible table
viewport, not merely hidden DOM cells. Promoting to the workspace then reloading
also renders the updated table with the hosted rail variant. Artifacts:
`feed-detached-fixed.png`, `feed-detached-updated.png`,
`feed-detached-reload-check.txt`, and `feed-workspace-restored-visible.png`.
The production Vite bundle was built from current source into the owned
`/tmp/agui-detached-ui-20261004` directory to avoid conflicting with another
agent's shared-dist build. Inline/rail and replay badge checks remain separate.
The actual Restore split view control retained the visible table and exposed
the composer; closing the promoted workspace returned to one detached overlay
with the updated row. See `feed-workspace-split-visible.png`,
`feed-returned-overlay.png`, and `feed-detached-network.txt`. The workspace's
“2 new” indicator was traced to the preexisting chat unread-row counter in
Root/ConversationWorkspaceSurface, not feed updates or repeated effects; it is
not evidence of a replayed workspace action.
Native docked/detached feed components now also receive the selected-backend
active flag. Their Blueprint portals cannot be hidden by a native ancestor's
`display:none`; rendering the portal only for the native selection preserves
component state while preventing it from covering a standard-backend chat.
Sixteen feed-surface and standard-host-isolation tests pass for this boundary.
The separate rail fixture at 20571 exposed a shell condition that hid chat chrome
when the preferred workspace mode was full even with no workspace open. Full
mode now applies only when a workspace pane exists. The actual rail renders its
table, updates through a real tool call, restores one updated row after reload,
and closes/reopens normally. Evidence: `feed-rail-fixed.png`,
`feed-rail-updated-reloaded.png`, `feed-rail-reload-check.txt`, and
`feed-rail-reopened-check.txt`. Root's 41 shell tests pass after the condition fix.

Combined validation snapshot: TypeScript SDK 517 passes, five opt-in skips,
typecheck clean; full web suite 1,075 passes and the three established shared
Forge permission-feedback failures (missing `windowMetadataFetchKey`, missing
`withWindowPermissionDeadline`, and existing timeout text mismatch). These are
recorded separately from current migration checks; they are not reported green.

GPT-6 Astra reviewed the completed feed reader and the unknown-state view path.
The view now retains lifecycle markers across content-only snapshot omissions,
so active → unknown → snapshot sync, and unknown → empty snapshot → legacy
content, cannot restore an unsupported active claim. Unknown does not create a
new active feed or invent inactivity; existing content is labeled as last known.
The review's restoration blockers are closed. Recorded-fact recovery does not
claim to capture transitions that no observer ever recorded.

Final inline recheck uses assembly
`output/playwright/agui-presentation/feed-inline-final-clean.png`: reload and a
new real feed update render the updated table with an 80px viewport, Ready state,
and no error toast. Root visually inspected this replacement. The earlier
`feed-inline-restored-visible.png` contained an unexplained conversation-not-found
toast and is retained as historical evidence, not the final clean acceptance.
The opt-in long MCP fixture was also run with
`AGENTLY_TEST_LONG_MCP_APPS=1`: `TestMCPAppsLongNativeJournalDispatchRenewsLease`
passed in 66.6 seconds, verifying renewal across a real 65-second HTTP tool call
without duplicate execution (`/tmp/agui-mcp-long-lease-final.log`). Its skip in
the earlier fast suite is therefore no longer an unverified duration gate.

- Existing web tests and production build pass with the AG-UI SDK worktree.
- Existing BFF login, cookie sessions, unauthorized behavior and logout remain.
- Browser checks preserve current layout, workspace/Forge surfaces, tool feed,
  progressive reports, MCP host results/approvals, goals and queue controls.
- Feed placement acceptance includes inline, docked/rail, and detached/overlay
  rendering and updates, mode switching where supported, and reload restoration.
- Replay, reconnect, reload mid-run, conversation switching, background work,
  independent commands and cancellation preserve identity and ordering.
- The same web shell consumes real Agently and independent AG-UI streams;
  protocol validation, rendered results and network traces provide evidence.
- Network checks prove primary chat uses AG-UI. Remaining BFF application
  services are documented; they do not substitute a hidden legacy chat stream.
- Mobile compatibility remains protected by SDK regression checks; mobile UI
  migration is a later goal.

## Initial state

The assembly Go module already uses the AG-UI core worktree. The web package and
Vite alias previously referenced the original core checkout; both now reference
`agently-core-ag-ui`. The UI still uses legacy chat transport until the new
session/projection/bootstrap path is implemented and verified. Do not describe
this preparatory dependency correction as completed UI migration.

## Current evidence and next integration

- `AgUiSession` owns portable standard reduction, interrupt state, independent
  connection identity, transport detachment and explicitly supported exact replay
  after a fresh client is created. It does not probe Agently on generic backends.
- `AgentlyClient.agUiTransport` reuses existing BFF cookies, credential refresh
  and error hooks without automatic POST retries. Typed `AgUiCommands` isolate
  management runs from conversation state and preserve falsy/null results.
- The complete TypeScript SDK suite passed 455 tests with five opt-in skips
  after the session/command additions; typecheck passed. Later execution-control
  and bootstrap additions have their own targeted checks and require combined
  verification again after integration.
- The web baseline passed 1,018 tests; three existing Forge permission tests
  fail because the shared Forge checkout lacks expected helper exports/behavior.
  The production web bundle builds. Neither result proves transport migration.
- The actual official hosted LangGraph Dojo backend passed four standard runs
  with the new session client, including follow-up and frontend-tool continuation.
  Separately, two real public-backend turns passed through authenticated Agently
  BFF routing. Evidence is in assembly `dev/ag-ui/interop/evidence/` and explicitly
  records `productionUI.tested=false`.
- The optional public-demo BFF registry requires named authorized subjects,
  server-issued principal/backend-scoped thread IDs and immutable run admission.
  It never forwards BFF credentials or follows redirects. These demo threads are
  deliberately ephemeral and text-only, with no replay claim. General production
  external-backend persistence/delegation is not implemented by this demo helper.

The next required integration is the existing web chat renderer/store projection,
native presentation metadata, queued/concurrent submission coordination, and
active-run attachment after bootstrap. Do not replace queueing with a single
session's one-active-run constraint. Canonical persisted Agently transcripts stay
authoritative and retain their existing structure; AG-UI supplies live protocol
state and explicitly versioned presentation details. Browser parity and the same
production UI against both backends remain required acceptance gates.

`run.attach` now observes an owned existing run using its thread/run identity,
without returning private accepted input or admitting another client request.
It delegates to existing authorization, replay and lease recovery. Targeted
tests prove exact completed replay, observing a still-running worker after
navigation detach without another Query, and rejection of foreign identity,
missing runs, changed state/messages and invalid cursors. The SDK starts a fresh
reduction from the full journal; partial-cursor attachment still requires a
compatible reducer checkpoint and is not the default client path.

## Web integration constraints

- Preserve the current singleton client facade and existing application service
  APIs. A web-only primary interaction mode can replace `query`/`streamEvents`
  internally without changing native mobile defaults or rewriting renderers.
- The native web UI allows queued follow-ups while another turn is running.
  Coordinate separate AG-UI run sessions per conversation, rather than rejecting
  the second submission through one `AgUiSession`'s concurrency guard. Generic
  backend concurrency must follow its advertised capabilities.
- Key sessions by authenticated account, backend, thread and run. On account
  change/logout, detach and invalidate old subscriptions; never send Agently
  history, workspace context, attachment references or private actions to an
  independent connection by reusing a session.
- Native `agently.turn` execution lifecycle is distinct from protocol
  `RUN_STARTED`: admission may precede queueing, native persistence and execution.
  The compatibility query promise must preserve accepted/queued/fast-result
  behavior instead of always waiting for the whole stream to finish.
- Presentation maps protocol IDs to native turn/user/page/tool identities.
  Preserve optimistic user correlation through explicit client-message metadata,
  not matching repeated prompt text. Canonical persisted transcript IDs stay as-is.
- Apply already-reduced text/reasoning as absolute view snapshots. The existing
  render store now supports `contentMode: snapshot` without a second accumulator;
  its legacy delta behavior is unchanged. Native page identity needs its explicit
  iteration as well as page ID; generic projection must assign stable local page
  coordinates where the backend has no Agently presentation metadata.
- During cold attachment, do not let replayed historical lifecycle events trigger
  duplicate workspace actions or regress an already hydrated terminal view.
  A caught-up/cursor strategy remains part of integration verification.
- Native approval decisions made through retained BFF APIs, or by another
  client, can advance native execution after an AG-UI run ended with an interrupt.
  The coordinator needs an observation/continuation path for that work; do not
  assume attaching the already-terminal protocol run can stream new events.
- A new per-run native client must not overwrite an existing shared document by
  implicitly submitting the upstream client's default empty state. Establish
  server-state selection or a revision-aware handoff for native web submissions;
  generic backend state continues to follow its standard client session.

Latest foundation regression: the complete TypeScript SDK passes 466 tests with
five opt-in skips and typecheck. The broad SDK AG-UI/MCP Apps selection passes
after correcting trusted native child recovery (generic protocol producers remain
unable to forge presentation identity). Logs:
`/tmp/agui-web-sdk-suite-20261003.log` and
`/tmp/agui-web-backend-foundation-verified-20261003.log`.

### Production shell integration checks, 2026-10-03

The actual production shell at the isolated fixture endpoint `127.0.0.1:20501`
exposed two integration defects not caught by the protocol tests:

- Anonymous conversation creation omitted the principal, causing the subsequent
  AG-UI ownership check to reject bootstrap. Creation now uses the same resolved
  principal as query/AG-UI. Tests cover cookie continuity, authenticated principal
  precedence, and rejection with authentication enabled but no identity. The
  ownership check itself remains strict. Focused Go tests passed in
  `/tmp/agui-conversation-identity-test.log`.
- Composer navigation removed the last view observer while the submitted POST
  was being admitted. The coordinator aborted that request. View unsubscription
  now removes only the listener; coordinator-owned work runs to completion or
  explicit account reset. A remount regression and existing coordinator tests
  passed (4 tests) in `/tmp/agui-coordinator-remount-tests.log`.

Browser verification of the rebuilt UI remains pending. These checks are not
full UI parity or independent-backend acceptance. The unchanged transcript
storage contract remains authoritative.

The rebuilt shell subsequently completed a real AG-UI submission: browser
conversation `a5d450e8-d9d7-4f41-b6bd-717d45526391`, request 58, returned HTTP 200
with fixture assistant text and `RUN_FINISHED` success. No legacy query request
was used. Rendering is still failing: the remounted view retains its pending
placeholder despite the complete stream. Thus transport completion is proven
for this case, while live subscription/render preservation is not yet accepted.
Console evidence is assembly `.playwright-cli/console-2026-10-04T05-40-11-982Z.log`.

Follow-up verification: the complete TypeScript SDK suite passed with 484 tests
and 5 skips (`/tmp/agui-sdk-current-suite.log`). Additional admission regressions
then passed in the six-test coordinator suite: pre-header view removal preserves
the submission, while account reset aborts it and does not synthesize successful
admission (`/tmp/agui-coordinator-admission-tests.log`). The full web suite again
reports 1,018 passing and the same three pre-existing shared-Forge failures
(`/tmp/agui-web-current-suite.log`); this is not an all-green web acceptance.

The same fixture conversation successfully recovered its persisted transcript on
browser reload, and a second message rendered live without a reload. Browser
artifacts are assembly `output/playwright/agui-primary/native-reload-transcript.png`,
`native-followup-rendered.png`, and `native-followup.requests.txt`; the request
log contains AG-UI POSTs and no legacy query/stream route. This isolates the
remaining observed first-submit defect to view remount/subscription handoff.

Rejected submissions now terminate only the exact unadmitted optimistic request
by clientRequestId. Missing/unknown IDs, another conversation, and already
admitted turns are excluded. Nine focused projection/lifecycle tests pass in
`/tmp/agui-rejected-submission-reducer.log`.

The remount correction is now verified in the rebuilt production shell. A new
conversation `d586c483-e2d7-4173-b932-ae5d5e31c1bd` displayed both the submitted
message and complete assistant response without reload. All three interaction
POSTs in the captured log are `/v1/ag-ui/run` with HTTP 200; no legacy query or
stream route was used. Artifacts: assembly
`output/playwright/agui-primary/native-first-submit-rendered.png` and
`native-first-submit-rendered.requests.txt`. Sol's UI remount/rejection tests
passed 96 cases (`/tmp/agui-web-remount-submit-tests.log`), and the production
build passed (`/tmp/agui-web-remount-fixed-build.log`). This closes the observed
basic first-submit defect, not the broader tool/workspace/auth/interoperability
gates.

### Approval continuation design after Astra consultation

The browser tool fixture completed real `system/os/getEnv` execution through
AG-UI (conversation `9aeb29fb-bc9e-4b54-9896-a555f2ab572a`); captured stream is
assembly `output/playwright/agui-primary/tool-run.sse.txt`. The approval fixture
(`c84d238f-1185-4dfe-a94f-05c338c23acc`) returned a valid approval interrupt but
revealed missing UI interrupt handling. The coordinator now forwards explicit
protocol outcomes; the view maps interrupts to waiting/eliciting without
manufacturing native turn completion. Runtime tests pass 80 cases, including
stale-subscription fencing (`/tmp/agui-interrupt-ui-tests.log`); typecheck passes.
The waiting-state browser check and decision/continuation implementation remain.

Astra recommends preserving the per-item inbox endpoint and real tool effects:
intercept AG-UI-owned decisions before legacy `decideToolApproval` executes;
resolve ownership from durable original run/interrupt records, not UI metadata.
Use a typed `approval.decide` resource command with its own journal and explicit
original target, reusing `aguiApplyApproval` and its existing durable
`aguiDecision`/`aguiOutcome` receipts. Return a completed outcome only after a
real effect, plus protocol references/remaining interrupt IDs. Once every
original interrupt has a genuine completed answer, admit one successor with the
complete standard resume array and existing prior-run revision CAS. The resumed
worker consumes receipts without repeating effects. Recover the gap between last
decision and successor admission through server-side durable discovery, not a
browser callback. Mixed elicitation/client-tool interrupts must stay unanswered
until genuine responses arrive. Decision-without-outcome is uncertain, never
reset/retried automatically. Native timeout producers must use the same routing
for AG-UI-owned approvals. Direct standard resume and inbox decisions must
coordinate through the same receipts/conflicts. UI keeps the existing inbox and
MCP outcome dispatch; attach returned/discovered continuation and retain exact
native tool identity. This design is approved for implementation under the
user's delegated design authority; it is not yet implemented.

The rebuilt browser now reattaches the persisted interrupted run on reload and
shows `Waiting for approval…` plus `Approvals 1`, rather than reporting execution
completion. Screenshot: assembly
`output/playwright/agui-primary/approval-waiting.png`. No approval decision was
sent through the legacy route during this check; durable per-item coordination
and continuation remain outstanding.

### Standard MCP Apps renderer binding

Actual browser run `a44307c6-c22c-4a60-be2e-e54908ddd7ef` executed the MCP fixture
and received a valid `mcp-apps` activity with full host-only result, but the web
coordinator had dropped the host descriptor. Captured wire: assembly
`output/playwright/agui-primary/mcp-app-run.sse.txt`.

Astra approved a separate official AppBridge-based standard renderer, retaining
the existing custom MCPUI renderer and visual attachment surfaces. Standard
host handlers must be registered before connection; send complete tool input
then full result after initialization. Bind proxy calls to captured opaque
server alias/hash; never accept guest replacements or native server IDs. Use
independent empty-input AG-UI proxy runs. Sandbox must isolate parent origin and
translate standard resource CSP through host policy, not legacy renderer/sandbox
overrides. Pending approval RPCs remain pending until genuine host results.
Mount identity includes backend/thread/activity/appInstance, not resource URI.

Completed groundwork: bootstrap returns separate owner-authorized
`hostActivities` plus unavailable IDs after journal scope and current host
permission checks. These remain excluded from model-facing messages. SDK
coordinator forwards live descriptors and replaces them from bootstrap; UI
renderer-only store keeps stable instance keys, rejects foreign-profile authority,
and clears at account reset. Focused bootstrap tests, seven coordinator tests,
and three host-store tests pass (`/tmp/agui-host-bootstrap-tests.log`,
`/tmp/agui-host-coordinator-tests.log`, `/tmp/agui-host-view-tests.log`). No standard
iframe renderer is mounted yet; initialization, RPC, workspace placement, replay,
revocation and approval round-trip browser gates remain open.

The standard renderer now mounts in the existing conversation surface using
`@modelcontextprotocol/ext-apps` AppBridge 1.7.5. Its independent host proxy keeps
interrupts pending and has an explicit resume method; disposal detaches transport,
never cancels/reexecutes backend effects. Initialization handlers are registered
before guest HTML loads, with complete input then full result notification.
Resource HTML uses a fixed scripts-only sandbox and a host-built CSP; legacy
sandbox overrides are ignored. The existing custom MCPUI renderer remains.

Browser proof: reloading the persisted MCP fixture mounted its iframe and delivered
full `_meta`, structured content and mixed content. Clicking its Call fixture
button returned the real tools/call result with instance `iframe`. Parent DOM
inspection confirmed sandbox `allow-scripts`, CSP present, and no same-origin
access to the iframe document. Artifacts: assembly
`output/playwright/agui-primary/standard-mcp-app-tool-result.png` and
`standard-mcp-app.requests.txt`. Six focused host/proxy/store tests and production
build pass. Broad UI tests are running. Known remaining gates: proxy approval
coordination/UI, newer synthetic-thread observer provenance fix, two-instance
browser proof, workspace placement, supported host-local actions, and replay/
revocation checks. The tested backend also revealed an invalid runtime LIMIT
placeholder in the new watchdog discovery DQL; Sol is correcting it.

Full web suite after the standard renderer integration reports 1,030 passing
and the same three known shared-Forge failures (122 files;
`/tmp/agui-standard-host-full-ui-tests.log`). Host result delivery is serialized
and deduplicated after initialization, with mount-lifetime fencing, so activity
updates cannot race initial input delivery or notify an unmounted frame.

### Approval browser integration findings

The existing inbox exercised a real pending approval and exposed two integration
issues: auth-disabled decision/list handlers did not inject the anonymous cookie
principal used by AG-UI; and the UI sent unmodified display arguments as
`editedFields`, even with no configured editors. Both are corrected. Local CLI
explicit identity remains supported only in auth-disabled mode, authenticated
requests retain their principal/scope checks, and ordinary approvals now send
only action. Authorized editor values and schema review payloads are distinct.
Approval HTTP tests and 30 focused UI tests pass. The TS SDK preserves real
outcomes, discovers current continuation through bootstrap (including replayed
receipts), responds to committed aguiUpdated app hints, and fences account resets;
31 focused SDK command/observer/approval tests pass.

The corrected browser decision reached effect/receipt processing, then successor
input validation failed on activity message fields and unset state. Sol is fixing
lossless automatic continuation construction. The completed effect must not be
reexecuted. Browser case: c84d238f-1185-4dfe-a94f-05c338c23acc, approval
476dfb2a-9c14-4728-9ec1-a9018b211ad9. This is not an approval round-trip PASS yet.

Native guest-tool lifecycle fixes separately close persisted host-request turns
on real success/failure and leave queued approval turns waiting. Genuine proxy
approval completion finalizes only after real full host receipt or rejection;
uncertain effects are not claimed complete. Native tests cover another running,
queued, or waiting turn staying intact; MCP Apps SDK tests pass. These changes
prevent successful app calls becoming orphan failures or leaving chat busy.

### Production web shell against the independent public backend

The real authenticated production shell at the owned BFF endpoint20531 selected
Public LangGraph demo from the BFF-configured catalog. Two standard turns
completed: a synthetic marker request, then a follow-up requesting that marker
without restating it. Both assistant responses were AGUI_WEB_INTEROP_OK.
BFF auth/me returned200 immediately after those runs. Request bodies are saved
as assembly output/playwright/agui-primary/public-web-first-request.json and
public-web-followup-request.json: respectively1 and4 standard messages, empty
tools/context/forwardedProps, no Agently extension or native workspace history.
Screenshot public-web-two-turns.png records this first production-shell proof.
System App Context was initially visible; the source fix hides system/developer
and unknown activity bodies by default while preserving official history. The
cleaner build still needs the final browser repeat.

Native and remote surfaces now coexist under the same Root/MenuBar. Switching
back exposes the preserved native composer; returning to the remote connection
retains the two-turn session. Remote IDs never enter native selection/global
conversation state. Native controls are hidden or return explicitly to Agently;
capability-limited remote controls do not call native services.

Operational correction: the Steward clone's default runtimeRoot expands to its
normal configured location unless overridden. The initial test startup found no
native runs/artifacts to repair (zero processed/repaired/cleaned); it submitted
no model/tool prompt. The test server was stopped, then restarted with explicit
AGENTLY_RUNTIME_ROOT, STEWARD_RUNTIME_ROOT, and AGENTLY_DB_PATH pointing to the
owned /tmp/agui-steward-compat-runtime-20261003 tree. The public two-turn proof
above used that isolated instance. Temporary plaintext token/browser cookie
state files were deleted immediately after session installation.

### Pending MCP proxy approval UI

The standard app host now offers Review approval through the existing inbox.
Canonical approval events retain typed protocol references; the pending RPC
matches exact approval, synthetic thread and original run identity, then attaches
the genuine successor. It never resolves with the inbox's model-safe result or
an execution acknowledgement. A run.get successor reference plus scoped native
application hints/on-open refresh handles the lost-ref/reconnect boundary without
permanent polling. SDK chat reconciliation uses nativeConversationId for proxy
refs, never the synthetic proxy thread. Focused proxy/UI tests pass9 and SDK
approval/observer/reconnect tests pass12; typecheck passes. Backend proxy inbox
capture/recovery extension is being implemented, so no full proxy approval
browser pass is claimed yet. The MCP fixture now includes an approval-protected
button for that forthcoming proof.

Follow-up browser evidence (2026-10-04): conversation
`222ec272-d3cf-470e-a579-226d98d3e589` exercised the real fixture button,
Review approval, and existing Approve control. The first attempt exposed the
official client's pending-interrupt guard: attaching the admitted successor on
the original interrupted reducer was invalid. The host now creates a fresh
observer session for the same synthetic thread and attaches the genuine successor
without submitting another resume or tool call. Five focused proxy tests and the
production build pass. A second real request, `iframe-approved-1791119982343`,
delivered complete `_meta`, mixed content, and structured content into the iframe;
the MCP request log contains exactly one tools/call for that instance. Evidence:
assembly `output/playwright/agui-primary/proxy-approval-full-result.png`,
`proxy-approval-fixed.requests.txt`, and `proxy-approval-decision.json.txt`.
This closes the normal browser handoff only: native chat also rendered the
model-safe guest result, which is being investigated as a host-isolation defect.
Restart, uncertain receipt, and competing decision checks remain separate gates.
The same actual inbox Reject path also settled the iframe RPC with “MCP app tool
was not approved.” Instance `iframe-approved-1791120114401` had zero tools/call
effects. Evidence: `proxy-approval-rejected.png` and
`proxy-rejection-effect-count.json` in the same assembly artifact directory.

The native chat isolation defect is now fixed and browser-verified. New guest
turns persist `origin: host_request`; older turns obtain the same projection
provenance only from the exact server-created interim assistant marker, never
from text matching. The canonical saved transcript remains available unchanged,
while chat rendering and standard bootstrap messages exclude host-only turns.
Reload of the affected conversation removed both previous guest result rows.
A new real approval, `iframe-approved-1791121222644`, delivered the full result
inside the iframe with no corresponding chat row and exactly one tool call.
Evidence: `proxy-chat-isolation-reloaded.png`, `proxy-chat-isolation-live.png`,
`proxy-chat-isolation-live.txt`, and `proxy-isolated-effect-count.json`. Native
origin persistence, fallback negative cases, and standard-message exclusion have
regression coverage; web projection/runtime checks pass86.
The actual bootstrap response also passed the official EventSchema validator
(four events). It retained all four native host turns in the canonical transcript,
with host origin established, while standard messages contained no guest-result
text. The compact evidence is `proxy-bootstrap-isolation.json` in the same
artifact directory. The broader SDK goal/canonical regression selection passed
in 65.4 seconds after the goal-route identity changes.

The cleaned-up public backend UI was repeated in the authenticated production
shell: two turns returned AGUI_WEB_CLEAN_OK, auth/me remained200, and neither
App Context nor Backend state appeared in normal conversation view. Artifacts:
assembly output/playwright/agui-primary/public-web-clean-two-turns.png and
public-web-clean.requests.txt (two standard remote runs, no native query). A
subsequent visual check found a nested native pane CSS selector stretching the
remote header; the remote container now has its own scoped class and reuses the
existing composer-shell frame. Native history/starter-task DOM helpers explicitly
exclude the standard connection composer to avoid inserting native prompt history.
Nine focused remote tests and production build pass for that correction.
The corrected layout was then reloaded and visually inspected in the authenticated
Steward shell: the remote header stays at the top and the rounded composer at the
bottom, with existing navigation intact. Artifact:
`output/playwright/agui-primary/public-web-layout-final.png`. This is a layout
check; the earlier two-turn evidence remains the interoperability proof.

Native deterministic report/feed proof is documented in assembly
`dev/ag-ui/presentation-acceptance.md`: current --ui-dist production AG-UI
renders the progressive Forge report/table, activates and updates the synthetic
MCP feed, and opens its real workspace table. Root inspected the report artifact.
An older embedded-bundle artifact is explicitly legacy baseline and is excluded.
Feed scope controls data gathering (last/all); no contract establishes terminal
turn expiration. No automatic inactive semantics were invented. Durable explicit
lifecycle and the absent native inactive producer remain under architecture audit.

### Cancellation and mobile follow-up

The production web Stop control canceled a running native AG-UI turn through the
existing BFF cancellation command. The server returned cancelled:true; the actual
chat stream ended RUN_FINISHED with outcome.type=cancelled. Reload retained the
Request canceled row, with no delayed assistant completion. Owned fixture uses
fixture-cancel-window and a30-second model wait to make the UI control test
repeatable. Evidence: assembly output/playwright/agui-primary/cancellation.sse.txt,
cancellation-reloaded.png, cancellation.requests.txt. Native turn
063e1c14-66ed-4d25-905e-8fcf48730055 in conversation
e5ef4738-61b3-4dc2-9cea-bec1e19bb0a4.

Swift's live test identified a real backend regression: approval coordination
attempted to decode absent Resume on a valid frontend-tool continuation. The
backend guard is fixed, with no Swift SDK source changes. All112 Swift package
tests pass with4 opt-in skips, and both real owned-backend live AG-UI tests pass,
including client-tool continuation and identical replay. Additive approval response
decode also passes. See doc/ag-ui-mobile-sdk-compatibility.md. Android source
compatibility was inspected; actual Android test/build verification is underway.
