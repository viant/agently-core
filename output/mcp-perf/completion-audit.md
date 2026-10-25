# Performance completion audit — latest checkpoint 2026-10-25

## Latest measured checkpoint: local DSPUI Segments

The overall goal is **not complete**. The measured local host-owned residual is
below 600 ms for these two Segment clicks, but whole-navigation gates still
fail, paint was not measured, and a cold remote TCP/TLS budget was not exercised.
The evidence below supersedes any assumption that schema caching alone fixes
the repeated catalog admission problem. Historical results follow separately.

The isolated runtime is AI Studio PID 48012 / binary `44f1d5e0…` on 56243 and
Steward PID 47977 / binary `1954c310…` on 56241/56242. These are minimal tested
backports, not wholesale updates to the newest repository heads. Dependency
selections, all 67 embedded UI assets, schema 26 and configurations were preserved.
They do not establish the current state of the historical 8098/8095 deployments.

| Diagnostic stage | First | Warm |
|---|---:|---:|
| Navigation to observed visibility | 3,104.9 ms | 2,422.3 ms |
| Native Segment click to exact row DOM | 755.6 ms | 545.7 ms |
| Critical composite HTTP | 728 ms | 524 ms |
| Server total | 723.466 ms | 519.900 ms |
| Producer-active wall interval union | 169.354 ms | 90.587 ms |
| Derived server residual | 554.112 ms | 429.313 ms |
| Derived click-to-DOM residual | 586.246 ms | 455.113 ms |

Residuals subtract the measured union of producer-call intervals. They are
observed wall residuals, not isolated CPU attribution or a causal counterfactual.
Other nested/parallel span sums must not be added to infer request latency.
These are two localhost diagnostic reads, not percentiles or cold WAN samples.

Schema source reads fell from eight to **one per composite fetch**, with seven
snapshot hits. The subsequent admission change reduced actual connection
admissions from **24 to 2**, and policy-gate calls from **240 to 20**. The small
unit fixture had six policy selections; that count must not replace the measured
production-shaped count of twenty. All twenty expected producer calls across
the two navigations still ran; `ResourceAuthorization` calls were zero.

Both original 2,000 ms navigation gates failed. Seven read assertions passed,
but no business case or persistence verification was qualified by this diagnostic.
The passive observer captured row DOM readiness, not two-frame/GPU paint.
The first/warm click scheduling gaps and the separate stamped workspace style
read remain visible: `style://platform/workspace` transfers about 1.09 MB with
HTTP 200 / `private, no-store`, taking 441/429 ms in the preceding capture.
Window model-cache counters do not prove a style-cache hit.

Next checks are the remaining client/navigation intervals, a producer-owned
eager immutable style-dependency contract, full business/persistence coverage,
and matched remote cold-transport measurements. Do not replace the user's
startup/eager cohort-cache mechanism with repeated navigation admission.

Evidence and exact operations: [sanitized checkpoint](segment-checkpoint-20261025.json).
The schema/admission changes are Forge `0156c926`, AI Studio `4c9cb1f` and
`be044ff`; the reviewed DSPUI checkpoint `bebd72e` is pushed through merge
`923516c`. Publication on one repository is not proof that another dependency
has been pinned or its runtime rebuilt.

## Historical audit — 2026-10-23

The goal remains active. The latest completed work is real-app cache/admission
optimization, concurrent-call correction, and native publication change/restore.
The user revised performance scope to exclude data-producer delays. Raw full-window latency is still reported, but producer optimization is no longer required. This audit separates
current evidence from historical baselines; it does not declare complete based
on the fast management-only workload.

## Historical deployment (2026-10-23; not reverified by the latest checkpoint)

- Agently: 8098, PID 78126, asset `main-BEQ9Vthj.js`.
- Data Studio: UI 8095 / MCP 8096, PID 41573, provider `datastudio-management`.
- Proper AI Studio `./mcp`: UI 18290, PID 74427, provider `aistudio-local`;
  DSP namespace MCP is `/api/namespaces/dspui/mcp` on 18290.
- Local source heads at this audit are Forge `1dc6fed`, AI Studio branch
  `ENG-57832` at pushed `1207932`, and Data Studio `fc1d663`. The running binaries are identified by
  the PIDs and receipts above; later source commits are not assumed deployed.
  Agently/Core integration changes remain in their existing worktrees.
- Runtime artifact state is outside the deployed Git workspace at
  `/opt/agently/data/state/mcp/artifacts`, derived through `StateStore.StatePath`.

The deployed Agently launch wrapper explicitly points the trusted
`datastudio-management` MCP connection to `http://127.0.0.1:8096/mcp` and the
`aistudio-dspui-local` connection to
`http://127.0.0.1:18290/api/namespaces/dspui/mcp`. Its private artifact-cohort
registration binds those exact provider identities/endpoints; the browser does
not select them. A Connectors open uses the local admitted metadata route
`/v1/api/agently/forge/window/window%3A%2F%2Fdatastudio%2Fconnectors`, then
the bound `/v1/api/datasources/connectors/fetch?cohort=1` POST. The source's
published descriptor still executes its real business tool with the current
verified user credential. The `mcp/datastudio-management.yaml` default remote
URL is not the active endpoint in this local run because the wrapper overrides
`DATASTUDIO_MANAGEMENT_MCP_URL`.

## Requirement-by-requirement evidence

| Requirement | Authoritative evidence | Result |
|---|---|---|
| Real Data Studio metadata and data via Agently | After producer restart to PID 41573, `datly-after-policy-ui-restart.json` / `datly-after-policy-ui-reopen.json`: first GET 200/data POST 200, then GET 304/fresh POST 200; four rows at 91.9/102.9 ms click-to-row DOM | Pass for the current localhost build |
| Preserve authorization | Existing SDK/Core/Forge tests; `live-policy-deny-sync.json` denies cached data and new metadata with 403; current wrong-provider open also 403 with no data call | Pass for exercised cases |
| Policy composition, revision overrides, denial | Forge artifactcohort race suite and policy change receipts; union grants, non-granting revision overrides, deny wins | Pass for exercised cases |
| Empty-cache first start and persisted restart | `agently-final-empty-cache-spans.json` / `agently-final-persisted-cache-spans.json`: 16 versus 0 fetches, 431.299 versus 3.244 ms artifact sync; `dspui-expanded-catalog-live.json`: 26-root persisted restart, 0 fetches, 9.008 ms provider preload | Pass for artifact cache; whole process still takes about 18–19 seconds |
| Browser persisted definitions and compiled model memo | `live-browser-reload-final.json`; `connectors-browser-100-summary.json`: 100 warm Connectors reopens, 100 model hits/0 misses, one metadata 304 and fresh data 200 each | Pass for the measured warm path |
| No redundant metadata download on reopen | `connectors-browser-100-summary.json`: all 100 metadata requests returned 304 with no definition body; `dspui-published-final-single-metadata.json` confirms no duplicate legacy permission preflight | Pass; one conditional metadata request remains per reopen |
| Revision update downloads only changed payload | `live-revision-synced.json`: one changed partition, one fetch/new blob; DSP stamp 4 fetched one changed payload | Pass |
| Policy/removal changes invalidate access | `live-policy-deny-sync.json`, `live-policy-restored.json`, `live-withdrawal-restored.json`: 403 on deny/withdraw, restored baseline works | Pass in live same-process test |
| Real DSP publication and restoration | `dspui-live-window-change.json`: marker appears at stamp 4, disappears at pristine stamp 5, 20 real IDs both times, no restart | Pass |
| Notification E2E and nonblocking active call | Tagged real HTTP MCP/Core/SQLite test passes with one blocked call and unrelated window/data completing; six real rapid-navigation DSP trials pass after numeric request-ID fix | Pass; native push evidence below |
| Native deployment receives push notifications | `native-push.json`: authenticated dynamic AI Studio push before/after producer restart; current Agently logs show Data Studio session/subscription restored at 08:57:06 after PID 41573 restart and later reconnects subscribed again | Pass for the shared push path on dynamic AI Studio and current Data Studio connection recovery; same-process native Data Studio catalog change is not exercised because its built-in bundle is immutable until restart |
| Components/downloads share runtime | Restored Reader Builder cohort routes and ZIP receipt; host navigation/download tests | Pass for exercised flows |
| Future performance diagnostics | Shared opt-in collector; `BACKEND_PRIMITIVE_TRACE`; `diagnostic-observer-regression.json` verifies one observer after a denied open; numeric push and binding-owner phases | Pass |
| Rehydrate real DSP source application | 26/27 strict-valid current-source windows imported/selected, exact TAR roundtrips | Incomplete: Advertiser mutation datasource contract missing |
| Owned request/render overhead <=600 ms | `connectors-browser-100-summary.json`: 100 warm Connectors row-DOM p50 64.3/p95 85.9/p99 93.6/max 188.8 ms; `owned-overhead-summary.json`: paired warm Order residual median 93.45/max 103.9 ms; Movement server-only residuals 802.547, then 525.887/521.585/493.619 ms | Mixed across workloads: warm Connectors passes 100/100; one multi-dataset Movement miss remains. Movement DOM/paint not measured |
| Matched original DSP/YAML A/B | Original DSP browser is signed in, but campaign Orders uses `/pdp/api/ms/adorders` while YAML uses paginated `AdvertisingOrder` | Incomplete: no matched query/parameter comparison |
| Leave deployment running | Current listeners verified; existing user runtime/database preserved | Pass at audit time |

## Measured outcomes and scope

The latest current-PID Data Studio receipt has Connectors first open at 111.0 ms
click-to-row DOM (metadata GET 200 in 10.7 ms, fresh data POST 200 in 16.0 ms)
and reopen at 104.3 ms (metadata 304 in 4.3 ms, fresh data 200 in 17.4 ms),
with four rows and a model-cache hit. The older five-open PID 48030 regression
had median data HTTP 17.4 ms, click-to-row DOM 92 ms and two-frame proxy
102.3 ms; its three-row count and first-miss 414.6 ms are historical, not the
current business-data snapshot. These are localhost measurements, not a WAN SLA.

The later 100-reopen Connectors batch is a stronger **warm** browser sample:
all 100 metadata requests returned 304, all 100 fresh data requests returned
200 with four rows, and the compiled-model memo recorded 100 hits/0 misses.
Click-to-row DOM was p50 **64.3 ms**, p95 **85.9 ms**, p99 **93.6 ms** and max
**188.8 ms**; the two-requestAnimationFrame proxy max was **201 ms**. Fresh data
HTTP was p50 **16.4 ms**, p95 **20.5 ms**, p99 **30.0 ms** and max **30.8 ms**.
The batch used one signed-in localhost tab after first-page cache hydration;
it is not 100 cold process, OAuth or TCP/TLS startups. Raw and summary receipts:
`connectors-browser-100.json`, `connectors-browser-100-summary.json`.

Final DSP Orders batch: metadata median 8.2 ms; data HTTP median 572.8 ms
(538.2–932.7 ms); click-to-row DOM median 637.45 ms; two-frame proxy median
670.55 ms. Eight of ten data requests met 600 ms; no complete row-DOM open did.
The outlier spent 901.3 ms inside the upstream business call. All ten samples,
including outliers, are in `dspui-published-final-single-metadata.json`.
Cold business-call TCP/TLS is now measured in `dspui-direct-cold-connection.json`: 780.1 and 802.8 ms, versus paired warm calls 529.5 and 540.6 ms. These exclude prepared OAuth/MCP initialization (364.9 ms). Both cold calls miss 600 ms; all four returned identical 20-row IDs.

The fixed selection cache partitions credential contexts, avoiding cross-context
eviction under the public configuration actor. Live hits reduced MCP configuration
work from 22–30 ms to roughly 3 ms. The normal gateway authorization checks remain.
Native definition bytes were already cached; the improvement removes repeat selection.

Real DSP change sync took 34.129 ms with one 6,998-byte artifact payload download;
restoration took 19.590 ms with zero artifact fetches, using the original content
hash. Those durations exclude the conditional-poll detection interval. Stamp 5
preserves pristine content; the original source files were never overwritten.

The cross-repository gate passed in `final-cohort-gate-20261023.log`. New native
namespace activation and numeric request-ID race tests also passed. Subsequent
frontend admission/preflight and diagnostics focused tests passed. Do not interpret
these targeted checks as a claim that every unrelated repository test passes.

## Latest restart and notification evidence

Native AIStudio push synchronized the new marker in 32.809 ms with one changed
artifact fetch. After an actual producer restart, a further push synchronized
in 16.618 ms using cached payload bytes. Final pristine restoration took
12.642 ms with zero artifact fetches. Stamp 9 retains the exact original content
hash; authored source and business rows were not modified. Polling remains a
fallback, and these push events completed before the next poll. Raw evidence:
`native-push.json`. The first attempted producer restart was a no-op and is
explicitly excluded from restart proof.

The Data Studio management bundle is built at producer startup and has no
same-process publish/reload seam. No new operator API was added just for testing.
Its authenticated stateful transport is tested; deployment changes reconcile on
restart. The dynamic AIStudio producer proves the real shared notification path.

An already-open browser window also survives an Agently restart now. In
`live-window-readmission-after-restart.json`, host 61485→61847 without a page reload
produces exactly one typed missing-binding 403, one admitted metadata 304, and one
successful data request with 20 numeric IDs. Genuine owner/session/policy/dataset
denials remain terminal. Total recovery was 1,169.4 ms, **not** a 600 ms result;
the next refresh used one data request and no metadata request (576.4 ms).

The final cross-repository gate passed in `readmission-gate-20261023.log`.
Focused Forge recovery tests passed (96/96); Agently recovery tests passed (5/5),
and SDK denial/owner/timing race tests passed. An unrelated broad SDK race run
was stopped without a result and is explicitly not counted as a pass.

## Current scope and remaining verification

Producer-delay optimization is explicitly excluded by the user's revised goal.
The paired warm samples show 27.0–31.4 ms outside the upstream tool span for
HTTP data completion (median 29.45 ms), and 84.5–103.9 ms for click-to-row DOM
(median 93.45 ms). Subtraction excludes the entire remote call, including
transport; it does not isolate producer CPU time. Raw totals above remain valid.

The earlier statement that producer metrics were inaccessible was incorrect:
the release HTTP listener exposes read-only metrics. No further producer latency
investigation is required under the revised scope.

Current follow-up work:
1. Matched original DSP/YAML A/B still requires query and parameter alignment;
   the original app is signed in, but its campaign Orders route and the YAML
   `AdvertisingOrder` route do different work. Existing cache tests are not a
   controlled React.memo A/B.
2. Advertiser remains explicitly excluded: three mutation datasource contracts
   are absent from current Platform MCP. See `advertiser-segment-gap.md` and
   `dspui-linked-window-scope.json`. Do not claim all 27 windows are complete.
3. The isolated AI Studio Movement report tab has one producer-excluded
   server-owned miss and three subsequent sub-600 ms server samples (details
   below). The remaining variability is not explained by a removed auth check;
   no authorization optimization is justified from these receipts alone.

Data Studio source is committed on main at `fc1d663`; backend packages and 20
focused UI tests pass. Runtime caches were preserved and excluded from Git.
The previous blocked audit is superseded: the goal is active and local
implementation/deployment verification is making progress.

## Expanded catalog final restart verification

`dspui-expanded-catalog-live.json` proves the 26-root manifest survives host
restart 77934 → 78126. Disk load took 0.475 ms, conditional discovery 7.752 ms,
and provider preload 9.008 ms. There were zero artifact fetches, zero manifest
rewrites, and a compact unchanged discovery response (161 normalized observer
bytes, not wire size). Producer PID74427 stayed running.

On that host, Chrome displayed “Timing diagnostics”. A fresh published Order
List rendered 20 rows, and clicking its first row opened the linked Order detail
with General, Bidding & Optimization, and Frequency & Pacing sections. No access
denial or loading placeholder remained. Screenshot: `dspui-linked-order-final.png`.
That fresh Order List sample was 845.7 ms click-to-row DOM (metadata 116.0 ms,
data HTTP 624.3 ms); server timing was off, so no producer-subtracted claim is
made for this sample. The earlier paired owned-overhead measurements remain the
quantitative scoped performance evidence.

## Latest Data Studio browser verification

On host78126 before the Data Studio restart, Connectors first open reached row
DOM in 111.0 ms, with metadata200 in10.7 ms and fresh data200 in16.0 ms.
Closing and reopening reached row DOM in104.3 ms, metadata304 in4.3 ms and
fresh data200 in17.4 ms; model-cache hit counter incremented. That receipt,
`datly-final-deployed-cache.json`, is historical relative to current producer
PID41573. The post-restart results are below.

The original DSP tab was confirmed signed in. Its campaign-scoped Orders screen
uses GET `/pdp/api/ms/adorders`, whereas the YAML list uses paginated
`AdvertisingOrder`. These are not matched query shapes. A matched-detail
comparison was being prepared when Chrome control became unavailable; no matched
A/B result is claimed. This supersedes the old login blocker.

## Multi-dataset report wall attribution

The earlier isolated Movement receipt `aistudio-tab-producer-wall.json` (PID
27124) records a successful `namespaces/preview/test` retry with server total
**4,309.961 ms** and a monotonic union of producer-call intervals of
**3,507.414 ms**. Host-owned time outside those intervals was **802.547 ms**,
above the revised 600 ms target. A preceding HTTP 404 attempt had 325.752 ms
of auth time and was automatically retried; it is separate from that successful
request. Its 12,281.471 ms summed producer metric across six concurrent calls
cannot be subtracted from wall time.

The same behavior with fixed serial phases on isolated PID 35070 produced
three successful Movement samples: server total / producer-call wall / residual
of **3,852.121 / 3,326.234 / 525.887 ms**,
**4,649.382 / 4,127.797 / 521.585 ms** (after a reload), and
**4,441.326 / 3,947.707 / 493.619 ms**. All three residuals are below 600 ms,
but this is a three-sample server-only result, not DOM/paint or an SLA; the
802.547 ms miss remains part of the evidence. The two initial serial traces
show selected-session revalidation 33–39 ms, final session revalidation
38–52 ms, `s.test` precheck 8–9 ms and postcheck 27–36 ms. The `s.test` execute
phase includes those nested pre/runner/post phases and must not be added to
them. Forge report compile was about 154–156 ms in those two samples, while
materialization was about 39 ms. AI Studio's connection-admission and policy
durations are still concurrent sums, not wall-time slots. The earlier ~419 ms
outside Forge report entry therefore cannot be assigned to the summed policy
total, and the new phases do not establish a safe redundant authorization
check to remove. No producer SQL, authorization check or business-row cache was
changed for these measurements.

## Post-policy-UI deployment verification

Data Studio was rebuilt and restarted through Endly (15995 → 41573) with
config/database preserved. Authenticated readiness passed; anonymous protected
MCP calls return 401. Chrome verified the resource-wide Reusable named policies
manager after login (`datastudio-named-policy-deployed.png`); no real policy was
modified during this read-only UI check.

Agently 8098 still reads real Connectors data after that producer restart:
first open 91.9 ms to four row DOMs, metadata200 3.2 ms, data200 17.1 ms; reopen
102.9 ms to rows, metadata304 4.0 ms, fresh data200 19.0 ms, model-cache hit1.
Receipts: `datly-after-policy-ui-restart.json`, `datly-after-policy-ui-reopen.json`.

The unchanged Agently PID78126 reconciled the restarted Data Studio producer:
numeric logs record one discovery error at 08:57:03, then compact discovery
cache-hit/sync success and a fresh notification session/subscription at
08:57:06 (1.044/1.751 ms). A later transient 09:00 outage produced two
20–21-second failed conditional discoveries and one failed session open; both
providers subsequently reconnected and subscribed successfully at
09:00:34–35. Later successful subscriptions continue in the log, and the host
currently has established TCP connections to 8096 and 18290. From the
08:57:02 producer restart onward, the log records zero artifact fetches and
zero blob puts; the persisted Data Studio manifest remains 16 roots with hash
`46fd2ed9aa0e5f2380ad58f54798e81cf929525f882ee5e9518d3706bb43c34b`,
matching the prior restored manifest receipt. Repeated compact discovery hits
carry 161 normalized observer bytes, **not** measured wire bytes. These facts
support recovery and retained-cache reuse, not uninterrupted connectivity or
a same-process Data Studio resource-change push.

AIStudio branch was changed at the user's explicit request to ENG-57832 and
merged with main; pushed commit1207932 includes the existing branch
configuration, tested source changes, and verified `info.entityPermissions`
mapping. Generated deployment assets remain local. The resource-wide Reusable
named policies UI was visibly verified on the restarted Data Studio process;
that check did not modify a real policy.

## Objective boundary at this audit

The active Agently 8098 → Data Studio 8096 path has live proof of authenticated
window/data delivery, a persisted source-scoped cache through restart, fresh
business data on every open, one metadata 304 rather than a redundant body
download, revision/policy/removal propagation, restored subscriptions after a
real producer restart, and a nonblocking in-flight-call integration test. The
same host PID and `StateStore.StatePath` persisted those bytes outside Git.
The current Connectors browser measurements and the deliberate transient
reconnect failure are both reported above. Dynamic same-process notification
delivery is proved with AI Studio; the built-in Data Studio bundle has no
same-process publisher change to exercise. This does not justify claiming a
Data Studio mutation push that was never observed.

The original 500 ms cold-success target applied to an operation including
fresh TCP/TLS, MCP initialization and its required initialized notification;
the historical plan explicitly listed **process startup as a separate
workload**. The 18–19-second Agently startup receipts are therefore a real
operational cost, **not** a miss against that per-operation 500 ms target. A
five-run remote small-read lifecycle had one 510.165 ms miss, and the DSP
Orders direct cold-connection calls took 780.1/802.8 ms before the user's
later producer-excluded revision. Neither the local prepared/component tests
nor a synthetic TLS-floor test proves a full cold browser window success.
Under the later 600 ms producer-excluded scope, warm Connectors reached row
DOM under 600 ms in all 100 measured reopens and the paired warm Orders
residuals were small; three recent Movement server-only residuals passed, but
one was 802.547 ms. A universal cold or multi-dataset guarantee remains
unproven.

The 26/27 DSPUI source migration and matched original DSP/YAML A/B are broader
real-app follow-ups: Advertiser still lacks an authoritative mutation tool,
and the original Orders route uses different query semantics from the YAML
list. They should remain open, but neither is evidence that the current
Data Studio management cohort cache failed. The reusable named-policy page
is now visibly deployed; this resolves the UI-visibility check, not the
separate IdP membership-group contract gap.

## Expanded cold remote lifecycle sample

The 100-sample actual remote Data Studio MCP probe is now complete:
100/100 successful fresh connections and data reads, but0/100 under600ms
(and0/100 under500ms). Full-chain p50/p95/p99/max:
825.241/2626.319/3950.325/7809.470ms. The chain includes DNS/TCP/TLS,
initialize, initialized notification202, and connectors.list. OAuth token
issuance and browser/window metadata/rendering are excluded. This supersedes
the earlier five-sample remote distribution for current network conditions.
See `provider-remote-cold-100-20261023.md` and its raw JSONL receipt.

This remote one-row lifecycle workload is distinct from the local four-row
100-open browser workload; their numbers are not a matchedbefore/afterA/B.
Prepared local cached interaction meets600ms in all100browser samples,
but a universal new-remote-connection600ms claim is disproved by this run.

## Current-source verification in progress — 2026-10-23 12:13 PDT

Listeners were rechecked: Agently PID78126 on8098 and Data Studio PID41573
on8096 remain running. The full `scripts/verify-artifact-cohorts.sh` gate is
running with output at `/tmp/artifact-cohort-verification-20261023.log`;
it is not yet a completed gate. Forge cohort/cache/window/clientpool/resourcehost
race suites, Core HTTP UI and datasource checks, and the actual Core HTTP/MCP
notification integration have passed. The latter verifies changed-root fetch,
stale binding rejection, an active blocked tool call, independent GET+POST
under600ms, and cancellation. Datly authenticated cohort/scope tests also passed.

Forge Markdown writer fix d146a168df27557388eb2d60f9743c9d5f62bfa8 is published
(remote main verified). Final Core pin/build and Steward migration are pending;
these source changes are not assumed active in PID78126.

The complete cross-repository gate subsequently exited0, including the final
Agently notification-sidecar readmission test. Full log:
`artifact-cohort-verification-20261023.log`. Timings in fixture tests are
not substituted for deployed browser timings. The coordinated final build
cutover was released after this gate, final36-definition parity, and backup
verification; post-restart verification is still pending.

## Final Playbook restart browser check — 2026-10-23

Final serving Agently PID28652 follows a second scoped config restart to expose
canonical playbook tools alongside legacy prompt. Signed-in Chrome reload
retained user identity/navigation/conversations. Actual Connectors first open
reached three row DOMs in191.4ms (metadata2004.8ms, data20032.5ms); reopen
reached rows in68.2ms (metadata3047.0ms, fresh data20014.5ms, model hit1).
Two-frame proxies were206.5/95.6ms, not GPU paint. Receipts:
`agently-final-playbook-first-open.json`, `agently-final-playbook-reopen.json`.
This is a two-open updated-build regression check; previous100-open evidence
is historical and its four-row data snapshot must not be attributed to this
run. Both requests met600ms locally. External MCP tool authentication remains
a separate check: anonymous and direct IdP-token attempts returned401.

## Published reader-cache live rollout — 2026-10-23

AIStudio93ee39c with published DatlyStudioff49e66/Datly0b9a0c1/x f9ec/Forge430
was rebuilt without a workspace overlay. DataStudio8095/8096 now serves PID7000;
Agently8098 remained PID28652. Backup DB quick_check passed; config and data
were preserved. First deployment compiled43 readers in28513.500ms and stored43
artifacts. A second real process restart reused all43: zero discovery/miss/store,
namespace compilation1812.520ms. Full readiness was checked via the new PID's
AI Studio ready log line, not only the earlier HTTP listener. Numeric trace remains
available through BACKEND_PRIMITIVE_TRACE.

Signed-in Chrome first/reopen/post-warm-restart row-DOM times:139.1/94.6/85.2ms,
three actual rows, all fresh data POSTs200. Reopens returned metadata304 and
model-cache hits. Post-warm data fetch20.8ms. After deployment Agently recorded
zero artifact fetches and blob writes; notification subscription recovered.
Anonymous business tool invocation returned401; tools/list remains public200.
See datastudio-cache-live-deployment.json and its linked browser receipts.

## Final completion audit

See `final-local-verification.md` for the latest scoped results, listener/binary verification and explicit misses. The fresh cross-repository verification script completed with exit 0; log `artifact-cohort-final-audit-20261023.log`. Earlier PID-specific sections above remain historical.
