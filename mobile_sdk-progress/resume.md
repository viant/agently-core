# Mobile SDK Resume

Resume from `mobile_sdk-progress/README.md`, not from the older top-level
handoff files or Android/iOS plan snapshots.

Current verified state (2026-08-06): active Forecasting prefill and
post-coalescing duplicate-suppression are closed on iPhone, iPad, Android
tablet, and Android phone against the isolated local Steward lane on `:9292`.
Canonical inline-report rendering is closed on web, iPhone, iPad, Android
tablet, and Android phone. Historical sections below record the path taken and
include superseded blockers; use the latest subsections and the status table in
`README.md` as authoritative when they disagree with older notes.

Current task: continue hardening mobile SDK / Forge parity without changing
Steward data semantics or crossing ownership boundaries. The real-device
Forecasting prefill and post-coalescing duplicate-suppression gates are closed
on iPhone, iPad, Android tablet, and Android phone against the isolated
`:9292` Steward lane; ongoing work should focus on fresh regressions,
auth/session durability, report-builder/dashboard compatibility, and keeping
the contract tests aligned with the Steward-owned workspace rules. The source
migration is complete: canonical `renderedContent` is additive on transcript
and live reducer state, exposed by Go, TypeScript, Android, and iOS SDK
models, and projected by native hosts into Forge's typed canonical parts.
Latest mobile auth rescan confirms the first sign-in screen is quiet again:
workspace OAuth sign-in plus settings only. Developer OOB/session helpers stay
behind developer settings, stale mobile `9191` fixtures were moved to `9292`,
and focused Android/iOS auth gates pass.
Latest native verification rescan refreshed the four-device Forecasting proof
on `:9292`: Android Pixel Tablet (`emulator-5554`), Android Pixel 10 Pro
(`emulator-5556`), iPad Pro 11-inch simulator
`B2AA0D68-7312-4CC9-85B8-0544341A942D`, and iPhone 17 simulator
`59317EFB-ADFE-4A22-817F-4B4F6658AB2E` all sent
`open forecast builder for line 7288336` from the native composer and verified
the Forecasting surface plus completed `ui/window/setFormData` payloads with
`prefill.scope.targetKey="line:7288336"`. Latest Android tablet proof is
conversation `24d86d0b-cfa9-4551-b7b3-33294f288dfd` from 2026-08-06, verified
through `adb reverse tcp:9292 tcp:9292` plus `http://127.0.0.1:9292`, with
Forecasting visible and `Filters` / `5 active`; screenshot evidence:
`/tmp/agently-tablet-forecasting.png`. Prior four-target proof conversations:
Android tablet `6e30c6b1-53b7-4565-8235-fad78b2f24b5`, Android phone
`a3683a86-cd93-4481-b4b9-278e8a6be278`, iPhone
`9783bc74-fc5e-42d6-bf4d-d1e3d38ac239`, and iPad
`b22b9809-cc86-46d7-bd0d-5928c09e1fe5`. Earlier screenshot evidence lives
under `/tmp/agently-rescan/*forecasting*20260805.png`. Ignore the earlier iPhone
conversation `c3556698-a1f8-4f20-a179-901bd664db76` as form-data proof: it
opened Forecasting but timed out after the old 15s live-test teardown window.
The live iOS test now holds the app for 45s after the Forecasting pane appears
so queued UI bridge commands can drain before teardown.
Latest auth/session and report-builder rescan also passed on 2026-08-05:
focused Agently Android app auth/settings/session/restore tests, Agently iOS
auth/data-source/presentation tests, Agently-core Android/iOS SDK
restore/client/stream tests, Forge Android report-builder/dashboard tests, and
Forge iOS 221-test dashboard/report-builder suite all passed. Boundary scans
remain clean for Forge native production and Agently-core SDK production;
Agently mobile still intentionally carries workspace endpoint presets for
Steward production plus local `9292`, which is app configuration rather than
generic renderer behavior.
Latest local-port cleanup removed the remaining `localhost:9191` fixtures from
Agently-core iOS SDK tests. A scan now finds no `9191` references under
Agently-core SDK, Agently Android, or Agently iOS paths, and
`swift test --package-path ios --filter 'AgentlySDKTests'` still passes all 68
Agently-core iOS SDK tests.

The concrete current duplication is:

- Android: `agently/android/app/src/main/java/com/viant/agently/android/ForgeFenceRuntime.kt`
- iOS: `agently/ios/Sources/AgentlyAppFoundation/Chat/ForgeFenceRuntime.swift`

The iOS migration is complete in source and focused Forge tests. Forge iOS now
owns `MarkdownFenceParser`, `TranscriptEnvelope`, `TranscriptWindowBuilder`,
`TranscriptInlinePresentationPolicy`, and `ForgeRuntime.updateWindowInline`.
Agently iOS projects completed SDK `renderedContent` into Forge canonical
parts, then retains only transcript composition, SwiftUI placement, and
hydration. The host uses raw parsing only for legacy or incomplete streaming
content. Focused Forge tests and an iPhone-simulator `xcodebuild` pass; the
Agently iOS package test command remains blocked by pre-existing macOS-only
composer and color errors, so iPhone/iPad evidence is still required.

Android source migration is complete. Forge Android owns the reviewed scanner,
`TranscriptEnvelope`, `TranscriptCanonicalPart`, `TranscriptWindowBuilder`,
`TranscriptWindowPresentation`, `TranscriptInlinePresentation`, JSON/CSV mode
materialization, synthetic summary values, generic dashboard and planner
adaptation, and explicit empty data sources. The app's `ForgeFenceRuntime.kt`
now projects completed SDK `renderedContent` into the Forge canonical contract
and owns only transcript composition, placement, rendering, and hydration.

The Android contract now matches iOS: malformed or unpaired data stays
markdown; only whitespace can separate a `forge-data` snapshot from its UI;
legacy headers, JSON/CSV, append, object patch, quoted multiline CSV, duplicate
header safety, synthetic summary rows, and missing current source data are
handled in Forge. No Android regexes are used in this pipeline. Two independent
Codex reviews found the temporary data-scope/hydration gaps and the final
follow-up reported no findings.

Before coding, read the active fence parser in
`agently-core/sdk/ts/src/richContent/parseFences.ts`, the current Forge models,
and the Android/iOS adapter tests. Do not copy a regular-expression parser into
another app layer. Use a deterministic scanner/state machine or a parser
already owned by the generic Forge layer.

Required proof for this feature:

1. Canonical state exposes `renderedContent` alongside backwards-compatible
   raw `content`, with golden fixtures for text, valid data/UI fences,
   malformed fences, adjacent fences, and data modes.
2. Android and iOS SDK decoding tests consume the same normalized payload.
3. The app layers no longer decode/adapt the generic Forge envelope.
4. No title/prompt/workspace heuristic selects a rendering size or semantic
   behavior.
5. Android phone/tablet and iPhone/iPad are verified against the signed-in
   Steward workspace, compared with the same web conversation.

Latest iOS-specific verification (2026-07-18):

```sh
(cd /Users/awitas/go/src/github.com/viant/forge/ios && \
  swift test --filter 'MarkdownFenceParserTests|TranscriptEnvelopeTests')
```

This passes the focused parser/envelope suite. The final independent review
found no remaining concrete iOS ownership, data-scope, no-UI fallback,
multiline-CSV, or runtime-update issue. Android's Forge pipeline likewise
handles compact JSON containing triple backticks safely and passes production
Kotlin compilation. The remaining work is native device/emulator parity, not
generic transcript ownership.

Current device evidence override (2026-08-04): use
`mobile_sdk-progress/README.md` as the authoritative status. Local Steward is
currently verified on `:9292`. Android Pixel Tablet reaches it via
`http://10.0.2.2:9292`, debug OOB completes, the app reaches authenticated
`Ready`, and message submission works. iPad Pro simulator reaches it via
`http://127.0.0.1:9292`, OOB launch succeeds, and forecast report transcript
content renders. Android and iOS local defaults now use this isolated `9292`
test server, and visible workspace presets no longer surface the shared `9191`
port. Latest boundary scan removed one Agently-specific Forge comment and
Android app datasource debug branches for ad-specific lookup ids; Forge native
code remains host/workspace agnostic. The active blocker is no longer OOB/IdP
TCP 443 for this local verification path; forecast-builder completion is
blocked by host-side timeout to
`http://steward.viantinc.com:5000/mcp`. Restore or retarget that MCP transport
before rerunning `Open forecast builder for line 7288336` across Android/iOS
devices.

Latest report-builder variant update (2026-08-05): Forge Android/iOS now
decode `reportBuilderRef` plus `reportBuilders` on generic
`dashboard.reportBuilder` containers and select the requested variant from
`windowForm.reportBuilderRef`, matching web's canonical `reportBuilder`
window plus `parameters.reportBuilderRef` contract. This fixes the mobile
reason `forecastingCubeBuilder` previously showed the default Reports builder
when the backend returned `windowKey=reportBuilder`. Verified commands:

```sh
(cd /Users/awitas/go/src/github.com/viant/forge/android && ./gradlew :sdk:testDebugUnitTest --no-daemon --console=plain)
(cd /Users/awitas/go/src/github.com/viant/forge && swift test --package-path ios)
(cd /Users/awitas/go/src/github.com/viant/agently/android && ./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon --console=plain)
```

The iOS Forge suite now includes
`testWindowMetadataDecodesReportBuilderVariants`,
`testReportBuilderVariantResolutionUsesWindowFormRef`, and
`testReportBuilderVariantResolutionReportsMissingRequestedRef`, covering
canonical `reportBuilderRef` plus multi-variant `reportBuilders` decode and
selection through the Swift model boundary.

The updated Android OOB debug app was reinstalled and launched against
`http://localhost:9292`; OOB authenticated successfully and the tablet sent a
fresh `Open forecast builder for line 7288336` turn. The original
`steward.viantinc.com:5000` blocker was retargeted locally by running Steward
Datly MCP on `127.0.0.1:5002/mcp` and restarting Agently with
`STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. The subsequent Agently-core
tool-surface discovery update below supersedes the earlier live-turn blocker
from unrelated `creative` and `operation` MCP discovery.

Latest code rescan (2026-08-05): Forge iOS passes all 216 Swift tests; Forge
Android SDK tests pass; Agently-core Android SDK `./gradlew test` passes;
Agently Android compile and app unit tests pass. Agently iOS SwiftPM test
drift is repaired:
`swift test --package-path ios` now passes all 92 Agently iOS tests, and the
Agently iOS simulator build passes with `xcodebuild -quiet -project
ios/AgentlyApp.xcodeproj -scheme AgentlyApp -configuration Debug -destination
'generic/platform=iOS Simulator' -derivedDataPath ios/.build/xcode-rescan
build`. The iOS fixes are generic host behavior: platform-compatible composer
lookup presentation, stale lookup pruning, hosted-window parameter seeding,
approval callback request shaping, elicitation schema validation helpers, and
datasource request-body test compatibility.
The latest portable verification rescan also passes Agently-core Android SDK
unit tests, Forge Android SDK unit tests, Forge iOS Swift tests, Agently iOS
Swift tests, and Agently Android compile/unit tests. Production-only boundary
searches show no Steward-specific or line/forecast-builder routing logic in
Forge production sources, Agently mobile production sources, or Agently-core
SDK production sources.

Historical rescan override (2026-08-05): at that scan point no `xcodebuild`,
Gradle, or local `agently serve -a :9292` process was running, and no listener
was bound to `:9292`. Later 2026-08-05 live sections restarted the isolated
lane and completed the four-device proof; inspect the current process table
before assuming either state. `git diff --check` passed in Agently-core,
Agently, and Forge. Focused verification from that working tree passed:
`go test ./service/shared/toolexec ./internal/tool/registry
./runtime/discovery ./service/agent -count=1` in Agently-core;
`./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
--console=plain` in Agently Android; `./gradlew :sdk:testDebugUnitTest
:sdk:compileDebugKotlin --no-daemon --console=plain` in Forge Android;
`swift test --package-path ios` in Forge passed 221 tests; and
`swift test --package-path ios` in Agently passed 92 tests.

Latest Agently-core discovery update (2026-08-05): generic tool-surface
discovery mode is implemented in Agently-core. Pre-model tool-surface registry
discovery now uses a short non-strict timeout, while strict scheduler
discovery and actual tool execution keep their existing behavior. Focused Go
tests pass for `runtime/discovery`, `internal/tool/registry`, and
`service/agent`; broader `go test ./...` in Agently still fails in unrelated
e2e paths (`TestTerminalQueryImageAttachment`,
`TestTerminalQueryJWTInvalidToken`, and
`TestTerminalQueryCoderRepoAnalysisLiveTranscript`).

Local Agently was rebuilt against the local Agently-core replace and restarted
on `:9292` with `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. Android
`Pixel_Tablet` is attached with `adb reverse tcp:9292 tcp:9292` and relaunches
authenticated against `http://127.0.0.1:9292`. A fresh
`open forecast builder for line 7288336` turn now reaches the Forge command
path: logs show bounded optional `creative` discovery, then `ui.window.open`
for canonical `reportBuilder` with
`parameters.reportBuilderRef=forecastingCubeBuilder`, followed by
`ui.data.fetch` and `ui.window.setFormData` returning `ok=true`. The reopened
conversation transcript says the Forecasting workspace is open with line
`7288336` loaded. Superseded by later 2026-08-05 live sections: hosted
Forecasting visual proof and UI-originated sends were subsequently captured on
Android tablet, Android phone, iPad, and iPhone against `:9292`.

Current device evidence (2026-07-19): the iOS `AgentlyApp` project builds for
the iPhone simulator, and the current Android app production compile and debug
assembly pass. The remote Steward endpoint resolves but TCP 443 stalls from
the host and simulator, so no signed-in conversation can be claimed yet.
Android `adb` is available at
`/Users/awitas/Library/Android/sdk/platform-tools/adb`; the configured
`Pixel_10_Pro` and `Pixel_Tablet` emulators are now booted and have the
currently installed Agently app. They show the same remote-workspace loading
state as iOS. Reuse all four running devices after connectivity is restored;
do not create mock conversation evidence.

The current OOB builds were reinstalled on all four targets after the canonical
route change. Android phone and tablet both show the workspace loading state;
the iPhone shows the explicit connection-timeout screen; the iPad shows the
Steward workspace selection screen. This confirms current binaries launch, but
it is not conversation visual-parity evidence. When access returns, validate
the same web conversation on all four devices: completed dashboard content must
render through Forge, phone must show only a conversation bubble rather than
execution details, tablet execution detail visibility must remain configurable,
and embedded surfaces must scroll within the explicit compact/regular bounds.
The real internal Steward workspace now serves at `http://127.0.0.1:9191` after
a generic bounded-auth-metadata correction in Agently core. It is the required
workspace for the parity run, not a mock. OOB session minting is still blocked
because `idp.viantinc.com:443` times out; do not disable auth or synthesize a
session to bypass that prerequisite.
All four targets can already reach this same local workspace: Android via
`10.0.2.2:9191`, iOS via simulator localhost. Once OOB sign-in can contact the
IdP, repoint the existing OOB launch scripts to those local addresses and run
the saved troubleshooting conversation across web, phone, and tablet.

Native Forge parameter parity is also now validated. Android Forge's entire
SDK test suite passes (211 tests), including compact `from`/`to` query and body
parameters at the actual REST request boundary. Android and iOS both project
`InputState` as `{ filter, parameters, fetch, refresh, page? }`; compact
`name: "..."` correctly spreads the whole source object. Two independent
`gpt-5.5` reviews found the Android compact-input and REST-request gaps, and
both are fixed with regression coverage. This is generic Forge behavior, not
Steward-specific logic.

For a review pass, use the requested Codex form from `agently-core`:

```sh
codex exec -m gpt-5.5 -C /Users/awitas/go/src/github.com/viant/agently-core \
  -s danger-full-access --dangerously-bypass-approvals-and-sandbox \
  "review <focused implementation and boundary instruction>"
```

Do not log or embed OOB credentials. Use the existing encrypted local secret
references through the Agently OOB scripts/build properties.

Latest verified commands (2026-07-19):

```sh
go test ./sdk -run 'TestNormalizeRenderedContent|TestReduceHydratesRenderedContentForLiveAssistantPage|TestBuildCanonicalState_ExtractsStandaloneAssistantFinal|TestParity_NormalTurn|TestMobileSDKPublicSurfacesCoverClientContract' -count=1
(cd sdk/ts && npm test)
(cd sdk/ios && swift test --filter AgentlySDKTests/testRenderedContentDecodesWithoutDiagnostics)
(cd sdk/android && ./gradlew compileDebugKotlin --rerun-tasks)
(cd /Users/awitas/go/src/github.com/viant/forge/ios && swift test --filter 'MarkdownFenceParserTests|TranscriptEnvelopeTests|TranscriptInlinePresentationTests')
(cd /Users/awitas/go/src/github.com/viant/forge/android && ./gradlew :sdk:compileDebugKotlin --rerun-tasks)
(cd /Users/awitas/go/src/github.com/viant/forge/android && ./gradlew :sdk:testDebugUnitTest --no-daemon --console=plain)
(cd /Users/awitas/go/src/github.com/viant/agently/android && ./gradlew :app:compileDebugKotlin --no-daemon --console=plain)
(cd /Users/awitas/go/src/github.com/viant/agently/android && ./gradlew :app:assembleDebug --no-daemon --console=plain)
```

## Latest Pairing Rescan (2026-07-19)

The four running targets are now correctly pointed at the real local Steward
workspace: Android phone/tablet use `http://10.0.2.2:9191`; iPhone/iPad use
`http://localhost:9191`. The Android targets visibly reach the expected
OAuth-required state. The iOS targets visibly reach local Steward, list the
OAuth provider, and report only a concise saved-sign-in retry message after
the upstream IdP timeout.

`scripts/launch-ios-oob-sim.sh` was corrected to pass `--enableDevAuth=1`
alongside its existing test-only endpoint/OOB arguments. The iOS app already
requires this explicit gate before honoring launch overrides; the script now
matches that contract without changing normal production configuration.

The rescan also corrected a phone privacy leak in
`ios/Sources/AgentlyAppFoundation/Auth/AuthRuntime.swift`: upstream OAuth/OOB
transport failures are now private in logs and are converted into safe
user-facing messages. A focused `AuthRuntimeTests` regression test was added.
`bash -n` and the iOS simulator build pass. The test itself cannot run via
Xcode because the project schemes have no test action, while `swift test` is
still blocked by the existing macOS-only Composer/colour compilation failures.

The only remaining acceptance blocker is unchanged: local Steward returns the
expected unauthenticated response, but its OOB flow times out at the upstream
IdP. Once that is restored, use the already installed local-endpoint builds to
open the saved web conversation and compare the completed rendered transcript
across phone/tablet and iPhone/iPad. Do not synthesize a session or use mock
conversation output.

Follow-up pairing fixed a duplicate iOS OOB trigger: app bootstrap and the
auth screen both initiated automatic OOB login. Bootstrap is now the sole
automatic owner, and `AuthRuntime.beginOOBLogin` has a shared single-flight
guard. New iPhone/iPad screenshots confirm the failed upstream request now
settles with a concise safe message and an enabled `Sign In` button, rather
than a lingering spinner. The latest iOS simulator app build passes. Keep the
same constraint for the next session: do not replace the unavailable IdP with
a fake session; signed-in transcript parity must use the real Steward flow.

The latest Android phone rescan also corrected the generic auth-card geometry:
it now fills compact width and wraps its content, while tablet width remains
capped at 760dp. The reinstalled phone no longer shows the previous embedded
scrollbar or empty full-height card. Its visible session-recovery controls are
debug-only content, not a layout artifact.

The current iOS follow-up tightened browser OAuth single-flight behavior: the
shared submission state now spans URL initiation, browser authentication, and
callback exchange, and a direct callback is rejected while sign-in is active.
Callback configuration errors now use generic administrator guidance rather
than showing callback URIs or returned endpoint URLs. The iPhone was rebuilt
and launched through the OOB script against local Steward; after the real IdP
timeout it settles with a safe error and enabled Sign In action. The required
Codex `gpt-5.5` review was attempted, but this local CLI failed to emit a final
review artifact after its inspection trace, so it must be rerun when reviewer
output works. The signed-in cross-platform transcript comparison is still
unproven and must use real IdP authentication rather than a manufactured
session.

## Authorization Follow-Up (2026-07-19)

The normal auth card now has a `Workspace settings` gear beside the exact
message `This workspace requires authorization.`. It keeps `Sign in` as the
only normal action, and that action always starts browser OAuth. The gear opens
the existing workspace endpoint picker, so switching between Steward/local
endpoints does not require revealing an endpoint on the auth surface.

Developer OOB is a distinct, explicit action on Android and iOS only when the
developer gate and OOB reference are present. It never replaces `Sign in`.
Android removed the legacy persisted IDP username/password helper entirely,
including a preference cleanup migration; no WebView credential injection
remains. Its OAuth WebView no longer reloads on normal IdP redirects and only
accepts the configured callback URI identity.

Current verification: Android production compile and debug APK assembly pass;
iOS simulator build against `238A6C82-3508-4785-9BAE-CF9413139636` passes. A
fresh Android phone rendering was inspected: normal auth shows only the message,
settings gear, and Sign in; settings exposes the Steward/local endpoint picker.
The rebuilt iPhone 17 Pro was also visually inspected and presents the same
message, settings gear, and Sign in action.
The independent `gpt-5.5` review found no functional regression after this
change. Continue with the pending signed-in iPhone/iPad conversation parity
inspection; do not fabricate sessions.

## Resume Point (2026-08-05)

Current local Steward lane is `:9292`, not `:9191`. Keep Android tablet on
`adb reverse tcp:9292 tcp:9292` with base URL `http://127.0.0.1:9292`. Start
the server with `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp` when local Steward
MCP is available; otherwise the workspace may fall back to the remote
`steward.viantinc.com:5000` MCP and stall.

Implemented and verified today:
- Agently Core hosted-workspace restore now recognizes `ui/window/open` in
  Android, iOS, and TS SDKs, with later `ui/window/setFormData` response
  `windowForm` patches applied as authoritative restore state.
- Forge Android/iOS report-builder models decode `reportBuilder.title` and
  `subtitle`; native report-builder panels render the resolved builder header.
- Forge Android/iOS dashboard rendering no longer wraps `dashboard.reportBuilder`
  in an extra generic dashboard card, avoiding the duplicated `Reports` shell
  around Forecasting.
- Agently registry/tool-surface discovery now applies a short non-strict
  tool-surface timeout and skips already-cooled-down optional servers without
  warning or manager calls. Strict scheduler discovery still returns cooldown
  errors.
- Tool-router auto-selected bundles now narrow broad agent default bundles for
  that turn instead of merging with every default bundle. Explicit
  caller/workspace metadata bundles still keep the old merge behavior, and
  auto-selected bundles are not persisted as conversation metadata. This keeps
  unrelated optional MCP servers out of UI/forecasting turns without
  special-casing Steward, Forge, or `creative`.
- Intake/planner-appended bundles are also marked as runtime-selected when they
  start from no explicit or metadata bundle selection. A first Android rerun
  proved the remaining gap by still touching `creative`; after this fix, a
  fresh Android tablet turn opened Forecasting with no unrelated `creative` or
  `operation` MCP discovery warning.
- Agently Android/iOS hosted-workspace labels split camelCase window keys
  generically, so fallback hosted labels render as `Report Builder` and
  `Forecasting Cube Builder` instead of collapsed title case.

Verification passed:
- Agently Core focused Go tests for discovery/registry/service agent.
- Android/TS/iOS SDK restore tests.
- Forge Android `ReportBuilderStateStorageTest`.
- Forge iOS `ForgeIOSTests/testReportBuilderVariantResolutionUsesWindowFormRef`.
- Agently Android `HostedWorkspacePresentationTest`.
- Agently iOS `HostedWorkspacePresentationTests`.
- Agently Core
  `TestResolveToolControl_AutoSelectedRuntimeBundlesNarrowAgentDefaults` and
  `TestApplyPlannerOutput_MarksNewRuntimeBundlesAutoSelected`,
  `TestApplyPlannerOutput_PreservesExplicitBundleMergeSemantics`, and
  `TestEnsureConversation_DoesNotPersistAutoSelectedToolBundles`.
- Android app rebuild/install through `scripts/install-android-oob-debug.sh`.
- `git diff --check` in `agently`, `agently-core`, and `forge`.

Live Android tablet state:
- Clean no-wrapper visual evidence is captured at
  `/tmp/agently-rescan/android-forecasting-local-mcp-final-20260805.png`.
  Fresh prompt: `open forecast builder for line 7288336`. Result: hosted
  Forecasting pane opens, resolved `Forecast Inventory` builder is visible, and
  the generic outer `Reports` wrapper is gone.
- Latest fresh prompt after the intake/planner auto-bundle fix used
  conversation `5e91aaef-b441-4784-8523-96a78208b91f`. Server logs show
  `ui.window.open`, `ui.data.fetch`, and `ui.window.setFormData` for
  `reportBuilderRef=forecastingCubeBuilder`, with no unrelated optional MCP
  discovery warning before or after. Screenshot:
  `/tmp/agently-rescan/android-forecasting-rerun-auto-bundle-final-after-setform-20260805.png`.

Superseded open issue:
- iPhone/iPad did rerun the same prompt against `:9292` in later 2026-08-05
  live sections. Current native send status is four-device Forecasting proof,
  not Android-tablet-only proof.

Latest rescan follow-up:
- Native stream hydration duplicate handling was tightened in Android and iOS
  SDKs. Exact hydrated event sequences are skipped after hydration even if the
  queued SSE event timestamp is post-cursor, while lower-numbered live
  post-cursor deltas still apply. This preserves the active-turn SSE versus
  historical transcript separation without using transcript max sequence as a
  cursor watermark.
- Historical verification from that follow-up passed for Agently Core Go
  discovery/registry/agent tests, Agently Core iOS SwiftPM tests, Android
  Agently Core SDK unit tests, TS hosted restore tests, Android Forge SDK
  compile/unit tests, Android app unit tests, and Forge iOS SwiftPM tests.
  Later sections close the iPhone/iPad live prompt parity gate.

2026-08-05 iPhone line-prefill rescan:
- iPhone opened the Forecasting hosted pane from
  `open forecast builder for line 7288336`, proving the window-open/list path
  is no longer the active failure.
- The transcript showed the executable line intake rule was still leaking the
  line id into `AudienceId` / `audienceIds`, so the builder set only audience
  scope instead of line targeting-derived prefill filters.
- Fixed Steward `intake/activation_rules.yaml` and Agently Core
  `service/agent/intake_query_test.go` so line builder-open rules preserve
  `AdLineId` only. Verified with
  `go test ./service/agent -run 'OpenForecastBuilderForLine'`, the Steward
  Forecasting builder JS tests, rebuilt `agently/agently`, and restarted
  `:9292`.
- Restarted `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`; OOB auth and local Steward MCP
  are reachable.
- Patched stale Steward prompt text in `agents/steward/prompt/parts/routing.md`,
  `system.tmpl`, and `instruction.tmpl` so line builder-prefill calls
  `steward-AdTargetingProfile` with `AdLineId` instead of converting the line
  to `AudienceId`.
- Clean iPhone rerun `cc52a862-6070-4990-98c5-286052e4d67e` still did not
  populate targeting filters. It activated `forecast-targeting` with
  `Builder-prefill for AdLineId 7288336`, then opened Forecasting and called
  `ui/window:setFormData`, but persisted restore state still contains only
  `prefill.scope.audienceIds:[7288336]` and no include/exclude predicates.
  Remaining work is to make the Steward builder-prefill path deterministically
  call `steward-AdTargetingProfile` before form mutation.
- The iPhone still showed the `Streaming response` banner after the host
  API-driven turn completed; verify again from a device-originated send before
  treating that as an SDK lifecycle regression.

Latest 2026-08-05 follow-up:
- Local Steward MCP now exposes `steward:AdTargetingProfile`; the missing route
  was fixed by syncing canonical Datly `steward/metadata/ad_profile` into the
  local dev route tree and refreshing `repo/dev/Datly/routes/paths.yaml`.
- `AdTargetingProfile` now has a first-class `AdLineId` input exposed as
  `line_id`, mapped to `au.ID`. Live `/v1/tools` confirms `AdLineId` is present
  in the `steward:AdTargetingProfile` schema.
- Fresh phone-context prompt on `:9292`, conversation
  `linealias-29ba1e90-b65a-4ec0-ba03-c394e4205ab8`, stored
  `{"AdLineId":[7288336],"timeoutMs":600000}` for the profile request. Datly
  audit logged `/v1/api/steward/metadata/ad_profile?line_id=7288336`, so the
  active turn no longer mixes line intent into `AudienceId`.
- Remaining blocker is local Steward Datly dependency health, not mobile/Forge:
  `ad_profile` fails before returning data because user-context auth queries
  cannot connect to MySQL `127.0.0.1:3307`. Start/fix that dependency, then
  rerun `open forecast builder for line 7288336` and verify the resulting
  `ui/window:setFormData` contains normalized targeting include/exclude filters.
- Verification passed: Steward forecast-targeting contract test, live OOB auth,
  live tool schema check, live phone-context request inspection, and
  `git diff --check` in Steward, Steward workspace, and Agently Core. Focused
  Go metadata test is blocked by an unrelated current compile error in
  `pkg/steward/acl/auth/handler.go` (`AccountId` string vs int).

Latest 2026-08-05 identity/data rescan:
- Docker and the local MySQL dependency are now running, but the required
  `ci_ads` verification data is absent. `CI_AUDIENCE`, `CI_AD_ORDER`,
  `CI_CONTACTS`, `CI_ACCOUNT`, `CI_ADVERTISER`, and `CI_AGENCY` all have zero
  rows in `mysql_dev`, so `AdTargetingProfile` fails at user-context auth with
  `403 user access denied`.
- Rebuilt Agently Core/Agently after fixing hosted-window identity for aliased
  views. The generic renderer key is still `reportBuilder`, but the hosted
  window identity now comes from the view `id`.
- Fresh phone-context rerun on `:9292`, conversation
  `identityfix-59312a8a-ea98-4b8f-afab-fc29cd09166a`, stored the profile
  request as `{"AdLineId":[7288336],"timeoutMs":600000}` and opened
  `windowId=forecastingCubeBuilder__identityfix-59312a8a-ea98-4b8f-afab-fc29cd09166a`
  with `windowKey=reportBuilder` and
  `parameters.reportBuilderRef=forecastingCubeBuilder`.
- Verification passed:
  `go test ./protocol/tool/service/ui/view -run 'TestComputeWindowID|TestOpenReturnsWindowIdVisibleToWindowList|TestOpenCanonicalizesReportStarterAcrossCommandOutputAndEvent'`,
  `go build -o /Users/awitas/go/src/github.com/viant/agently/agently/agently ./agently`,
  local OOB auth on `:9292`, live phone-context prompt inspection, and
  `git diff --check` in Agently Core.
- Remaining work: point local Steward Datly at a populated `ci_ads` source or
  seed the minimal authorized line/order/contact graph for line `7288336`, then
  rerun the phone prompt and require `ui/window:setFormData` to contain the
  normalized targeting include/exclude filters, not just scope/source metadata.

Latest 2026-08-05 forecasting prefill verification:
- Seeded local `mysql_dev` `ci_ads` with the minimal authorized graph for OOB
  user `34012`, account/advertiser `900001`, campaign `900002`, order
  `2664518`, line/audience `7288336`, postal-list `70731`, and 18 PMP deal
  rows. Optional campaign/order date fields were set `NULL` because this local
  MySQL connection returns DATETIME values as bytes without parse-time support.
- Restarted Datly MCP on `:5002` and Agently on `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. Detached `nohup` Agently still
  exits silently; foreground serve sessions are stable for verification.
- Fresh phone-context CLI run with mobile OAuth scope completed in conversation
  `bf0a8c7d-a629-48fc-9d61-59fb6f5658ea`.
  `steward/AdTargetingProfile` completed with request
  `{"AdLineId":[7288336],"Limit":1,"timeoutMs":600000}`.
  `ui/view:open` returned
  `windowId=forecastingCubeBuilder__bf0a8c7d-a629-48fc-9d61-59fb6f5658ea`,
  `windowKey=reportBuilder`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`.
  `ui/window:setFormData` completed with `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, and all 18 `includeDealsPmp` values.
- Verification passed: focused Agently Core hosted-window Go tests, focused
  Steward auth/metadata Go tests, Forge Android SDK compile/unit tests,
  `git diff --check` in Agently Core, Steward, and Forge, local OOB auth with
  `ROLE_STEWARD_MOBILE`, and live payload inspection.
- iPhone/iPad native render proof was captured after launching both simulators
  against `http://127.0.0.1:9292` with OOB auto sign-in. iPhone screenshot:
  `/tmp/agently-rescan/ios-iphone-local-9292-after-launch-20260805-050028.png`.
  iPad screenshot:
  `/tmp/agently-rescan/ios-ipad-local-9292-forecast-builder-bf0a8c7d-20260805-050147.png`.
  Both render the verified Forecasting builder conversation through the native
  aliased `reportBuilder` surface, with chart data visible and without the
  generic default Reports shell.
- Detached serve rescan suggests this Codex tool wrapper cleans up descendant
  processes when a command returns; Agently stayed alive during the command and
  foreground serve remains stable. Treat foreground sessions as the reliable
  verification path inside Codex.
- Superseded remaining work: later 2026-08-05 sections prove actual
  UI-originated sends from iPad, iPhone, Android tablet, and Android phone
  against foreground Datly/Agently services on `:9292`, with DB/tool payload
  inspection and visual evidence.

Latest 2026-08-05 code rescan:
- `git diff --check` passes in Agently, Agently Core, Forge, and Steward.
- Verification passed:
  `go test ./internal/tool/registry ./runtime/discovery ./protocol/tool/service/ui/view ./service/agent`,
  Steward report-builder/forecasting contract checks, Forge iOS `swift test`
  with 219 XCTest cases, Android
  `:forge-sdk:compileDebugKotlin :app:testDebugUnitTest`, iOS `AgentlyApp`
  simulator build, and `AgentlyAppLiveUITests` `build-for-testing`.
- Superseded open gate: later 2026-08-05 sections ran the live UI-originated
  iPhone/iPad sends for `open forecast builder for line 7288336` and captured
  conversation/tool evidence.

Latest 2026-08-05 native UI-originated iOS verification:
- `AgentlyAppLiveUITests` now launches with a per-run
  `--uiBridgeClientID=ios-ui-test-<UUID>` and handles the generic phone
  navigation stack by walking back from restored detail views until
  `agently-new-chat` is visible.
- iPad Pro 11-inch live UI test passed on
  `B2AA0D68-7312-4CC9-85B8-0544341A942D`. Native composer conversation
  `0d9dbcf9-064f-42bf-bc29-a9b7c324bc0e` succeeded. Payload proof:
  `AdLineId:[7288336]`; `ui/view/open` returned
  `windowId=forecastingCubeBuilder__0d9dbcf9-064f-42bf-bc29-a9b7c324bc0e`,
  `windowKey=reportBuilder`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`; and
  `ui/window:setFormData` applied `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, plus all 18 PMP deal ids. Screenshot:
  `/tmp/agently-live-ui/ipad-live-ui-run5-20260805-0539.png`.
- iPhone 17 live UI test passed on
  `59317EFB-ADFE-4A22-817F-4B4F6658AB2E`. Native composer conversation
  `74b86d2c-4346-4869-ab3f-daca53036d14` succeeded. Payload proof:
  `AdLineId:[7288336]`; `ForecastingTargetingConvert` returned
  `includeCountry:["US"]`, `includePostalCodeList:[70731]`, and all 18
  `includeDealsPmp` values; `ui/view/open` returned
  `windowId=forecastingCubeBuilder__74b86d2c-4346-4869-ab3f-daca53036d14`,
  `windowKey=reportBuilder`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`; and
  `ui/window:setFormData` completed `ok:true`. Screenshot:
  `/tmp/agently-live-ui/iphone-live-ui-run3-20260805-0548.png`.
- Note: the iPad run recorded duplicate identical `ui/window:setFormData`
  completions for the same client/window/value payload. Final state is correct
  and idempotent; keep an eye on replay noise separately.
- Current open work after this pass: Android command can be rerun for parity if
  needed, but the key iPhone/iPad UI-originated Forecasting prefill gate is now
  closed on local `:9292`.

Latest 2026-08-05 Android tablet UI-originated verification:
- Pixel Tablet AVD `emulator-5554` was attached. The emulator gateway route
  reported `Network is unreachable` for `10.0.2.2:9292` and `10.0.3.2:9292`,
  so verification used `adb reverse tcp:9292 tcp:9292` plus
  `AGENTLY_ANDROID_BASE_URL=http://localhost:9292`.
- Cleared Android app data, reinstalled the debug OOB build, selected
  `Localhost 9292` on the first-run workspace picker, and reached the
  authenticated conversation list.
- `scripts/android-semantic-compose-replay.sh` now generically matches either
  `content-desc` or visible `text`; its self-test passes. This was needed
  because the tablet composer exposed `Message`/`Send` as text in the
  UIAutomator tree.
- Android semantic replay sent `open forecast builder for line 7288336` and
  verified visible `Forecasting`. Screenshot:
  `/tmp/agently-live-ui/android-forecasting-live-replay-20260805.png`.
- Fresh Android conversation `b56f0685-04e6-46bf-8093-44e39655690d` succeeded.
  Payload proof: `AdLineId:[7288336]`;
  `ForecastingTargetingConvert` returned `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, and all 18 PMP deal ids; `ui/view/open`
  returned
  `windowId=forecastingCubeBuilder__b56f0685-04e6-46bf-8093-44e39655690d`,
  `windowKey=reportBuilder`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`; and
  `ui/window:setFormData` completed `ok:true` with the normalized prefill.
- Current native send status after this pass: Forecasting prefill is proven on
  iPad, iPhone, and Android tablet against local `:9292`. Android phone proof
  remains open until a phone emulator/device is attached.

Latest 2026-08-05 Android phone UI-originated verification:
- Booted Pixel 10 Pro AVD `emulator-5556` alongside the tablet emulator and
  used `adb reverse tcp:9292 tcp:9292` with
  `AGENTLY_ANDROID_BASE_URL=http://localhost:9292`.
- Cleared only the phone app data, installed the debug OOB build, selected
  `Localhost 9292` on first run, and reached the authenticated phone
  new-conversation surface.
- Android semantic replay sent `open forecast builder for line 7288336` using
  visible `Ask anything` and `Send` labels and verified visible `Forecasting`.
  Screenshot:
  `/tmp/agently-live-ui/android-phone-forecasting-live-replay-20260805.png`.
- Fresh Android phone conversation
  `0cc9d393-2d2f-45de-b4fe-abed975d06a7` succeeded. Payload proof:
  `AdLineId:[7288336]`; `ForecastingTargetingConvert` returned
  `includeCountry:["US"]`, `includePostalCodeList:[70731]`, and all 18 PMP
  deal ids; `ui/view/open` returned
  `windowId=forecastingCubeBuilder__0cc9d393-2d2f-45de-b4fe-abed975d06a7`,
  `windowKey=reportBuilder`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`; and
  `ui/window:setFormData` completed `ok:true` with the normalized prefill.
- The phone run recorded duplicate identical `ui/window:setFormData`
  completions, matching the earlier iPad replay-noise observation. Final state
  is correct and idempotent.
- Follow-up traced the duplicate completions to parallel identical tool calls
  reaching Agently-core registry before the existing recent-result cache was
  populated. The registry now coalesces in-flight identical unprotected calls
  through the same generic recent-call memoization path; this is not
  Steward-, Forge-, or mobile-app-specific. Focused registry/discovery/agent
  tests pass. Local Agently was rebuilt/restarted on `:9292`, and Android
  Pixel Tablet conversation `6f05b293-7998-427c-aacd-399f4bab2e98` verified
  visible Forecasting with exactly one `ui/window/setFormData` tool row
  (sequence 12) and exactly one `ui.window.setFormData` server UI command.
  Evidence:
  `/tmp/agently-live-ui/android-tablet-after-coalescing-20260805.png`.
  Fresh iPad conversation `3ddd7bfe-5324-40b7-9eb8-aea769df7419` was rerun
  after the same rebuild and also recorded exactly one
  `ui/window/setFormData` tool row (sequence 13). Evidence:
  `/tmp/agently-live-ui/ipad-after-coalescing-20260805.png`.
  Fresh iPhone conversation `70fdb977-47d0-4f03-84de-7addd84f9b9a` was rerun
  after the same rebuild through `AgentlyAppLiveUITests`; the test passed,
  Forecasting was found by the native accessibility tree, and exactly one
  `ui/window/setFormData` tool row was recorded (sequence 12). Fresh Android
  phone conversation `f7cf4ad0-caac-4d53-a872-1c899f9938b9` was rerun after
  the same rebuild through `scripts/android-semantic-compose-replay.sh`;
  Forecasting was visible on the Pixel 10 Pro AVD and exactly one
  `ui/window/setFormData` tool row was recorded (sequence 12). Android phone
  evidence:
  `/tmp/agently-live-ui/android-phone-after-coalescing-20260805.png`.
- Current native send status: Forecasting prefill is proven on iPad, iPhone,
  Android tablet, and Android phone against local `:9292`; the fresh
  post-coalescing no-duplicate proof is now proven on Android tablet, iPad,
  iPhone, and Android phone.

Latest 2026-08-05 canonical inline report visual proof:
- Created authenticated Steward conversation
  `a5fc8e9f-8d48-431c-9cb9-820a819eb7aa` through the local `:9292` OOB query
  path. The assistant response contains a committed `report-document-v1`
  inline report. Diagnostic evidence was unavailable in that turn, so this is
  renderer and transcript-parity proof, not business-diagnosis proof for ad
  order `2664518`.
- Web proof captured native inline report rendering at
  `/tmp/agently-live-ui/inline-report-web-a5fc8e9f-20260805.png`.
- iPhone proof captured the same committed report at
  `/tmp/agently-live-ui/inline-report-ios-phone-a5fc8e9f-20260805.png`.
- iPad proof captured the same committed report at
  `/tmp/agently-live-ui/inline-report-ios-ipad-a5fc8e9f-20260805.png`.
- Android tablet proof captured the same committed report at
  `/tmp/agently-live-ui/inline-report-android-tablet-a5fc8e9f-20260805-final.png`.
  This Pixel Tablet AVD required `adb reverse tcp:9292 tcp:9292`; direct
  emulator aliases `10.0.2.2:9292` and `10.0.3.2:9292` were unreachable.
- Android phone proof captured the same committed report at
  `/tmp/agently-live-ui/android-phone-inline-report-a5fc8e9f-20260805.png`.
  The phone was booted as `emulator-5556`, installed with the debug OOB build,
  selected the `Localhost 9292` workspace, authenticated, and opened the same
  recent conversation. The UI tree confirmed `Ad order 2664518 delivery
  troubleshooting`, `20 report blocks`, and `Primary read`.
- Current inline-report status: web, iPhone, iPad, Android tablet, and Android
  phone visual proof are complete for the same committed conversation.

Latest 2026-08-05 review follow-up:
- Delegated review found and the code now fixes four post-parity issues:
  Steward diagnostic/report formatting was removed from Agently-core generic
  direct-action production code; Agently iOS active-conversation test overrides
  are gated behind developer auth mode; Android/iOS SDK hosted restore can
  apply `setFormData` by unique `windowKey` when `windowId` is absent; and
  Forge iOS report-builder rendering supports variant-only `reportBuilders`
  without requiring legacy fallback config.
- Verification passed:
  `go test ./service/agent -run 'DirectAction|NormalizeInterfaceMap' -count=1`;
  Agently-core iOS SDK hosted restore tests; Android Agently-core SDK hosted
  restore tests through the Agently Android Gradle project; Agently iOS
  `AppStateTargetingTests`; and Forge iOS
  `testReportBuilderVariantResolution`.
- Boundary scan now finds no Steward diagnostic/report formatter symbols in
  Agently-core direct-action production code or mobile SDK production sources.
- Follow-up review found and Forge now fixes one remaining generic
  report-builder lifecycle issue: metrics and forecasting variants no longer
  share the same persisted state or chart preset key, and iOS resets/re-hydrates
  report-builder selections when `windowForm.reportBuilderRef` changes after
  the view is already mounted. Focused Forge iOS variant tests and Android
  `ReportBuilderStateStorageTest` pass with the new key isolation coverage.
- Final delegated review of that Forge variant-state fix reported no findings
  and independently reran Forge Android `ReportBuilderStateStorageTest` plus
  Forge iOS `swift test --filter ForgeIOSTests`.

Latest 2026-08-05 code rescan and Android tablet replay:
- Local Steward remains up on `:9292` with Datly MCP on `:5002`; Android Pixel
  Tablet is attached, and iPhone/iPad simulators are booted.
- Android tablet composer automation selectors were restored in the Agently app
  so the replay harness can locate `new_conversation_composer_input`,
  `reply_composer_input`, `send_new_conversation`, and `send_reply` resource
  ids/test tags on the tablet workspace path while screen readers keep
  user-facing `Message` and `Send` labels.
- Rebuilt/reinstalled the Android debug OOB build against
  `AGENTLY_ANDROID_BASE_URL=http://localhost:9292`; Gradle reported
  `BUILD SUCCESSFUL`.
- Reran
  `./scripts/android-semantic-compose-replay.sh --device emulator-5554 --prompt
  "open forecast builder for line 7288336" --expect "Forecasting" --wait 120`.
  The script found the composer selectors, sent the prompt, and completed with
  `verified: Forecasting`.
- Fresh conversation `71843d7a-033e-4db8-af42-350444a4d9b2` proved the
  expected path: `AdLineId:[7288336]`; `ui/view/open` returned
  `windowId=forecastingCubeBuilder__71843d7a-033e-4db8-af42-350444a4d9b2`,
  `windowKey=reportBuilder`, `windowTitle=Forecasting`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`; `ui/window/setFormData`
  completed once with `ok:true` and prefilled US, postal `70731`, all 18 PMP
  deals, and scope `audienceIds:[7288336]` / `adOrderIds:[2664518]`.
- Evidence screenshot:
  `/tmp/agently-live-ui/android-tablet-semantic-replay-forecasting-20260805.png`.

Latest 2026-08-05 fresh rescan after registry-cache rebuild:
- Re-scanned the current tree and runtime lane. Steward is listening on
  `:9292`, local Datly MCP is alive on `:5002`, Pixel Tablet `emulator-5554`
  is attached, and `adb reverse tcp:9292 tcp:9292` is active.
- Focused checks passed:
  `go test ./internal/tool/registry -run 'RecentResults|ExecutionProtection'
  -count=1`, `./scripts/android-semantic-compose-replay.sh --self-test`, and
  the Android app `AppEndpointConfigTest` Gradle run.
- Fresh live tablet replay completed with `verified: Forecasting`.
  Conversation `13c21cfb-ab59-446c-804b-30a6af49f9f7`, turn
  `d067ea8a-980a-40cd-9c49-812bd334ea65`, used
  `AdLineId:[7288336]`, opened
  `windowId=forecastingCubeBuilder__13c21cfb-ab59-446c-804b-30a6af49f9f7`
  with `windowKey=reportBuilder`, `windowTitle=Forecasting`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`, then completed
  exactly one `ui/window/setFormData` row.
- The fresh `setFormData` request contains normalized `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, all 18 `includeDealsPmp` values, and scope
  `audienceIds:[7288336]`, `adOrderIds:[2664518]`,
  `targetKey:"line:7288336"`.
- Evidence screenshot:
  `/tmp/agently-live-ui/android-tablet-after-latest-rescan-20260805.png`.

Latest 2026-08-05 iPad executor-coalescing rescan:
- Re-scanned the current code. Agently still uses
  `replace github.com/viant/agently-core => ../agently-core`, so the local
  Agently-core executor change is in the Agently build. Focused checks passed:
  `go test ./service/shared/toolexec -run
  'CoalescesConcurrentDuplicateActiveTurnSteps|RetryBehavior' -count=1` and
  `go test ./internal/tool/registry -run
  'RecentResults|ExecutionProtection' -count=1`.
- Restarted local Steward on `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. The server loaded
  `reportBuilder` and `forecastingCubeBuilder` from the Steward Forge
  workspace.
- Reran the iPad Pro 11-inch live UI-originated Xcode test
  `AgentlyAppLiveUITests/ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer`;
  Xcode reported `** TEST SUCCEEDED **`.
- Fresh iPad conversation `73871a78-5e18-4d44-a8d6-66b4b6cfa26a`, turn
  `e9b52209-fc05-4912-b4b1-b199b0b4529b`, opened Forecasting with
  `windowId=forecastingCubeBuilder__73871a78-5e18-4d44-a8d6-66b4b6cfa26a`,
  `windowKey=reportBuilder`, `windowTitle=Forecasting`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`.
- Strict DB check now shows exactly one completed `ui/window/setFormData` row
  for the fresh iPad turn. The decoded request contains `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, all 18 `includeDealsPmp` values,
  `sharedIncludeFilters` from the canonical converter, and scope
  `audienceIds:[7288336]`, `adOrderIds:[2664518]`,
  `targetKey:"audience:7288336"`.
- Evidence screenshot:
  `/tmp/agently-live-ui/ipad-live-ui-after-toolexec-coalesce-20260805.png`.

Latest 2026-08-05 Android tablet protocol-coalescing rescan:
- A fresh Android Pixel Tablet replay against the first executor-coalescing
  build exposed a protocol gap. Conversation
  `cf5fa5b9-bed7-4551-b430-5a61159d3066` opened Forecasting and completed one
  UI side-effect row, but the turn failed with OpenAI
  `No tool output found` because the model had emitted two parallel
  `ui_window-setFormData` call ids and only one tool output was persisted.
- Agently-core now persists a protocol-visible tool result for coalesced
  duplicate active-turn tool calls while still sharing the first registry
  execution. The duplicate call id uses `coalesced` status, preserving a single
  `completed` UI side-effect row while satisfying model continuation protocol.
- Focused checks passed:
  `go test ./service/shared/toolexec -run
  'CoalescesConcurrentDuplicateActiveTurnSteps|RetryBehavior' -count=1` and
  `go test ./internal/tool/registry -run
  'RecentResults|ExecutionProtection' -count=1`.
- Rebuilt Agently, restarted local Steward on `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`, and reinstalled the Android
  debug OOB build against `http://localhost:9292`. The current Pixel Tablet
  AVD reaches the host through `adb reverse tcp:9292 tcp:9292`; direct
  `10.0.2.2:9292` and `10.0.3.2:9292` probes failed during this run.
- Reran
  `./scripts/android-semantic-compose-replay.sh --device emulator-5554
  --prompt "open forecast builder for line 7288336" --expect "Forecasting"
  --wait 120`. The script reported `verified: Forecasting`.
- Fresh conversation `9615ac11-e0e2-4440-af02-ac840f97595f`, turn
  `3fd6d694-f100-4bcc-8735-a8fd07ed4735`, has turn status `succeeded` and a
  final assistant message. The live model again emitted duplicate
  `ui/window/setFormData` calls; the DB now shows one real `completed` row and
  one protocol-visible `coalesced` row for the duplicate, both with the same
  request payload. The decoded request contains
  `includeCountry:["US"]`, `includePostalCodeList:[70731]`, all 18
  `includeDealsPmp`, converter `sharedIncludeFilters`, and scope
  `audienceIds:[7288336]`, `adOrderIds:[2664518]`.
- Evidence screenshot:
  `/tmp/agently-live-ui/android-tablet-protocol-coalesced-live-20260805.png`.
- Fresh iPad Pro live sanity test after the same server-side protocol fix:
  `xcodebuild test -project ios/AgentlyApp.xcodeproj -scheme
  AgentlyAppLiveUITests -destination
  'platform=iOS Simulator,id=B2AA0D68-7312-4CC9-85B8-0544341A942D'
  -only-testing:AgentlyAppUITests/ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer`
  succeeded. Conversation `c9aa6577-a9ac-4a14-82b5-01f7e1016b2d`, turn
  `8cfffdd2-c43a-47dd-b113-e3350fb83b45`, has turn status `succeeded` and
  exactly one completed `ui/window/setFormData` row. The decoded request
  contains the same normalized US/postal/PMP prefill and line/audience scope.
- iPad evidence screenshot:
  `/tmp/agently-live-ui/ipad-live-ui-protocol-coalesced-current-20260805.png`.

Latest 2026-08-05 four-target Forecasting live rescan:
- Re-scanned Agently-core, Agently, Forge, and Steward. `git diff --check`
  passes in Agently-core, Agently, and Forge. Boundary searches remain clean:
  Forge native production sources have no Agently, Steward, forecasting-window,
  line-id, or ad-lookup special casing; Agently mobile production sources have
  no forecasting-window or ad-lookup special casing. Remaining
  `forecastingCubeBuilder` and `7288336` references in Agently-core are in
  generic service tests and SDK restore fixtures.
- Rebuilt local Agently and restarted local Steward on `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. The workspace loaded
  `reportBuilder` plus `forecastingCubeBuilder`, and all fresh device runs
  opened web-compatible `windowKey=reportBuilder` with
  `parameters.reportBuilderRef=forecastingCubeBuilder`.
- Android tablet passed semantic replay. Conversation
  `911d5bbc-b3a2-4825-b180-c5a216ec2090`, turn
  `7804c625-80f9-47aa-874b-33e9cac0caeb`, succeeded with one completed
  `ui/window/setFormData` row. Evidence:
  `/tmp/agently-live-ui/android-tablet-forecast-20260805-current.png`.
- iPad Pro live UI test passed. Conversation
  `b2b76aa2-6610-4c5e-b082-28ee60b26fca`, turn
  `afa06745-c308-49ba-bdb4-700c2944448a`, succeeded with one completed
  `ui/window/setFormData` row. Evidence:
  `/tmp/agently-live-ui/ipad-forecast-replay-20260805-current.png`.
- iPhone live UI test passed. Conversation
  `3dd57065-38ed-4ba0-96d9-7a85eb3c1c8e`, turn
  `8194290c-0a8c-40bc-a77b-b146a74d1779`, succeeded with one completed
  `ui/window/setFormData` row. Evidence:
  `/tmp/agently-live-ui/iphone-forecast-replay-20260805-current.png`.
- Android phone passed semantic replay. Conversation
  `6f8bdf0f-02f3-40c2-ac54-f2747995cbbf`, turn
  `68f84ed1-1131-456b-8ccf-aaa10b5712ea`, succeeded with completed
  `llm/skills/activate`, `steward/AdTargetingProfile`,
  `steward/ForecastingTargetingConvert`, `ui/context/get`, `ui/view/open`,
  `ui/window/list`, and `ui/window/setFormData` rows. Evidence:
  `/tmp/agently-live-ui/android-phone-forecast-20260805-current.png`.

Latest 2026-08-05 dirty-state curation rescan:
- Added local-only `.git/info/exclude` rules in Agently-core, Agently, Forge,
  and Steward for generated verification/runtime byproducts. No tracked
  `.gitignore` files changed, no source/evidence files were deleted, and real
  candidate files remain visible in `git status`.
- Visible untracked files are now mostly candidate source/docs/tests:
  Agently-core `mobile_sdk-progress/README.md` and MCP docs; Agently helper
  scripts plus iOS live UI tests; Forge report-builder demo/runtime JS tests;
  and Steward `skills/forecast-targeting.contract.test.mjs`.
- `git diff --check` passes in Agently-core, Agently, Forge, and Steward.
  Boundary scans remain clean for Forge native production sources and Agently
  mobile production sources.
- Focused Agently-core gate passes:
  `go test ./service/shared/toolexec ./internal/tool/registry
  ./runtime/discovery ./service/agent -count=1`.

Latest 2026-08-05 portable native/SDK verification:
- Agently Android app compile/unit test passed:
  `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
  --console=plain`.
- Agently iOS SwiftPM passed 92 tests:
  `swift test --package-path ios`.
- Forge Android SDK compile/unit test passed:
  `./gradlew :sdk:testDebugUnitTest :sdk:compileDebugKotlin --no-daemon
  --console=plain`.
- Forge iOS SwiftPM passed 221 tests:
  `swift test --package-path ios`.
- Agently-core Android SDK passed:
  `./gradlew testDebugUnitTest --no-daemon --console=plain`.
- Agently-core TypeScript SDK passed 22 files / 366 tests:
  `npm test`.
- Agently-core iOS SDK passed 68 tests:
  `swift test`.

Latest 2026-08-05 code rescan:
- Re-scanned latest worktrees and did not touch Reporter.
- Agently-core fixes from this pass:
  - `service/reactor` now treats inline `llm/skills:activate` as a real
    barrier before later sibling tool calls, so same-response tool execution
    sees active-skill constraints.
  - `service/scheduler` run-now tests finish the intentionally blocked first
    run before aging persisted rows, removing self-inflicted SQLite locks.
  - `service/scheduler` Datly initialization cache now uses the service
    pointer as the key instead of a raw uintptr, preventing false "already
    initialized" skips when test DAO addresses are reused.
  - `service/agent` tool-control tests now use an actually loaded visible
    skill when asserting skill-control tools.
- Verification passed:
  - `cd /Users/awitas/go/src/github.com/viant/agently-core && go test ./...`
  - `cd /Users/awitas/go/src/github.com/viant/forge && npm test`
  - `cd /Users/awitas/go/src/github.com/viant/agently/android &&
    ./gradlew :app:assembleDebug :app:testDebugUnitTest --console=plain`
  - `cd /Users/awitas/go/src/github.com/viant/agently/ios && swift test`
  - `cd /Users/awitas/go/src/github.com/viant/agently-core &&
    git diff --check`
- Local Steward-backed Agently server is still listening on `:9292`.

Latest 2026-08-05 boundary/native launch smoke:
- Local Steward-backed Agently remains listening on `:9292` as PID `26277`.
- Devices attached/booted:
  Android tablet `emulator-5554`, Android phone `emulator-5556`, iPhone 17
  simulator `59317EFB-ADFE-4A22-817F-4B4F6658AB2E`, and iPad Pro 11-inch
  simulator `B2AA0D68-7312-4CC9-85B8-0544341A942D`.
- Boundary scans passed:
  Forge native production sources have no Agently/Steward/forecasting/line
  `7288336`/ad-lookup special casing; Agently mobile production sources have
  no forecasting/line/ad-lookup/Steward targeting tool special casing;
  Agently-core SDK production sources have no Steward/forecasting/line special
  casing. Steward extension remains the owner of `reportBuilderRef`,
  `targetOverrides`, reporting builder definitions, targeting dialogs, and
  Steward-specific window/report wiring.
- Android smoke passed:
  `adb reverse tcp:9292 tcp:9292`, install current debug APK, launch
  `com.viant.agently.android/.MainActivity`. Tablet PID `16361`, phone PID
  `9775`, and recent logcat showed no fatal exception.
- iOS smoke passed:
  `xcodebuild build -scheme AgentlyApp` succeeded for both booted iPhone and
  iPad simulators. Installed and launched current `Agently.app`; `simctl
  launch` returned iPhone PID `70733` and iPad PID `70754`.

Latest 2026-08-05 four-target Forecasting replay:
- Re-ran the exact prompt `open forecast builder for line 7288336` against the
  isolated local Steward lane on `:9292`.
- iPad Pro live UI test passed with OOB bootstrap:
  `xcodebuild test -project ios/AgentlyApp.xcodeproj -scheme
  AgentlyAppLiveUITests -destination
  'platform=iOS Simulator,id=B2AA0D68-7312-4CC9-85B8-0544341A942D'
  -only-testing:AgentlyAppUITests/ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer`.
  Result: `** TEST SUCCEEDED **` in 53.336s.
- iPhone live UI test passed with OOB bootstrap using destination
  `59317EFB-ADFE-4A22-817F-4B4F6658AB2E`. Result:
  `** TEST SUCCEEDED **` in 77.012s.
- Android tablet replay passed:
  `ADB=$HOME/Library/Android/sdk/platform-tools/adb
  ./scripts/android-semantic-compose-replay.sh --device emulator-5554
  --prompt 'open forecast builder for line 7288336' --expect 'Forecasting'
  --wait 90`; output included `verified: Forecasting`.
- Android phone replay passed with the same command on `emulator-5556`; output
  included `verified: Forecasting`.
- This verifies mobile auth/session bootstrap plus composer send plus hosted
  Forecasting open on iPad, iPhone, Android tablet, and Android phone. The
  opened surface uses the web-compatible generic split: canonical
  `reportBuilder` plus `reportBuilderRef=forecastingCubeBuilder`.

Latest 2026-08-05 auth UX cleanup:
- Android and iOS required-auth screens now show only the normal workspace
  sign-in action plus settings access. Developer OOB remains available through
  settings/debug bootstrap paths, but is no longer shown on the primary
  sign-in card.
- Existing native unit harnesses do not include Android Compose UI rendering
  assertions or SwiftUI view inspection, so no brittle source-string UI test
  was added for this cleanup.
- Rechecked the boundary-sensitive direct-action cleanup in Agently-core: core
  no longer formats Steward diagnostic payloads into reports. The remaining
  Steward diagnostic mention is a test asserting raw tool-result behavior.
- Focused verification passed: Agently-core agent/tool/scheduler/registry
  gates, Forge report-builder JS/runtime tests, Forge Android SDK tests, Forge
  iOS 221-test suite, Agently iOS 92-test suite, Agently Android
  compile/unit tests, and Agently/Forge `git diff --check`.

Latest 2026-08-05 Steward forecast-scope curation:
- Builder-prefill line activation and plain forecast line activation now both
  preserve line ids as `AdLineId`; only audience activations emit
  `AudienceId` / `audienceId` / `audienceIds`.
- `deployment/steward/skills/forecast-targeting.contract.test.mjs` now asserts
  the line-vs-audience skill and intake scope contract.
- Steward checks passed:
  `node deployment/steward/skills/forecast-targeting.contract.test.mjs`,
  `node deployment/steward/extension/forge/windows/forecastingCubeBuilder.test.js`,
  `node deployment/steward/extension/forge/windows/forecastingCubeBuilder.predicates.test.mjs`,
  `node deployment/steward/extension/forge/windows/metricReportBuilder.windowParams.test.mjs`,
  and Steward `git diff --check`.
- Generic import/target support checks passed in Agently-core and Forge
  backend:
  `go test ./workspace/service/meta ./protocol/tool/service/ui/view
  ./service/ui/window -run 'Import|Target|ReportBuilder|Window|Prepare'
  -count=1`, and `go test ./backend/service/meta -run 'Target|Import'
  -count=1`.
- Boundary scans remain clean for Forge native production, Agently mobile
  production, and Agently-core SDK production.

Latest Agently-core intake harness rescan (2026-08-05):
- `service/agent/intake_query_test.go` now mirrors the Steward split:
  line forecast activations emit only `AdLineId`, audience forecast
  activations emit `audienceId` and `audienceIds`, and each test asserts the
  opposite key family is absent.
- Focused verification passed:
  `go test ./service/agent -run
  'Forecast.*RoutesToForecastTemplate|OpenForecastBuilderForLine' -count=1`,
  `go test ./service/agent -run
  'DirectAction|Tool|Intake|ConversationMetadata|AutoSelect|Planner|Forecast'
  -count=1`, and Agently-core `git diff --check`.
- Independent Codex review found one missing Steward contract guard: the test
  covered plain forecast line/audience rules but not the builder-prefill rules
  that previously leaked line ids into audience scope. The contract test now
  also asserts `open_forecasting_builder_for_line` and
  `open_forecasting_builder_for_line_no_run` emit only `AdLineId`; Steward
  window checks, the focused Agently-core forecast gate, and `git diff --check`
  in Steward and Agently-core pass after the fix.

Latest Agently-core SDK restore/streaming rescan (2026-08-05):
- Android, iOS, and TypeScript SDK restore logic now has current proof for
  modern `ui/window/open` hosted-workspace restore plus later
  `ui/window:setFormData` hydration.
- Added iOS parity coverage for the Android cursor/sequence regression:
  hydrated transcript event sequences are skipped exactly, even after the
  hydration cursor, while new lower live event sequences are still applied.
  This avoids the old max-sequence behavior that could drop valid live SSE.
- Verification passed: Agently-core iOS SDK `swift test` passed 68 tests;
  Android SDK `ConversationStreamTrackerTest` and `WorkspaceRestoreTest`
  passed through Gradle; TypeScript `npm test -- workspaceRestore` passed
  10 tests; Agently-core `git diff --check` passed.

Latest Forge report-builder variant rescan (2026-08-05):
- Android now has parity coverage with iOS for variant-only report-builder
  metadata: `reportBuilders` plus default `reportBuilderRef` resolves without
  requiring a legacy `dashboard.reportBuilder` fallback copy.
- Focused Forge verification passed:
  Android `ReportBuilderStateStorageTest`, iOS
  `testReportBuilderVariantResolution*` plus
  `testWindowMetadataDecodesReportBuilderVariants`, and Forge
  `git diff --check`.

Latest Forge/Agently report-store boundary rescan (2026-08-05):
- Re-scanned latest Agently, Forge, and Agently-core worktrees; Reporter was
  not touched.
- Forge report-catalog refresh now uses the generic
  `forge:report-store-changed` event, and Agently's web host report-store
  service emits that same generic event after save/update/duplicate/delete/run
  actions.
- Forge preset report cards now use workspace-neutral owner copy instead of
  naming Steward, and the generic report document model comment no longer
  names Datly/Steward.
- Boundary scans passed for the old Agently event name, Datly/Steward comment,
  and preset Steward owner copy. Forge native/report-dashboard production paths
  still have no Steward forecasting, line-id, ad-lookup, Viant-host, or
  Agently special casing.
- Focused verification passed:
  `node --no-warnings src/components/dashboard/reportBuilderHostServices.test.js`,
  `node --no-warnings src/reporting/reportDocumentModel.test.js`,
  `node --no-warnings src/components/dashboard/reportBuilderRuntimePreview.test.js`,
  `node --no-warnings src/components/dashboard/reportCatalogPagination.test.js`,
  `APPSERVER_URL=http://127.0.0.1:9292 npm test -- --run
  src/services/reportStoreService.test.js src/services/forgeHostServices.test.js`,
  and `git diff --check` in Agently and Forge.
- Remaining gap: this was a focused boundary/code rescan, not another fresh
  four-target native replay. The latest iPhone/iPad/Android phone/Android
  tablet Forecasting replay evidence above remains the current mobile proof
  for the `:9292` lane.

Latest four-target native Forecasting replay rescan 2 (2026-08-05):
- Closed the previous fresh-native-replay gap by rerunning
  `open forecast builder for line 7288336` on all available mobile targets
  against local Steward on `:9292`.
- Preflight passed: `agently` PID `26277` was listening on `*:9292`, auth
  providers returned the Viant OAuth/BFF provider, Android tablet
  `emulator-5554` and Android phone `emulator-5556` were attached, and iPhone
  17 plus iPad Pro 11-inch simulators were booted.
- Android tablet `emulator-5554`: `adb reverse tcp:9292 tcp:9292`, OOB debug
  install/launch against `http://localhost:9292`, then
  `android-semantic-compose-replay.sh --prompt "open forecast builder for line
  7288336" --expect "Forecasting" --wait 90` passed with
  `verified: Forecasting`. Evidence:
  `/tmp/agently-rescan/android-tablet-forecasting-9292-20260805-rescan2.png`.
- Android phone `emulator-5556`: same install/replay path passed with
  `verified: Forecasting`. Evidence:
  `/tmp/agently-rescan/android-phone-forecasting-9292-20260805-rescan2.png`.
- iPad Pro 11-inch
  `B2AA0D68-7312-4CC9-85B8-0544341A942D`: `AgentlyAppLiveUITests`
  `ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer`
  passed with `** TEST SUCCEEDED **`; one UI test, zero failures. Evidence:
  `/tmp/agently-rescan/ios-ipad-forecasting-9292-20260805-rescan2.png`.
- iPhone 17 `59317EFB-ADFE-4A22-817F-4B4F6658AB2E`: same live UI test
  passed with `** TEST SUCCEEDED **`; one UI test, zero failures. Evidence:
  `/tmp/agently-rescan/ios-iphone-forecasting-9292-20260805-rescan2.png`.
- Current gap after this pass: none for the four-target native Forecasting
  replay. The broader goal remains active for continuing report-builder,
  dashboard, auth/session durability, and boundary parity audits.

Latest Forecasting prefill payload and restore-boundary rescan (2026-08-05):
- Followed up on the original mobile acceptance detail that the Forecasting
  builder must be populated from line targeting, not merely opened.
- SQLite `tool_call` / `call_payload` records for the fresh four-target replay
  prove the builder-prefill path ran through `steward/AdTargetingProfile`,
  `forecast-targeting`, `steward/ForecastingTargetingConvert`,
  `ui/view/open`, and completed `ui/window/setFormData` calls.
- Decoded completed setFormData payloads contain the normalized top-level
  Forecasting fields required for filter population:
  `includeCountry=["US"]`, `includePostalCodeList=[70731]`,
  `includeDealsPmp=[64512,66016,76060,76084,76105,76162,76711,89531,90473,90476,90482,98075,143925,143934,144156,146708,148790,149114]`,
  plus shared include maps for `location`, `location.postalcode.list`, and
  `ad.pmp.deal.id`.
- Android/iOS accessibility text does not consistently expose the populated
  filter chips, so persisted tool payloads are the authoritative proof for
  the filter population requirement.
- Cleaned generic hosted-workspace restore fixtures in Agently Android app and
  Agently-core Android/iOS/TypeScript SDK tests. Those fixtures no longer use
  Steward-specific `forecastingCubeBuilder`, `Forecasting`, `7288336`, or
  `audience:7288336`; they now use neutral `capacityBuilder`,
  `Capacity Builder`, and `record:12345`.
- Focused verification passed:
  Agently-core Android SDK `WorkspaceRestoreTest`, Agently-core iOS SDK
  `AgentlySDKTests` (68 tests), Agently-core TS `workspaceRestore` (10 tests),
  and Agently Android app `HostedWorkspaceRestoreTest`.

Latest-code rescan (2026-08-05):
- Re-scanned current Agently, Agently-core, and Forge worktrees. Reporter was
  not touched.
- Local Steward lane remains active on `:9292` with `agently` PID `26277`.
- Current device bench is available: Android tablet `emulator-5554`, Android
  phone `emulator-5556`, iPhone 17 simulator
  `59317EFB-ADFE-4A22-817F-4B4F6658AB2E`, and iPad Pro 11-inch simulator
  `B2AA0D68-7312-4CC9-85B8-0544341A942D`.
- Generic hosted-workspace restore fixture scan remains clean for
  `forecastingCubeBuilder`, `Forecasting`, `7288336`, and
  `audience:7288336`.
- Focused latest-code verification passed again:
  Agently-core Android SDK `WorkspaceRestoreTest`, Agently-core iOS SDK
  `AgentlySDKTests` (68 tests), Agently-core TS `workspaceRestore` (10 tests),
  and Agently Android app `HostedWorkspaceRestoreTest`.
- `git diff --check` passed in Agently, Agently-core, and Forge.
- Remaining caveat: accessibility still does not expose every populated
  Forecasting filter chip on all native targets, so completed
  `ui/window/setFormData` payloads remain the authoritative filter-prefill
  proof.

Latest Forecasting line targetKey contract (2026-08-05):
- Fixed the Steward-owned prompt/skill contract so line builder-prefill keeps
  `prefill.scope.targetKey = "line:<requested line id>"` even when the
  profile also exposes internal/resolved audience ids for Forecasting
  execution. Audience builder-prefill keeps
  `audience:<requested audience id>`.
- Patched `skills/forecast-targeting/SKILL.md`,
  `agents/steward/prompt/parts/routing.md`,
  `agents/steward/prompt/instruction.tmpl`, and
  `agents/steward/prompt/system.tmpl`.
- Extended `skills/forecast-targeting.contract.test.mjs` to guard the skill
  and all three Steward prompt sources against targetKey drift.
- Verification passed:
  `node skills/forecast-targeting.contract.test.mjs`,
  `node extension/forge/windows/forecastingCubeBuilder.test.js`, and Steward
  `git diff --check`.
- Restarted isolated local Steward lane on `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. Active Agently PID after this
  pass was `1923`.
- Fresh Android tablet replay on `emulator-5554` passed for
  `open forecast builder for line 7288336`.
- Fresh conversation `6e30c6b1-53b7-4565-8235-fad78b2f24b5` completed
  `AdTargetingProfile`, `ForecastingTargetingConvert`, `ui/view/open`, and
  `ui/window/setFormData`.
- Decoded setFormData request payload
  `cfdadd82-1012-4b84-ae67-a44de9a140a1` contains
  `prefill.scope.targetKey = "line:7288336"`,
  `audienceIds = [7288336]`, `adOrderIds = [2664518]`, country `US`, postal
  list `70731`, all 18 PMP deal ids, and shared include filters for
  `location`, `location.postalcode.list`, and `ad.pmp.deal.id`.
  Screenshot:
  `/tmp/agently-rescan/android-tablet-forecasting-line-targetkey-20260805.png`.

Latest Forge target customization rescan (2026-08-05):
- Re-scanned target customization across Forge, Agently-core, and Steward.
  Current implementation remains correctly generic:
  - Forge native resolvers own `targetOverrides` selection.
  - Agently-core owns workspace `$import` and target branch selection.
  - Steward owns window metadata that requests mobile/tablet/phone behavior.
  - Reporter was not touched.
- Steward report-builder windows keep web/base as the no-target default and use
  `targetOverrides` for `mobile`, `tablet`, and `phone`. Order already uses the
  `$import` mobile subfolder pattern via `order/mobile/main.yaml`.
- Android and iOS Forge resolvers both cover broad `mobile`, form factor,
  platform, exact `platform:formFactor`, compact aliases such as
  `androidPhone` / `iosTablet`, foldable-as-mobile, nested report-builder
  merges, and web desktop staying on base content.
- Agently-core tests cover imported `targetOverrides` surviving `$import`,
  exact `ios/phone` branch winning before mobile fallback, Android phone using
  mobile fallback, and no-target requests using shared default content.
- Verification passed:
  Forge Android `*TargetingTest`, Forge iOS
  `MetadataResolver|Targeting`, Agently-core
  `workspace/service/meta -run ResolveImports`, and Agently-core
  `adapter/http/ui -run
  TestWindowHandler_WorkspaceForgeWindow(AppliesTargetBranchToSharedImports|NoTargetUsesSharedDefault)`.
- Local Steward lane remained on `:9292` as Agently PID `1923`.

Latest mobile auth, SDK, and Forge compatibility rescan 2 (2026-08-05):
- Local Steward-backed Agently is still listening on `:9292` as PID `1923`.
- Boundary scans are clean in the current worktree:
  Forge Android/iOS production has no Steward/Forecasting/line-id/ad-lookup
  special casing; Agently-core SDK production has no
  Steward/Forecasting/line-id special casing; Agently Android/iOS production
  has no Forecasting builder, line-id, or Steward targeting tool special
  casing; mobile SDK/app paths have no stale `9191` references.
- Focused verification passed:
  Agently Android app auth/settings/session/hosted-restore unit gate,
  Agently iOS app `AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests`
  (19 tests), Agently-core Android SDK restore/client/stream unit gate,
  Agently-core iOS SDK `AgentlySDKTests` (68 tests), Forge Android
  `*ReportBuilder*|*Dashboard*` unit gate, and Forge iOS `ForgeIOSTests`
  (221 tests).
- `git diff --check` passed in Agently, Agently-core, Forge, and Steward.
  No runtime patch was required during this pass, and Reporter was not touched.

Latest four-target native Forecasting replay rescan 3 (2026-08-05):
- Re-ran `open forecast builder for line 7288336` against the isolated local
  Steward lane on `:9292`; Agently was listening as PID `1923`.
- Android tablet `emulator-5554` (`Pixel_Tablet`) and Android phone
  `emulator-5556` (`sdk_gphone16k_arm64`) were attached with
  `adb reverse tcp:9292 tcp:9292`. The debug APK rebuilt/installed with
  `AGENTLY_ANDROID_BASE_URL="http://127.0.0.1:9292"
  ./scripts/install-android-oob-debug.sh`; the helper only failed at its final
  generic launch because more than one emulator was attached, so both devices
  were launched explicitly with `adb -s <serial> shell am start`.
- Android semantic composer replay passed on both emulators with
  `verified: Forecasting`.
- iPad Pro 11-inch simulator `B2AA0D68-7312-4CC9-85B8-0544341A942D` passed
  `AgentlyAppLiveUITests/ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer`
  with `** TEST SUCCEEDED **` in 161.055s. Result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-ipad-rescan3/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_14-25-10-+0200.xcresult`.
- iPhone 17 simulator `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` passed the same
  live UI test with `** TEST SUCCEEDED **` in 141.140s. Result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-iphone-rescan3/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_14-25-11-+0200.xcresult`.
- Persisted Steward DB evidence proves all four native turns populated the
  Forecasting builder, not only opened the pane. Conversations/payloads:
  Android phone `cb332f8c-c8d9-44f2-b641-46b4302e92bb` /
  `38aedc99-dd0c-494d-866a-c07760a648f9`; Android tablet
  `13313691-6ef9-4b86-a88f-4338c365c8ea` /
  `fe810538-8987-4c33-a4de-128db88ac148`; iOS phone
  `bd253aa8-632b-4f84-8d7f-0ff7f366c0cf` /
  `1dff527a-da04-4955-a1bf-8a5bdd24000e`; iOS tablet
  `20f943a5-7ebf-4665-a3c0-92d8a736ed6c` /
  `708424f6-6fcf-4848-9cf1-e32519c710c0`.
- All four completed `steward/AdTargetingProfile`,
  `steward/ForecastingTargetingConvert`, `ui/view/open`, and
  `ui/window/setFormData`. Decoded `setFormData` requests contain
  `prefill.scope.targetKey="line:7288336"`, `audienceIds=[7288336]`,
  `adOrderIds=[2664518]`, `includeCountry=["US"]`,
  `includePostalCodeList=[70731]`, all 18 PMP deal ids, and iOS gzip payloads
  include `forecastHandoff.sharedIncludeFilters` for `location`,
  `location.postalcode.list`, and `ad.pmp.deal.id`.
- Accessibility still does not consistently expose every populated native
  filter chip, so completed `ui/window/setFormData` payloads remain the
  authoritative proof for filter prefill.

Latest code boundary and contract rescan (2026-08-05):
- Re-scanned current Agently, Agently-core, Forge, and Steward worktrees after
  the rescan 3 handoff update. Reporter was not touched, and local
  Steward-backed Agently remains live on `:9292` as PID `1923`.
- Boundary scans passed for Forge Android/iOS production, Agently Android/iOS
  production, and Agently-core SDK production: no Steward/Forecasting/line-id,
  ad-lookup, `7288336`, or stale mobile `9191` leakage in the generic layers.
- Focused verification passed:
  Agently-core import/window/target Go gate; Forge Android
  `*TargetingTest|*ReportBuilder*|*Dashboard*`; Forge iOS
  `MetadataResolver|Targeting|ForgeIOSTests` with 221 tests; Agently-core
  Android SDK restore/client/stream gate; Agently-core iOS SDK
  `AgentlySDKTests` with 68 tests; Agently Android app
  auth/settings/session/hosted-restore gate; Agently iOS app
  `AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests`
  with 19 tests; and Steward `node skills/forecast-targeting.contract.test.mjs`.
- `git diff --check` passed in Agently, Agently-core, Forge, and Steward
  before this documentation update.

Latest nested report-builder variant target override fix (2026-08-05):
- Independent review probing found and Forge now fixes a real generic iOS
  metadata gap: nested `reportBuilders.<ref>.targetOverrides` were preserved
  on decode but not applied through the typed `MetadataResolver.resolve`
  round trip because the newly preserved variant field needed explicit default
  decoding after the resolver stripped applied target metadata.
- Forge iOS `DashboardReportBuilderVariantDef` now retains
  `targetOverrides` with default decoding; Forge Android's variant model also
  carries the same field for parity.
- Added web, Android, and iOS resolver regressions proving nested
  report-builder variant target overrides apply for mobile/phone and are
  stripped after resolution.
- Verification passed:
  Forge web `metadataResolver.test.js` and `reportBuilderVariantModel.test.js`;
  Forge Android `*TargetingTest` plus `*ReportBuilderStateStorageTest`; Forge
  iOS `MetadataResolver` slice and full `ForgeIOSTests` slice, now 222 tests.

Latest downstream native app verification after variant fix (2026-08-05):
- Agently Android passed `./gradlew :app:compileDebugKotlin
  :app:testDebugUnitTest --no-daemon --console=plain` against the updated
  local Forge SDK. The build emitted only existing Kotlin warnings and finished
  `BUILD SUCCESSFUL`.
- Agently iOS passed `swift test --package-path ios --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests'`
  with 27 tests and 0 failures.
- Boundary scans remain clean after the Forge fix: no Steward/Forecasting,
  line-id, ad-lookup, or stale mobile `9191` leakage in Forge native
  production, Agently mobile production, or Agently-core SDK production.
- Local Steward-backed Agently remained live on `:9292` as PID `1923`.

Latest broader Forge and Steward rescan after variant fix (2026-08-05):
- Forge JS `npm test` completed successfully after covering the large reporting
  fixture/preview/runtime/component suite.
- Forge Android full SDK unit suite passed:
  `./gradlew :sdk:testDebugUnitTest --no-daemon --console=plain`.
- Steward workspace smoke gates passed:
  `forecastingCubeBuilder.test.js`,
  `forecastingCubeBuilder.predicates.test.mjs`,
  `metricReportBuilder.test.js`,
  `metricReportBuilder.predicates.test.mjs`,
  `metricReportBuilder.windowParams.test.mjs`,
  `metricReportBuilder.sharedEndpointDatasets.test.mjs`,
  `skills/forecast-targeting.contract.test.mjs`, and
  `reportPresetPrimitiveCoverage.test.mjs`. Only existing Node module-type
  warnings appeared.
- Agently UI host-service tests passed against `http://127.0.0.1:9292`:
  `reportStoreService.test.js` and `forgeHostServices.test.js`, 9 tests total.
- Local Steward-backed Agently still listens on `:9292` as PID `1923`;
  Reporter remains untouched.
- Final hygiene checks passed: `git diff --check` in Agently, Agently-core,
  Forge, and Steward; focused production leakage scans in Forge, Agently
  mobile, and Agently-core SDK; and stale mobile `9191` scans in Agently
  mobile plus Agently-core SDK paths.

Latest fresh four-target native Forecasting replay (2026-08-05):
- Re-ran `open forecast builder for line 7288336` against local Steward-backed
  Agently on `:9292` after VPN returned.
- Android tablet `emulator-5554` rebuilt/installed and ultimately succeeded
  after one transient `steward/AdTargetingProfile` failure/retry while VPN was
  recovering. Conversation `5b3902ac-a2c1-4743-a276-5cb10ecda048`, setFormData
  payload `3265971e-15f9-4557-a7a9-1b9bac2824c0`.
- Android phone `emulator-5556` semantic replay passed with
  `verified: Forecasting`. Conversation
  `ac5268b1-4d7f-46b5-817e-d1667d8211d5`, setFormData payload
  `d011227a-5c12-4d30-9c17-2be1a735d2c0`.
- iPad Pro 11-inch (M5) simulator
  `B2AA0D68-7312-4CC9-85B8-0544341A942D` had one pre-tool rejected starter
  turn on the first attempt, then passed the warm rerun. Result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-ipad-fresh/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_16-39-18-+0200.xcresult`.
  Conversation `0743ea0a-be1f-457d-b878-3525969f4007`, setFormData payload
  `2ce33af6-a7e0-464a-9082-53951f9a2f27`.
- iPhone 17 simulator `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` passed fresh live
  UI verification. Result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-iphone-fresh/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_16-42-33-+0200.xcresult`.
  Conversation `4490ab54-f834-40ae-9c04-4c1fd765c16d`, setFormData payload
  `ed47605c-8d47-4d10-91e9-aad13e4409a8`.
- All four successful turns completed
  `steward/AdTargetingProfile`, `llm/skills/activate`,
  `steward/ForecastingTargetingConvert`, `ui/window/list`, `ui/view/open`,
  and `ui/window/setFormData`. All four payloads contain
  `targetKey="line:7288336"`, `audienceIds=[7288336]`,
  `adOrderIds=[2664518]`, `includeCountry=["US"]`,
  `includePostalCodeList=[70731]`, and all 18 PMP deal IDs. Three of four also
  carried the optional shared include filter mirror; the fresh iPhone payload
  carried the canonical typed include fields only.
- Final post-replay hygiene passed: `git diff --check` in Agently,
  Agently-core, Forge, and Steward; focused production leakage scans in Forge,
  Agently mobile, and Agently-core SDK; and stale mobile `9191` scans in
  Agently mobile plus Agently-core SDK paths.

Latest turn error durability fix from iPad replay (2026-08-05):
- Root-caused the failed first iPad live UI attempt: turn
  `b9a0596f-af21-47aa-b855-aae37d3dd0d1` in conversation
  `8bcf749f-5533-40b4-9c70-d198b78be671` made three OpenAI model attempts and
  all failed with `dial tcp: lookup api.openai.com: i/o timeout`; no Steward or
  Forge tool executed. The terminal turn had collapsed that into
  `no final content produced`.
- Fixed Agently-core's generic empty-output failure path to recover the latest
  failed durable model-call error and use it as the terminal turn error. This
  improves mobile/web diagnostics without Steward, Forge, Forecasting, or
  mobile-specific logic.
- Verification passed: `go test ./service/agent -run
  'TestServiceRunPlanAndStatus' -count=1` and
  `go test ./service/agent -count=1`.

Latest Steward merge and local `:9292` refresh (2026-08-05):
- Fetched Steward and fast-forwarded local `ENG-54517` to `origin/ENG-54517`
  after the push rejection; the branch is no longer behind remote.
- Stashed and reapplied the local Steward mobile/forecasting/report-builder
  edits. The only overlap,
  `agents/steward/prompt/instruction.tmpl`, auto-merged cleanly.
- Rebuilt `/Users/awitas/go/src/github.com/viant/agently/agently/agently`
  with a temporary Go workspace pointing `github.com/viant/agently-core` and
  `github.com/viant/forge` at local checkouts, avoiding a committed local-only
  `replace` in Agently `go.mod`.
- Restarted local Steward-backed Agently on `:9292`; it is listening as PID
  `49751`.
- Verification passed: focused Agently-core
  `go test ./service/agent -run 'TestServiceRunPlanAndStatus' -count=1`,
  `curl -I http://127.0.0.1:9292/` returned HTTP 200, and
  `git diff --check` passed in Steward and Agently-core.

Latest post-merge Steward smoke verification (2026-08-05):
- Re-ran the focused Steward smoke/contract suite with
  `FORGE_ROOT=/Users/awitas/go/src/github.com/viant/forge` after the
  `ENG-54517` fast-forward and local edit reapply.
- Passed 10/10: Forecasting builder, Forecasting predicates, Metric Report
  Builder, Metric predicates, Metric window params, shared endpoint datasets,
  report preset primitive coverage, forecast-targeting contract, reporting
  delivery contract, and audience forecast dashboard export contract.
- Local Steward-backed Agently stayed healthy after verification: PID `49751`
  is listening on `:9292`, and `curl -I http://127.0.0.1:9292/` returned HTTP
  200.

Latest post-merge native mobile SDK verification (2026-08-05):
- Agently Android app compile/unit gate passed:
  `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon --console=plain`
  finished `BUILD SUCCESSFUL` in 1m39s.
- Agently iOS focused package gate passed:
  `swift test --package-path ios --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests'`
  executed 27 tests with 0 failures.
- The iOS slice covered OAuth mobile redirect behavior, developer session
  cookie login, workspace endpoint persistence including local `:9292`,
  composer lookup parsing/selection/focus behavior, hosted workspace labels,
  and Forge datasource input preservation.
- Production boundary scans remained clean: no stale mobile `9191` in Agently
  Android/iOS production source; no Steward/Forecasting/line-id/ad-lookup
  leakage in Agently mobile production source, Forge production source outside
  demos/tests, or Agently-core production source outside tests/docs/progress.
- `git diff --check` passed in Agently, Agently-core, Forge, and Steward.
- Local Steward-backed Agently stayed healthy after the mobile checks: PID
  `49751` is listening on `:9292`, and `curl -I http://127.0.0.1:9292/`
  returned HTTP 200.

Latest post-merge web/Forge target override verification (2026-08-05):
- Agently web host bridge tests passed against `http://127.0.0.1:9292`:
  `reportStoreService.test.js` and `forgeHostServices.test.js`, 9 tests total.
- Forge JS target override regressions passed when run directly:
  `src/runtime/metadataResolver.test.js` and
  `src/components/dashboard/reportBuilderVariantModel.test.js`.
- Forge Android target/report-builder slice passed:
  `./gradlew :sdk:testDebugUnitTest --tests '*TargetingTest' --tests
  '*ReportBuilderStateStorageTest' --no-daemon --console=plain`, `BUILD
  SUCCESSFUL` in 2m44s with only existing Kotlin warnings.
- Forge iOS metadata/report-builder slice passed:
  `swift test --package-path ios --filter 'MetadataResolver|ReportBuilder'`,
  26 tests with 0 failures, including nested report-builder variant target
  overrides and filter auto-collapse.
- A package-level Forge `npm test -- --run ...` attempt was interrupted after
  many reporting/preview checks had passed because the package script expands
  to the full suite and its hosted Steward render smoke tail ran too long for
  this focused pass. No stray Node/Vite child test processes remained.
- Boundary and lane checks stayed clean: no stale `9191` in Agently
  Android/iOS production source; no Steward/Forecasting/line-id/ad-lookup
  leakage in Agently mobile production or Forge production outside demos/tests;
  `git diff --check` passed in Agently, Agently-core, Forge, and Steward; and
  local Steward-backed Agently stayed healthy on `:9292` as PID `49751`.

Latest Android OOB auth replay stabilization (2026-08-05):
- Root cause found on the phone emulator: OOB login to
  `http://10.0.2.2:9292` succeeded, saved an `agently_session`, and loaded
  Steward metadata/conversations, but a stale concurrent auth refresh against
  the alternate emulator alias `10.0.3.2:9292` later timed out and demoted the
  active session to `Required`.
- Agently local commit `be857252` (`fix(android): stabilize workspace auth
  bootstrap`) now resets the OOB bootstrap latch on endpoint changes, avoids
  workspace bootstrap repeats when metadata is loaded but recents are empty,
  ignores stale auth-refresh failures after the session ID changes, and
  preserves established sessions on transient connectivity failures while still
  treating 401/403 as sign-in required.
- Verification passed:
  - Host-side OOB cookie authorized `/v1/api/auth/me`,
    `/v1/workspace/metadata?platform=android&formFactor=phone&surface=native`,
    and `/v1/conversations?limit=25` against `http://127.0.0.1:9292`.
  - Android compile/unit gate:
    `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon --console=plain`
    finished `BUILD SUCCESSFUL` in 2m49s.
  - Phone emulator `emulator-5556` first-run replay with local `:9292` and
    auto-OOB stayed on the Steward conversation screen past the old timeout
    window. UI dump showed `Ready for a new conversation`, starter tasks,
    recent conversations, and composer controls; logcat showed `Ignoring stale
    auth refresh failure after session changed` instead of a sign-in demotion.
- Push remains blocked by local GitHub credentials, not branch state: Agently is
  one commit ahead, but HTTPS push returns 403 for `awitas_viant` and SSH lacks
  an accepted public key.

Latest Android report-builder predicate lowering (2026-08-05):
- Android Forecasting prefill was functionally correct but visually incomplete:
  current metadata exposes canonical `predicates`, `predicateBuckets`, and
  `predicateGroups`, while native Android Forge only rendered legacy
  `dynamicFilterGroups` and `dynamicFilterFamilies`.
- Forge Android now decodes and lowers canonical report-builder predicates into
  static filters, dynamic filter groups, and dynamic filter families at the
  generic report-builder config boundary. The implementation contains no
  Steward, Forecasting, line-id, prompt-text, workspace, or window-id routing.
- Verified Android Forge regression slice:
  `./gradlew :sdk:testDebugUnitTest --tests '*ReportBuilderPredicatesTest'
  --tests '*ReportBuilderStateStorageTest' --tests '*TargetingTest'
  --no-daemon --console=plain`, plus a focused
  `*ReportBuilderPredicatesTest` rerun.
- The patched Agently Android APK was built against `http://10.0.2.2:9292`,
  installed with direct `adb install -r` after Gradle install hung, and
  relaunched authenticated on the Steward home screen.
- Command-path proof remains good: completed conversation
  `2dcbde9f-eafb-4bb0-8664-236ac9b30a73` opened Forecasting and completed
  `ui.window.setFormData` with `prefill.scope.targetKey="line:7288336"`.
  Latest fresh replay `3e0be0d6-00af-4125-8ab4-6845db7fc999` also reached
  `ui.window.setFormData ok=true`, but the turn stayed running behind Steward
  MCP discovery timeouts and was canceled.
- A shared Android SDK restore bug was fixed on the way to visual proof:
  completed conversations can keep buffered live execution groups after a
  stream ends, and that must not suppress durable hosted-workspace restore.
  The helper now suppresses historical restore only while an active turn id is
  present, preserving the active-SSE-vs-history separation.
- Verification passed:
  `./gradlew clean testDebugUnitTest --tests '*WorkspaceRestoreTest'
  --no-daemon --console=plain` in Agently-core Android SDK, and
  `./gradlew :app:testDebugUnitTest --tests '*HostedWorkspaceRestoreTest'
  --no-daemon --console=plain` in Agently Android.
- The patched APK was rebuilt, installed, and used to reopen completed
  conversation `2dcbde9f-eafb-4bb0-8664-236ac9b30a73`. Android phone now
  restores the hosted Forecasting workspace from history and renders canonical
  predicate-derived filter controls. Evidence:
  `/tmp/agently-mobile-verify/android-phone-completed-forecasting-restored-after-sdk-fix.png`
  and
  `/tmp/agently-mobile-verify/android-phone-completed-forecasting-restored-filters-scrolled.png`.
- Filter opening also works on Android phone: Inventory Add line adds a
  Publisher row, and tapping Publisher opens the selector with Publisher,
  Site Type, Site List, Deal / PMP, and External Deal. Evidence:
  `/tmp/agently-mobile-verify/android-phone-forecasting-inventory-add-line.png`
  and
  `/tmp/agently-mobile-verify/android-phone-forecasting-publisher-filter-open.png`.
- Forge iOS now has the same generic predicate-lowering parity: Swift decodes
  `predicates`, `predicateBuckets`, and `predicateGroups` and lowers them at
  the report-builder config boundary. Focused Forge iOS report-builder tests
  passed 22 tests, including
  `testReportBuilderPredicateLoweringDerivesFiltersGroupsAndFamilies`.
- Android tablet visual proof is now captured too. The `Pixel_Tablet` AVD
  could not reach local Steward through `10.0.2.2` or `10.0.3.2`, so it uses
  `adb reverse tcp:9292 tcp:9292` plus `http://localhost:9292`. After clearing
  app data, selecting `Localhost 9292`, and completing auto-OOB, the tablet
  reopened completed conversation `2dcbde9f-eafb-4bb0-8664-236ac9b30a73` and
  restored the hosted Forecasting workspace with canonical predicate-derived
  filter controls. Evidence:
  `/tmp/agently-mobile-verify/android-tablet-after-clear-auto-oob-poll.png`,
  `/tmp/agently-mobile-verify/android-tablet-localhost-selected-oob-home.png`,
  `/tmp/agently-mobile-verify/android-tablet-completed-forecasting-restored.png`,
  and
  `/tmp/agently-mobile-verify/android-tablet-completed-forecasting-restored-loaded.png`.
- iPhone/iPad predicate-filter proof is complete as well. The live
  `ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer`
  now waits up to 300 seconds for Forecasting and verifies real
  predicate-derived controls from the native accessibility tree: Date Range,
  Channels, Inventory, Location, and Add line. iPhone 17 simulator
  `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` passed against `127.0.0.1:9292`
  with result bundle
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-iphone/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_20-29-22-+0200.xcresult`.
  iPad Pro 11-inch simulator `B2AA0D68-7312-4CC9-85B8-0544341A942D` passed
  the same test with result bundle
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-ipad/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_20-34-56-+0200.xcresult`.
- Current predicate-filter status: Android phone, Android tablet, iPhone, and
  iPad all prove native Forecasting predicate filter rendering. Android phone
  additionally proves filter opening by adding Inventory Publisher and opening
  the selector. Fresh full line-targeting-expression prefill still depends on
  a healthy Steward MCP lane, but the completed proof confirms target-key
  prefill plus native predicate-filter rendering across all four mobile
  targets.

Latest iOS predicate verification hardening (2026-08-05):
- Independent review found the new iOS live UI assertion could false-pass
  because it scanned all accessibility labels and accumulated label matches
  across scroll states.
- Forge iOS now exposes generic report-builder accessibility identifiers for
  filter summary, static filters, dynamic filters, dynamic families, groups,
  and Add line buttons. The Agently iOS live test now checks exact Forge
  identifiers instead of user-facing labels:
  `forge-report-builder-filter-summary`,
  `forge-report-builder-static-filter-dateRange`,
  `forge-report-builder-dynamic-filters`,
  `forge-report-builder-dynamic-family-inventory`, and
  `forge-report-builder-add-line-inventory`.
- A first live iPhone rerun showed the parent-level `forge-report-builder`
  identifier flattened child identifiers in SwiftUI accessibility output. A
  second live rerun showed section-level identifiers could smear onto
  descendants as well. The identifiers are now attached to concrete leaf
  text/buttons instead of layout containers. The interrupted reruns are not
  counted as passing proof for the hardened assertion.
- Verified after leaf-identifier cleanup:
  `swift test --package-path ios --filter 'ReportBuilder'` in Forge passed 22
  tests, and `git diff --check` passed in Forge, Agently, and Agently-core.
- The focused exact-identifier live UI tests now pass on iPhone and iPad
  against local Steward on `127.0.0.1:9292` with OOB bootstrap. iPhone 17
  simulator `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-iphone-identifiers-leaf/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_21-59-17-+0200.xcresult`.
  iPad Pro 11-inch (M5) simulator `B2AA0D68-7312-4CC9-85B8-0544341A942D`
  result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-ipad-identifiers-leaf/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_22-05-21-+0200.xcresult`.
  Both bundles report one test, no failures, no errors, and action status
  `succeeded`.
- Current push blocker: `git push origin main` from
  `/Users/awitas/go/src/github.com/viant/agently` fails with GitHub 403,
  `Permission to viant/agently.git denied to awitas_viant`. Agently is still
  only one local commit ahead of `origin/main`, so the push rejection is local
  credentials/access, not branch divergence. SSH is also unavailable from this
  machine: `git@github.com: Permission denied (publickey)`.

Latest Android/shared/server refresh (2026-08-05):
- Forge Android predicate/report-builder slice passed:
  `./gradlew :sdk:testDebugUnitTest --tests '*ReportBuilderPredicatesTest'
  --tests '*ReportBuilderStateStorageTest' --tests '*TargetingTest'
  --no-daemon --console=plain` from
  `/Users/awitas/go/src/github.com/viant/forge/android`; `BUILD SUCCESSFUL` in
  2m11s.
- Agently-core Android SDK restore slice passed:
  `./gradlew clean testDebugUnitTest --tests '*WorkspaceRestoreTest'
  --no-daemon --console=plain` from
  `/Users/awitas/go/src/github.com/viant/agently-core/sdk/android`;
  `BUILD SUCCESSFUL` in 1m41s.
- Agently Android app hosted-restore integration passed:
  `./gradlew :app:testDebugUnitTest --tests '*HostedWorkspaceRestoreTest'
  --no-daemon --console=plain` from
  `/Users/awitas/go/src/github.com/viant/agently/android`; `BUILD SUCCESSFUL`
  in 4m15s, compiling the local Forge and Agently-core SDK modules inside the
  app build.
- Cleaned the modified Forge Android `ReportBuilderRenderer.kt` ambiguous
  nested-lambda warning with an explicit measure-loop label. The focused Forge
  Android slice passed after the cleanup (`BUILD SUCCESSFUL` in 2m55s), and
  the Agently Android hosted-restore integration was rerun afterward with the
  local Forge SDK recompiling inside the app build (`BUILD SUCCESSFUL` in
  3m48s). Remaining Kotlin warnings are pre-existing in unrelated Forge UI
  files.
- Agently-core service-agent status/error recovery passed:
  `go test ./service/agent -run 'TestServiceRunPlanAndStatus_'` from
  `/Users/awitas/go/src/github.com/viant/agently-core`; `ok` in 3.506s.
- Boundary scans remain clean in production source: Forge has no
  Steward/Forecasting/line-id/stale-9191/UI-command assumptions; Agently-core
  SDK/service has no Steward/Forecasting/line-id/stale-9191/ad-targeting
  fixture strings; Agently Android/iOS production has no
  Forecasting/line-id/AdTargetingProfile/stale-9191 logic. Mobile workspace
  endpoint presets for Steward production and local `9292` are intentional.
- Agently-side mobile overlay docs were cleaned:
  `/Users/awitas/go/src/github.com/viant/agently/ios-app.md` and
  `/Users/awitas/go/src/github.com/viant/agently/android-app.md` now point at
  `agently-core/mobile_sdk-progress/README.md` and
  `agently-core/mobile_sdk-progress/resume.md` as the canonical shared mobile
  handoff. They no longer advertise the deleted `mobile_sdk/README.md`
  absolute path as a runnable instruction.
- Shared mobile SDK public-surface parity was rechecked. The only active
  exceptions in `sdk/mobile_parity_exceptions.json` remain the Go-only `mode`
  transport discriminator for Android and iOS, both expiring 2026-12-31.
  `go test ./sdk -run 'TestMobileSDKPublicSurfacesCoverClientContract'`
  passed, and the broader focused SDK slice
  `go test ./sdk -run
  'TestMobile|TestMetadataTarget|TestWorkspace|TestForge|TestLookups'` also
  passed.

Latest dashboard/backward-compatibility refresh (2026-08-05):
- Forge iOS dashboard and inline-report compatibility passed:
  `swift test --package-path /Users/awitas/go/src/github.com/viant/forge/ios
  --filter 'Dashboard|InlineReport|ReportDocument'`, 50 selected tests with 0
  failures. Coverage included compact dashboard compatibility blocks, legacy
  forecast categories, report-builder variants, dashboard runtime actions,
  filters, selections, summaries, and inline report runtime compilation.
- Forge Android dashboard and inline-report compatibility passed:
  `./gradlew :sdk:testDebugUnitTest --tests '*DashboardModelsTest' --tests
  '*DashboardRuntimeTest' --tests '*DashboardRendererSupportTest' --tests
  '*InlineReportRuntimeCompilerTest' --no-daemon --console=plain` from
  `/Users/awitas/go/src/github.com/viant/forge/android`; `BUILD SUCCESSFUL` in
  4m11s. The remaining Kotlin warnings are pre-existing in unrelated Forge UI
  files.
- Agently-core SDK dashboard/inline-report normalization and transcript
  hydration passed:
  `go test ./sdk -run
  'Test(NormalizeRenderedContent|Handler_GetTranscript_HydratesPersistedInlineReports|ApplyInlineReportWorkspaceCatalogToState|BackendClient_|HTTPClient_GetForgeWindowMetadata)'`
  from `/Users/awitas/go/src/github.com/viant/agently-core`; `ok` in 8.851s.
- No product-code changes were needed by this refresh; the current evidence
  supports dashboard/report-document backward compatibility across Forge iOS,
  Forge Android, and Agently-core SDK rendered transcript handling.

Latest backend window import and target refresh (2026-08-05):
- Agently-core Forge window loader coverage was rechecked for `$import(...)`,
  keyed report-builder imports, and broad target override keys including
  `mobile`, `tablet`, `phone`, `android`, `android:phone`, and `iosTablet`.
  `go test ./service/ui/window/...` passed from
  `/Users/awitas/go/src/github.com/viant/agently-core`.
- Workspace metadata target/platform coverage passed:
  `go test ./service/workspace -run
  'Test.*Metadata|Test.*Target|Test.*Platform|Test.*Forge|Test.*Legacy'`; `ok`
  in 3.488s.
- No backend code changes were needed by this pass. The verified split remains:
  Agently-core loads generic imported Forge windows and target context,
  Steward owns workspace-specific window content, and Forge owns native
  rendering behavior.

Latest current-diff boundary audit (2026-08-05):
- Reviewed the changed production diffs in Agently-core, Forge Android, Forge
  iOS, and Agently iOS for Steward leakage, Forecasting/line-id hardcoding,
  stale `9191`, UI-command routing assumptions, and fallback/heuristic drift.
  No product-code patch was needed from this audit.
- Forge predicate lowering remains generic and config-driven: authored
  `predicates`, `predicateBuckets`, and `predicateGroups` are lowered into the
  existing native report-builder structures without Steward, Forecasting,
  line-id, prompt-text, workspace, or window-id routing logic.
- Agently-core Android restore remains scoped to active-turn separation: an
  active live turn suppresses durable hosted-workspace restore, but completed
  turns with leftover buffered execution groups can restore historical hosted
  content.
- Focused predicate symmetry checks passed after the audit:
  `./gradlew :sdk:testDebugUnitTest --tests '*ReportBuilderPredicatesTest'
  --no-daemon --console=plain` from Forge Android finished `BUILD SUCCESSFUL`
  in 45s, and
  `swift test --package-path /Users/awitas/go/src/github.com/viant/forge/ios
  --filter 'testReportBuilderPredicateLoweringDerivesFiltersGroupsAndFamilies'`
  passed 1 selected test with 0 failures.

Latest Steward contract refresh (2026-08-05):
- Rechecked current Steward workspace state on `ENG-54517`; local edits remain
  in Steward prompts, skills, intake rules, and Forge window/reporting assets.
  No Agently or Forge runtime code change was needed by this pass.
- Ran the focused Steward contract set with
  `FORGE_ROOT=/Users/awitas/go/src/github.com/viant/forge`: Forecasting builder,
  Forecasting predicates, Metric Report Builder, Metric predicates, shared
  endpoint datasets, Metric window params, report preset primitive coverage,
  forecast-targeting contract, audience forecast dashboard export contract, and
  reporting delivery contract.
- The set passed. It covers line builder-prefill preserving
  `line:<requested line id>`, unified predicates lowering to legacy runtime
  structures, request parity across canonical and lowered config forms,
  shared-endpoint dataset save/reopen durability, canonical window parameters,
  report primitive coverage, audience forecast dashboard export, and 69
  reporting delivery checks.
- Node emitted existing module-type warnings while importing Forge ES modules
  from files without package-level `"type": "module"`; no contract failed.

Latest mobile endpoint boundary fix (2026-08-05):
- Removed Steward production endpoint presets from generic Agently Android and
  iOS app source. Both apps keep generic local `:9292` development presets in
  source and accept workspace-specific presets from configuration instead:
  Android via `agently.android.workspaceEndpointsJson` or
  `AGENTLY_WORKSPACE_ENDPOINTS_JSON`, and iOS via
  `AGENTLY_WORKSPACE_ENDPOINTS_JSON` or `--workspaceEndpointsJSON=...`.
- Steward can still be selected in local/dev builds by passing configured
  endpoint JSON, but generic Agently mobile source no longer embeds
  `https://steward.agently.viantinc.com`.
- Verification passed:
  `./gradlew :app:testDebugUnitTest --tests '*AppSettingsRuntimeTest'
  --tests '*AppEndpointConfigTest' --no-daemon --console=plain` from Agently
  Android (`BUILD SUCCESSFUL` in 1m42s), and
  `swift test --package-path /Users/awitas/go/src/github.com/viant/agently/ios
  --filter 'AuthRuntimeTests'` from Agently iOS (13 tests, 0 failures).
- The Android configured-endpoint compile path also passed with
  `AGENTLY_WORKSPACE_ENDPOINTS_JSON='[{"title":"Steward","subtitle":"Viant Steward workspace","value":"https://steward.agently.viantinc.com"}]'
  ./gradlew :app:compileDebugKotlin --no-daemon --console=plain`; `BUILD
  SUCCESSFUL` in 2m22s.
- A production-source scan of Agently Android and iOS found no remaining
  Steward production URL, `AdTargetingProfile`, hardcoded `line:7288336`, or
  stale `9191` matches.

Latest broader mobile app verification (2026-08-05):
- Agently Android app compile/unit lane passed after the endpoint-boundary fix:
  `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
  --console=plain` from `/Users/awitas/go/src/github.com/viant/agently/android`;
  `BUILD SUCCESSFUL` in 2m23s.
- Focused Agently iOS foundation lane passed:
  `swift test --package-path /Users/awitas/go/src/github.com/viant/agently/ios
  --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests|HostedWorkspacePolicyTests'`;
  34 selected tests, 0 failures.
- The iOS pass covered configured endpoint injection, mobile OAuth/OOB auth,
  developer session credential paste handling, hosted workspace reuse policy,
  hosted workspace presentation, Forge datasource input preservation, lookup
  datasource shape, and composer lookup/focus behavior.
- Re-ran the mobile production-source leakage scan; Agently Android/iOS
  production source still has no Steward production URL, `AdTargetingProfile`,
  hardcoded `line:7288336`, or stale `9191` matches.

Latest full Agently iOS foundation verification (2026-08-05):
- Full Agently iOS foundation package passed after the endpoint-boundary fix:
  `swift test --package-path /Users/awitas/go/src/github.com/viant/agently/ios`;
  93 tests, 0 failures.
- Coverage included endpoint injection/auth, active-turn transcript separation,
  hosted workspace restore/layout policy, Forge transcript block adaptation,
  Forge datasource loading, composer lookup behavior, approval callback/editing
  helpers, elicitation validation, branding, and platform target context.
- Re-ran mobile production-source leakage scan and diff hygiene afterward:
  Agently Android/iOS production source still has no Steward production URL,
  `AdTargetingProfile`, hardcoded `line:7288336`, or stale `9191`; `git diff
  --check` passed in Agently, Agently-core, Forge, and Steward.

Latest Agently merge and Forge full-lane verification (2026-08-05):
- Fetched Agently origin and merged the incoming `origin/main` version update
  (`v0.3.96`) into local `main`. The merge completed cleanly, with local
  mobile endpoint-boundary edits preserved.
- Agently local `main` is now ahead of `origin/main` by two commits. Push is
  still blocked by GitHub credentials, not by branch divergence:
  `Permission to viant/agently.git denied to awitas_viant`.
- Full Forge iOS package passed:
  `swift test --package-path /Users/awitas/go/src/github.com/viant/forge/ios`;
  223 tests, 0 failures.
- Full Forge Android SDK passed:
  `./gradlew :sdk:testDebugUnitTest --no-daemon --console=plain` from
  `/Users/awitas/go/src/github.com/viant/forge/android`; `BUILD SUCCESSFUL`
  in 1m45s.
- The full Android suite initially exposed a test-only timing issue in
  `ActionHookRuntimeTest.openWindowUsesRegisteredMetadataLoader`: the test used
  a fixed `50ms` delay for asynchronous metadata loading. The test now waits for
  the metadata signal with a bounded timeout; production Forge runtime code was
  not changed for this fix.
- The focused metadata-loader test then passed:
  `./gradlew :sdk:testDebugUnitTest --tests
  'com.viant.forgeandroid.runtime.ActionHookRuntimeTest.openWindowUsesRegisteredMetadataLoader'
  --no-daemon --console=plain`; `BUILD SUCCESSFUL` in 1m52s.
- `git diff --check` passed in Agently, Agently-core, and Forge after the
  merge/test cleanup.

Latest Android native post-merge verification (2026-08-06):
- Default Agently Android compile/unit lane passed after merging Agently
  `origin/main`: `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest
  --no-daemon --console=plain` from
  `/Users/awitas/go/src/github.com/viant/agently/android`; `BUILD SUCCESSFUL`
  in 2m45s.
- Configured local Steward endpoint install lane passed on the attached phone
  emulator with
  `AGENTLY_WORKSPACE_ENDPOINTS_JSON='[{"title":"Local Steward","subtitle":"Local Agently Steward workspace","value":"http://10.0.2.2:9292"}]'
  ./gradlew :app:installDebug --no-daemon --console=plain`.
- Started the installed phone app with `adb -s emulator-5556 shell am start -n
  com.viant.agently.android/.MainActivity`; Android reported
  `com.viant.agently.android/.MainActivity` focused and the app process alive.
- Native screenshot evidence at `/tmp/agently-android-5556.png` shows the
  cleaned auth screen: `This workspace requires authorization.`, `Sign in`,
  and workspace settings only. UI hierarchy search confirmed no visible `OOB`
  or `session` sign-in noise.
- Tablet emulator `emulator-5554` could not be counted: Gradle skipped it after
  adb property fetch timeouts (`Unknown API Level`), and direct `adb install`
  hung. Restart/recover the tablet emulator before using it as verification
  evidence.

Latest iOS simulator native forecast verification (2026-08-06):
- Built the iOS simulator app with `xcodebuild -project AgentlyApp.xcodeproj
  -scheme AgentlyApp -sdk iphonesimulator -destination 'generic/platform=iOS
  Simulator' -derivedDataPath .build/xcode-native-20260806
  CODE_SIGNING_ALLOWED=NO build`; `BUILD SUCCEEDED`, producing
  `.build/xcode-native-20260806/Build/Products/Debug-iphonesimulator/Agently.app`.
- Installed it on the booted iPhone 17 and iPad Pro 11-inch simulators. Seeded
  normal persisted workspace selection with `UserDefaults` key
  `agently.ios.settings.apiBaseURL=http://127.0.0.1:9292` and launched with
  `SIMCTL_CHILD_AGENTLY_WORKSPACE_ENDPOINTS_JSON`, keeping endpoint selection
  config-driven rather than baked into app source.
- iPhone screenshot `/tmp/agently-ios-iphone.png` showed the Viant Steward
  mobile shell loading a selected conversation. iPad screenshot
  `/tmp/agently-ios-ipad-2.png` showed the tablet shell connected with
  transcript/composer visible.
- Ran the live iPad UI test for `open forecast builder for line 7288336`:
  `AGENTLY_IOS_LIVE_UI_TESTS=1
  AGENTLY_IOS_UI_TEST_BASE_URL=http://127.0.0.1:9292 xcodebuild -project
  AgentlyApp.xcodeproj -scheme AgentlyAppLiveUITests -destination 'platform=iOS
  Simulator,id=B2AA0D68-7312-4CC9-85B8-0544341A942D' -derivedDataPath
  .build/xcode-live-ipad-forecast-20260806
  -only-testing:AgentlyAppUITests/ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer
  CODE_SIGNING_ALLOWED=NO test`.
- The live UI test passed: 1 test, 0 failures, `** TEST SUCCEEDED **`. It
  tapped New Chat, typed and sent the forecast prompt, observed `Forecasting`,
  and found `forge-report-builder-filter-summary`,
  `forge-report-builder-static-filter-dateRange`,
  `forge-report-builder-dynamic-filters`,
  `forge-report-builder-dynamic-family-inventory`, and
  `forge-report-builder-add-line-inventory`.
- Test result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-ipad-forecast-20260806/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.06_00-22-13-+0200.xcresult`.

Latest Steward forecast-builder prefill hardening (2026-08-06):
- Android phone replay on `emulator-5556` opened Forecasting for
  `open forecast builder for line 7288336`, but the builder showed
  `Filters 0 active`. Conversation `726c323f-16fe-4d10-a1a8-55a1dd3cdeed`
  proved mobile received an ID-only `ui/window/setFormData` payload:
  `AdLineId=[7288336]` plus `prefill.scope.targetKey="line:7288336"`, without
  targeting predicates. Treat that as a Steward orchestration miss, not a
  Forge/mobile rendering miss.
- Steward-only fix: `intake/activation_rules.yaml` now sets
  `requireTargetingProfile=true` and `requireBuilderPredicatePrefill=true` for
  direct line builder-prefill asks, and `prompts/workspace_ui.yaml` now
  preserves line target keys as `line:<requested line id>` while requiring
  `steward-AdTargetingProfile` before any successful predicate prefill.
- Contract checks passed:
  `node skills/forecast-targeting.contract.test.mjs` and
  `node extension/forge/windows/forecastingCubeBuilder.test.js`.
- Host OOB replay against Agently `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp` produced conversation
  `2a0dfb33-b284-486b-afa0-4655821b52c6` and the corrected tool order:
  `steward/AdTargetingProfile`, `llm/skills/activate(forecast-targeting)`,
  `steward/ForecastingTargetingConvert`, then UI open. The converter resolved
  `includeCountry=["US"]`, `includePostalCodeList=[70731]`, and the expected
  PMP deal list `[64512,66016,76060,76084,76105,76162,76711,89531,90473,90476,90482,98075,143925,143934,144156,146708,148790,149114]`.
- Android phone native proof gap is now closed. Rebuilt/installed the debug APK
  on `emulator-5556`, cleared only phone app data, selected the default
  `Android Host 9292` workspace, authenticated through the debug-only
  developer session helper, and ran:
  `ADB="$HOME/Library/Android/sdk/platform-tools/adb"
  ./scripts/android-semantic-compose-replay.sh --device emulator-5556 --prompt
  "open forecast builder for line 7288336" --expect "Forecasting" --wait 120`.
  The wrapper completed with `verified: Forecasting` and `done`.
- The successful Android conversation was
  `a9acab62-b195-4bff-a7f4-046c7fe7e130`. Tool order completed as
  `steward/AdTargetingProfile`, `llm/skills/activate`,
  `steward/ForecastingTargetingConvert`, `ui/window/list`, `ui/view/open`,
  and `ui/window/setFormData`.
- Android visible UI showed Forecasting open with `Filters` and `5 active`.
  The persisted `ui/window/setFormData` request carried the converted
  predicates: `includeCountry=["US"]`, `includePostalCodeList=[70731]`,
  the expected PMP deal list
  `[64512,66016,76060,76084,76105,76162,76711,89531,90473,90476,90482,98075,143925,143934,144156,146708,148790,149114]`,
  `sharedIncludeFilters` for PMP/location/postal code, and
  `scope.targetKey="line:7288336"`.
- Android tablet native proof is now closed on `emulator-5554`. The Pixel
  Tablet emulator could not reach `10.0.2.2:9292`, so the verified local path
  is `adb reverse tcp:9292 tcp:9292` plus the app's `Localhost 9292`
  workspace endpoint (`http://127.0.0.1:9292`). After reinstalling the debug
  APK and authenticating through the debug-only developer session helper, the
  tablet layout showed `Backend http://127.0.0.1:9292`.
- The tablet replay command
  `ADB="$HOME/Library/Android/sdk/platform-tools/adb"
  ./scripts/android-semantic-compose-replay.sh --device emulator-5554 --prompt
  "open forecast builder for line 7288336" --expect "Forecasting" --wait 120`
  completed with `verified: Forecasting` and `done`.
- Tablet conversation `24d86d0b-cfa9-4551-b7b3-33294f288dfd` completed
  `steward/AdTargetingProfile`, `llm/skills/activate`,
  `steward/ForecastingTargetingConvert`, `ui/window/list`, `ui/view/open`,
  and `ui/window/setFormData`. The visible UI showed Forecasting with
  `Filters` and `5 active`, and the persisted `setFormData` payload carried
  `includeCountry=["US"]`, `includePostalCodeList=[70731]`, the expected PMP
  deal list, and `scope.targetKey="line:7288336"`.

Latest native auth/session and boundary refresh (2026-08-06):
- Agently iOS focused foundation slice passed 34 selected tests with 0
  failures:
  `swift test --package-path ios --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests|HostedWorkspacePolicyTests'`.
  This covers configured workspace endpoints, mobile OAuth callback behavior,
  OOB/developer-session auth, hosted workspace policy, Forge datasource input
  preservation, and composer lookup behavior.
- Agently-core shared checks passed:
  `./gradlew testDebugUnitTest --tests '*WorkspaceRestoreTest' --no-daemon
  --console=plain` from `sdk/android`, and
  `go test ./service/agent -run 'TestServiceRunPlanAndStatus_' -count=1`.
- Production-only boundary scans passed: Agently Android/iOS production,
  Agently-core production, and Forge production had no Steward production URL,
  `AdTargetingProfile`, hardcoded `line:7288336` / `7288336`, stale mobile
  `9191`, or Forge-to-Agently import leakage.

Latest Forge and Steward contract refresh (2026-08-06):
- Forge iOS focused report-builder/metadata slice passed:
  `swift test --package-path ios --filter 'ReportBuilder|MetadataResolver'`
  with 27 selected tests and 0 failures.
- Forge Android focused report-builder/targeting slice passed:
  `./gradlew :sdk:testDebugUnitTest --tests '*ReportBuilderPredicatesTest'
  --tests '*ReportBuilderStateStorageTest' --tests '*TargetingTest'
  --no-daemon --console=plain`.
- Steward forecasting contracts passed with
  `FORGE_ROOT=/Users/awitas/go/src/github.com/viant/forge`:
  `node extension/forge/windows/forecastingCubeBuilder.test.js`,
  `node extension/forge/windows/forecastingCubeBuilder.predicates.test.mjs`,
  and `node skills/forecast-targeting.contract.test.mjs`.
- Steward report-builder/window contracts passed:
  `node extension/forge/windows/metricReportBuilder.test.js`,
  `node extension/forge/windows/metricReportBuilder.predicates.test.mjs`,
  `node extension/forge/windows/metricReportBuilder.windowParams.test.mjs`,
  and
  `node extension/forge/windows/metricReportBuilder.sharedEndpointDatasets.test.mjs`.
- Steward report preset/dashboard/prompt contracts passed after exporting
  `FORGE_ROOT` for every command:
  `node extension/forge/windows/reportPresetPrimitiveCoverage.test.mjs`,
  `node templates/audienceForecastDashboard.contract.test.mjs`, and
  `node agents/steward/prompt/reportingDelivery.contract.test.mjs`.
  Node emitted only existing module-type warnings while importing Forge ES
  modules.
- Agently push remains blocked by GitHub credentials, not by branch
  divergence: after `git fetch origin main`, Agently is `0` behind and `2`
  ahead, but `git push origin main` fails with
  `Permission to viant/agently.git denied to awitas_viant`.
- Cleaned the untracked Agently mobile helper scripts so they stay generic
  before being packaged: Android OOB install defaults to
  `http://10.0.2.2:9292`, iOS OOB simulator launch defaults to
  `http://127.0.0.1:9292`, and both require the OOB secret reference via
  environment variables instead of embedding a personal secret path or Steward
  production endpoint. `bash -n` passed for all three scripts,
  `./scripts/android-semantic-compose-replay.sh --self-test` passed, and the
  Agently app/script leakage scan found no Steward prod URL, personal OOB
  secret reference, hardcoded line id, stale `9191`, or Steward targeting tool.
- Focused Agently Android settings/auth slice passed after the cleanup:
  `./gradlew :app:testDebugUnitTest --tests '*AppSettingsRuntimeTest'
  --tests '*AuthRuntimeTest' --tests '*HostedWorkspaceRestoreTest'
  --no-daemon --console=plain`. Only existing Forge Kotlin warnings were
  emitted.
- Scrubbed exact local secret references from the Agently iOS live Forecasting
  UI test and the tracked `ui/tmp-report-builder-probe.mjs` helper. The UI
  test now requires `AGENTLY_IOS_UI_TEST_OOB_SECRET`; the probe requires
  `AGENTLY_PROBE_OOB_SECRET_REF` and `AGENTLY_PROBE_AUTH_CONFIG_REF`. A scan
  for the exact local secret file names across Agently, this progress folder,
  and Forge native production sources returned no matches.
- iOS verification passed after the scrub:
  `swift test --package-path ios --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests|HostedWorkspacePolicyTests'`
  passed 34 selected tests with 0 failures, and
  `xcodebuild -quiet -project ios/AgentlyApp.xcodeproj -scheme AgentlyApp
  -configuration Debug -destination 'generic/platform=iOS Simulator'
  -derivedDataPath ios/.build/xcode-scrub build-for-testing
  CODE_SIGNING_ALLOWED=NO` exited with code 0. `node --check
  ui/tmp-report-builder-probe.mjs` also passed.
- Classified the untracked Agently app-plan notes (`android-app.md`,
  `ios-app.md`, `froge-fence.md`) as historical context only; this progress
  folder remains authoritative. Verified the untracked Agently Go contract
  tests before packaging decisions: `go test . -run 'TestWorkspaceReporting'
  -count=1` and `go test ./tools/system/platform -run 'TestService_' -count=1`
  both passed.
- Rechecked the untracked Forge report-builder preview/runtime assets. Running
  every `src/demos/reportBuilder/*.test.js` directly passed. The project
  runner `npm run test:authored-runtime -- --no-color` also passed end to end,
  including hosted Steward builder render smoke, fixed-position loading-icon
  rotation coverage, authored runtime semantic sections, export/import fixture
  families, and runtime preview helpers.
- Forge packaging note: the untracked preview/runtime files are required
  assets, not scratch. Tracked Forge files already reference them from
  `package.json`, `scripts/run-authored-runtime-unit-tests.mjs`,
  `scripts/verify-semantic-preview-phase1.mjs`,
  `ReportBuilderPreview.jsx`, dashboard runtime tests, and reporting fixtures.
  Include the 36 untracked candidates under `src/demos/reportBuilder` plus the
  native `ReportBuilderPredicates` source/tests when packaging Forge. Hygiene
  scans found no personal secret refs, stale mobile `9191`, or Agently imports
  in those untracked assets; Steward refs are demo fixture model/target ids.
  `node --check` passed for every untracked JS asset.

Latest Agently and Agently-core packaging refresh (2026-08-06):
- Agently-core focused regressions passed:
  `go test ./service/agent -run
  'TestServiceRunPlanAndStatus_(RecoversDurableFinalAssistantContent|EmptyResultUsesLastFailedModelCallError)'
  -count=1`, and `./gradlew testDebugUnitTest --tests
  '*WorkspaceRestoreTest' --no-daemon --console=plain` from
  `agently-core/sdk/android`.
- Agently untracked packaging classification: tracked `ios/README.md`
  references `../ios-app.md`, so `ios-app.md` must be included or that
  tracked reference must change. `android-app.md` and `froge-fence.md` are
  historical context only. The untracked `scripts/` helpers are verified local
  mobile launch/replay helpers referenced by this handoff, and the untracked
  Go tests are verified contract coverage.
- Hygiene for Agently untracked items passed: no exact local secret refs, stale
  mobile `9191`, hardcoded forecast line id, or Steward targeting tool names.
  `bash -n` passed for each untracked shell script, and `node --check
  ui/tmp-report-builder-probe.mjs` passed.
- Agently-core untracked docs are classified: include
  `mobile_sdk-progress/README.md` with this mobile/Forge handoff because this
  resume file delegates to it. Keep `doc/mcp-2026-extension-upgrade.md` and
  `doc/mcp-2006/` separate unless intentionally packaging the MCP 2026
  protocol-extension workstream; they are not part of the current mobile SDK /
  Forge parity package.
- Agently-core TypeScript SDK test fixtures were scrubbed of the remaining
  personal OOB path; the exact secret rescan over progress docs, MCP docs,
  `sdk`, and `service` is clean, and `npm test` from `sdk/ts` passed all 366
  Vitest tests.

Latest Steward packaging refresh (2026-08-06):
- Steward's untracked `skills/forecast-targeting.contract.test.mjs` is required
  contract coverage for the prompt/intake changes. It protects line requests
  staying `AdLineId`, no audience masquerading, mandatory
  `steward-AdTargetingProfile`, and mandatory resolved predicate fields before
  Forecasting builder prefill success.
- Compact Steward verification passed with
  `FORGE_ROOT=/Users/awitas/go/src/github.com/viant/forge`:
  `node skills/forecast-targeting.contract.test.mjs`,
  `node extension/forge/windows/forecastingCubeBuilder.test.js`,
  `node extension/forge/windows/forecastingCubeBuilder.predicates.test.mjs`,
  `node extension/forge/windows/metricReportBuilder.predicates.test.mjs`,
  `node extension/forge/windows/reportPresetPrimitiveCoverage.test.mjs`, and
  `node agents/steward/prompt/reportingDelivery.contract.test.mjs`. Only
  existing Forge ES-module type warnings were emitted.
- Steward boundary scan over changed and untracked files found no exact local
  secret refs, stale mobile `9191`, or Agently app/source references. The
  existing `targetOverrides` stanza is generic/data-driven; the current
  report-builder diff only adds the Forecasting builder through `$import`.

Latest local packaging/push status (2026-08-06):
- Agently local commit `719460dc` packages mobile auth/bootstrap helpers and
  verification scripts, but push to `viant/agently` is blocked by the current
  GitHub HTTPS credential.
- Agently-core local commit `320207d` packages restore/run durability plus this
  progress handoff, but push to `viant/agently-core` is blocked by the same
  credential issue.
- Forge local commit `1cec1ca` packages native predicate lowering and
  report-builder preview/runtime assets, but push to `viant/forge` is blocked
  by the same credential issue.
- Steward merged the remote SendGrid commits, re-ran the compact forecasting
  and reporting contracts, and pushed `ENG-54517` successfully as `17e2457`.
