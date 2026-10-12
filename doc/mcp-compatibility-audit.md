# MCP compatibility audit — 2026-10-08

This is an evidence report for the current source trees, not a certification of
an application deployment or every feature in the MCP specification. Draft
behavior and the older November 2025 Tasks API are outside this adapter.

## Authoritative versions

| Contract | Target and authoritative source |
| --- | --- |
| Core MCP | [2026-07-28 specification][C], [per-request metadata][B], [Streamable HTTP][H], [subscriptions][S] |
| Tasks | [`io.modelcontextprotocol/tasks`, 2026-07-28][T] |
| MCP Apps | [`io.modelcontextprotocol/ui`, stable 2026-01-26][A]. The current upstream README still links this dated stable document; draft is separate. |
| Apps SDK interoperability | Installed/tested `@modelcontextprotocol/ext-apps` **1.7.5**, base SDK **1.29.0**. Upstream source currently declares **2.0.3** and [documents unchanged 1.x/2.x wire compatibility][U]; this audit does not claim a local 2.x SDK test. |

Current source bases: Core `966575881540d2b4cba33f960141773be2445c6e`,
Tasks `626d608a29b5bc590637c2c30e3bc36c29a4b546`, mcp-ui
`e2befc0bbbafe5162d4e36c92e568bf0f318adf2`, and Agently UI
`ac989dda612b3ef1ffe2747ee9f96a4ab449ac1e`, plus the audit/subscription changes.
The coordinated transport fix uses local viant/mcp base
`228d17f0aec4a7e5bf40856502eff971f18bbb5b`. This checkout lacked the already
published no-retry API used by Core, so the exact seven published API/context/
transport-guard/test files from `e3fb56e588cd` were restored unchanged before
combined validation; no retry-policy redesign or Git reset was performed.
Go dependencies are `mcp-protocol v0.19.1-0.20261005144737-c504db7a02bb`,
`viant/mcp v0.24.1-0.20261005185040-e3fb56e588cd`; Core currently pins the
published Tasks base `v0.0.0-20261008041354-626d608a29b5` and mcp-ui
`v0.2.1-0.20261007203211-e2befc0bbbaf`. Tasks retains Go 1.25.5; Core requires
1.25.8; mcp-ui declares 1.24.0.

## Local commit status

The attributable module/UI changes are committed locally and have not been pushed:

| Repository | Local commit |
| --- | --- |
| viant/mcp | `578802b6957d0e7b58d75871a4ca02992f74fe0d` |
| viant/mcp-tasks | `762637b5ea3a5070c7645d22d12f0bc589582d8a` |
| viant/mcp-ui | `be1ceb5c4c0e0e920cb2ec63b96e43d9db3145c8` |
| viant/agently | `c5a21765486807af2bc4894770ca053264b6e0d1` |

Core's adapter, ledger guards, tests, dependency edits and audit/integration docs
remain uncommitted. Its published Tasks base does not contain the subscription
and validation exports used by that source. No local replacements were added to
committed module files, and no unpublished version was represented as a public pin.

### Exact remaining Core files

| Files | Pending integration |
| --- | --- |
| `sdk/mcp_tasks.go`, `sdk/mcp_tasks_test.go` | Existing-run ownership/status/input/cancel adapter and tests |
| `sdk/mcp_tasks_subscription.go`, `sdk/mcp_tasks_subscription_test.go` | Native AG-UI wakeup adapter and real HTTP stream tests |
| `protocol/mcp/expose/tasks.go`, `protocol/mcp/expose/tasks_test.go` | Opt-in authenticated transport/capture/subscription wrapper |
| `sdk/agui_recovery.go`, `sdk/handler_agui_durable.go` | Exclude protocol ledgers from execution recovery/resume |
| `doc/mcp-integration.md`, `doc/mcp-compatibility-audit.md` | Integration contracts and this evidence report |
| `go.mod`, `go.sum` | Existing published-base Tasks pin/x/sys edits; replace with verified published revisions after release |

A real `GOWORK=off go test -mod=readonly ./protocol/mcp/expose -run TasksWrapper`
currently fails on missing `ValidRequestID`, `ValidRequestMetadata`,
`SubscriptionOptions`, notifier fields and subscription method constants in the
published Tasks base. Core was therefore neither staged nor committed.

## Requirement and evidence matrix

“Tested” means the named source path is exercised; it does not enable a host
endpoint automatically. Native execution remains authoritative.

| Requirement | Source | Implementation / evidence | Boundary |
| --- | --- | --- | --- |
| Ordinary calls remain ordinary without Tasks | [T] | Real `viant/mcp` handler/high-level client; real HTTP high-level client; Core `WithTasks` non-negotiated regression | No automatic outbound Tasks conversion |
| Request-local protocol/capabilities; optional clientInfo | [B] | RPC/HTTP rejection before admission; omitted clientInfo accepted; prior declarations do not authorize later requests | Self-reported metadata is never authentication |
| Missing extension is `-32021` | [T] | Valid metadata with empty capabilities rejects task get/update/cancel/listen; HTTP error body preserved | Missing required metadata instead yields `-32602` |
| HTTP header/body agreement | [H], [T] | Tasks adapter checks method/version/name/taskId, Base64 sentinel and HTTP 400/404 errors. viant/mcp client/server now encode/decode names and reject missing July capabilities; real HTTP Unicode/padded/sentinel tool calls passed | Schema-owned `x-mcp-header` extraction remains a high-level host/client hook |
| Non-null string/integer request identity | [B], [S] | Invalid IDs rejected; server string/zero/negative IDs retain subscription correlation; typed HTTP listen emits positive integers | No wire identity grants task ownership |
| Flat creation seed versus detailed polling state | [T] | Creation accepts any Task status without detailed payload; real HTTP already-completed seed retrieves the original result | Additional seed details are not required; marshaler emits Task fields |
| Durable creation before handle | [T] | Reference storage reopens/CAS tests; Core alias immediately resolves the owned admitted native run | Custom Backend implementations must honor the same durability contract |
| Full `tasks/get`, `resultType: complete` | [T] | Required fields/nullable TTL/status payload validation; original tool `isError` retained; malformed failed error objects rejected | Bare native errors/queue receipts are not fabricated as final MCP outcomes |
| Outstanding inputs and unique keys | [T] | All outstanding keys, partial answers, duplicate/superseded keys, durable input receipts and continuation tests | Ordinary consent/approval/input-schema policy still applies |
| Cooperative cancellation | [T] | Durable intent/empty acknowledgement; actual canonical cancelled terminal state over HTTP | Acknowledgement does not assert execution stopped |
| Disconnect is observer cancellation | [S], [T] | HTTP disconnect/reconnect and owned stdio cancellation leave task execution intact | Explicit task cancellation uses `tasks/cancel` |
| First message acknowledges agreed task IDs | [S], [T] | Exact selected IDs, correlation metadata, wrong/early/malformed frames tested | Standalone mixed task/base filters reject explicitly; composition is a host hook |
| Notifications contain full canonical task state | [T] | Real HTTP working/input/terminal states; required ttlMs checked; no task progress/logging forwarding | No cross-owner task list/stream |
| Reconnect and lost native hints | [S] | Watch-before-snapshot; AG-UI event wakeup with a one-hour reconcile timer; missing-hint timer and reconnect rereads | Bounded fresh subscriptions, no execution/backfill replay |
| Ownership throughout lifecycle/stream | [T] | Tenant/subject isolation, reconstruction, foreign cancellation/polling rejection, input-capability recheck after acknowledgement | Host resolver must use current authenticated authority |
| Native async modes remain native | Adapter policy | Existing async run/status/cancel, selectors, wait/detach/fork preserved; native regression suites and detached-child HTTP observation passed | Full application-specific fork/result capture still needs host qualification |
| Generic UI resource identity and MIME | [A] | mcp-ui accepts generic `ui://` resources; scoped naming is explicit legacy; exact resource/MIME matching and bounded blob decoding | Helpers are not an HTML5 conformance validator |
| UI capability negotiation | [A] | Required HTML MIME must be in negotiated MIME list; no capability-presence-only inference | View protocol is negotiated separately by official SDK |
| Model/app tool visibility | [A] | Core registry/agent policy and browser catalog tests enforce audience restrictions; malformed audiences fail closed | Visibility narrows existing approval/agent policy |
| Standard JSON-RPC view lifecycle | [A] | Real App/AppBridge SDK and real Chromium: initialize, ordered partial/input/result, tool call, inline mode and acknowledged teardown | Unsupported optional methods reject; legacy `mcpui:*` is explicit only |
| No host data before initialized | [A] | Outgoing transport gate; bounded ordered queue; close-before-initialize rejection; partial input stops after complete input | No uncertain guest tool effect is replayed |
| Separate-origin proxy and sandboxed view | [A] | Actual Chromium on separate loopback origins; nested opaque iframe cannot read parent DOM; exact window/origin guards tested | Production sandbox URL/origin/response policy remains deployment-owned |
| Resource-owned CSP domains | [A] | Actual Chromium allows declared fetch and blocks undeclared fetch before it reaches server; defaults exclude eval/blob/frame/network relaxations | Browser proof covers fetch/opaque isolation, not every CSP directive/production header |
| Optional browser permissions | [A] | Typed camera/microphone/geolocation/clipboard-write metadata roundtrip; host grants none by default | Declaration is a request, never authority |
| Workspace UI remains separate | Architecture | Standard renderer uses app instance/resource lifetime; Forge/legacy routing tests remain separate | MCP Apps never installs a workspace UI definition |
| Browser MCP has no backend fallback | Adapter policy | Manager rejection before server pool/client creation; browser catalog binding, revocation, origin/conversation and no-replay tests | Requires active originating browser connection; not a CLI transport |

## Verification

Completed checks for the audit source:

- Tasks: `go test -race ./...`, `go vet ./...`, `git diff --check`; race suite also passed with the fixed local viant/mcp dependency.
- viant/mcp: full `go test -race ./...`, `go vet ./...`, `git diff --check`; legacy protocol and exact published no-retry regressions passed.
- mcp-ui: `go test -race ./...`, `go vet ./...`, `git diff --check`.
- Core full SDK/expose suites excluding Live: SDK **119.082s**, expose **0.219s**, against all three local modules.
- Core focused Tasks/wrapper/Apps/browser race suite: SDK **52.334s**, expose **1.311s**, against all three local modules.
- Core Apps/browser/visibility/async/fork/detach/dependency regression selection
  passed across SDK, expose, tool execution, async, clienttool, agent, mcpapps,
  browser registry, manager and tool registry packages.
- Core TypeScript SDK: typecheck; **541 passed, 5 skipped**, 50 test files passed.
- Agently Apps tests: **83 passed**, 13 files passed; official SDK lifecycle
  tests also passed after final queue/cancellation checks.
- Existing `ui/scripts/proof-mcp-apps.mjs` and new `ui/scripts/test-mcp-apps-sandbox.mjs`: actual isolated Chromium + official SDK
  + actual sandbox proxy/CSP proof passed. It creates only disposable loopback
  servers/profile; no live user Chrome session or account is used.

Core checks use `/tmp/agently-core-mcp-audit.mod`, copied from Core's public pins
with explicit local replacements for the uncommitted Tasks, mcp-ui and viant/mcp revisions.
This validates the combined source; it is not a public-dependency build claim.

Reproduction from the relevant module roots:

```sh
go test -race ./...
go vet ./...
```

Core:

```sh
go test -modfile=/tmp/agently-core-mcp-audit.mod -mod=mod ./sdk ./protocol/mcp/expose -skip Live -count=1
go test -modfile=/tmp/agently-core-mcp-audit.mod -mod=mod -race ./sdk ./protocol/mcp/expose -run 'MCPTasks|TasksWrapper|MCPApp|Browser' -skip Live -count=1
cd sdk/ts && npm run typecheck && npm test
```

Agently UI (the proxy target is a dummy Vite test-config value; tests do not
contact an application backend):

```sh
APPSERVER_URL=http://127.0.0.1:1 npx vitest run src/services/mcpApps src/components/mcpApps
PLAYWRIGHT_BROWSERS_PATH=/tmp/agently-mcp-apps-audit-browsers node scripts/proof-mcp-apps.mjs
PLAYWRIGHT_BROWSERS_PATH=/tmp/agently-mcp-apps-audit-browsers node scripts/test-mcp-apps-sandbox.mjs
```

The Chromium proof requires Playwright (tested **1.60.0**) and its disposable
headless shell. If absent, install that test dependency with
`npm install --no-save --package-lock=false playwright@1.60.0`, then use
`PLAYWRIGHT_BROWSERS_PATH=/tmp/agently-mcp-apps-audit-browsers npx playwright install chromium --only-shell`. An alternate test executable may be supplied via
`MCP_APPS_CHROME_EXECUTABLE`; use an isolated test profile, never a user profile.

## Remaining integration and release gates

1. Publish the corrected viant/mcp revision first. Publish mcp-ui independently,
   and publish Tasks after pinning/testing against the corrected transport.
   Then update Core's public pins for all three and rerun without replacements. The currently
   pinned Tasks base lacks the new subscription/helper exports, so the dirty
   Core adapter cannot yet build using only those public pins. Only the local commits listed above were made; no push or
   release was performed.
2. Wire `WithTasks`/`WithTaskSubscriptions` in the real authenticated host
   transport factory. Trusted capture must identify already-admitted canonical
   execution and its operation/causal links. The closed `viant/mcp` method switch
   does not automatically install this decorator.
3. Integrate outbound Tasks consumption only where explicitly selected. Existing
   fixed `CallToolResult` consumers and browser transports remain ordinary MCP
   clients; they must not be silently advertised as task-aware.
4. Supply a canonical `Snapshot` for native async result/error stores that are
   not represented as original MCP outcomes in the AG-UI journal. This adapter
   never starts a second engine or turns an arbitrary native error into a made-up
   terminal MCP result.
5. Production host authentication/reauthorization, tool-schema custom header
   mirroring, cross-process aggregate quotas/retention/shutdown, composed base
   subscriptions, sandbox deployment headers/origins and optional permissions
   remain host responsibilities. Published lower-level transport behavior is not
   certified wholesale by these targeted checks.

[C]: https://modelcontextprotocol.io/specification/2026-07-28
[B]: https://modelcontextprotocol.io/specification/2026-07-28/basic
[H]: https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http
[S]: https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/patterns/subscriptions.mdx
[T]: https://tasks.extensions.modelcontextprotocol.io/specification/2026-07-28/tasks
[A]: https://github.com/modelcontextprotocol/ext-apps/blob/main/specification/2026-01-26/apps.mdx
[U]: https://github.com/modelcontextprotocol/ext-apps/blob/main/README.md
