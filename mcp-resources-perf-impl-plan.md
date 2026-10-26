# Authorized MCP UI resources: measured latency and implementation plan

> Historical diagnosis and initial proposal. The implemented architecture is in
> [Published artifact cohorts](mcp-resources-cohort-implementation.md), and the
> current live results and remaining limits are in the [completion audit](output/mcp-perf/completion-audit.md).
> The 2026-10-25 checkpoint records real schema-source and admission reductions,
> derived producer-excluded residuals, and the still-failing navigation gates;
> it does not claim a cold WAN or paint SLA.
> Startup replication, local authz and namespace × kind synchronization supersede
> the on-demand admission design below. The original 500 ms cold *operation*
> budget included transport/MCP initialization but explicitly excluded whole
> process startup; the user later revised the measured host-owned scope to
> 600 ms with data-producer delay excluded. Original baseline evidence remains.

Date: 2026-10-22, America/Los_Angeles. Status: **historical baseline and superseded proposal; see the current audit for production measurements and open checks.**

## Decision

Keep external MCP as the authority and execution boundary. Move immutable UI bytes into a precompiled, hash-addressed window dependency bundle. Admit the complete selected graph in one provider operation, then verify current policy/version at the existing delivery barriers with one bounded graph check, rather than rereading every member. Keep form/filter state in the authorized local window instance. Execute an actual datasource once, with exact resource/selection/input authorization. Never use a content hash, a browser claim, or an old manifest as permission.

Do **not** download all applications for every user. Synchronize the allowed application menu plus a small startup working set at connection time; fetch another window's complete closure on demand. Treat bytes retained after logout or revocation as inert. Preserve MCP for all provider/host communication.

The immediate measured problem is the host's validation/read amplification, not the connector database query. A direct authenticated remote `studio.sdk.connectors.list` takes about 80 ms. The same operation through Steward's datasource route takes about 8 seconds.

## Measurement scope and reproducibility

Repositories inspected: `viant/agently-core`, `viant/agently`, `viant/forge`, `viant-internal/aistudio`, `viant-internal/steward_ai/deployment/steward`, and the `viant/datly-studio` dependency implementing the compared management provider. The repeated Forge path in the request denotes the same repository.

- Original host: `http://127.0.0.1:8098`, PID 77000 at measurement time; browser uses native Forge Connectors, not an iframe. Direct UI: `http://127.0.0.1:18980`.
- Steward's configured provider is `https://datastudio.viantinc.com/v1/studio/sdk-mcp/mcp`, with BFF ID-token authentication and user-scoped resources. It is not the local `18980` process. The local and remote databases returned different connector rows. Compare the same operation; do not claim identical datasets.
- Deployed executable dependency versions: Agently Core `4393e69b6aa0`; Forge `8f16de481346`. Local uncommitted owner-state changes are absent from that executable.
- Browser numbers use Chrome CDP monotonic `requestWillBeSent` to `loadingFinished`; they exclude the human/model/tool delay between automation calls. Open-to-data starts with the window HTTP request and ends with the datasource response; no paint timestamp was captured.
- These browser pages were already authenticated and processes were warm. A newly opened window is **not** a fully cold process, identity, socket, and metadata cache. Fresh TCP/TLS was separately measured against the remote MCP endpoint using separate authenticated curl processes.
- A second isolated host on `18098` ran the same deployed executable with `AGENTLY_PRIMITIVE_TRACE=1`, separate MCP port `18001`, and background workers disabled. It reproduced the problem and supplied server spans. The user's `8098` process was not restarted.
- An attempted transport relay changed the configured provider origin and failed layout binding (503). Its timings are excluded; do not weaken origin/binding validation to make a probe work.
- Evidence: [browser-measurements.json](output/mcp-perf/browser-measurements.json), [server-measurements.json](output/mcp-perf/server-measurements.json), [backend audit](output/mcp-perf/backend-findings.md), [provider audit](output/mcp-perf/provider-findings.md), [frontend audit](output/mcp-perf/frontend-findings.md). No credentials or connector rows are in the measurement JSON.

### Observed user-visible waterfall

| Operation | HTTP result | Elapsed | Classification |
|---|---:|---:|---|
| Direct local Connectors refresh | 200 | **84.905 ms** | Includes fresh local TCP, 0.234 ms; TTFB 82.724 ms |
| Steward Connectors refresh | 200 | **8,108.135 ms** | Reused browser→host TCP; TTFB 8,106.564 ms |
| Steward fresh window metadata | 200 | **2,206.925 ms** | First blocking stage |
| Metadata complete→datasource requests | — | **42–43 ms** | Browser apply/mount/scheduling interval; not a render benchmark |
| Unwanted `connector_activate` fetch | 500 | **6,366.894 ms** | Runs on mount; command must not be implicit default |
| `connector_filter` fetch | 200 | **7,421.286 ms** | Local form/filter state incorrectly represented as remote service |
| Actual `connectors` fetch | 200 | **8,174.095 ms** | Last blocking data response |
| Window-request start→data complete | — | **10,424.407 ms** | Metadata + mount gap + slowest parallel data request |
| Background `ui.poll` | 200 | about 4,002 ms | Explicit 4,000 ms long-poll timeout; independent of this critical path |

The three datasource requests overlap: do not add their durations. Browser→host connection cost was below 1 ms on the relevant fresh sockets. There was no `Server-Timing` in the original responses. Small payload transfer is not the multi-second bottleneck.

### Server attribution: every measured blocking slot above 500 ms

Same executable on the isolated traced host:

| Blocking stage | Server wall time | Evidence inside stage | Meaning |
|---|---:|---|---|
| Window open | **1,709.7 ms** | window resolve 1,708.4; dependency resolution 1,408.3; definition reads 1,702.5 / 20 calls | Dependency retrieval dominates; pool acquisition 1.4 ms |
| Unwanted activate datasource | **6,269.0 ms** | definition reads 5,612.6 / 78; backend 2,479.1; registry 773.9; actual tool 75.1 | Extra command plus repeated validation; returns error |
| Filter datasource | **7,188.9 ms** | definition reads 7,148.6 / 120; backend 1,705.6 | No useful remote row query needed for filter state |
| Connector datasource | **7,989.0 ms** | definition reads 7,320.3 / 122; backend 2,481.2; registry 776.5; actual tool 77.5 | Validation dominates the useful query |

These are **nested aggregate spans**, not additive slots. `gateway.definition.read` is a wrapper count; it is not the wire-call count. The original `gateway.mcp.call` measures `tools/call`, **not** standard `resources/read`, so its small value does not demonstrate cache hits. A new transport span separates that gap (see implementation below). On the connector request, 307 pool acquisitions total only 10.7 ms; 3,400 configuration checks total 248.9 ms. Both should be reduced, but neither explains 8 seconds by itself.

Unmeasured in this baseline: individual provider policy/DB spans inside each `resources/read`, exact wire-read count (resolved by the instrumented follow-up below), individual late-validation barriers, and final browser paint. The plan explicitly closes these gaps; do not label aggregate telemetry as an exhaustive distributed trace.

### Remote cold network and individual provider operations

Five separate fresh-connection authenticated initialize requests returned HTTP 200:

| Boundary | Observed range |
|---|---:|
| DNS | 2.5–4.1 ms |
| DNS + TCP connected, cumulative | 72.6–78.3 ms |
| DNS + TCP + TLS complete, cumulative | 222.9–228.4 ms |
| MCP initialize complete, including new connection | **296.1–302.5 ms** |
| Same operation on localhost, fresh connections | 1.0–1.5 ms |

TLS values above are cumulative, not extra durations to add. Five samples do not establish p95/p99.

Direct authorized resource comparison on an established session:

| Operation | Remote | Local |
|---|---:|---:|
| `resources/read(window://datastudio/connectors)` | 209.7 ms | 44.1 ms |
| Ten sequential referenced datasource reads | 1,372.9 ms total; 134.6–141.6 each | 426.7 ms total; 40.4–45.2 each |
| Exact `studio.sdk.connectors.list` | 79.7–80.1 ms | Separate dataset; not used for equality claim |
| Full tools/list | 296.6 ms / 142 KB | 130.1 ms / 143 KB |

Cold initialize + remote window + ten references was about **1,887 ms** before host decode/apply. Each datasource reply was only 393–696 bytes. Transport round trips and admission repeated per member are the expensive unit. The deployed SDK resource handler accepts `window://` and `datasource://` reads even though its advertised tool list omits `windows/get` and `datasources/get`. A proposed optimization must negotiate capabilities against the actual deployment rather than assume a source checkout's tool set.

### Follow-up with standard-resource transport instrumentation

The instrumented isolated build counted the actual `client.ReadResource` calls. Opening Connectors made **11 reads / 1,555 ms**, plus 147 ms preparing requests. The three concurrent datasource requests made 18, 32 and 41 transport reads respectively; cache/coalescing attribution differs when they overlap.

A **single refresh with no competing initialization**, server request 62, took **8,165.9 ms** and made **46 standard-resource reads totaling 6,519.3 ms**, plus **763.7 ms request preparation**. It still made only one useful connector tool call, **88.5 ms**. The preparation path rechecks independent-window and target capabilities via repeated MCP initialize calls. A tested source fix negotiates both capabilities with one Initialize per window resource read, preserving validation; it does not remove the graph fanout. The registry total was 907.2 ms (including nested work); tools/list accounted for 182.3 ms and all configuration checks for 243.6 ms. These child durations overlap parent totals. The added wire-level phase establishes remote-read amplification directly; it is no longer inferred from 122 wrapper reads.

A final standalone interval trace (request 60 / trace 62, with the capability preparation fix) took **7,754.611 ms**. The [complete phase ledger](output/mcp-perf/phase-intervals.json) partitions that wall time without adding nested spans:

| Nonoverlapping attribution | Wall time |
|---|---:|
| 46 actual resource reads | **6,527.496 ms** |
| Resource request/capability preparation | 390.228 ms |
| Actual connector tool call | 87.815 ms |
| Tool schema listing | 102.255 ms |
| Remaining registry work | 492.375 ms |
| Other/unclassified intervals combined | 154.443 ms |
| Total | **7,754.612 ms** (rounding) |

The complete sequence of continuous >500 ms blocks is:

| Offset from request start | Duration | Work |
|---|---:|---|
| +80.229 to +3,717.275 ms | 3,637.046 ms | First validation/read block, 24 remote reads (3,401.777 ms read time) |
| +3,717.275 to +4,482.602 ms | 765.327 ms | Registry/discovery/query transition, broken down below |
| +4,482.602 to +7,754.147 ms | 3,271.545 ms | Second validation/read block, 22 remote reads (3,125.718 ms read time) |

Leading 80.2 ms and trailing 0.5 ms are below threshold. These blocks partition the request; they must not be added to their child resource-read totals.

One continuous interval outside resource reads exceeds 500 ms: **+3,717.275 to +4,482.602 ms**. Its 765.327 ms consists of registry work 487.539 ms, tools/list 102.255 ms, actual query 87.815 ms, next resource preparation 84.373 ms, and 3.346 ms other work. Every larger top-level blocking stage is therefore assigned to measured intervals; provider-internal subdivisions of each approximately 140 ms resource read remain outside this host trace. The partition uses fixed phase priority, not a claim of causal ownership. Logging overhead is included. The capability change reduced preparation from 764 to 390 ms across these runs, consistent with removing duplicate Initialize calls; this is not an isolated A/B benchmark.

The newly instrumented delegated credential-store phases did **not** fire on this connection. It uses the configured BFF ID token, so there is no evidence that the separate Datly delegated-token lookup path causes these seconds. Do not disable token-store/revocation checks as a speculative fix.

The exact retained definition text is **23,812 bytes** (21,456 window + 2,356 across ten datasource definitions), approximately **6,045 bytes gzip** after repacking. This excludes CSS, initial rows and envelope overhead. A complete small closure is a realistic transport unit.

The repeatable authenticated [cold connector probe](output/mcp-perf/probe_connectors_cold.go) now measures the **complete MCP lifecycle: TCP/TLS + initialize + `notifications/initialized` (HTTP 202) + first `studio.sdk.connectors.list`**, with `MCP-Protocol-Version` on subsequent requests. Five fresh HTTP/2 sessions took **510.165, 451.951, 452.374, 451.354, 448.728 ms** (median 451.951 ms; max 510.165 ms). All returned successful data, but one exceeded 500 ms. The initialized notification costs another 72–75 ms. An earlier probe reported 377–482 ms but omitted that required notification; those figures are explicitly **superseded and invalid as a full cold-lifecycle claim**. Credentials stay in process memory; output includes timing and sizes only. This is a small-read test, not a window/render test or percentile guarantee.

## Root causes and previous work

1. **Graph-wide work on a selected datasource.** `forge/backend/resourcehost/window_execution.go` `ResolveDatasource` reads the parent, checks every dependency, then rereads the parent and checks every dependency again. `dependencies.go` resolves the children sequentially. `agently-core/service/datasource/service.go` resolves before backend execution and again before release. Read-sequence dispatch/release checks add further validation. With ten dependencies, one simple read expands dramatically. Preserve the security boundaries; change their implementation.
2. **Ambiguous default datasource.** `forge/src/components/WindowContent.jsx` used `Object.keys(metadata.dataSource)[0]` when no explicit default existed. Serialized Connectors metadata begins with `connector_activate`, so mounting triggered the action datasource. The PoC now infers a default only for a single-source window. Multi-source windows need explicit defaults or refs from their controls/table.
3. **Lost local state semantics.** All ten sources, including `connector_filter` and draft data, were projected to host POST services. Existing uncommitted owner-state work in Core is absent from the original measured binary. Rebuilding current Go sources for the follow-up still produced the external `connector_filter` fetch, so that work alone does not fix this external-provider projection. Local state needs an admitted window owner, not remote execution, and must never become an authorization shortcut for real datasource rows.
4. **No precompiled remote closure in this path.** Datly Studio's v3 window publishes separate references. Current AI Studio archive work is narrower: datasource/dialog public parser; published archive internals also understand lookup/style, while windows/navigation remain separate. The stock deployed Datly Studio endpoint rejected `resource-archive://datastudio/datasources`. Do not assume that an archive already solves startup.
5. **Observability had a blind spot.** Existing aggregate trace correctly exposed the definition phase but omitted actual standard-resource transport. Added resource prepare/read/decode spans and opt-in datasource `Server-Timing` address this.

The named `mcp-resources-perf-impl-plan.md` was not present in the working tree or searched Core history; this file is the new consolidated plan. The prior [browser-MCP performance note](doc/browser-mcp-performance.md) and commits `2aff15e6` / `073a4837` concern browser-extension handoff/catalog validation, with two recorded 233/438 ms results versus about 8.7 seconds earlier. They do not establish the native Forge path's latency. Existing gateway matrix tests also show cold discovery/list above 500 ms with injected 20 ms hops, while warm operations are cheap; that supports reducing fanout but is synthetic evidence.

## Proposed production contract

### Published bytes and policy are separate

Compile an immutable closure at publication/activation (for the current working-file Datly Studio provider, use a trusted source-snapshot/config generation and atomic activation): selected window variant, CSS, dialogs, models, and datasource **descriptors**. Local form state is explicitly marked `local`; executable sources carry operation intent and `autoFetch` only when authoring authorizes it. Do not embed user credentials, capability decisions, tenant row data, or mutable auth snapshots in shareable blobs.

Key content by `(provider identity, format version, target profile, compiled closure SHA-256)`. A manifest entry includes canonical resource URI, kind, selected revision, content hash, MIME, size and dependency hashes. Include theme/locale/platform/form factor where they change bytes. Validate the transitive closure and supported schema before publication; atomically advance a generation pointer. Reject cycles, unknown executable references, hash mismatch and oversized graphs. A hash protects integrity, not authorization or origin.

Precompute common compiled variants independently of users. Compute the authorized startup manifest using the trusted policy engine at connection time. Cache an entitlement cohort only if the policy system supplies a canonical cohort identity **and** a revision/invalidation contract; otherwise use principal/account/namespace-scoped admission. Avoid per-user copies of identical bytes.

### MCP operations (new negotiated capability)

All of the following are proposed contracts, not existing upstream API guarantees:

- **Bootstrap/open:** one versioned MCP tool or resource request admits a window graph and returns its complete compiled closure, server-held selection handle, current policy/catalog generations, bounded expiry, and optionally the read-only initial datasource results. Initial data execution uses verified principal, input scope and selected pins exactly as a standalone call would. Writes are never prefetched.
- **Sync:** request includes known content/catalog generation and bounded known member hashes; server resolves the caller itself. Response is unchanged, a complete replacement, or an explicitly complete delta with base-generation, changed members and removals. An unchanged-content response still requires current authorization.
- **Execute:** browser supplies the opaque instance/selection handle and named source plus inputs, not an authoritative dependency graph. Host retrieves its admitted graph; provider authorizes the exact graph/operation/input at dispatch and checks the required policy/content fence before result release. Return data and refreshed decision metadata together. No loop over unrelated individual resource GETs.
- **Revalidate:** where existing PRE/dispatch/POST/final checks remain necessary, perform one batched graph check per decision boundary. It compares policy epoch, graph generation, selected revisions and expiry against authoritative state, not the content cache. Within a boundary, coalesce identical graph checks. Never share a positive decision across users, account switches or boundaries merely because hashes match.

A production provider can expose a `bundle://...` resource using standard `resources/read` and negotiated `_meta` sync fields, or a typed tool with the same semantics. Implement using `github.com/viant/mcp` and protocol types. The synthetic PoC uses the resource shape solely to validate mechanics.

### Authorization and revocation

Ingress verifies identity normally. A host-owned selection record binds principal/account/namespace/provider, target profile, selected graph hash, allowed operation set, policy epoch, provider configuration generation, expiry and a nonce. Browser claims are selectors only. On execution, recheck relevant row/input permissions at the source.

Current revocation must be observable at the decision boundary. Options are (a) a single online provider batch decision, or (b) a trusted policy replica with ordered epochs and a defined consistency protocol. A short TTL alone is **not** immediate revocation. Offline success and instantaneous remote revocation cannot both be guaranteed during a network partition. Fail closed when policy freshness cannot be proved; retain only inert bytes. Do not call stale data or metadata "authorized" to satisfy latency.

Recheck after provider work if policy changed during execution; do not release a result admitted under a revoked epoch. Never increase a lease while reusing an older pin. Bind selection handles to the server-held current generation; a client cannot substitute a hash, change namespace, or restore a deleted resource by replaying an old response.

### Removal and synchronization

Stage new content, verify hashes/schema/dependencies, then atomically swap the admitted manifest. Apply removals for deleted, revoked, unassigned and missing resources; absence counts as removal only in a response marked complete. Deltas require the exact base generation, monotonic sequence and bounded replay detection; otherwise fetch a full snapshot. A removed window loses its selection handle, dependent dialogs/datasources and style references. Cancel or deny pending operations; release shared CSS only when its remaining admitted reference count reaches zero.

On logout, account switch, provider change or epoch invalidation, remove active manifests, row caches, local window state and selection handles. Persisted immutable bytes may remain scoped/encrypted as required but cannot be rendered or executed before admission. On reconnect, synchronize epochs before enabling interactions. MCP change notifications trigger synchronization; they are hints, not proof of fresh authorization. A lost-notification/reconnect path must work without trusting the last event.

## The 500 ms budget

Measure two success criteria separately, and keep connection establishment inside each applicable timer:

1. **Fresh network session, credentials already valid, small precompiled window and initial rows:** start before DNS/TCP/TLS/MCP initialize; end after usable browser apply. No prior socket, tool discovery, blob or selection cache is allowed in this scenario.
2. **Authenticated connection already active:** open another admitted/synchronized window or refresh its data. Preconnect at user connection improves this path but must not be advertised as cold networking.

The measured remote initialize already consumes about 300 ms, and the required initialized notification adds about 73 ms. That leaves only **127 ms for everything else** under 500 ms. The current window-only read is about 210 ms, so even one existing window read cannot fit this topology's full cold budget, before browser apply. Fetching tools/list or ten definitions makes the miss much larger. The measured full small-read chain itself reached 510 ms in one of five samples.

A viable **proposed deployment budget** requires a faster provider connection, not just warmed sockets. For example, provision and verify a path with roughly <=40 ms RTT, then test this complete budget:

| Critical-path stage | Budget |
|---|---:|
| DNS + new TCP + TLS | 125 ms |
| MCP initialize exchange | 45 ms |
| Required initialized notification/acknowledgement | 45 ms |
| One fused open/admission/initial-read RPC, including network | 145 ms |
| Host validation/projection + browser decode/apply | 60 ms |
| Margin | 80 ms |
| Total | **500 ms** |

These are engineering hypotheses, **not measured guarantees or current-network results**. The current window-only 210 ms must improve to fit the fused 145 ms stage; precompilation, one graph decision and source-query placement need real measurements. With the current ~75 ms RTT and observed TLS behavior, keep the ~373 ms lifecycle as measured and demand a fused RPC + all host/browser work within 127 ms, or change provider placement/handshake cost. Do not omit the initialized notification, skip authorization, prewarm the socket and relabel it cold, or substitute a timeout response for successful completion. Connection pooling addresses the connected path only.

For the connected path, target <100 ms metadata-only open from an admitted compiled bundle and <200 ms simple data refresh; retain current policy fences. For arbitrary warehouse queries, OAuth interactive login/refresh, process startup and very large windows, record separate workloads. A hard 500 ms deadline can bound a failure response; it does not prove successful completion within 500 ms. Do not silently narrow the stated cold success criterion.

## Implementation sequence and acceptance

| Phase | Owner / location | Concrete work | Acceptance gate |
|---|---|---|---|
| 0: attribution | Agently/Core/Forge/provider | Trace ID propagated across browser HTTP and MCP; phase start/end, calls, max, cache result, HTTP status, bytes (implemented host interval ledger is linked above). Add DNS/TCP/TLS/pool queue separately. Record each >500 ms phase and non-overlapping residual. | Reproduce Connectors; reconcile client/server wall time; no unexplained >500 ms critical-path segment |
| 1: immediate waste | Forge UI + Core owner projection | Ship explicit default semantics; preserve local filter/draft state; enforce command intent/autoFetch false. Verify existing owner-state changes before release. | One real `connectors` read on open; zero activate/filter remote calls; create/test/update only after user actions |
| 2: compiled bundle | Datly Studio generic provider + Forge types | Versioned immutable closure, publication validation, content hash, size limit, one authorized read. Extend AI Studio archives only through generic upstream hooks; reporting stays private. | Cold open uses one closure request; no per-member network reads; exact existing rendering and dialog/data wiring |
| 3: graph decision | Forge resourcehost + Core datasource | Server-held graph handle; one batch revalidation at each required phase; singleflight within phase; execute selected source once; current epoch fences. | No O(dependency count × phase count) wire reads; revoke/change/expiry tests still deny at every old boundary |
| 4: session sync | Agently SDK/TS + Forge + provider | Initial authorized working set, hash comparison, deltas/tombstones, atomic swap, logout/account isolation, reconnect recovery. | Same hash sends no bytes; delete/revoke closes access immediately at next authoritative boundary; partial/out-of-order sync never revives old resources |
| 5: cold-path fusion | Provider + host | Bootstrap/open includes permitted initial reads; avoid tools/list critical path; bounded transport pool; optimize/relocate provider if measured WAN budget fails. | Fresh TCP/TLS successful Connectors open+data <500 ms in stated environment; not just warm/local handler timing |
| 6: release gate | Endly + browser suite | Cold/warm/adversarial matrix, rollout capability flag, trace-based rollback. | >=100 representative cold and warm samples; p50/p95/p99/max and all failures reported; no auth correctness regression |

Do not parallelize security barriers simply to shorten the benchmark. Independent immutable blob downloads can be bounded-concurrent after admission; writes remain serialized as required. Report throughput/queueing at 1, 10 and representative concurrent users, with cancellation, bounded cache size, and stampede protection.

Required authorization regressions: cross-user/account/namespace replay; revoked role/row scope between PRE and dispatch, during source work and before release; stale selection; deleted dependency; changed provider config; expired token/lease; corrupted bytes; mixed bundle generations; missing/extra dependency; unexpected command execution; partial sync, out-of-order delta and reconnect after missed removal. Use synthetic fixtures for these tests.

## Work implemented in this investigation

- Forge implicit-default fix and focused tests: multi-source windows no longer auto-fetch their first serialized source. 52 window tests and 7 queue/config tests passed. This source change is not yet in the user's running UI bundle.
- Core narrow native-snapshot optimization: content-aware `CheckCandidate` plus exact policy revalidation can avoid a second body transfer; arbitrary sources retain full reread semantics. Tests cover revocation, source drift, late-read error and shortened lease. This is not the remote graph-fanout fix.
- Forge capability preparation now reads the two advertised window extensions from one Initialize response per window read; direct negotiation and existing target/cache tests passed.
- Bounded opt-in datasource `Server-Timing` (`?perf=1`) and standard resource prepare/read/decode instrumentation; exact scope and validation in the backend audit.
- [Runnable Go bundle PoC](output/mcp-perf/bundle-poc/README.md): synthetic MCP-shaped resource sync with SHA-256, complete atomic snapshots, removals, trusted session map, current policy-generation checks, and deny cases. `go test -race ./...` passed. Fresh loopback TCP/TLS plus `resources/read` averaged about 1.92 ms; warm about 0.17 ms. This simulator benchmark excludes MCP initialization/session negotiation. This demonstrates the mechanics, not real-provider 500 ms performance. It has an explicitly documented in-flight revocation limitation and must not be deployed as production authorization.
- [Endly read-only probe](output/mcp-perf/provider-readonly-endly.yaml) loaded and executed through the invoked Endly MCP skill. It has zero assertions and is a timing probe, not a claimed e2e success. Authenticated provider measurements used the user-authorized encrypted scy credentials without retaining token bytes in evidence.

Production completion requires phases 2–6 and the cold end-to-end acceptance gate. The measured problem is established and the small fixes/prototype are reviewable; the complete authorized bundle/graph protocol remains proposed.
