# Mobile SDK Progress And Contract

Last updated: 2026-08-06

This folder is the current source of truth for the mobile work. It replaces the
former top-level `mobile_sdk.md` and `mobile_sdk-progress.md` handoff files and
supersedes historical status claims in Android/iOS app plans when current code
or tests disagree.

## Product Contract

The Android and iOS apps must render the same canonical conversation state as
web, including assistant text, ordinary code fences, Forge UI fences,
approvals, elicitations, artifacts, and lookup input. Phone keeps execution
details out of the transcript; tablet may expose them only through an explicit
user-configurable inspector.

Desktop/tablet and phone are different presentations of the same state, not
different product implementations. A response must remain usable when a
structured Forge surface is wider or longer than the available viewport.

## Ownership Boundaries

| Layer | Owns | Must not own |
|---|---|---|
| Steward workspace | Workspace-specific prompts, endpoint configuration, data and credentials | Generic mobile rendering behavior |
| Agently | Conversation lifecycle, transcript placement, auth/session and user interaction | Dashboard parsing, chart semantics or Steward-specific UI behavior |
| Forge | Generic, data-driven visual models, fence decoding/materialization, layout and scroll policy | Conversation lifecycle or workspace-specific business rules |
| Agently core SDK | Canonical API/state contracts and platform-neutral validation | Native presentation implementation |

No client may inspect a title, prompt text, or workspace name to select layout,
data behavior, or a semantic rendering path. Those are metadata-driven Forge
decisions. Platform differences belong in target overrides or explicit
presentation parameters.

## Verification Rules

Each feature is complete only after all applicable checks pass:

1. Contract and focused native unit tests pass.
2. No forbidden dependency is introduced: `forge` has no Agently or Steward
   imports; Agently has no Steward-specific rendering logic.
3. Android phone and tablet are checked in the signed-in Steward workspace.
4. iPhone and iPad are checked in the signed-in Steward workspace.
5. The same prompt/conversation is compared with web. The transcript shows
   assistant bubbles and rendered Forge content, not raw internal execution
   detail on phone.

OOB configuration is supplied only through local encrypted secret references or
environment/build properties. Never place credential values in source, plans,
logs, screenshots, or test fixtures. Use the current scripts in
`/Users/awitas/go/src/github.com/viant/agently/scripts/`:

- `install-android-oob-debug.sh`
- `launch-ios-oob-sim.sh`

## Review Protocol

Before implementation, delegate an architecture review to Codex from
`agently-core` using a read-only prompt. After each feature, independently
review the diff for boundary leaks and brittle behavior before running device
verification. A review may identify work, but does not substitute for tests or
emulator evidence.

| Item | State | Evidence | Next action |
|---|---|---|---|
| Public mobile SDK parity | Complete | `go test ./sdk -run 'TestMobileSDKPublicSurfacesCoverClientContract|TestCanonical' -count=1` passed 2026-07-18 | Keep the test and expiry-checked exception list current. |
| Shared mobile handoff | Complete | `mobile_sdk-progress/README.md` now owns contract plus progress history; `mobile_sdk-progress/resume.md` points future sessions there. | Maintain this README after each feature. |
| Native Forge-fence scanning | Complete | Forge Android `:sdk:compileDebugKotlin --rerun-tasks` passed; Forge iOS focused parser/envelope/presentation tests passed. Both scanners preserve legacy headers and do not close a compact JSON fence inside a JSON string. | Verify the exact conversation on devices when Steward is reachable. |
| iOS Forge transcript ownership | Complete, Forecasting device proof captured | Forge Runtime owns scanner, data/UI envelope decoding, JSON/CSV modes, generic block translation, canonical typed-part adaptation, normalization, and inline metadata updates. Agently iOS only projects SDK content into Forge parts, places, and hydrates the rendered window. iPhone and iPad native composer runs opened Forecasting with the canonical `reportBuilder`/`reportBuilderRef` split. | Keep device proof current when transcript ownership changes. |
| Android Forge transcript ownership | Complete, phone/tablet Forecasting device proof captured | Forge Android owns scanner, data/UI envelope decoding, JSON/CSV modes, block translation, canonical typed-part adaptation, normalization, synthetic summary data, and explicit empty source hydration. Agently Android only projects SDK content into Forge parts, places, renders, and hydrates. Pixel Tablet and Pixel 10 Pro native replays opened Forecasting with the canonical `reportBuilder`/`reportBuilderRef` split. | Keep device proof current when transcript ownership changes. |
| Canonical rendered-content envelope | Complete | Canonical transcript and live reducer state expose the same additive `renderedContent`; Go, TS, Android, and iOS SDK fields decode it. Completed native assistant turns now use that typed contract; raw parsing is limited to legacy and streaming content. | Retain fixtures and verify on devices. |
| Inline surface presentation | Complete, web/iOS/Android visual proof captured | Forge owns an explicit form-factor policy: compact 340 dp/pt, regular 420 dp/pt. It is metadata-aware and contains no title, prompt, workspace, or content heuristic. The same committed inline report conversation renders as native report content on web, iPhone, iPad, Android tablet, and Android phone. | Keep regression coverage current as new inline block types are added. |
| Canonical inline report compilation | Complete, full target visual proof captured | Forge iOS and Android now compile `report-document-v1` into the existing native `dashboard.reportRuntime`. Focused suites preserve all 17 native primitive kinds, JSON/CSV datasets, layout order, and KPI values; Swift and Kotlin each pass 3 tests with 0 failures. Conversation `a5fc8e9f-8d48-431c-9cb9-820a819eb7aa` renders the same committed report on web, iPhone, iPad, Android tablet, and Android phone. | Business-data diagnosis remains separate from renderer parity. |
| Android device verification | Phone and tablet Forecasting prefill verified post-coalescing; latest tablet smoke refreshed 2026-08-06 | Pixel Tablet reaches local Steward through `adb reverse tcp:9292 tcp:9292` and `http://localhost:9292` after the emulator gateway route reported unreachable for `10.0.2.2`; Pixel 10 Pro also verifies the phone path. Debug OOB completes, semantic replay sends `open forecast builder for line 7288336`, and the latest 2026-08-06 tablet smoke verified the native Forecasting surface with `Filters` and `5 active`. | Keep reverse/gateway behavior documented for local runs. |
| iOS device verification | iPhone and iPad Forecasting prefill verified post-coalescing; latest smoke refreshed 2026-08-05 | iPhone 17 simulator and iPad Pro simulator were sent from native composers through `AgentlyAppLiveUITests` against `127.0.0.1:9292`. The latest 2026-08-05 live UI tests passed on both devices and verified the native Forecasting surface after `open forecast builder for line 7288336`. | Keep live UI tests current with app navigation changes. |

## 2026-08-06 iOS Forecasting Prefill Refresh

- Reproduced the iPhone live UI test as a real execution rather than a skipped
  XCTest. Direct `xcodebuild test` did not pass the OOB secret reference into
  the XCTest process, so verification used the generated `.xctestrun` file for
  local-only environment injection and kept the secret value out of source.
- Fixed iOS workspace URL normalization in Agently so a single-slash scheme
  form such as `http:/127.0.0.1:9292` is repaired to
  `http://127.0.0.1:9292` before client creation. The focused SwiftPM test
  `AppStateTargetingTests/testNormalizeAPIBaseURLRepairsSingleSlashScheme`
  passed.
- Fixed Forge iOS report-builder initialize hook application to match Android's
  generic semantics: hook props now receive a runtime-shaped state with
  `dynamicGroups`, legacy dynamic-filter keys, and runtime static-filter
  values; hook results are merged over fallback state instead of requiring a
  complete persisted-state payload. iOS also refreshes window metadata before
  invoking initialize hooks so action-code lookup is not stale, accepts
  runtime-shaped hook result filters, stringifies numeric dynamic-filter
  drafts, and defaults omitted dynamic-selection `group` values to match
  Android's hook payload shape.
- Verified the focused Forge iOS hook-result contract with:
  `swift test --package-path ios --filter
  ForgeIOSTests/testReportBuilderHookResultAcceptsRuntimeShapedFilters`.
- Verified the full Forge iOS package after the hook parity patch:
  `swift test --package-path ios`; 224 tests passed, 0 failed.
- Verified iPhone 17 live Forecasting replay against the patched local
  `:9292` Steward workspace:
  result bundle
  `ios/.build/xcode-live-iphone-forecast-20260806-prefillfix/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.06_04-58-01-+0200.xcresult`;
  summary reports `passedTests=1`, `failedTests=0`, `skippedTests=0`.
- Verified iPad Pro 11-inch (M5) live Forecasting replay against the same
  patched local server:
  result bundle
  `ios/.build/xcode-live-ipad-forecast-20260806-prefillfix/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.06_05-05-51-+0200.xcresult`;
  summary reports `passedTests=1`, `failedTests=0`, `skippedTests=0`.
- Server evidence for both runs showed the full generic bridge sequence:
  `ui.window.open`, `ui.data.fetch`, and `ui.window.setFormData` for the
  Forecasting report-builder window. This preserves the ownership boundary:
  Agently owns URL/auth/bootstrap, Forge owns generic report-builder hook/state
  application, and Steward remains only the workspace provider of the
  Forecasting builder metadata and hook code.

## 2026-08-06 Android Local Auth And Build Rescan

- Re-verified the Android app against current local Agently/Forge sources from
  `/Users/awitas/go/src/github.com/viant/agently/android` with
  `AGENTLY_ANDROID_BASE_URL=http://10.0.2.2:9292`.
- Ran the Android build/unit gate:
  `./gradlew :forge-sdk:compileDebugKotlin :app:testDebugUnitTest
  --console=plain`; build succeeded, including the Forge SDK Kotlin compile
  path that previously failed.
- Installed and launched the debug APK on the attached Android emulator
  `emulator-5556` / `Pixel_10_Pro(AVD) - 17` with:
  `./gradlew :app:installDebug --console=plain`, `adb shell pm clear
  com.viant.agently.android`, and `adb shell am start -n
  com.viant.agently.android/.MainActivity`; install succeeded and the app
  stayed alive with PID `30405`.
- Built the server-capable Agently binary from current local sources using a
  temporary Go workspace at `/tmp/agently-mobile-verify/go.work` that points to
  local `agently`, `agently-core`, and `forge`, then ran:
  `GOWORK=/tmp/agently-mobile-verify/go.work go build -o
  /tmp/agently-mobile-verify/agently-local ./agently`. The first attempted
  build from `agently-core/cmd/agently` was intentionally not used because that
  entrypoint is a skill/query CLI and does not expose `serve`.
- Started the current-source local Steward-backed Agently server on `:9292`
  with `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp
  /tmp/agently-mobile-verify/agently-local serve -a ':9292'
  -w=/Users/awitas/go/src/github.com/viant-internal/steward_ai/deployment/steward`.
  Startup loaded Steward reporting and the expected Forge windows including
  `forecastingCubeBuilder`; host `/v1/api/auth/me` returned the expected 401
  unauthenticated response, and the Android emulator reached
  `10.0.2.2:9292` with `nc` exit 0.
- UI automation confirmed the first-run Android workspace selector defaults to
  `Android Host 9292` / `http://10.0.2.2:9292`, then advances to the
  auth-required screen after Continue. App logcat showed `/v1/api/auth/me`
  reaching `http://10.0.2.2:9292` and returning the expected 401
  unauthenticated response instead of crashing.
- Tapping `Sign in` opened the in-app OAuth dialog and WebView with status
  `Loading idp.viantinc.com...`; logcat showed no `FATAL EXCEPTION` or app
  crash. The debug-only `Use developer session` affordance was visible because
  this verification used a debug APK and remains gated by `BuildConfig.DEBUG`.

## 2026-08-04 Latest Boundary and Port Rescan

- Re-scanned Agently Android/iOS, Forge Android/iOS, and the mobile handoff
  docs after the latest source changes. Forge native sources contain no Agently
  or Steward imports; one Android Forge comment was updated from an
  Agently-specific host reference to generic host-application wording.
- Removed Android Agently datasource-loader debug logging that special-cased
  `line_header_lookup`, `campaign_header_lookup`, and `order_header_lookup`.
  The loader remains a generic Agently-to-Forge datasource bridge and no longer
  has ad-workspace-specific log branches.
- Updated local mobile defaults and visible presets to use the isolated
  Steward test port `9292`: Android defaults to `http://10.0.2.2:9292`, iOS
  defaults to `http://127.0.0.1:9292`, and the workspace preset lists no
  longer surface the shared `9191` port.
- Verified:
  `cd /Users/awitas/go/src/github.com/viant/agently/android &&
  ./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon --console=plain`
  passes after the port/default and datasource-loader cleanup. `git diff
  --check` passes in both Agently and Forge.
- Verified iOS build against the booted iPad simulator:
  `xcodebuild -project ios/AgentlyApp.xcodeproj -scheme AgentlyApp
  -configuration Debug -destination
  'platform=iOS Simulator,id=B2AA0D68-7312-4CC9-85B8-0544341A942D'
  -derivedDataPath ios/.build/xcode-rescan build` passes.

## 2026-08-04 Mobile Rescan and Auth Verification

- Started an isolated local Steward workspace on `:9292`:
  `./agently serve -a ':9292' -w='/Users/awitas/go/src/github.com/viant-internal/steward_ai/deployment/steward'`.
  The server loaded Forge windows including `campaign`, `line`, `reports`,
  `reportBuilder`, `metricReportBuilder`, `order`, and
  `forecastingCubeBuilder`.
- Verified the local auth surface from the host:
  `http://127.0.0.1:9292/v1/api/auth/providers` returns the Viant OAuth/BFF
  provider, and direct host OOB login returns a session cookie.
- iOS tablet verification: launched the iPad Pro 11-inch (M5) simulator
  against `http://127.0.0.1:9292` with the OOB helper. The app reached an
  authenticated Steward conversation, rendered the forecast transcript/report,
  and after the composer sizing fix the empty tablet composer no longer takes a
  large blank block. Evidence:
  `/tmp/agently-rescan/ios-ipad-127-9292-compact-composer-20260804-224318.png`.
- Android tablet auth verification: booted a fresh `Pixel_Tablet` AVD, verified
  the emulator route to `10.0.2.2:9292`, installed the debug OOB build with
  `AGENTLY_ANDROID_BASE_URL='http://10.0.2.2:9292'`, and confirmed the
  debug-only bootstrap OOB path completes:
  `OOB sign-in completed sessionPresent=true`, followed by `authState=Ready`.
  Evidence:
  `/tmp/agently-rescan/android-pixel-tablet-auto-oob-network-ok-20260804.png`.
- Android message submission verification: submitted
  `Open forecast builder for line 7288336` from the authenticated tablet app.
  The message appears in the active conversation, but the Steward turn is
  currently blocked by host-side MCP discovery timeouts to
  `http://steward.viantinc.com:5000/mcp`. The same endpoint times out from the
  Mac via `nc` and `curl`, so this is not a mobile SDK or Forge rendering
  blocker. Evidence:
  `/tmp/agently-rescan/android-pixel-tablet-forecast-final-after-wait-20260804.png`.
- Android tablet keyboard UX follow-up: the tablet composer now applies IME
  padding and keeps the Send action reachable while the soft keyboard is open.
  The same pass carries no-auto-capitalization and shared line-growth behavior
  into the tablet composer, matching the phone composer. Evidence:
  `/tmp/agently-rescan/android-pixel-tablet-ime-padding-composer-20260804.png`
  and
  `/tmp/agently-rescan/android-pixel-tablet-ime-padding-send-tapped-20260804.png`.
- Android app changes verified:
  `cd /Users/awitas/go/src/github.com/viant/agently/android &&
  ./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon --console=plain`
  passes. The only observed warning is the existing
  `FeedRuntimeTest.kt` unnecessary safe-call warning.
- iOS app build verified with the simulator launch script. Direct SwiftPM unit
  execution for the new composer helper remains blocked by existing
  macOS-target incompatibilities in iOS-only SwiftUI APIs; use the iOS
  simulator/Xcode build path for native verification.
- iOS tablet keyboard UX follow-up: phone and tablet now share
  `agentlyDismissKeyboardOnInteraction()` for non-composer content. Tapping or
  dragging the transcript/workspace content requests platform keyboard
  dismissal without putting the behavior in Forge or Steward-specific code.
  Verified by rebuilding, installing, and launching the iPad Pro 11-inch (M5)
  simulator against `http://127.0.0.1:9292` through
  `scripts/launch-ios-oob-sim.sh`. Evidence:
  `/tmp/agently-rescan/ios-ipad-keyboard-dismiss-build-20260804.png`.

Superseded blocker note: the Steward MCP transport was locally retargeted
through `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. The later 2026-08-05
Agently-core tool-surface discovery fix bounds optional pre-model discovery so
unreachable `creative` and `operation` MCP endpoints no longer block the
forecast-builder UI command path.

## 2026-08-04 Report Builder Variant Rescan

- Re-scanned the latest Agently, Agently-core, Forge, and Steward workspace
  code after the Android transport split. Local Steward remains on isolated
  port `9292`; the Android tablet is attached as `Pixel_Tablet` with
  `adb reverse tcp:9292 tcp:9292`.
- Fixed generic Forge mobile report-builder variant support. Android and iOS
  now decode `reportBuilderRef` and `reportBuilders` on a
  `dashboard.reportBuilder` container, then select the requested builder from
  `windowForm.reportBuilderRef` without changing the canonical `windowKey`.
  This matches web's model: `forecastingCubeBuilder` is opened as canonical
  `reportBuilder` plus `parameters.reportBuilderRef=forecastingCubeBuilder`.
- The implementation is generic Forge behavior. It does not hardcode Steward,
  forecasting, line ids, workspace names, prompt text, titles, or window ids.
  Agently Android only gained a null-safe metadata normalization entry for
  `reportBuilders`.
- Verified:
  `cd /Users/awitas/go/src/github.com/viant/forge/android &&
  ./gradlew :sdk:testDebugUnitTest --no-daemon --console=plain` passes,
  including new Android variant-resolution coverage.
- Verified:
  `cd /Users/awitas/go/src/github.com/viant/forge &&
  swift test --package-path ios` passes all 216 iOS Forge tests, including
  `testWindowMetadataDecodesReportBuilderVariants`,
  `testReportBuilderVariantResolutionUsesWindowFormRef`, and
  `testReportBuilderVariantResolutionReportsMissingRequestedRef`, which cover
  canonical `reportBuilderRef` plus multiple `reportBuilders` variants and
  verify the requested forecasting variant's data source/config survive the
  Swift model boundary.
- Verified:
  `cd /Users/awitas/go/src/github.com/viant/agently/android &&
  ./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
  --console=plain` passes against the updated Forge SDK.
- Reinstalled the Android OOB debug app against
  `AGENTLY_ANDROID_BASE_URL=http://localhost:9292`, launched it on the
  `Pixel_Tablet` emulator, and confirmed OOB reaches authenticated `Ready`.
  Evidence: `/tmp/agently-9292-variant-after-launch.png`.
- Submitted `Open forecast builder for line 7288336` from the Android tablet.
  The mobile client sends the message and streams without the previous false
  transport timeout. The original Steward MCP reachability blocker has since
  been retargeted locally through `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`;
  the remaining live-turn stall is broader workspace MCP discovery reaching
  unavailable `creative` and `operation` remote MCPs before `ui.window.open`.

## 2026-08-05 Latest Code Rescan

- Re-scanned current Agently, Agently-core, Forge, and Steward worktrees. The
  expected active source changes remain in mobile auth/composer/transport,
  generic Forge report-builder variants, and one Steward builder config. Other
  generated or untracked artifacts were left untouched.
- Verified Forge mobile report-builder variant support:
  `cd /Users/awitas/go/src/github.com/viant/forge &&
  swift test --package-path ios` passes all 216 iOS tests, and
  `cd /Users/awitas/go/src/github.com/viant/forge/android &&
  ./gradlew :sdk:testDebugUnitTest --no-daemon --console=plain` passes.
  `git diff --check` passes in Forge.
- Verified Agently-core Android SDK transport split:
  `cd /Users/awitas/go/src/github.com/viant/agently-core/sdk/android &&
  ./gradlew testDebugUnitTest --no-daemon --console=plain` passes.
- Verified Agently Android app wiring:
  `cd /Users/awitas/go/src/github.com/viant/agently/android &&
  ./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
  --console=plain` passes. `git diff --check` passes in Agently and
  Agently-core.
- Fixed a shared iOS compile issue found during the rescan by routing the
  composer lookup presentation through existing platform compatibility helpers
  and using `Color.agentlySystemBackground` in hosted workspace chrome. This
  preserves fullscreen lookup on iOS while allowing the shared Swift package
  to build on macOS.
- Repaired Agently iOS SwiftPM test drift without moving generic behavior into
  Steward or Forge. The fixes expose testable generic helpers for hosted
  workspace notices, approval callback request shaping, elicitation schema
  validation, composer lookup state, and datasource request-body inspection.
  Hosted Forge window restore now seeds `parameters` into `windowForm` through
  a generic recursive merge, so mobile preserves prefill data while transcript
  `windowForm` values still win.

## 2026-08-05 Forecasting Line Prefill Four-Target Rescan

- Kept the isolated local Steward lane running on `:9292` with local Datly MCP
  override `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. The listener was
  `agently` PID `1923`, and Datly MCP was PID `53081` during this pass.
- Verified the Steward-owned line targeting contract and builder window test:
  `node skills/forecast-targeting.contract.test.mjs` and
  `node extension/forge/windows/forecastingCubeBuilder.test.js` passed.
- Android tablet `emulator-5554` replay passed from the native composer with
  conversation `6e30c6b1-53b7-4565-8235-fad78b2f24b5`. The completed
  `ui/window/setFormData` request payload
  `cfdadd82-1012-4b84-ae67-a44de9a140a1` contains
  `prefill.scope.targetKey="line:7288336"` and `windowId`
  `forecastingCubeBuilder__6e30c6b1-53b7-4565-8235-fad78b2f24b5`.
  Screenshot:
  `/tmp/agently-rescan/android-tablet-forecasting-line-targetkey-20260805.png`.
- Android phone `emulator-5556` replay passed from the native composer with
  conversation `a3683a86-cd93-4481-b4b9-278e8a6be278`. The completed
  `ui/window/setFormData` request payload
  `b0968b7e-7354-472d-adf2-609843b19816` contains
  `prefill.scope.targetKey="line:7288336"` and `windowId`
  `forecastingCubeBuilder__a3683a86-cd93-4481-b4b9-278e8a6be278`.
  Screenshot:
  `/tmp/agently-rescan/android-phone-forecasting-line-targetkey-20260805.png`.
- iPhone 17 simulator `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` passed the live
  UI test after extending the post-open hold in
  `ios/UITests/AgentlyAppUITests/ForecastingPrefillUITests.swift` from 15s to
  45s so queued bridge commands can drain before teardown. Conversation
  `9783bc74-fc5e-42d6-bf4d-d1e3d38ac239` has completed
  `ui/window/setFormData`; request payload
  `9c176ed6-1766-40e0-b8d5-f28851346732` contains
  `prefill.scope.targetKey="line:7288336"`, and response payload
  `ab363d78-06f5-4d10-a8a0-9f39e03edcff` returned `{"ok":true}`. The earlier
  conversation `c3556698-a1f8-4f20-a179-901bd664db76` opened the Forecasting
  pane but timed out during teardown, so it is not counted as full form-data
  proof. Screenshot:
  `/tmp/agently-rescan/ios-iphone-forecasting-line-targetkey-rerun-20260805.png`.
- iPad Pro 11-inch simulator `B2AA0D68-7312-4CC9-85B8-0544341A942D` passed the
  same live UI test. Conversation
  `b22b9809-cc86-46d7-bd0d-5928c09e1fe5` has completed
  `ui/window/setFormData`; request payload
  `69d2e225-5246-4a48-8a79-610b74a3a86c` contains
  `prefill.scope.targetKey="line:7288336"`, and response payload
  `cc2eedbe-7b0c-4f86-b543-4c91203e160e` returned `{"ok":true}`. Screenshot:
  `/tmp/agently-rescan/ios-ipad-forecasting-line-targetkey-20260805.png`.

## 2026-08-05 Stream Hydration Follow-Up

- Re-ran the latest rescan across Agently, Agently-core, Forge, and Steward.
  Local Agently is still listening on `:9292`; iPhone 17 and iPad Pro 11-inch
  simulators are booted. Steward has the expected `$import`-based
  `forecastingCubeBuilder` variant under the canonical report-builder window.
- Fixed a native stream hydration edge case in Agently Core SDKs. When an SSE
  stream is opened before transcript hydration completes, a queued post-cursor
  event whose sequence is already present in the hydrated turn is now skipped
  as a duplicate. The cursor still wins over transcript sequence watermarks, so
  lower-numbered genuinely live post-cursor deltas continue to apply.
- The fix is shared across Android and iOS SDKs and remains generic: no
  Steward, Forge, prompt, title, workspace, or window-key special casing.
- Verified:
  `cd /Users/awitas/go/src/github.com/viant/agently-core &&
  go test ./runtime/discovery ./internal/tool/registry ./service/agent`
  passes.
- Verified:
  `cd /Users/awitas/go/src/github.com/viant/agently-core/sdk/ios &&
  swift test` passes all 68 Agently SDK tests, including
  `testTrackConversationStartsStreamBeforeHydrationAndSkipsHydratedEventSequences`.
- Verified:
  `cd /Users/awitas/go/src/github.com/viant/agently-core/sdk/ts &&
  npm test -- workspaceRestore` passes all 10 hosted restore tests.
- Verified:
  `cd /Users/awitas/go/src/github.com/viant/agently/android &&
  ./gradlew :agently-core-sdk:testDebugUnitTest --console=plain` passes after
  adding Android coverage for exact hydrated-sequence duplicate skipping while
  preserving post-cursor live deltas.
- Verified Android/Forge app compile and unit surfaces from the local composite:
  `./gradlew :forge-sdk:compileDebugKotlin :forge-sdk:testDebugUnitTest
  --console=plain` and
  `./gradlew :agently-core-sdk:testDebugUnitTest :app:testDebugUnitTest
  --console=plain` both pass.
- Verified Forge iOS:
  `cd /Users/awitas/go/src/github.com/viant/forge/ios && swift test` passes
  all 219 tests.
- Verified Agently iOS SwiftPM tests:
  `cd /Users/awitas/go/src/github.com/viant/agently &&
  swift test --package-path ios` passes all 90 tests.
- Verified Agently iOS simulator build:
  `cd /Users/awitas/go/src/github.com/viant/agently &&
  xcodebuild -quiet -project ios/AgentlyApp.xcodeproj -scheme AgentlyApp
  -configuration Debug -destination 'generic/platform=iOS Simulator'
  -derivedDataPath ios/.build/xcode-rescan build` passes.
- Verification rescan after the iOS repair: production-only boundary searches
  found no Steward, forecast-builder, report-builder window-key, line id, or
  lookup-id routing logic in Forge production sources, Agently mobile
  production sources, or Agently-core SDK production sources. The only
  workspace-name hits in Agently are endpoint presets/tests, and the only
  forecast/report-builder hits in Forge are generic model fields and focused
  test fixtures.
- Current portable verification passes from the working tree:
  `cd /Users/awitas/go/src/github.com/viant/agently-core/sdk/android &&
  ./gradlew test --no-daemon --console=plain`;
  `cd /Users/awitas/go/src/github.com/viant/forge/android &&
  ./gradlew :sdk:testDebugUnitTest --no-daemon --console=plain`;
  `cd /Users/awitas/go/src/github.com/viant/forge &&
  swift test --package-path ios`;
  `cd /Users/awitas/go/src/github.com/viant/agently &&
  swift test --package-path ios`;
  `cd /Users/awitas/go/src/github.com/viant/agently/android &&
  ./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
  --console=plain`.
- Last live runtime reachability: local Agently on `:9292` was healthy, local
  Steward Datly MCP was healthy on `127.0.0.1:5002/mcp`, and Agently was run
  with `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. Android `Pixel_Tablet` was
  attached with `adb reverse tcp:9292 tcp:9292`. The 2026-08-05 rescan found no
  current `:9292` listener; restart the isolated Steward lane before the next
  live phone/tablet prompt verification.
- Fresh Android tablet live attempt after the latest app rebuild: the native
  tablet app relaunches authenticated against `http://127.0.0.1:9292`
  through `adb reverse`, showing the native Steward workspace and local
  backend label. Evidence:
  `/tmp/agently-rescan/android-relaunch.png`.
- Implemented generic Agently-core tool-surface discovery mode. Pre-model
  tool-surface registry discovery now uses a short non-strict bound while
  strict scheduler discovery and actual tool execution keep their existing
  behavior. This is Agently-owned lifecycle behavior, not Steward, Forge, or
  mobile-app business logic.
- Verified the tool-surface change with:
  `cd /Users/awitas/go/src/github.com/viant/agently-core &&
  go test ./runtime/discovery ./internal/tool/registry ./service/agent
  -run 'TestMergeModeToolSurface|TestWithDiscoveryTimeout_ToolSurfaceUsesShortBoundUnlessStrict|TestListServerTools_CachesTransportFailureForCooldown|TestResolveToolControl_MergesAgentProfileAndRuntimeSelections'
  -count=1`, followed by
  `go test ./runtime/discovery ./internal/tool/registry ./service/agent`.
- Rebuilt local Agently against the local Agently-core replace and restarted
  Steward on isolated port `9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. The Android `Pixel_Tablet`
  relaunched authenticated against `http://127.0.0.1:9292` through
  `adb reverse tcp:9292 tcp:9292`. Evidence:
  `/tmp/agently-rescan/android-after-discovery-fix-relaunch.png`.
- Re-ran `open forecast builder for line 7288336` from the Android tablet.
  Local Agently logs show the optional `creative` discovery timing out under
  the bounded surface timeout, then Forge commands landing successfully:
  `ui.window.open` for canonical `reportBuilder` with
  `parameters.reportBuilderRef=forecastingCubeBuilder`, followed by
  `ui.data.fetch` and `ui.window.setFormData` returning `ok=true`. Reopening
  the conversation showed the assistant transcript response:
  "The Forecasting workspace is open with line 7288336 loaded." Evidence:
  `/tmp/agently-rescan/android-after-discovery-fix-conversation-open.png`.
- Superseded by later 2026-08-05 live sections: hosted Forecasting visual proof
  and UI-originated sends were subsequently captured on Android tablet, Android
  phone, iPad, and iPhone against the isolated `:9292` Steward lane.
- Broader `cd /Users/awitas/go/src/github.com/viant/agently && go test ./...`
  was attempted after the local rebuild. It still fails in unrelated e2e
  paths (`TestTerminalQueryImageAttachment`,
  `TestTerminalQueryJWTInvalidToken`, and
  `TestTerminalQueryCoderRepoAnalysisLiveTranscript`), while the focused
  Agently-core packages for this discovery change pass.

## 2026-08-05 Current Rescan

- Historical process and port scan: at that moment no `xcodebuild`, Gradle, or
  local `agently serve -a :9292` process was running. Later 2026-08-05 live
  sections restarted the isolated lane with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`; inspect the current process
  table before assuming either state.
- Current diff shape remains expected: Agently-core owns generic tool-surface
  discovery, active-turn tool-call coalescing, and SDK stream hydration;
  Agently owns mobile auth/composer/host wiring; Forge owns generic
  report-builder variants and target-aware rendering. No Reporter files were
  touched in this pass.
- Boundary scan: Forge production Android/iOS sources contain no Steward,
  Agently, forecasting, line-id, or workspace-specific identifiers. Agently
  mobile and Agently-core production SDK/service paths contain no Steward tool
  names, forecast-builder window ids, local line ids, or Steward endpoint
  routing logic.
- `git diff --check` passes in Agently-core, Agently, and Forge.
- Verified Agently-core focused packages:
  `cd /Users/awitas/go/src/github.com/viant/agently-core &&
  go test ./service/shared/toolexec ./internal/tool/registry
  ./runtime/discovery ./service/agent -count=1`.
- Verified Agently Android:
  `cd /Users/awitas/go/src/github.com/viant/agently/android &&
  ./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
  --console=plain`. The build passes with existing Forge warning noise only.
- Verified Forge Android:
  `cd /Users/awitas/go/src/github.com/viant/forge/android &&
  ./gradlew :sdk:testDebugUnitTest :sdk:compileDebugKotlin --no-daemon
  --console=plain`.
- Verified Forge iOS:
  `cd /Users/awitas/go/src/github.com/viant/forge &&
  swift test --package-path ios` passes 221 tests.
- Verified Agently iOS:
  `cd /Users/awitas/go/src/github.com/viant/agently &&
  swift test --package-path ios` passes 92 tests.
- Remaining verification: restart the `:9292` Steward lane, rerun
  `open forecast builder for line 7288336` on Android phone/tablet and
  iPhone/iPad, and capture visual hosted-window persistence/replay proof after
  the successful `ui.window.open`/`ui.window.setFormData` path.

## 2026-07-18 Rescan

- The former `mobile_sdk/README.md` and `mobile_sdk-progress/resume.md` were
  absent from this checkout. Earlier plan notes that refer to them are stale.
- The tracked SDK public-surface parity test is green for Android and iOS.
- `agently-core/sdk/ts/src/richContent/parseFences.ts` is the existing generic
  web fence parser. Native Forge fences are currently parsed independently in
  Agently Android and iOS, creating the active duplication.
- Independent Codex architecture review completed. It found three high-priority
  issues: app-owned Forge DSL adaptation, Android/iOS data-mode divergence, and
  app-generated IDs/labels/data-source references. The recommended contract is
  `renderedContent` beside raw transcript content, normalized in agently-core
  and rendered by Forge.
- The first implementation slice moved deterministic native fence segmentation
  from Agently regexes to Forge UI packages. It preserves ordinary malformed
  text and exposes whether a fence is closed, so a streaming or malformed
  payload is never silently treated as valid Forge content.
- Android's former title-length-dependent inline height rule has been removed.
  The temporary fixed transcript cap keeps nested content scrollable until the
  canonical Forge presentation policy is carried in `renderedContent`.
- `sdk/rendered_content.go` now owns deterministic Forge-fence segmentation
  for canonical transcript state. The contract is additive and leaves raw
  `content` intact; malformed or unfinished fences remain ordinary text.
- Verification after this slice: `go test ./sdk -run
  'TestNormalizeRenderedContent|TestMobileSDKPublicSurfacesCoverClientContract|TestCanonical' -count=1`
  passed on 2026-07-18.
- Review follow-up verification passed on 2026-07-18:
  - `go test ./sdk -run 'TestNormalizeRenderedContent|TestReduceHydratesRenderedContentForLiveAssistantPage|TestBuildCanonicalState_ExtractsStandaloneAssistantFinal|TestParity_NormalTurn|TestMobileSDKPublicSurfacesCoverClientContract' -count=1`
  - `npm test` in `sdk/ts` (22 files, 363 tests)
  - `swift test --filter AgentlySDKTests/testRenderedContentDecodesWithoutDiagnostics`
  - `./gradlew compileDebugKotlin --rerun-tasks` in `sdk/android`
  - Forge iOS `MarkdownFenceParserTests` and Forge Android production Kotlin compile.
- The independent Codex review initially found legacy header omission and
  transcript/live divergence; those were corrected. Its follow-up found and
  verified fixes for an iOS `diagnostics` decode failure, TypeScript state
  propagation, and a JSON payload containing embedded triple backticks.
- The Android Forge unit-test source set remains blocked by unrelated existing
  test-source errors (`content` unresolved and missing `initial` arguments),
  although the Forge Android production source compiles successfully.
- Verification: `go test ./sdk -run
  'TestMobileSDKPublicSurfacesCoverClientContract|TestCanonical' -count=1`
  passed; Forge Android production Kotlin compiled from scratch; Forge iOS
  parser tests passed.
- Broader Android unit tests are currently blocked before test execution by
  pre-existing Forge test compilation errors (`content` unresolved and several
  `initial` constructor arguments absent). The Android app is blocked by the
  unrelated missing `HostedWorkspaceEventNotice` symbol. The iOS app package
  is blocked by unrelated macOS-incompatible lookup UI APIs and
  `Color(.systemBackground)` resolution. These must be repaired or isolated
  before the required emulator verification can be claimed.

## 2026-07-18 iOS Forge Extraction

- Moved iOS generic scanner and header parsing to Forge Runtime:
  `forge/ios/Sources/ForgeIOSRuntime/MarkdownFenceParser.swift`.
  The scanner preserves malformed fences and legacy attributes, avoids a raw
  triple-backtick close inside a valid compact JSON payload, and has no regex.
- Added Forge-owned transcript envelope handling in
  `forge/ios/Sources/ForgeIOSRuntime/TranscriptEnvelope.swift`. It supports
  structured and legacy `forge-data`, `replace`/`append`/object `patch`, JSON,
  CSV including quoted multiline cells, and preserves raw markdown when no
  usable `forge-ui` surface follows.
- Added `TranscriptWindowBuilder` in Forge Runtime. It owns generic
  `planner.table`/dashboard adaptation and synthetic summary data sources;
  the Agently compatibility function is now a one-line delegate.
- Added `ForgeRuntime.updateWindowInline` so a streamed Forge surface updates
  metadata in place rather than allocating an unbounded set of windows.
  The Agently SwiftUI host now keeps its runtime in `@State`, fingerprints both
  UI payload and data, and gives unsupported content a terminal message rather
  than a permanent loading state.
- Independent review pass one found malformed-header safety, CSV, stale
  streamed data, app-owned normalization, and no-UI fallback gaps. All were
  fixed and covered by focused tests. Review pass two found SwiftUI runtime
  lifetime, incomplete payload fingerprint, and duplicate CSV-header crash
  risks. Those are fixed as well.
- Final review correction: `forge-data` fences are scoped to the immediately
  following `forge-ui` block. A later UI block receives no old data and the
  builder emits an explicit empty collection for an otherwise unfenced current
  data source. A final independent review reported no findings.
- Verified:
  `cd forge/ios && swift test --filter 'MarkdownFenceParserTests|TranscriptEnvelopeTests'`
  passes 10 envelope tests plus parser coverage on 2026-07-18; `git diff --check`
  passes.
- Forge Android now has the same safe compact-JSON closing behavior in
  `sdk/.../MarkdownFenceParser.kt`; production Kotlin compile passes and its
  independent review reported no findings. Targeted unit execution remains
  blocked by unrelated test-source/Gradle environment failures.

## 2026-07-19 Android Envelope Extraction

- Added Forge-owned Android `TranscriptEnvelope` with typed `forge-data` and
  `forge-ui` payloads, scoped data snapshots, JSON/CSV materialization, and
  raw-markdown preservation for malformed content. The scanner and envelope
  are generic Forge code; there is no Steward or Agently semantic in either.
- Independent reviews found and corrected multiline CSV handling, malformed
  CSV preservation, structured JSON decode safety, whitespace-only markdown,
  blank structured IDs, and the ambiguity between a legacy header ID and a
  JSON row whose own `id` field is blank. Header IDs are now authoritative;
  only headerless fences are treated as structured envelopes.
- Verification: `cd forge/android && ./gradlew :sdk:compileDebugKotlin
  --rerun-tasks` passes on 2026-07-19. Focused unit-test source is present but
  execution remains blocked before tests by the pre-existing Gradle lock/cache
  environment issue and unrelated broader Android test-source errors.
- Next: move `TranscriptWindowBuilder`/block adaptation to Forge Android,
  swap Agently Android to the Forge envelope and builder types, delete its
  parser/materializer/adapters, then run device verification.
- `cd agently/ios && swift test --filter ChatRuntimeTests/testParseTranscriptContentPartsExtractsForgeUIBlocks`
  compiles the changed `ForgeFenceRuntime.swift`, then remains blocked by the
  existing macOS-unavailable `ComposerScreen` lookup APIs and
  `HostedWorkspaceSection` `Color(.systemBackground)` error. This is not
  device/iOS verification.

## 2026-07-19 Android Builder Extraction

- Added Forge-owned Android `TranscriptWindowBuilder` and
  `TranscriptWindowPresentation` in
  `forge/android/sdk/src/main/java/com/viant/forgeandroid/ui`. The builder owns
  generic `planner.table` and dashboard block adaptation, label/key
  normalization, synthetic summary values, data-source declarations, and an
  explicit empty collection for a referenced source with no current data.
- `agently/android/.../ForgeFenceRuntime.kt` was reduced from the old parser,
  CSV materializer, and adapters to transcript composition, placement, and
  hydration. Its metadata helper is a compatibility delegate only.
- Independent review pass one found two real regressions: `forge-data` crossed
  a markdown/malformed-UI boundary, and synthesized/empty sources were not
  hydrated. The envelope now clears parsed state whenever it restores pending
  raw markdown, and the Forge presentation returns the exact normalized store
  that the host hydrates. The second review found no remaining issues.
- Added Forge unit coverage for whitespace-adjacent data/UI, markdown and
  malformed-UI boundaries, synthesized summary data, and explicit empty
  sources. The project-wide test source still fails before these tests execute
  due to pre-existing `content` and `initial` test errors.
- Verification on 2026-07-19:
  - `cd forge/android && ./gradlew :sdk:compileDebugKotlin --rerun-tasks`
    passed.
  - `cd agently/android && ./gradlew :app:compileDebugKotlin --rerun-tasks`
    reached the unchanged unrelated
    `HostedWorkspaceNoticeCard.kt:17` missing `HostedWorkspaceEventNotice`.
  - `git diff --check` passed for the changed Android application source.

## 2026-07-19 iOS Simulator Bring-up

- Booted iPhone 17 Pro and iPad Pro 11-inch (M5) simulators on iOS 26.5.
  `xcodebuild` successfully built `AgentlyApp` for the iPhone simulator with
  `CODE_SIGNING_ALLOWED=NO`, including Forge's new transcript scanner,
  envelope, builder, and runtime update support.
- Installed and launched `com.viant.agently.ios` on both simulators. The iPad
  shows the Steward/localhost workspace selector and the phone starts the
  Steward connection flow, so the current source is installable on both phone
  and tablet targets.
- Live signed-in Steward verification is currently blocked outside the app:
  `https://steward.agently.viantinc.com` resolves but TCP port 443 stalls from
  the host and the simulator's workspace metadata request. Do not record
  conversation visual parity until that endpoint is reachable or the approved
  local Steward service is running. The booted simulators and built app can be
  reused immediately once connectivity returns.

## 2026-07-19 Android Emulator Bring-up

- Booted the configured `Pixel_10_Pro` phone and `Pixel_Tablet` AVDs. Both are
  online through the local Android SDK and both already have
  `com.viant.agently.android` installed.
- Launching the installed app on each device reaches the same loading state
  while it waits for the remote Steward workspace. This matches the unreachable
  endpoint observed on iOS; it is not evidence of an Android-only visual or
  Forge regression. Reinstall the newly built app after the unrelated Android
  app compilation error is resolved, then use these same two devices for the
  signed-in transcript parity pass.

## 2026-07-19 Canonical Native Route and Presentation Policy

- Added generic typed canonical-part adapters to Forge Android and Forge iOS:
  `TranscriptCanonicalPart` and `TranscriptEnvelope.fromCanonical`. They
  preserve source boundaries and make invalid or data-only payloads markdown
  rather than silently materializing a UI. This is a Forge contract, not a
  Steward contract.
- Completed assistant turns now carry the SDK's `renderedContent` into the
  native Forge adapter. Raw markdown parsing remains intentionally limited to
  legacy transcript entries and incomplete streaming entries, where the typed
  final representation does not yet exist.
- Fixed an iOS envelope edge case discovered during the move: pending
  `forge-data` is now flushed when ordinary markdown intervenes, matching the
  Android immediate-adjacency rule.
- Added Forge-owned inline presentation policy on both native platforms.
  Compact form factors receive a 340 dp/pt inline window; regular form factors
  receive 420 dp/pt. The rule is explicit, metadata-aware, and contains no
  title/content/prompt/workspace selector.
- Independent Codex review found one Android hydration defect: a Forge window
  whose data store changed without changing its message key could retain stale
  rows. `LaunchedEffect` now keys on the presentation data store as well as the
  message and metadata. The production Android compile and debug assembly pass
  after that correction.
- Verification on 2026-07-19:
  - Forge iOS focused parser/envelope/presentation suite: 19 tests passed.
  - Forge Android production Kotlin compile passed.
  - Agently Android `:app:compileDebugKotlin` and `:app:assembleDebug` passed.
  - Agently iOS `xcodebuild` for the iPhone simulator passed.
  - `git diff --check` passed for Forge and Agently source changes.
- Remaining test limitations are outside Forge Android: its stale `content`
  imports and missing selection initial state were repaired, and the full SDK
  unit suite passes. Agently Android test sources still reference stale
  composer symbols; Agently iOS package tests use macOS-unavailable composer
  APIs and `Color(.systemBackground)`. The changed production paths compile.
- Live device parity remains blocked only by environment connectivity: the
  Steward hostname resolves but TCP 443 times out from this host. Do not claim
  signed-in conversation parity or manufacture mock transcript evidence.

## 2026-07-19 Current OOB Device Install

- Forced `:app:packageDebug --rerun-tasks` after Gradle retained APK metadata
  without the corresponding file. The task produced
  `app/build/outputs/apk/debug/app-debug.apk`; this was an incremental-output
  artifact issue, not a source or signing failure.
- Installed and launched the current Android OOB debug build through
  `scripts/install-android-oob-debug.sh` on both `Pixel_10_Pro` and
  `Pixel_Tablet`. The script consumes the approved encrypted reference without
  exposing decrypted material. Both apps own the foreground activity and show
  the same blank loading surface while workspace discovery waits on Steward.
- Rebuilt, installed, and launched the current iOS OOB app through
  `scripts/launch-ios-oob-sim.sh` on iPhone 17 Pro and iPad Pro 11-inch (M5).
  The iPhone renders an explicit API connection-timeout screen. The iPad
  renders the workspace chooser with Steward selected; it cannot continue to a
  signed-in transcript while the same endpoint is unreachable.
- A final independent Codex `gpt-5.5` review of the completed-message
  canonical route and Android hydration key reported no findings. It confirmed
  that Android and iOS route completed SDK content through
  `TranscriptEnvelope.fromCanonical`, reserve raw parsing for legacy/streaming
  content, and rehydrate when normalized Android data changes.
- The original local fallback had no listener; the real internal Steward
  workspace is now launched on `localhost:9191`. OOB session minting remains
  unavailable because `idp.viantinc.com:443` times out, so do not claim a
  signed-in parity run yet.

## 2026-07-19 Bounded Auth Metadata Startup

- Starting the real internal Steward workspace initially hung before binding
  `9191`. A Go stack dump identified a generic cause: `service/auth.NewRuntime`
  synchronously requested issuer/JWKS metadata through `http.DefaultClient`,
  which has no timeout.
- Added a generic five-second OAuth metadata client in
  `service/auth/runtime_loader.go`. It is used for both RFC 8414 issuer
  metadata and OpenID discovery, while an explicitly injected
  `oauth2.HTTPClient` remains authoritative. The change does not synthesize a
  JWKS URL, weaken verifier behavior, or mention Steward/Agently host logic.
- Focused and full verification passed:
  `go test ./service/auth -run 'TestOAuthVerifierConfig_|TestOAuthMetadataHTTPClient_' -count=1`
  and `go test ./service/auth`. An independent Codex `gpt-5.5` review reported
  no findings.
- Rebuilt Agently against the current core source and started the real internal
  Steward workspace at `http://127.0.0.1:9191`. It loads the expected Forge
  windows and returns the web UI. This replaces the previous absent-local-server
  blocker.
- The final signed-in prerequisite remains external: the approved OOB flow
  reaches the local workspace but times out contacting `idp.viantinc.com:443`.
  Do not disable auth, mint a synthetic session, or substitute mock evidence.
- Transport proof is complete for all four targets: Android phone and tablet
  reach `10.0.2.2:9191`; iPhone and iPad reach simulator-local `9191`; each
  receives HTTP 200 from the same real Steward workspace. The remaining gap is
  authentication only, not device routing or local server availability.

## 2026-07-19 Native Parameter Parity and Test Recovery

- Repaired stale Android Forge test source imports (`content` became
  `contentOrNull`) and aligned explicit `SignalRegistry.selection` test setup
  with the required single/multi initial state. The full Android Forge SDK unit
  suite now executes and passes: `./gradlew :sdk:testDebugUnitTest` (211 tests).
- Fixed generic Android parameter resolution to project `InputState` into the
  same map shape used by iOS for both legacy and compact parameters. A compact
  parameter named `...` now reads the whole source object rather than a
  nonexistent literal `...` property on Android and iOS.
- Corrected the actual Android REST datasource path, not only the isolated
  resolver: compact `from`/`to` parameter rows are resolved through the generic
  `ParameterResolver` and merged into `input.query`/`input.body`. New Android
  transport coverage verifies compact `:input` values for both GET query and
  POST body requests; the full 211-test suite passes after the change.
- iOS focused resolver coverage passes for both legacy input and combined
  compact spread/multi-select/input behavior:
  `swift test --filter 'ForgeIOSTests.testParameterResolverCompactRowsSupportSpreadAndMultiSelectionArrays|ForgeIOSTests.testParameterResolverResolvesLegacyDataSourceInputAndFilterSources'`.
- Two independent `gpt-5.5` reviews found the compact Android input projection
  gap and the REST-path compact `from` gap. Both are fixed and covered by the
  commands above; `git diff --check` passes. No auth bypass or mock evidence
  was introduced.
- A final narrow review noted that legacy Android `input` paths now resolve
  `InputState.parameters` rather than returning null. This is intentional: the
  existing datasource regression test already specified that behavior, and its
  initial failure exposed the defect. The final Android host compile passes
  against the updated Forge runtime.
- Rechecked `idp.viantinc.com:443` after the fixes. The TCP probe still stalls,
  so OOB session minting and the required signed-in four-device visual parity
  pass remain externally blocked.

## 2026-07-19 OOB Endpoint And Auth Detail Hygiene

- A fresh four-device rescan found that the running Android and iOS apps had
  retained the remote Steward endpoint even though the real local Steward
  workspace was healthy. Reinstalled Android with
  `AGENTLY_ANDROID_BASE_URL=http://10.0.2.2:9191` and iOS with
  `AGENTLY_API_BASE_URL=http://localhost:9191`; both Android emulators and
  both iOS simulators reach the same local server.
- Fixed the generic iOS OOB debug launcher so it passes the app's explicit
  debug-override gate together with its existing endpoint, OOB reference, and
  auto-sign-in arguments. This preserves normal production configuration
  precedence while making the documented OOB launcher functional.
- The real device run exposed an iOS privacy regression: failed OOB sign-in
  displayed the raw upstream OAuth transport error, including request details.
  `AuthRuntime` now logs those diagnostics privately and presents concise,
  operation-specific retry guidance instead. A focused regression test asserts
  failed OOB sign-in cannot expose a secret reference or URL in visible state.
- Rebuilt and reinstalled the patched iOS app on iPhone 17 Pro and iPad Pro
  11-inch (M5). Both visibly show the local endpoint and the sanitized message;
  no OAuth request detail is shown. Android phone and tablet likewise reach
  local Steward and stop at the expected OAuth-required state.
- Verification: `bash -n scripts/launch-ios-oob-sim.sh`, `git diff --check`,
  and iOS simulator app builds pass. The focused Xcode test cannot run because
  neither available Xcode scheme has a test action; `swift test` remains blocked
  by the pre-existing macOS-unavailable composer APIs and
  `Color(.systemBackground)`. The changed code is compiled by the successful
  iOS app build.
- OOB sign-in itself still fails only because the reachable local workspace
  cannot contact the upstream IdP over TCP 443. Do not bypass it; signed-in
  transcript/dashboard parity remains the final required proof.

## 2026-07-19 OOB Single-Flight Pairing Follow-Up

- The first privacy-safe iOS device pass exposed a second interaction defect:
  automatic OOB login was being started both during application bootstrap and
  by `AuthRequiredScreen`. The first failed request surfaced the safe error,
  while the duplicate still left the visible sign-in control in a busy state.
- Bootstrap is now the sole automatic OOB owner. `AuthRuntime.beginOOBLogin`
  also rejects a concurrent request, so manual and automatic sign-in use the
  same single-flight guard. Regression coverage verifies both the guard and
  that visible OOB failures contain neither a secret reference nor URL.
- Fresh iPhone and iPad screenshots show `http://localhost:9191`, the concise
  retry message, and an enabled `Sign In` button after the IdP timeout. The
  auth surface therefore no longer exposes transport details or strands the
  user in a spinner. `xcodebuild ... AgentlyApp ... build` and focused static
  privacy checks pass; the existing Xcode/Swift test-runner limitation remains.
- The Android phone rescan found its shared auth card was using a desktop-like
  72% width and had become an internally scrolling, near-full-height surface.
  The generic Compose shell now fills compact width, retains the existing
  760dp readable maximum on tablet, and wraps its actual content height.
  Reinstalling the Android phone confirms the internal scrollbar and empty
  stretched region are gone.

## 2026-07-19 OAuth Callback Lease Follow-Up

- The iOS web OAuth path now holds one generic submission lease through URL
  initiation, browser authentication, and callback exchange. Direct callback
  handling uses the same guard, so it cannot race with an active sign-in.
- A rejected mobile callback configuration now presents generic administrator
  guidance instead of a callback URI or workspace URL. Protocol-level tests
  cover that visible error and concurrent callback rejection.
- The iPhone was rebuilt and launched through the encrypted-reference OOB
  script against `http://localhost:9191`. It reached real local Steward and,
  after the real upstream timeout, settled with the sanitized retry state and
  an enabled `Sign In` action. This is transport/privacy evidence only, not
  signed-in transcript parity.
- The iPhone simulator app build passes. The requested `gpt-5.5` Codex review
  was invoked, including the built-in uncommitted review mode, but this local
  CLI emitted initialization and inspection trace without a final review
  artifact. Do not count that as a clean independent review; repeat it when
  the local reviewer output path is healthy.

## 2026-07-19 Live Android Steward Parity

- VPN restoration enabled real OOB authentication against the local Steward
  workspace. The canonical live conversation is
  `90ee46ed-bb27-483d-b986-402f530b6d48` using the required order `2664518`
  troubleshooting prompt; the web UI and both Android targets load it from
  `http://127.0.0.1:9191` through ADB reverse forwarding.
- Fresh Android phone evidence confirms the real transcript and Forge dashboard
  render without an execution-details control. The phone dashboard scrolls and
  no longer exposes raw Markdown tokens or upstream request details.
- Fresh Android tablet evidence confirms the same transcript and dashboard.
  Execution details are tablet-only and are now a real mode switch: selecting
  the tab shows `Execution` and `Page 1` instead of appending the inspector
  below the transcript. The phone keeps no execution-details entry point.
- Auth entry is intentionally reduced to `This workspace requires
  authorization.` with a `Sign in` action on Android and iOS. No OAuth,
  endpoint, session, token, or error detail is shown in the normal auth state.
- Generic Forge fixes validated in focused Go/Kotlin/Swift suites: dashboard
  report prose uses the existing Markdown renderer; chart errors expose only a
  safe status; iOS and Android agree on legacy fence header ownership and
  camel-case synthetic data-source identifiers. Canonical Agently SDK content
  now retains header-declared CSV as a JSON string payload
  (`go test ./sdk -run 'TestNormalizeRenderedContent_(RecognizesLegacyForgeDataHeader|RecognizesLegacyCSVForgeDataHeader)$' -count=1`).
- Remaining acceptance work: install and navigate the current iPhone/iPad
  builds to the same signed-in conversation, inspect the exact Forge surface
  and scroll behavior, then complete the final cross-platform audit. Do not
  claim four-target parity until that evidence exists.

## 2026-07-19 Quiet Authorization Surface

- Android and iOS now render the same normal authorization surface: `This
  workspace requires authorization.` followed only by `Sign in`. The visible
  surface no longer branches into connection-error copy, exposes provider or
  OOB/session/settings controls, or displays a sign-in progress label.
- The sign-in action still selects the existing saved-OOB or browser OAuth
  flow internally; failures remain available to the runtime for recovery and
  logging, but are intentionally not user-facing on this screen.
- Verification: `./gradlew :app:compileDebugKotlin --no-daemon --console=plain`
  and `xcodebuild -project AgentlyApp.xcodeproj -scheme AgentlyApp -sdk
  iphonesimulator -configuration Debug build CODE_SIGNING_ALLOWED=NO` pass.
  The Android unit-test compile remains blocked by unrelated existing
  `ComposerScreenTest` unresolved symbols; this edit's production compile is
  clean. A focused independent Codex review has been invoked and is pending
  its local output artifact.

## 2026-07-19 Authorization Workspace Switcher And OOB Separation

- The normal unauthenticated state now keeps the quiet contract while adding a
  compact `Workspace settings` gear next to `This workspace requires
  authorization.`. The only primary action remains `Sign in`; it always starts
  browser OAuth. The gear opens the already canonical workspace endpoint
  settings rather than exposing an endpoint, token, provider, or diagnostic on
  the auth card.
- Android phone evidence was captured from a fresh local-data reset against
  local Steward: the rendered card contains exactly the authorization message,
  gear, and `Sign in`. Opening the gear shows the existing `Workspace Endpoint`
  picker with `Steward`, `Localhost 9191`, and `Android Host 9191` choices.
  The developer endpoint and OOB reference remain confined to debug settings.
- The rebuilt iPhone 17 Pro was also installed and visually inspected. Its
  current native screen presents the same authorization message, settings gear,
  and `Sign in` action, confirming the workspace-switch affordance is present
  on iOS without reintroducing auth diagnostics.
- OOB is now separate on both Android and iOS. It is visible only with the
  explicit developer-auth gate and a configured OOB reference, and is labeled
  `Developer OOB sign-in`; it no longer replaces the ordinary OAuth action.
  Android also removed the retired saved-IDP username/password dialog, debug
  fields, bootstrap inputs, and persisted values. Legacy encrypted preference
  keys are removed on load/save.
- Android OAuth WebView navigation now reloads only when its initial auth URL
  changes, not while the IdP follows redirects. Callback handling verifies the
  configured callback scheme, host, and path before reading the `code` and
  `state`; no credential injection or broad IdP-page heuristic remains.
- Verification: Android `:app:compileDebugKotlin` and `:app:assembleDebug`
  succeed. The iOS app build succeeds against simulator ID
  `238A6C82-3508-4785-9BAE-CF9413139636`. An independent `gpt-5.5` review
  found no functional regression after the fixes; its only copy suggestion was
  `OAuth Sign in`, which was intentionally not applied because the requested
  quiet copy and iOS parity use `Sign in`. The existing Android unit-test
  compilation remains blocked by unrelated unresolved composer-test symbols.

## 2026-07-23 Native Inline Report Compiler

- Added Forge-owned iOS and Android compilers for canonical inline report
  assemblies. Agently remains the host and transport layer; no Steward report
  semantics or workspace-specific field mappings were added to either SDK.
- The compiler lowers `report-document-v1` into the existing native
  `dashboard.reportRuntime`, preserves source/layout/block identity, hydrates
  materialized datasets, resolves KPI values, and extracts unresolved
  workspace dataset requests for the host.
- Focused Swift and Kotlin tests cover all 17 supported native primitive kinds:
  tab group, section, KPI, table, chart, geo, collection, timeline, markdown,
  filter bar, refinement bar, badges, composite, stepper, info panel, callout,
  and kanban. Canonical CSV strings use the existing Forge CSV materializers,
  including quoted fields and numeric coercion. Both suites pass 3 tests with
  0 failures; `git diff --check` is clean.
- Agently Android produces `app-debug.apk`. Agently iOS builds, installs, and
  launches on the iPhone 17 Pro simulator against local Steward.
- Persisted transcript responses now normalize report fences at the Agently
  Core HTTP boundary before workspace datasource authorization. A focused
  handler regression proves an unhydrated backend message is returned with one
  committed canonical report assembly.
- Live conversation `889424ba-c7c6-41b0-9f3e-d48369053a19` renders
  `Audience forecast review` through the native Forge report runtime. The
  progressive source atoms are hidden once their canonical assembly is
  available; focused iOS and Android transcript-envelope tests cover the same
  behavior. Android device-level visual comparison remains open.

## 2026-08-04 Latest Mobile Rescan

- Rescanned current `agently-core`, `agently`, and `forge` working trees. The
  canonical `renderedContent` source remains in Agently Core; native Agently
  hosts still project Forge-owned transcript envelopes instead of parsing
  Steward-specific payloads.
- Local Steward is reachable on `127.0.0.1:9191` and advertises the OAuth/BFF
  provider. IdP TCP/TLS reachability is no longer the July blocker.
- iOS simulator launch against `http://localhost:9191` still rendered a
  connection-error screen, but relaunching the same app against
  `http://127.0.0.1:9191` reached an active Steward conversation. Keep iOS
  simulator verification on numeric loopback unless the hostname mapping is
  explicitly fixed.
- Android composer now disables keyboard auto-capitalization for both compact
  and new-conversation prompt fields. Compact composer line capacity again
  expands from 2 up to 6 lines so long selected prompts are visible instead of
  being trapped in a two-line field.
- Android OAuth callback matching now uses `java.net.URI` for pure callback
  identity checks, so JVM unit tests validate the mobile callback path without
  depending on Android runtime stubs.
- Verification passed:
  `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon --console=plain`
  from `agently/android`.
- Current live-device gap: `adb devices -l` reports no attached Android
  emulator/device. iPhone 17 simulator is booted; iPad/tablet and Android
  visual parity remain pending live runs.

## 2026-08-05 Android Tablet Forecasting Rescan

- Local Steward is running on `127.0.0.1:9292` for this pairing lane; do not
  reuse `:9191` while the reporting lane owns it. Android tablet access uses
  `adb reverse tcp:9292 tcp:9292` and app base URL `http://127.0.0.1:9292`.
  The verified server lane was started with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`.
- Rescanned `agently-core`, `agently`, and `forge`. Reporter was not touched.
- Agently Core hosted-workspace restore now treats modern `ui/window/open`
  tool steps as hosted workspace open events across Android, iOS, and TS SDKs,
  and applies later `ui/window/setFormData` responses using the authoritative
  returned `windowForm`.
- Android tablet live proof now shows the Forecasting hosted workspace can open
  from `open forecast builder for line 7288336`. The earlier missing-hosted-pane
  issue was the SDK missing the modern `ui/window/open` restore path.
- Forge native report-builder rendering now decodes generic
  `reportBuilder.title` / `subtitle`, renders the resolved builder header
  instead of the generic container title, and skips the extra dashboard wrapper
  around `dashboard.reportBuilder` so mobile does not render a card inside a
  card. This is generic Forge behavior, not Steward-specific branching.
- Clean Android tablet evidence after the final wrapper removal is captured at
  `/tmp/agently-rescan/android-forecasting-local-mcp-final-20260805.png`. The
  fresh prompt `open forecast builder for line 7288336` opens the Forecasting
  hosted pane, shows the resolved `Forecast Inventory` builder, and no longer
  renders the generic outer `Reports` wrapper.
- Agently mobile hosted-workspace labels now split camelCase window keys
  generically on Android and iOS, so fallback labels render as `Report Builder`
  instead of `Reportbuilder` while preserving acronym-style tokens. Focused
  Android and iOS presentation tests cover `reportBuilder` and
  `forecastingCubeBuilder`.
- Tool-router auto-selected bundles now narrow broad agent default bundles for
  that turn instead of merging with every agent-default bundle. Explicit
  caller/workspace metadata bundle selections still merge with agent defaults
  for backward compatibility. Auto-selected bundles are not persisted as
  conversation metadata, so each turn can reselect the minimal surface instead
  of making a previous auto-route sticky. This is generic Agently behavior and
  avoids probing unrelated optional MCP servers on UI/forecasting turns.
- Live Android rerun exposed one more automatic path: intake/planner-appended
  bundles also need to be marked as runtime-selected when they start from no
  explicit or metadata bundle selection. After that generic fix, a fresh
  Android tablet prompt on `:9292` opened Forecasting without any unrelated
  `creative` or `operation` MCP discovery warning. Server log evidence for
  conversation `5e91aaef-b441-4784-8523-96a78208b91f` shows only
  `ui.window.open`, `ui.data.fetch`, and `ui.window.setFormData` for
  `reportBuilderRef=forecastingCubeBuilder`. Visual evidence is captured at
  `/tmp/agently-rescan/android-forecasting-rerun-auto-bundle-final-after-setform-20260805.png`.
- Verification passed:
  `go test ./runtime/discovery ./internal/tool/registry ./service/agent`,
  Android Agently Core `WorkspaceRestoreTest`, TS `workspaceRestore.test.ts`,
  iOS `AgentlySDKTests/testHostedWorkspaceRestoreUsesWindowOpenPayloads`, Forge
  Android `ReportBuilderStateStorageTest`, Forge iOS
  `ForgeIOSTests/testReportBuilderVariantResolutionUsesWindowFormRef`, Android
  app `HostedWorkspacePresentationTest`, iOS app
  `HostedWorkspacePresentationTests`, Agently Core
  `TestResolveToolControl_AutoSelectedRuntimeBundlesNarrowAgentDefaults`,
  `TestApplyPlannerOutput_MarksNewRuntimeBundlesAutoSelected`,
  `TestApplyPlannerOutput_PreservesExplicitBundleMergeSemantics`,
  `TestEnsureConversation_DoesNotPersistAutoSelectedToolBundles`, Android app
  reinstall via `scripts/install-android-oob-debug.sh`, Android live rerun on
  Pixel Tablet, and `git diff --check` for `agently`, `agently-core`, and
  `forge`.

## 2026-08-05 iPhone Line-Prefill Rescan

- Rescan found the iPhone window-open path working for
  `open forecast builder for line 7288336`: the native phone opened the
  Forecasting hosted pane via `reportBuilderRef=forecastingCubeBuilder`.
- The same transcript exposed a remaining correctness bug: the executable
  intake rule for the line builder family still wrote `AudienceId` and
  `audienceIds` alongside `AdLineId`. That caused Steward to activate
  `forecast-targeting` as `Builder-prefill for AudienceId 7288336` and apply
  only `prefill.scope.audienceIds`, not the line targeting-derived filter
  fields.
- Patched Steward `intake/activation_rules.yaml` so line builder-open and
  no-run variants preserve only `AdLineId`. Patched Agently Core
  `service/agent/intake_query_test.go` so the regression asserts line ids do
  not leak into audience scope.
- Verification passed:
  `go test ./service/agent -run 'OpenForecastBuilderForLine'` in
  `agently-core`, Steward Forecasting builder JS tests
  (`forecastingCubeBuilder.predicates.test.mjs`,
  `forecastingCubeBuilder.test.js`, and
  `metricReportBuilder.windowParams.test.mjs`), `go build -o agently/agently
  ./agently` in `agently`, and `git diff --check` for `agently-core` and
  Steward.
- The local `:9292` server was rebuilt/restarted with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`, and OOB login succeeded.
  A clean iPhone rerun reached local Steward MCP with no remote discovery
  timeout.
- A second stale prompt instruction was patched in
  `agents/steward/prompt/parts/routing.md`, `system.tmpl`, and
  `instruction.tmpl`: line builder-prefill now tells Steward to call
  `steward-AdTargetingProfile` with `AdLineId`, not to convert the line id to
  `AudienceId` before reading targeting.
- Remaining gap after the prompt fix: the clean iPhone transcript now activates
  `forecast-targeting` with `Builder-prefill for AdLineId 7288336`, but the
  model still skips the actual `steward-AdTargetingProfile` call and applies a
  scope-only `ui/window:setFormData`. Persisted iOS restore state for
  conversation `cc52a862-6070-4990-98c5-286052e4d67e` still only has
  `prefill.scope.audienceIds:[7288336]` and no normalized include/exclude
  filter fields. The next fix should make this path deterministic enough that
  `steward-AdTargetingProfile` is executed before Forecasting form mutation.
- iPhone also still shows a `Streaming response` banner after the API-driven
  turn has completed. This may be an artifact of driving the query by host API
  while the phone only hosts the UI bridge, but it needs a device-originated
  send verification before closing the active-turn lifecycle issue.

## 2026-08-05 Steward AdTargetingProfile Line Alias

- Rescan showed the previous gap was partly below mobile/Forge: local Steward
  MCP on `:5002` did not expose `steward:AdTargetingProfile`, even though the
  Steward workspace bundles and Forecasting skills referenced it. Synced the
  canonical Datly `steward/metadata/ad_profile` route family into the local
  dev route tree and refreshed the local `paths.yaml` index.
- Added a first-class `AdLineId` input to `AdTargetingProfile` at the Steward
  metadata surface. It is exposed as `line_id` and filters the same line table
  key (`au.ID`) without forcing the agent to call the request an `AudienceId`.
  The live `/v1/tools` schema now lists `AdLineId`, `AdOrderId`, `AudienceId`,
  and `CampaignId` for `steward:AdTargetingProfile`.
- Fresh phone-context rerun on `:9292` for
  `open forecast builder for line 7288336` stored the tool request
  `{"AdLineId":[7288336],"timeoutMs":600000}` and Datly audit logged
  `GET /v1/api/steward/metadata/ad_profile?line_id=7288336`. This fixes the
  line-vs-audience leakage in the active builder-prefill turn.
- Full filter prefill is still blocked locally because the profile route's
  auth/user-context dependency cannot connect to MySQL at `127.0.0.1:3307`.
  Datly returns `Internal Server Error` before it can return the targeting
  profile. The next verification step is to start or point the local `ci_ads`
  MySQL dependency correctly, then rerun the same phone prompt and confirm
  normalized include/exclude filters populate before `ui/window:setFormData`.
- Verification passed: Steward `forecast-targeting.contract.test.mjs`,
  live `:9292` OOB auth, live `/v1/tools` schema check, live phone-context
  prompt request inspection, and `git diff --check` in Steward, Steward
  workspace, and Agently Core. Focused Steward Go metadata test is currently
  blocked by an unrelated compile error in `pkg/steward/acl/auth/handler.go`
  (`AccountId` string vs int).

## 2026-08-05 Forecasting Window Identity Rescan

- Rescanned the latest local state. Docker, `mysql_dev`, and `steward_aero`
  are running; MySQL now listens on `127.0.0.1:3307`, Datly MCP is on `:5002`,
  and the local Agently lane is `:9292`.
- The local MySQL dependency is reachable, but the `ci_ads` seed is empty for
  this verification case: `CI_AUDIENCE`, `CI_AD_ORDER`, `CI_CONTACTS`,
  `CI_ACCOUNT`, `CI_ADVERTISER`, and `CI_AGENCY` all report zero rows. The
  fresh OOB session authenticates as `awitas`, but `steward/AdTargetingProfile`
  now fails with `403 user access denied` because the user-context query has no
  local `CI_CONTACTS` row to authorize against.
- Fresh phone-context prompt on the rebuilt `:9292` lane, conversation
  `identityfix-59312a8a-ea98-4b8f-afab-fc29cd09166a`, confirms the agent calls
  `steward/AdTargetingProfile` with exactly
  `{"AdLineId":[7288336],"timeoutMs":600000}`. The remaining full-filter
  prefill blocker is therefore local Steward data/auth, not line-vs-audience
  leakage.
- Found and fixed a separate Agently Core hosted-window identity bug for
  aliased Forge views. `forecastingCubeBuilder` intentionally reuses the
  generic `reportBuilder` renderer via `windowKey: reportBuilder` and
  `reportBuilderRef: forecastingCubeBuilder`, but `ui/view:open` was also using
  `windowKey` as the hosted window identity. That returned
  `reportBuilder__<conversation>` and made subsequent list/setFormData calls
  look like they targeted the wrong window.
- Patched `protocol/tool/service/ui/view/service.go` so a view's model-facing
  `id` is the hosted window identity when present, while `windowKey` remains
  the renderer key. Added
  `TestComputeWindowIDUsesViewIDForAliasedHostedViewIdentity`.
- Verification passed:
  `go test ./protocol/tool/service/ui/view -run 'TestComputeWindowID|TestOpenReturnsWindowIdVisibleToWindowList|TestOpenCanonicalizesReportStarterAcrossCommandOutputAndEvent'`,
  rebuilt `/Users/awitas/go/src/github.com/viant/agently/agently/agently`,
  restarted `:9292`, OOB-authenticated, reran the same phone prompt, and
  decoded the persisted `ui/view:open` response. The response now returns
  `windowId:"forecastingCubeBuilder__identityfix-59312a8a-ea98-4b8f-afab-fc29cd09166a"`
  with `windowKey:"reportBuilder"` and
  `parameters.reportBuilderRef:"forecastingCubeBuilder"`, which is the intended
  separation.

## 2026-08-05 Forecasting Prefill Verification Rescan

- Seeded the local `mysql_dev` `ci_ads` database with a minimal verification
  graph for OOB user `34012`, account `900001`, advertiser `900001`, campaign
  `900002`, order `2664518`, and line/audience `7288336`. The line target is:
  `location:["US"] AND location.postalcode.list:["70731"] AND ad.pmp.deal.id:[...]`.
  The seed is local fixture data only; no production route code was changed for
  the fixture.
- Restarted local Steward Datly MCP on `:5002` and Agently on isolated `:9292`
  with `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. The detached Agently
  process still exits silently in this environment, so the successful
  verification used foreground service sessions.
- Reran the exact phone-context prompt through the CLI:
  `open forecast builder for line 7288336`, with mobile OAuth scope
  `openid profile email ROLE_STEWARD_MOBILE` and UI client
  `ios-ui-8D189CF7-1E3D-4F32-AEB2-843EBD0F0897`.
- Clean conversation `bf0a8c7d-a629-48fc-9d61-59fb6f5658ea` completed. The
  stored `steward/AdTargetingProfile` request is
  `{"AdLineId":[7288336],"Limit":1,"timeoutMs":600000}` and the tool completed.
  Datly read user auth, audience, campaign, order, postal-list, and all 18 PMP
  deal rows successfully.
- The stored `ui/view/open` response uses the intended split identity:
  `windowId=forecastingCubeBuilder__bf0a8c7d-a629-48fc-9d61-59fb6f5658ea`,
  `windowKey=reportBuilder`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`.
- The stored `ui/window:setFormData` request contains normalized targeting
  prefill values:
  `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, and
  `includeDealsPmp:[64512,66016,76060,76084,76105,76162,76711,89531,90473,90476,90482,98075,143925,143934,144156,146708,148790,149114]`.
  The final assistant message says: `The Forecasting workspace is open and
  prefilled for line 7288336 with its country, postal-list, and PMP deal
  targeting.`
- Verification passed: focused Agently Core hosted-window Go tests, focused
  Steward auth/metadata Go tests, Forge Android SDK compile/unit tests,
  `git diff --check` in Agently Core, Steward, and Forge, local OOB auth with
  mobile scope, and live phone-context Forecasting prefill payload inspection.
- iPhone visual proof: launched the app on booted iPhone 17 simulator
  `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` against
  `http://127.0.0.1:9292` with OOB auto sign-in. Screenshot
  `/tmp/agently-rescan/ios-iphone-local-9292-after-launch-20260805-050028.png`
  shows the native phone hosted pane rendering the verified
  `bf0a8c7d-a629-48fc-9d61-59fb6f5658ea` Forecasting builder conversation.
- iPad visual proof: launched the app on booted iPad Pro 11-inch simulator
  `B2AA0D68-7312-4CC9-85B8-0544341A942D` against
  `http://127.0.0.1:9292` with OOB auto sign-in and
  `--activeConversationID=bf0a8c7d-a629-48fc-9d61-59fb6f5658ea`. Screenshot
  `/tmp/agently-rescan/ios-ipad-local-9292-forecast-builder-bf0a8c7d-20260805-050147.png`
  shows the native tablet hosted pane resolving the aliased
  `reportBuilder` surface as Forecasting, with chart data rendered and without
  the generic default Reports shell.
- Detached serving rescan: Datly and Agently stayed alive during the shell
  command, but both disappeared after the command returned in this Codex
  execution environment. That points to descendant process cleanup by the tool
  wrapper rather than an Agently crash. Foreground service sessions remain
  stable for verification.
- Remaining work: perform an actual UI-originated send from iPhone/iPad
  against `:9292`. The current project has no UI-test target, `simctl` can
  screenshot but not tap/type, and macOS UI scripting is not reliable in this
  session. Do not count the iOS send gate complete until either manual
  simulator input is captured or a proper accessibility/UI-test harness drives
  the composer and Send button.

## 2026-08-05 Latest Code Rescan

- Rescanned Agently, Agently Core, Forge, and Steward after the latest mobile
  changes. `git diff --check` passes in all four repos.
- Agently iOS now has a generated `AgentlyAppUITests` target and
  `AgentlyAppLiveUITests` scheme. The composer editor and send button expose
  stable generic accessibility identifiers:
  `agently-composer-editor` and `agently-composer-send`.
- Verification passed:
  `go test ./internal/tool/registry ./runtime/discovery ./protocol/tool/service/ui/view ./service/agent`
  in Agently Core;
  Steward report-builder/forecasting contract checks;
  Forge iOS `swift test` with 219 XCTest cases;
  Android `:forge-sdk:compileDebugKotlin :app:testDebugUnitTest`; and
  iOS `AgentlyApp` simulator build plus `AgentlyAppLiveUITests`
  `build-for-testing`.
- The previous Android Forge compile blockers are resolved in the current tree:
  `formatDashboardValue` is present in Forge Android runtime and
  `FormRenderer.kt` no longer imports Compose's internal `weight`.
- Remaining work: run the live `AgentlyAppLiveUITests` UI-originated send
  against foreground Datly/Agently services on `:9292`, then inspect the new
  conversation DB/tool payload for `open forecast builder for line 7288336`.
  Until that run is captured, the UI-originated send gate remains open even
  though the harness now compiles.

## 2026-08-05 Native UI-Originated Forecasting Verification

- Reran the live iOS UI-originated prompt through
  `AgentlyAppLiveUITests` against foreground Agently `:9292` and local Steward
  Datly MCP `:5002`. The test launches with OOB auto sign-in and an isolated
  `--uiBridgeClientID=ios-ui-test-<UUID>` so queued Forge commands are tied to
  the current native app instance.
- iPad Pro 11-inch simulator
  `B2AA0D68-7312-4CC9-85B8-0544341A942D` passed. Conversation
  `0d9dbcf9-064f-42bf-bc29-a9b7c324bc0e` succeeded from the native composer.
  Stored tool evidence:
  `steward/AdTargetingProfile` request `{"AdLineId":[7288336],"timeoutMs":600000}`;
  `ui/view/open` returned
  `windowId=forecastingCubeBuilder__0d9dbcf9-064f-42bf-bc29-a9b7c324bc0e`,
  `windowKey=reportBuilder`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`; and
  `ui/window/setFormData` applied `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, and all 18 `includeDealsPmp` values.
  Screenshot:
  `/tmp/agently-live-ui/ipad-live-ui-run5-20260805-0539.png`.
- iPhone 17 simulator `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` passed after the
  UI harness learned the generic phone stack navigation path: when the app
  restores into a hosted detail view, it walks back until the generic
  `agently-new-chat` action is visible. Conversation
  `74b86d2c-4346-4869-ab3f-daca53036d14` succeeded from the native composer.
  Stored tool evidence:
  `steward/AdTargetingProfile` request
  `{"AdLineId":[7288336],"Limit":1,"timeoutMs":600000}`;
  `steward/ForecastingTargetingConvert` returned `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, and all 18 `includeDealsPmp` values;
  `ui/view/open` returned
  `windowId=forecastingCubeBuilder__74b86d2c-4346-4869-ab3f-daca53036d14`,
  `windowKey=reportBuilder`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`; and
  `ui/window/setFormData` completed with `ok:true` for the same normalized
  prefill. Screenshot:
  `/tmp/agently-live-ui/iphone-live-ui-run3-20260805-0548.png`.
- The iPad run recorded duplicate identical `ui/window/setFormData` completions
  for the same client/window/value payload; the final state is correct and
  idempotent. Follow-up on 2026-08-05 traced this to parallel identical tool
  calls reaching the registry before the short recent-result cache was written.
  Agently-core now coalesces in-flight identical unprotected calls in the same
  existing memoization path, so this should no longer produce duplicate
  provider/UI commands after rebuild.
- Current status: the `open forecast builder for line 7288336` native send gate
  is now closed for iPad and iPhone on the local `:9292` Steward lane. Android
  tablet proof remains the earlier completed native visual/backend run on the
  same lane.

## 2026-08-05 Android Tablet UI-Originated Forecasting Verification

- Pixel Tablet AVD `emulator-5554` was attached. The emulator gateway route
  reported `Network is unreachable` for both `10.0.2.2:9292` and
  `10.0.3.2:9292`, so this local verification used the standard reverse tunnel:
  `adb reverse tcp:9292 tcp:9292` with app endpoint `http://localhost:9292`.
- Cleared only the Android app data, reinstalled the debug build with
  `AGENTLY_ANDROID_BASE_URL=http://localhost:9292` and bootstrap OOB, selected
  the `Localhost 9292` workspace on the first-run picker, and verified the app
  reached the authenticated conversation list.
- Updated `scripts/android-semantic-compose-replay.sh` as a generic test helper
  improvement: semantic lookup now matches either `content-desc` or visible
  `text`, because the tablet composer exposed `Message`/`Send` as text in the
  UIAutomator tree. The helper self-test passes.
- Sent `open forecast builder for line 7288336` from the Android tablet
  composer through the semantic replay helper. It verified visible
  `Forecasting` after the send. Screenshot:
  `/tmp/agently-live-ui/android-forecasting-live-replay-20260805.png`.
- Fresh Android conversation `b56f0685-04e6-46bf-8093-44e39655690d`
  succeeded. Payload proof:
  `steward/AdTargetingProfile` request
  `{"AdLineId":[7288336],"Limit":10,"timeoutMs":600000}`;
  `steward/ForecastingTargetingConvert` returned `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, and all 18 `includeDealsPmp` values;
  `ui/view/open` returned
  `windowId=forecastingCubeBuilder__b56f0685-04e6-46bf-8093-44e39655690d`,
  `windowKey=reportBuilder`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`; and
  `ui/window:setFormData` completed `ok:true` with the same normalized prefill.
- Current status: Forecasting prefill is now proven from native-originated
  sends on iPad, iPhone, and Android tablet against the local `:9292` Steward
  lane.

## 2026-08-05 Android Phone UI-Originated Forecasting Verification

- Booted Pixel 10 Pro AVD `emulator-5556` alongside the tablet emulator. The
  phone used the same local verification route:
  `adb reverse tcp:9292 tcp:9292` and
  `AGENTLY_ANDROID_BASE_URL=http://localhost:9292`.
- Cleared only the phone app data, installed the debug OOB build, selected
  `Localhost 9292` on the first-run workspace picker, and verified the phone
  reached the authenticated new-conversation surface.
- Sent `open forecast builder for line 7288336` from the Android phone composer
  through the semantic replay helper using the visible `Ask anything` and
  `Send` labels. It verified visible `Forecasting` after the send. Screenshot:
  `/tmp/agently-live-ui/android-phone-forecasting-live-replay-20260805.png`.
- Fresh Android phone conversation
  `0cc9d393-2d2f-45de-b4fe-abed975d06a7` succeeded. Payload proof:
  `steward/AdTargetingProfile` request
  `{"AdLineId":[7288336],"Limit":10,"timeoutMs":600000}`;
  `steward/ForecastingTargetingConvert` returned `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, and all 18 `includeDealsPmp` values;
  `ui/view/open` returned
  `windowId=forecastingCubeBuilder__0cc9d393-2d2f-45de-b4fe-abed975d06a7`,
  `windowKey=reportBuilder`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`; and
  `ui/window:setFormData` completed `ok:true` with the same normalized prefill.
- The Android phone run recorded duplicate identical `ui/window:setFormData`
  completions for the same client/window/value payload, matching the earlier
  iPad replay noise observation. Final state is correct and idempotent. The
  root cause is now addressed generically in Agently-core registry
  in-flight recent-call coalescing; rerun the four-device Forecasting proof
  after rebuilding the local Agently binary.
- Current status: Forecasting prefill is now proven from native-originated
  sends on iPad, iPhone, Android tablet, and Android phone against the local
  `:9292` Steward lane.

## 2026-08-05 Tool Call Coalescing Follow-Up

- Root cause for duplicate `ui/window:setFormData` completions on iPad and
  Android phone: the registry had a short per-conversation recent-result cache,
  but two identical tool calls emitted in the same assistant response could run
  concurrently before the first call populated that cache.
- Fixed in Agently-core, generically, by adding an in-flight coalescing map to
  the same recent-call memoization path. This is not Steward-specific, does not
  inspect window keys or prompt text, and does not move behavior into Forge or
  native apps.
- The recent-call key now includes the selector component as well as user,
  canonical tool name, and stable argument JSON, avoiding selector/no-selector
  collisions while preserving the existing short-TTL behavior.
- Verified:
  `go test ./internal/tool/registry -run
  'TestRegistryRecentResultsCoalescesConcurrentUnprotectedCalls|TestRegistryExecutionProtectionAppliesSameScopeAcrossTurnKinds|TestRegistryExecutionProtectionBypassesRecentResults|TestRegistryExecutionProtectionConcurrentDuplicateSuppression'
  -count=1` passes.
- Verified:
  `go test ./internal/tool/registry -count=1` and
  `go test ./runtime/discovery ./service/agent -count=1` pass.
- Rebuilt the local Agently binary against the local Agently-core replace,
  restarted Steward on `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`, and reran
  `open forecast builder for line 7288336` from the Android Pixel Tablet app
  through `scripts/android-semantic-compose-replay.sh`.
- Fresh Android tablet conversation
  `6f05b293-7998-427c-aacd-399f4bab2e98` verified visible Forecasting and
  recorded exactly one `ui/window/setFormData` tool row at sequence 12. Server
  logs also show exactly one `ui.window.setFormData` UI command for
  `android-ui-8009ecb6-b543-4cdc-8e7f-8ab05c8bb118`.
- Evidence screenshot:
  `/tmp/agently-live-ui/android-tablet-after-coalescing-20260805.png`.
- Fresh iPad conversation `3ddd7bfe-5324-40b7-9eb8-aea769df7419` was sent
  from `AgentlyAppLiveUITests` after the same rebuild. It verified visible
  Forecasting and recorded exactly one `ui/window/setFormData` tool row at
  sequence 13 for the per-run `ios-ui-test-*` bridge client.
- Evidence screenshot:
  `/tmp/agently-live-ui/ipad-after-coalescing-20260805.png`.
- Fresh iPhone conversation `70fdb977-47d0-4f03-84de-7addd84f9b9a` was sent
  from `AgentlyAppLiveUITests` after the same rebuild. The UI test passed,
  Forecasting was found by the native accessibility tree, and the transcript
  recorded exactly one `ui/window/setFormData` tool row at sequence 12 for the
  per-run `ios-ui-test-*` bridge client. The Forecasting conversion included
  `includeCountry:["US"]`, `includePostalCodeList:[70731]`, and all 18
  `includeDealsPmp` values. The post-test simulator screenshot is not used as
  app visual evidence because Xcode returned to the home screen during
  teardown.
- Fresh Android phone conversation
  `f7cf4ad0-caac-4d53-a872-1c899f9938b9` was sent from the Pixel 10 Pro AVD
  after the same rebuild through `scripts/android-semantic-compose-replay.sh`
  with visible `Ask anything` and `Send` labels. It verified visible
  Forecasting and recorded exactly one `ui/window/setFormData` tool row at
  sequence 12 for `android-ui-9c5eb72f-d959-4daa-83bd-f913d84b7513`. The
  Forecasting conversion included `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, and all 18 `includeDealsPmp` values.
- Evidence screenshot:
  `/tmp/agently-live-ui/android-phone-after-coalescing-20260805.png`.
- Current proof status: post-coalescing no-duplicate Forecasting prefill is
  verified on Android tablet, iPad, iPhone, and Android phone against the
  local `:9292` Steward lane.

## 2026-08-05 Boundary and Inline Report Rescan

- Re-scanned production Forge Android/iOS sources for Agently, Steward,
  Forecasting, line-id, and workspace-specific routing leakage. The only
  production hits in Forge are generic `reportBuilderRef` model/selection
  fields. Re-scanned Agently Core SDK production sources for Steward,
  Forecasting, line-id, and lookup-id leakage; no production hits were found.
- Updated the status table above to use the fresh post-coalescing device proof
  conversations instead of older pre-coalescing runs.
- Verified the portable inline-report model layer remains green:
  `cd /Users/awitas/go/src/github.com/viant/forge/android &&
  ./gradlew :sdk:testDebugUnitTest --tests '*InlineReportRuntimeCompilerTest'
  --no-daemon --console=plain`;
  `cd /Users/awitas/go/src/github.com/viant/forge &&
  swift test --package-path ios --filter InlineReportRuntimeCompilerTests`;
  and
  `cd /Users/awitas/go/src/github.com/viant/agently-core &&
  go test ./sdk -run
  'TestNormalizeRenderedContent|TestCanonical|TestMobileSDKPublicSurfacesCoverClientContract'
  -count=1`.
- Closed the main canonical inline report visual acceptance gate for web,
  iPhone, iPad, Android tablet, and Android phone with the same committed
  conversation:
  `a5fc8e9f-8d48-431c-9cb9-820a819eb7aa`.
  - Web proof:
    `/tmp/agently-live-ui/inline-report-web-a5fc8e9f-20260805.png`.
  - iPhone proof:
    `/tmp/agently-live-ui/inline-report-ios-phone-a5fc8e9f-20260805.png`.
  - iPad proof:
    `/tmp/agently-live-ui/inline-report-ios-ipad-a5fc8e9f-20260805.png`.
  - Android tablet proof:
    `/tmp/agently-live-ui/inline-report-android-tablet-a5fc8e9f-20260805-final.png`.
  - Android phone proof:
    `/tmp/agently-live-ui/android-phone-inline-report-a5fc8e9f-20260805.png`.
- The inline report was produced through the real authenticated Steward query
  path using local OOB auth against `:9292`; the response is a committed
  `report-document-v1` report. Diagnostic evidence was unavailable in that
  turn, so this proof validates canonical report rendering and transcript
  parity, not a business diagnosis for ad order `2664518`.
- Android tablet local connectivity required
  `adb reverse tcp:9292 tcp:9292` and the `Localhost 9292` workspace preset.
  Direct emulator host aliases `10.0.2.2:9292` and `10.0.3.2:9292` were not
  reachable from this Pixel Tablet AVD during the run.
- Android phone was booted as `emulator-5556`, installed with the debug OOB
  build, selected the `Localhost 9292` workspace, authenticated, and opened the
  same recent conversation. The UI tree confirmed `Ad order 2664518 delivery
  troubleshooting`, `20 report blocks`, and `Primary read`.

## 2026-08-05 Review Follow-Up Rescan

- Delegated architecture review found four concrete follow-ups after the
  mobile parity pass: Agently-core still had a Steward diagnostic formatter in
  generic direct-action production code; Agently iOS accepted live-test active
  conversation overrides outside developer mode; mobile SDK restore only
  applied `setFormData` by `windowId`; and Forge iOS required legacy
  `dashboard.reportBuilder` fallback config even when the generic
  `reportBuilders` variant contract was sufficient.
- Fixed the Agently iOS override by gating
  `AGENTLY_IOS_ACTIVE_CONVERSATION_ID` and `--activeConversationID=` behind
  developer auth mode.
- Fixed Android and iOS SDK hosted restore so `ui/window:setFormData` patches
  can target the unique restored window by `windowKey` when a response does not
  carry `windowId`, matching the web restore behavior without guessing.
- Fixed Forge iOS `dashboard.reportBuilder` rendering so variant-only
  `reportBuilders` definitions render through the same generic resolver and do
  not require copy-pasted legacy fallback config.
- Removed the Steward diagnostic/report formatter from Agently-core
  `service/agent/direct_action.go`. Generic direct actions now return trimmed
  `$toolResult` text for every tool; Steward-specific diagnostic/report
  presentation must live in the Steward workspace or generic Forge report
  grammar, not in Agently-core.
- Verification passed:
  `go test ./service/agent -run 'DirectAction|NormalizeInterfaceMap' -count=1`;
  `swift test --package-path sdk/ios --filter AgentlySDKTests/testHostedWorkspaceRestore`;
  `cd /Users/awitas/go/src/github.com/viant/agently/android &&
  ./gradlew :agently-core-sdk:testDebugUnitTest --tests
  'com.viant.agentlysdk.WorkspaceRestoreTest' --no-daemon --console=plain`;
  `cd /Users/awitas/go/src/github.com/viant/agently &&
  swift test --package-path ios --filter AppStateTargetingTests`; and
  `cd /Users/awitas/go/src/github.com/viant/forge &&
  swift test --package-path ios --filter 'testReportBuilderVariantResolution'`.
- Boundary scan now finds no Steward diagnostic/report formatter symbols in
  Agently-core direct-action production code or mobile SDK production sources.
- A follow-up delegated review then found one remaining generic Forge
  lifecycle risk: variant-only report builders shared persisted state and
  chart presets under the same container key, and iOS could keep hydrated
  default-builder selections after `windowForm.reportBuilderRef` changed.
  Fixed in Forge Android/iOS by deriving saved state and preset keys from the
  base container key plus a sanitized `reportBuilderRef`; iOS also resets and
  rehydrates report-builder UI state when the effective variant key changes.
- Verification passed after the Forge variant-state fix:
  `cd /Users/awitas/go/src/github.com/viant/forge &&
  swift test --package-path ios --filter
  'testReportBuilderVariantResolution|testReportBuilderVariantStateKey'`;
  and
  `cd /Users/awitas/go/src/github.com/viant/forge/android &&
  ./gradlew :sdk:testDebugUnitTest --tests
  'com.viant.forgeandroid.ui.ReportBuilderStateStorageTest'
  --no-daemon --console=plain`.
- Final delegated review of the variant-state fix reported no findings and
  independently verified Forge Android `ReportBuilderStateStorageTest` plus
  Forge iOS `swift test --filter ForgeIOSTests`.

## 2026-08-05 Latest Code Rescan and Android Tablet Replay

- Re-scanned the current local state after the Forge variant-state fix and the
  Agently-core boundary cleanup. The local Steward lane is still running on
  `:9292` with the Steward workspace and Datly MCP on `:5002`; Android Pixel
  Tablet plus iPhone and iPad simulators are available.
- Fixed a tablet Android automation/UX gap in the Agently app: the tablet
  workspace composer now exposes stable resource ids/test tags for
  `new_conversation_composer_input`, `reply_composer_input`,
  `send_new_conversation`, and `send_reply`, while screen readers keep
  user-facing `Message` and `Send` labels. The visible UI was usable, but the
  replay harness could not reliably find the tablet composer before these
  selectors were restored.
- Rebuilt and reinstalled the Android debug OOB build against
  `AGENTLY_ANDROID_BASE_URL=http://localhost:9292`; Gradle completed
  `BUILD SUCCESSFUL`.
- Reran
  `./scripts/android-semantic-compose-replay.sh --device emulator-5554 --prompt
  "open forecast builder for line 7288336" --expect "Forecasting" --wait 120`.
  The script found the composer/send selectors, sent the prompt, and completed
  with `verified: Forecasting`.
- Fresh replay conversation:
  `71843d7a-033e-4db8-af42-350444a4d9b2`.
  Payload proof: `AdLineId:[7288336]`; `ui/window/list` returned only
  `chat/new` before opening; `ForecastingTargetingConvert` normalized
  `location:["US"]`, `location.postalcode.list:["70731"]`, and the 18 PMP deal
  ids; `ui/view/open` returned
  `windowId=forecastingCubeBuilder__71843d7a-033e-4db8-af42-350444a4d9b2`,
  `windowKey=reportBuilder`, `windowTitle=Forecasting`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`; and
  `ui/window/setFormData` completed once with `ok:true`.
- Evidence screenshot:
  `/tmp/agently-live-ui/android-tablet-semantic-replay-forecasting-20260805.png`.

## 2026-08-05 Fresh Rescan After Registry Cache Rebuild

- Re-scanned the latest code and process state. Local Steward is listening on
  `:9292`, Datly MCP is alive on `:5002`, and Pixel Tablet `emulator-5554` is
  attached with `adb reverse tcp:9292 tcp:9292`.
- Focused verification passed:
  `go test ./internal/tool/registry -run 'RecentResults|ExecutionProtection'
  -count=1`,
  `./scripts/android-semantic-compose-replay.sh --self-test`, and
  `cd /Users/awitas/go/src/github.com/viant/agently/android &&
  ./gradlew :app:testDebugUnitTest --tests
  'com.viant.agently.android.AppEndpointConfigTest' --no-daemon
  --console=plain`.
- Reran the live tablet replay:
  `./scripts/android-semantic-compose-replay.sh --device emulator-5554
  --prompt "open forecast builder for line 7288336" --expect "Forecasting"
  --wait 120`. The script found
  `new_conversation_composer_input` and `send_new_conversation`, submitted the
  prompt, and completed with `verified: Forecasting`.
- Fresh conversation `13c21cfb-ab59-446c-804b-30a6af49f9f7`, turn
  `d067ea8a-980a-40cd-9c49-812bd334ea65`, proved the current
  post-rebuild path. `AdTargetingProfile` was requested with
  `AdLineId:[7288336]`; `ForecastingTargetingConvert` normalized country `US`,
  postal list `70731`, and all 18 PMP deal ids; `ui/view/open` returned
  `windowId=forecastingCubeBuilder__13c21cfb-ab59-446c-804b-30a6af49f9f7`,
  `windowKey=reportBuilder`, `windowTitle=Forecasting`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`.
- Strict DB check showed exactly one completed `ui/window/setFormData` row for
  the fresh turn. The request prefilled `includeCountry:["US"]`,
  `includePostalCodeList:[70731]`, all 18 `includeDealsPmp` values, and scope
  `audienceIds:[7288336]`, `adOrderIds:[2664518]`,
  `targetKey:"line:7288336"`.
- Evidence screenshot:
  `/tmp/agently-live-ui/android-tablet-after-latest-rescan-20260805.png`.

## 2026-08-05 iPad Executor-Coalescing Rescan

- Re-scanned the latest Agently-core, Agently, and Forge checkouts. Agently
  still replaces `github.com/viant/agently-core` with the local
  `/Users/awitas/go/src/github.com/viant/agently-core` checkout. Focused
  executor/registry checks passed:
  `go test ./service/shared/toolexec -run
  'CoalescesConcurrentDuplicateActiveTurnSteps|RetryBehavior' -count=1` and
  `go test ./internal/tool/registry -run
  'RecentResults|ExecutionProtection' -count=1`.
- Restarted the isolated local Steward lane on `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`; the server loaded
  `reportBuilder` and `forecastingCubeBuilder` from the Steward Forge
  workspace.
- Reran the iPad Pro 11-inch live UI-originated test:
  `AgentlyAppLiveUITests/ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer`.
  Xcode reported `** TEST SUCCEEDED **`.
- Fresh conversation `73871a78-5e18-4d44-a8d6-66b4b6cfa26a`, turn
  `e9b52209-fc05-4912-b4b1-b199b0b4529b`, opened
  `windowId=forecastingCubeBuilder__73871a78-5e18-4d44-a8d6-66b4b6cfa26a`
  with `windowKey=reportBuilder`, `windowTitle=Forecasting`, and
  `parameters.reportBuilderRef=forecastingCubeBuilder`.
- Strict DB check now shows exactly one completed `ui/window/setFormData` row
  for the fresh iPad turn. The decoded request contains
  `includeCountry:["US"]`, `includePostalCodeList:[70731]`, all 18
  `includeDealsPmp` values, `sharedIncludeFilters` from the canonical
  targeting converter, and scope `audienceIds:[7288336]`,
  `adOrderIds:[2664518]`, `targetKey:"audience:7288336"`.
- Evidence screenshot:
  `/tmp/agently-live-ui/ipad-live-ui-after-toolexec-coalesce-20260805.png`.

## 2026-08-05 Android Tablet Protocol-Coalescing Rescan

- Reran the Android Pixel Tablet semantic replay after the executor coalescing
  change and found a protocol-level gap: conversation
  `cf5fa5b9-bed7-4551-b430-5a61159d3066` opened Forecasting and completed
  exactly one UI side-effect row, but the turn failed because the model had
  emitted a second parallel `ui_window-setFormData` call id and the next
  OpenAI request lacked a matching tool output for that duplicate call id.
- Fixed Agently-core tool execution so coalesced duplicate active-turn tool
  calls still persist a protocol-visible tool result for their own model call
  id while sharing the first registry execution. Duplicate rows use a
  non-side-effect `coalesced` status; the actual UI/tool side effect remains
  the single `completed` row.
- Focused checks passed after the fix:
  `go test ./service/shared/toolexec -run
  'CoalescesConcurrentDuplicateActiveTurnSteps|RetryBehavior' -count=1` and
  `go test ./internal/tool/registry -run
  'RecentResults|ExecutionProtection' -count=1`.
- Rebuilt Agently and restarted local Steward on `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`, then reinstalled Android with
  debug OOB against `http://localhost:9292`. The current Pixel Tablet AVD
  reaches host `:9292` through `adb reverse tcp:9292 tcp:9292`; direct
  `10.0.2.2:9292` and `10.0.3.2:9292` probes failed in this run.
- Reran
  `./scripts/android-semantic-compose-replay.sh --device emulator-5554
  --prompt "open forecast builder for line 7288336" --expect "Forecasting"
  --wait 120`. The script completed with `verified: Forecasting`.
- Fresh conversation `9615ac11-e0e2-4440-af02-ac840f97595f`, turn
  `3fd6d694-f100-4bcc-8735-a8fd07ed4735`, has turn status `succeeded` and a
  final assistant message. The live model again emitted duplicate
  `ui/window/setFormData` calls; the DB now shows one real `completed` row and
  one protocol-visible `coalesced` row for the duplicate, both with the same
  request payload. The decoded request contains
  `includeCountry:["US"]`, `includePostalCodeList:[70731]`, all 18
  `includeDealsPmp` values, `sharedIncludeFilters` from the canonical
  converter, and scope `audienceIds:[7288336]`, `adOrderIds:[2664518]`.
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

## 2026-08-05 Four-Target Forecasting Live Rescan

- Re-scanned the latest Agently-core, Agently, Forge, and Steward worktrees.
  `git diff --check` passes in Agently-core, Agently, and Forge. Boundary
  searches show no Steward, Agently, forecasting-window, line-id, or ad-lookup
  special casing in Forge native production sources, and no forecasting-window
  or ad-lookup special casing in Agently mobile production sources. Remaining
  `forecastingCubeBuilder` and `7288336` references in Agently-core are in
  generic service tests and SDK restore fixtures.
- Rebuilt local Agently and restarted the isolated Steward lane on `:9292`
  with `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`. The workspace loaded
  `campaign`, `line`, `reports`, `reportBuilder`, `metricReportBuilder`,
  `order`, and `forecastingCubeBuilder`.
- Android tablet replay passed on `emulator-5554` with
  `adb reverse tcp:9292 tcp:9292` and `AGENTLY_ANDROID_BASE_URL=http://localhost:9292`.
  Fresh conversation `911d5bbc-b3a2-4825-b180-c5a216ec2090`, turn
  `7804c625-80f9-47aa-874b-33e9cac0caeb`, has status `succeeded` and exactly
  one completed `ui/window/setFormData` row. The server opened canonical
  `windowKey=reportBuilder` with
  `parameters.reportBuilderRef=forecastingCubeBuilder`. Evidence:
  `/tmp/agently-live-ui/android-tablet-forecast-20260805-current.png`.
- iPad Pro live UI test passed with `** TEST SUCCEEDED **`. Fresh conversation
  `b2b76aa2-6610-4c5e-b082-28ee60b26fca`, turn
  `afa06745-c308-49ba-bdb4-700c2944448a`, has status `succeeded` and one
  completed `ui/window/setFormData` row. Replay screenshot after relaunching
  the active conversation shows the Forecasting hosted surface restored.
  Evidence: `/tmp/agently-live-ui/ipad-forecast-replay-20260805-current.png`.
- iPhone live UI test passed with `** TEST SUCCEEDED **`. Fresh conversation
  `3dd57065-38ed-4ba0-96d9-7a85eb3c1c8e`, turn
  `8194290c-0a8c-40bc-a77b-b146a74d1779`, has status `succeeded` and one
  completed `ui/window/setFormData` row. Replay screenshot after relaunching
  the active conversation shows the Forecasting hosted surface restored.
  Evidence: `/tmp/agently-live-ui/iphone-forecast-replay-20260805-current.png`.
- Android phone replay passed on `emulator-5556` with
  `adb reverse tcp:9292 tcp:9292` and `AGENTLY_ANDROID_BASE_URL=http://localhost:9292`.
  Fresh conversation `6f8bdf0f-02f3-40c2-ac54-f2747995cbbf`, turn
  `68f84ed1-1131-456b-8ccf-aaa10b5712ea`, has status `succeeded` and one
  completed row each for `llm/skills/activate`, `steward/AdTargetingProfile`,
  `steward/ForecastingTargetingConvert`, `ui/context/get`, `ui/view/open`,
  `ui/window/list`, and `ui/window/setFormData`. Evidence:
  `/tmp/agently-live-ui/android-phone-forecast-20260805-current.png`.
- All four live runs use the web-compatible split: canonical
  `reportBuilder` window plus `reportBuilderRef=forecastingCubeBuilder`.
  Steward-specific routing remains in the Steward workspace; Forge resolves
  the requested builder variant generically from metadata, and Agently only
  hosts/restores the surface.

## 2026-08-05 Dirty-State Curation Rescan

- Added local-only Git excludes in each checkout's `.git/info/exclude` for
  generated verification/runtime byproducts: `.agently` state, local DB files,
  built binaries/archives, temporary screenshots/PDFs, and transient UI
  request captures. No tracked `.gitignore` files were changed, and no source
  or evidence files were deleted.
- After local exclude cleanup, the visible untracked files are candidate
  source/docs/tests rather than runtime exhaust:
  Agently-core keeps `mobile_sdk-progress/README.md`, the MCP 2026 docs, and
  the extension-upgrade doc visible; Agently keeps mobile helper scripts,
  iOS live UI-test files, and a small set of review/proof docs visible; Forge
  keeps report-builder demo/runtime JS tests visible; Steward keeps
  `skills/forecast-targeting.contract.test.mjs` visible.
- Verified `git diff --check` in Agently-core, Agently, Forge, and Steward.
- Re-ran production boundary scans. Forge Android/iOS production sources still
  contain no Agently, Steward, forecasting-window, line-id, or ad-lookup
  identifiers. Agently Android/iOS production sources still contain no
  forecasting-window ids, line `7288336`, ad-lookup ids, or Steward-specific
  tool routing.
- Re-ran the focused Agently-core gate:
  `go test ./service/shared/toolexec ./internal/tool/registry
  ./runtime/discovery ./service/agent -count=1`, covering tool execution
  coalescing, registry discovery/protection, bounded discovery, intake/direct
  action, and tool-bundle behavior. It passes.

## 2026-08-05 Portable Native And SDK Verification

- Re-ran the broader portable gates from the cleaned visible tree:
  - Agently Android:
    `cd /Users/awitas/go/src/github.com/viant/agently/android &&
    ./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
    --console=plain` passed.
  - Agently iOS:
    `cd /Users/awitas/go/src/github.com/viant/agently &&
    swift test --package-path ios` passed 92 tests.
  - Forge Android:
    `cd /Users/awitas/go/src/github.com/viant/forge/android &&
    ./gradlew :sdk:testDebugUnitTest :sdk:compileDebugKotlin --no-daemon
    --console=plain` passed.
  - Forge iOS:
    `cd /Users/awitas/go/src/github.com/viant/forge &&
    swift test --package-path ios` passed 221 tests.
  - Agently-core Android SDK:
    `cd /Users/awitas/go/src/github.com/viant/agently-core/sdk/android &&
    ./gradlew testDebugUnitTest --no-daemon --console=plain` passed.
  - Agently-core TypeScript SDK:
    `cd /Users/awitas/go/src/github.com/viant/agently-core/sdk/ts &&
    npm test` passed 22 files / 366 tests.
  - Agently-core iOS SDK:
    `cd /Users/awitas/go/src/github.com/viant/agently-core/sdk/ios &&
    swift test` passed 68 tests.
- Forge/Android Gradle still emits existing Kotlin warnings for unnecessary
  safe calls, unused parameters/variables, and a duplicate label; they are
  warnings only and did not fail the build.

## 2026-08-05 Latest Code Rescan

- Re-scanned latest Agently-core, Agently, and Forge worktrees. No Reporter
  files were changed.
- Fixed Agently-core verification regressions found by the rescan:
  - Same-response `llm/skills:activate` now acts as a true execution barrier
    before later sibling tool calls, so active-skill constraints apply within
    the same model response.
  - Scheduler run-now rate-limit tests no longer race their own blocked run
    database writes.
  - Scheduler Datly component registration now keys the initialized-service
    cache by the Datly service pointer, not a raw uintptr, avoiding missing
    routes when Go reuses an address between tests.
  - Tool-control tests now seed a real visible skill service when expecting
    `llm/skills:list` and `llm/skills:activate`.
- Verification passed:
  - Agently-core: `go test ./...`.
  - Forge: `npm test`.
  - Agently Android app: `./gradlew :app:assembleDebug
    :app:testDebugUnitTest --console=plain`.
  - Agently iOS: `swift test` in `agently/ios` passed 92 tests.
  - Agently-core `git diff --check`.
- Local Steward-backed Agently server remains listening on `:9292`.

## 2026-08-05 Boundary And Native Launch Smoke

- Confirmed local Steward-backed Agently server is still listening on `:9292`
  as PID `26277`.
- Confirmed attached devices:
  - Android Pixel Tablet: `emulator-5554`.
  - Android phone: `emulator-5556`.
  - iPhone 17 simulator:
    `59317EFB-ADFE-4A22-817F-4B4F6658AB2E`.
  - iPad Pro 11-inch simulator:
    `B2AA0D68-7312-4CC9-85B8-0544341A942D`.
- Re-ran boundary leakage scans:
  - Forge Android/iOS production SDK/UI/runtime sources have no Agently,
    Steward, forecasting-window, line `7288336`, ad-lookup, or Steward tool
    special casing.
  - Agently Android/iOS production app sources have no forecasting-window,
    line `7288336`, ad-lookup, or Steward targeting tool special casing.
  - Agently-core SDK production sources have no Steward/forecasting/line
    special casing.
  - Steward extension remains the owner of `reportBuilderRef`,
    `targetOverrides`, reporting builder definitions, targeting dialogs, and
    Steward-specific window/report wiring.
- Android native smoke:
  - Applied `adb reverse tcp:9292 tcp:9292` on both attached emulators.
  - Reinstalled `/Users/awitas/go/src/github.com/viant/agently/android/app/build/outputs/apk/debug/app-debug.apk`
    on both devices.
  - Launched `com.viant.agently.android/.MainActivity`.
  - Tablet process stayed alive as PID `16361`; phone process stayed alive as
    PID `9775`; recent logcat slice contained no fatal exception.
- iOS native smoke:
  - Built `AgentlyApp` for the booted iPhone and iPad simulators with
    `xcodebuild build`; both builds succeeded.
  - Installed and launched
    `/Users/awitas/Library/Developer/Xcode/DerivedData/AgentlyApp-gwpvijlznkvhtubbawqrcrfcyqxm/Build/Products/Debug-iphonesimulator/Agently.app`.
  - `simctl launch` returned iPhone PID `70733` and iPad PID `70754`.

## 2026-08-05 Auth UX And Focused Gate Rescan

- Cleaned up the mobile required-auth screens: Android and iOS now show the
  normal workspace sign-in action plus settings access only. Developer OOB
  remains available through settings/debug bootstrap paths, but it is no
  longer advertised on the primary sign-in card.
- Checked the current native test harnesses before adding coverage. Android
  has no Compose UI-test/Robolectric setup in the app module, and iOS has no
  SwiftUI view-inspection harness, so this pass avoids brittle source-string
  assertions and relies on the native compile/unit gates plus source-level
  auth-screen cleanup.
- Rechecked boundary-sensitive core changes. The large Agently-core
  direct-action diff removes Steward-specific diagnostic/report formatting
  from core; the remaining Steward reference is a test asserting that core
  does not special-case `steward/Diagnostic`.
- Verification passed:
  - Agently-core focused core gate:
    `go test ./service/shared/toolexec ./internal/tool/registry
    ./runtime/discovery ./service/scheduler -count=1`.
  - Agently-core focused agent gate:
    `go test ./service/agent -run
    'DirectAction|Tool|Intake|ConversationMetadata|AutoSelect|Planner'
    -count=1`.
  - Forge report-builder JS/runtime slice:
    `node --no-warnings src/components/dashboard/reportBuilderRuntimePreview.test.js`
    plus every `src/demos/reportBuilder/*.test.js`.
  - Forge Android SDK:
    `./gradlew :sdk:testDebugUnitTest --no-daemon --console=plain`.
  - Forge iOS:
    `swift test --package-path ios` passed 221 tests.
  - Agently iOS:
    `swift test --package-path ios` passed 92 tests.
  - Agently Android app:
    `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
    --console=plain`.
  - `git diff --check` passes in Agently and Forge.

## 2026-08-05 Steward Forecast Scope Curation

- Re-scanned the Steward prompt, intake, skill, and Forge-extension changes
  that own product-specific forecasting/report-builder behavior.
- Builder-prefill line activations now carry only `AdLineId`; they no longer
  seed `AudienceId` or `audienceIds` before the targeting profile is read.
- Plain forecast line activations are now split from audience activations:
  `run_forecast_for_line` emits `AdLineId`, while
  `run_forecast_for_audience` keeps `audienceId` and `audienceIds`.
- Strengthened the Steward contract test so it asserts both the skill wording
  and the intake scope contract for line-vs-audience forecast requests.
- Verified Steward-specific checks:
  - `node deployment/steward/skills/forecast-targeting.contract.test.mjs`
  - `node deployment/steward/extension/forge/windows/forecastingCubeBuilder.test.js`
  - `node deployment/steward/extension/forge/windows/forecastingCubeBuilder.predicates.test.mjs`
  - `node deployment/steward/extension/forge/windows/metricReportBuilder.windowParams.test.mjs`
  - `git diff --check` in Steward.
- Rechecked generic import/target support:
  - Agently-core:
    `go test ./workspace/service/meta ./protocol/tool/service/ui/view
    ./service/ui/window -run 'Import|Target|ReportBuilder|Window|Prepare'
    -count=1`.
  - Forge backend:
    `go test ./backend/service/meta -run 'Target|Import' -count=1`.
- Re-ran production boundary scans. Forge native, Agently mobile production,
  and Agently-core SDK production remain free of Steward-specific
  forecasting/line/report-builder business logic.
- Re-aligned the generic Agently-core intake activation harness with the
  Steward line-vs-audience split. Line forecast examples now assert
  `AdLineId` only, audience forecast examples assert `audienceId` plus
  `audienceIds`, and both paths assert the other scope keys are absent.
- Verified Agently-core forecast activation gates:
  - `go test ./service/agent -run
    'Forecast.*RoutesToForecastTemplate|OpenForecastBuilderForLine' -count=1`.
  - `go test ./service/agent -run
    'DirectAction|Tool|Intake|ConversationMetadata|AutoSelect|Planner|Forecast'
    -count=1`.
  - `git diff --check` in Agently-core.
- Independent Codex review found the Steward contract test only covered plain
  forecast rules, not the builder-prefill rules that previously leaked line ids
  into audience scope. Follow-up strengthened
  `forecast-targeting.contract.test.mjs` so
  `open_forecasting_builder_for_line` and
  `open_forecasting_builder_for_line_no_run` must emit only `AdLineId`.
  The same Steward window checks, focused Agently-core forecast gate, and
  `git diff --check` in Steward and Agently-core pass after the fix.

## 2026-08-05 SDK Restore And Streaming Rescan

- Re-scanned the Agently-core Android, iOS, and TypeScript SDK restore and
  streaming changes that keep mobile active turns separated from hydrated
  transcript history.
- Added matching iOS coverage for the Android cursor/sequence regression:
  after transcript hydration, an SSE event whose `eventSeq` exactly matches a
  hydrated transcript message is skipped even if it arrives after the
  hydration cursor, while a later live event with a new sequence is still
  applied. This preserves the active-turn rule without using max-sequence
  heuristics that would drop valid post-hydration events.
- Verified SDK restore/streaming gates:
  - Agently-core iOS SDK: `swift test` passed 68 tests.
  - Agently-core Android SDK:
    `./gradlew testDebugUnitTest --tests
    'com.viant.agentlysdk.stream.ConversationStreamTrackerTest' --tests
    'com.viant.agentlysdk.WorkspaceRestoreTest' --no-daemon --console=plain`.
  - Agently-core TypeScript SDK: `npm test -- workspaceRestore` passed
    10 tests.
  - `git diff --check` in Agently-core.

## 2026-08-05 Forge Report-Builder Variant Rescan

- Re-scanned Forge Android/iOS report-builder variant support for the
  web-compatible `reportBuilder` plus `reportBuilderRef` contract.
- Added Android parity coverage for the iOS no-legacy-fallback case: native
  Forge now proves a workspace can provide only `reportBuilders` plus a
  default `reportBuilderRef` without also copying the selected builder into
  legacy `dashboard.reportBuilder`. This keeps mobile aligned with metadata
  driven variants instead of requiring per-model copy/paste fallback config.
- Verified Forge focused gates:
  - Android:
    `./gradlew :sdk:testDebugUnitTest --tests
    'com.viant.forgeandroid.ui.ReportBuilderStateStorageTest' --no-daemon
    --console=plain`.
  - iOS:
    `swift test --package-path ios --filter
    'testReportBuilderVariantResolution|testWindowMetadataDecodesReportBuilderVariants'`.
  - `git diff --check` in Forge.

## 2026-08-05 Mobile Auth Rescan

- Re-scanned the latest Agently Android/iOS mobile auth and local endpoint
  changes. Primary sign-in screens now present only the workspace sign-in path
  plus settings access; OOB/session helpers remain developer-only settings
  controls and are not exposed on the first auth screen.
- Cleaned stale mobile test fixtures from the old shared `9191` lane to the
  isolated Steward verification lane on `9292`.
- Strengthened Android bootstrap OOB gating coverage so the dev-only automatic
  OOB path proves it does not run outside debug, without the build flag, while
  auth is busy, when auth is already ready/checking, after the one-shot attempt,
  or without a stored OOB secret reference.
- Verified mobile auth gates:
  - Android:
    `./gradlew :app:testDebugUnitTest --tests
    'com.viant.agently.android.MainActivityHelpersTest' --tests
    'com.viant.agently.android.AuthRuntimeTest' --tests
    'com.viant.agently.android.AppEndpointConfigTest' --tests
    'com.viant.agently.android.AppSettingsRuntimeTest' --tests
    'com.viant.agently.android.AppRuntimeTest' --no-daemon --console=plain`.
  - iOS:
    `swift test --package-path ios --filter
    'AuthRuntimeTests|AppStateTargetingTests'`.
  - `git diff --check` in Agently.
- Production/test scan for stale mobile auth UI and port strings found no
  `9191`, `Developer OOB sign-in`, `Use saved OOB`,
  `Session ID or token`, or `Open workspace sign-in` hits in Agently Android
  app sources/tests or Agently iOS Foundation sources/tests.

## 2026-08-05 Native Forecasting Verification Rescan

- Confirmed the isolated Steward lane was already running on `:9292` from the
  Steward workspace and remained auth-gated: providers returned the Viant
  OAuth/BFF provider while unauthenticated metadata returned authorization
  required.
- Verified Android tablet on `emulator-5554` / Pixel Tablet:
  - Relaunched through `scripts/install-android-oob-debug.sh` with
    `AGENTLY_ANDROID_BASE_URL=http://localhost:9292` and existing
    `adb reverse tcp:9292 tcp:9292`.
  - Logcat showed bootstrap OOB completed with `sessionPresent=true` and
    `authState=Ready`.
  - `scripts/android-semantic-compose-replay.sh --device emulator-5554
    --prompt "open forecast builder for line 7288336" --expect "Forecasting"
    --wait 70` passed.
  - Evidence:
    `/tmp/agently-rescan/android-tablet-forecasting-9292-20260805.png`.
- Verified Android phone on `emulator-5556` / Pixel 10 Pro:
  - Relaunched through the same OOB debug script against
    `http://localhost:9292`.
  - Logcat showed bootstrap OOB completed with `sessionPresent=true` and
    `authState=Ready`.
  - `scripts/android-semantic-compose-replay.sh --device emulator-5556
    --prompt "open forecast builder for line 7288336" --expect "Forecasting"
    --wait 70` passed.
  - Evidence:
    `/tmp/agently-rescan/android-phone-forecasting-9292-20260805.png`.
- Verified iPad Pro 11-inch simulator
  `B2AA0D68-7312-4CC9-85B8-0544341A942D`:
  - `AGENTLY_IOS_LIVE_UI_TESTS=1
    AGENTLY_IOS_UI_TEST_BASE_URL=http://127.0.0.1:9292
    xcodebuild -project ios/AgentlyApp.xcodeproj -scheme
    AgentlyAppLiveUITests -configuration Debug -destination
    'platform=iOS Simulator,id=B2AA0D68-7312-4CC9-85B8-0544341A942D'
    -derivedDataPath ios/.build/live-ui-ipad-20260805 test` passed.
  - Evidence:
    `/tmp/agently-rescan/ios-ipad-forecasting-9292-20260805.png`.
- Verified iPhone 17 simulator
  `59317EFB-ADFE-4A22-817F-4B4F6658AB2E`:
  - The same `AgentlyAppLiveUITests` command with destination
    `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` and derived data
    `ios/.build/live-ui-iphone-20260805` passed.
  - Evidence:
    `/tmp/agently-rescan/ios-iphone-forecasting-9292-20260805.png`.

## 2026-08-05 Forge Report-Store Boundary Rescan

- Re-scanned the latest Agently, Forge, and Agently-core worktrees after the
  native Forecasting verification pass. Reporter was not touched.
- Cleaned the report-catalog refresh boundary so Forge owns a generic
  `forge:report-store-changed` event instead of an Agently-named browser
  event. Agently's web host service now emits the same generic event when
  saved reports change.
- Removed generic Forge web copy that named Steward as the preset report owner;
  preset report cards now use the workspace-neutral `Workspace` owner label.
- Removed the Datly/Steward-specific wording from the generic Forge report
  document model source-contract comment.
- Boundary scans passed:
  - No `agently:report-store-changed`, `Datly/Steward`, or preset
    `Steward` owner label remains in Forge report/dashboard production paths
    or Agently web UI production paths.
  - Forge native/report-dashboard production paths still have no
    Steward-specific forecasting, line-id, ad-lookup, Viant-host, or Agently
    special casing.
- Focused verification passed:
  - Forge:
    `node --no-warnings src/components/dashboard/reportBuilderHostServices.test.js`
  - Forge:
    `node --no-warnings src/reporting/reportDocumentModel.test.js`
  - Forge:
    `node --no-warnings src/components/dashboard/reportBuilderRuntimePreview.test.js`
  - Forge:
    `node --no-warnings src/components/dashboard/reportCatalogPagination.test.js`
  - Agently web UI:
    `APPSERVER_URL=http://127.0.0.1:9292 npm test -- --run
    src/services/reportStoreService.test.js src/services/forgeHostServices.test.js`
  - `git diff --check` passed in Agently and Forge.
- Remaining gap: this was a focused code/test rescan, not a fresh four-device
  native replay. The latest four-target Forecasting replay evidence above is
  still current for the `:9292` lane.

## 2026-08-05 Four-Target Native Forecasting Replay Rescan 2

- Closed the previous remaining gap by rerunning the exact prompt
  `open forecast builder for line 7288336` on all currently available mobile
  targets against the isolated local Steward lane.
- Confirmed local lane state before running:
  - `agently` PID `26277` was listening on `*:9292`.
  - `http://127.0.0.1:9292/v1/api/auth/providers` returned the Viant
    OAuth/BFF provider.
  - Android devices attached: Pixel Tablet `emulator-5554` and phone
    `emulator-5556`.
  - iOS simulators booted: iPhone 17
    `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` and iPad Pro 11-inch
    `B2AA0D68-7312-4CC9-85B8-0544341A942D`.
- Android tablet verification:
  - Applied `adb reverse tcp:9292 tcp:9292`.
  - Installed/launched with
    `ANDROID_SERIAL=emulator-5554
    AGENTLY_ANDROID_BASE_URL=http://localhost:9292
    ./scripts/install-android-oob-debug.sh`.
  - `./scripts/android-semantic-compose-replay.sh --device emulator-5554
    --prompt "open forecast builder for line 7288336" --expect
    "Forecasting" --wait 90` passed with `verified: Forecasting`.
  - Evidence:
    `/tmp/agently-rescan/android-tablet-forecasting-9292-20260805-rescan2.png`.
- Android phone verification:
  - Applied `adb reverse tcp:9292 tcp:9292`.
  - Installed/launched with
    `ANDROID_SERIAL=emulator-5556
    AGENTLY_ANDROID_BASE_URL=http://localhost:9292
    ./scripts/install-android-oob-debug.sh`.
  - `./scripts/android-semantic-compose-replay.sh --device emulator-5556
    --prompt "open forecast builder for line 7288336" --expect
    "Forecasting" --wait 90` passed with `verified: Forecasting`.
  - Evidence:
    `/tmp/agently-rescan/android-phone-forecasting-9292-20260805-rescan2.png`.
- iPad verification:
  - `AGENTLY_IOS_LIVE_UI_TESTS=1
    AGENTLY_IOS_UI_TEST_BASE_URL=http://127.0.0.1:9292
    xcodebuild -project ios/AgentlyApp.xcodeproj -scheme
    AgentlyAppLiveUITests -configuration Debug -destination
    'platform=iOS Simulator,id=B2AA0D68-7312-4CC9-85B8-0544341A942D'
    -derivedDataPath ios/.build/live-ui-ipad-rescan2-20260805
    -only-testing:AgentlyAppUITests/ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer
    test` passed with `** TEST SUCCEEDED **` and one executed UI test.
  - Evidence:
    `/tmp/agently-rescan/ios-ipad-forecasting-9292-20260805-rescan2.png`.
- iPhone verification:
  - `AGENTLY_IOS_LIVE_UI_TESTS=1
    AGENTLY_IOS_UI_TEST_BASE_URL=http://127.0.0.1:9292
    xcodebuild -project ios/AgentlyApp.xcodeproj -scheme
    AgentlyAppLiveUITests -configuration Debug -destination
    'platform=iOS Simulator,id=59317EFB-ADFE-4A22-817F-4B4F6658AB2E'
    -derivedDataPath ios/.build/live-ui-iphone-rescan2-20260805
    -only-testing:AgentlyAppUITests/ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer
    test` passed with `** TEST SUCCEEDED **` and one executed UI test.
  - Evidence:
    `/tmp/agently-rescan/ios-iphone-forecasting-9292-20260805-rescan2.png`.
- Current gap after this pass: none for the four-target native Forecasting
  replay. Broader goal completion still requires continued parity/boundary
  audit across remaining report-builder, dashboard, auth/session durability,
  and Steward/Forge/Agently separation requirements.

## 2026-08-05 Forecasting Prefill Payload And Restore Boundary Rescan

- Rechecked the persisted tool evidence for the fresh four-target Forecasting
  replay instead of relying only on UI title assertions.
- The latest SQLite `tool_call` / `call_payload` records in the local Steward
  workspace prove each replay followed the required builder-prefill path:
  `steward/AdTargetingProfile`, `llm/skills/activate`, optional
  `steward/ForecastingTargetingConvert`, `ui/view/open`, and completed
  `ui/window/setFormData`.
- Decoded completed `ui/window/setFormData` payloads include normalized
  Forecasting builder predicates:
  - `prefill.includeCountry = ["US"]`
  - `prefill.includePostalCodeList = [70731]`
  - `prefill.includeDealsPmp` with the 18 PMP deal ids from the line
    targeting expression
  - `prefill.sharedIncludeFilters` for `location`,
    `location.postalcode.list`, and `ad.pmp.deal.id`
  - `prefill.scope.adOrderIds = [2664518]` and
    `prefill.scope.audienceIds = [7288336]`
- Accessibility labels still do not expose all filter chips consistently on
  phone/tablet, so DB payload inspection is the authoritative current proof
  for prefill/filter population.
- Cleaned generic Agently/Agently-core hosted-workspace restore fixtures so
  they no longer use Steward-specific `forecastingCubeBuilder`,
  `Forecasting`, or `audience:7288336` examples. The tests now use neutral
  `capacityBuilder`, `Capacity Builder`, and `record:12345` values while
  preserving the same restore behavior coverage.
- Focused verification passed:
  - Agently-core Android SDK:
    `./gradlew testDebugUnitTest --tests
    'com.viant.agentlysdk.WorkspaceRestoreTest' --no-daemon --console=plain`
  - Agently-core iOS SDK:
    `swift test --package-path sdk/ios --filter AgentlySDKTests`
  - Agently-core TypeScript SDK:
    `npm test -- workspaceRestore`
  - Agently Android app:
    `./gradlew :app:testDebugUnitTest --tests
    'com.viant.agently.android.HostedWorkspaceRestoreTest' --no-daemon
    --console=plain`
- Cleaned-fixture scan found no remaining `forecastingCubeBuilder`,
  `Forecasting`, `7288336`, or `audience:7288336` strings in the generic
  restore test files touched by this pass.

## 2026-08-05 Latest-Code Rescan

- Re-scanned current Agently, Agently-core, and Forge worktrees after the
  latest window. Reporter was not touched.
- Local Steward lane is still active on `:9292` with `agently` PID `26277`.
- Attached/booted device bench is available:
  - Android tablet `emulator-5554` (`Pixel_Tablet`)
  - Android phone `emulator-5556` (`sdk_gphone16k_arm64`)
  - iPhone 17 simulator `59317EFB-ADFE-4A22-817F-4B4F6658AB2E`
  - iPad Pro 11-inch simulator `B2AA0D68-7312-4CC9-85B8-0544341A942D`
- Boundary fixture scan remains clean for the generic hosted-workspace
  restore tests: no `forecastingCubeBuilder`, `Forecasting`, `7288336`, or
  `audience:7288336` remains in the touched generic restore fixtures.
- Focused verification passed again on latest code:
  - Agently-core Android SDK:
    `./gradlew testDebugUnitTest --tests
    'com.viant.agentlysdk.WorkspaceRestoreTest' --no-daemon --console=plain`
  - Agently-core iOS SDK:
    `swift test --package-path sdk/ios --filter AgentlySDKTests`
    passed 68 tests.
  - Agently-core TypeScript SDK:
    `npm test -- workspaceRestore` passed 10 tests.
  - Agently Android app:
    `./gradlew :app:testDebugUnitTest --tests
    'com.viant.agently.android.HostedWorkspaceRestoreTest' --no-daemon
    --console=plain`
- `git diff --check` passed in Agently, Agently-core, and Forge.
- Current caveat remains unchanged: Android/iOS accessibility does not expose
  every populated Forecasting filter chip consistently, so the persisted
  completed `ui/window/setFormData` payloads remain the authoritative proof of
  line-targeting filter population.

## 2026-08-05 Forecasting Line TargetKey Contract

- Closed the post-rescan scope inconsistency where some fresh
  `open forecast builder for line 7288336` payloads preserved
  `scope.targetKey` as `audience:7288336` even though the user explicitly
  requested a line.
- Kept the fix Steward-owned:
  - `skills/forecast-targeting/SKILL.md` now requires line builder-prefill to
    set `prefill.scope.targetKey` as `line:<requested line id>`, while audience
    builder-prefill uses `audience:<requested audience id>`.
  - `agents/steward/prompt/parts/routing.md`,
    `agents/steward/prompt/instruction.tmpl`, and
    `agents/steward/prompt/system.tmpl` carry the same rule and explicitly
    forbid replacing requested line scope with the profile's internal audience
    target key.
  - `skills/forecast-targeting.contract.test.mjs` now guards the skill plus all
    three Steward prompt sources.
- Verification passed:
  - `node skills/forecast-targeting.contract.test.mjs`
  - `node extension/forge/windows/forecastingCubeBuilder.test.js`
  - `git diff --check` in the Steward workspace
- Restarted local Agently on the isolated Steward lane with local Datly MCP:
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp ./agently/agently serve -a :9292
  -w /Users/awitas/go/src/github.com/viant-internal/steward_ai/deployment/steward`.
- Fresh Android tablet replay passed:
  `android-semantic-compose-replay.sh --device emulator-5554 --prompt "open
  forecast builder for line 7288336" --expect "Forecasting" --wait 120`.
- Fresh conversation evidence:
  `6e30c6b1-53b7-4565-8235-fad78b2f24b5` completed
  `steward/AdTargetingProfile`, `steward/ForecastingTargetingConvert`,
  `ui/view/open`, and `ui/window/setFormData`.
- Decoded completed `ui/window/setFormData` request payload
  `cfdadd82-1012-4b84-ae67-a44de9a140a1` contains:
  - `prefill.scope.targetKey = "line:7288336"`
  - `prefill.scope.audienceIds = [7288336]`
  - `prefill.scope.adOrderIds = [2664518]`
  - `includeCountry = ["US"]`
  - `includePostalCodeList = [70731]`
  - all 18 `includeDealsPmp` values
  - shared include filters for `location`, `location.postalcode.list`, and
    `ad.pmp.deal.id`
- Visual evidence:
  `/tmp/agently-rescan/android-tablet-forecasting-line-targetkey-20260805.png`.

## 2026-08-05 Auth, Session, And Report-Builder Compatibility Rescan

- Continued from the four-target Forecasting `targetKey` proof instead of
  treating it as full project completion. The goal remains active for ongoing
  mobile SDK / Forge parity work.
- Current local Steward lane stayed on isolated port `:9292`; during this
  pass `agently` was listening as PID `1923`, and local Datly MCP was running
  as PID `53081`.
- Boundary rescan:
  - Forge native production sources have no Agently or Steward imports.
  - Forge native production sources have no `7288336`, ad-lookup id, or
    Steward targeting special casing.
  - Agently-core SDK production sources have no Steward/Forecasting/line-id
    special casing.
  - Agently mobile production contains user-facing workspace endpoint presets
    for Steward production and local `9292`; this is app configuration, not
    renderer/data behavior selection.
- Focused auth/session verification passed:
  - Agently Android app:
    `./gradlew :app:testDebugUnitTest --tests 'com.viant.agently.android.*Auth*'
    --tests 'com.viant.agently.android.*Settings*' --tests
    'com.viant.agently.android.*Session*' --tests
    'com.viant.agently.android.HostedWorkspaceRestoreTest' --no-daemon
    --console=plain`
  - Agently iOS app:
    `swift test --package-path ios --filter
    'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests'`
    passed 19 tests.
  - Agently-core Android SDK:
    `./gradlew testDebugUnitTest --tests '*WorkspaceRestoreTest' --tests
    '*AgentlyClientTest' --tests '*ConversationStreamTrackerTest' --no-daemon
    --console=plain`
  - Agently-core iOS SDK:
    `swift test --package-path ios --filter 'AgentlySDKTests'` passed 68
    tests, including OAuth mobile initiate/callback, session debug headers,
    active-turn SSE-vs-transcript hydration, and hosted-window restore.
- Focused report-builder/dashboard compatibility verification passed:
  - Forge Android SDK:
    `./gradlew :sdk:testDebugUnitTest --tests '*ReportBuilder*' --tests
    '*Dashboard*' --no-daemon --console=plain`
  - Forge iOS:
    `swift test --package-path ios --filter 'ForgeIOSTests'` passed 221
    tests, covering dashboard compatibility blocks, report-builder variants,
    target overrides, report runtime actions, and inline transcript ownership.
- `git diff --check` remains clean in Agently, Agently-core, Forge, and
  Steward after the iOS live-test hold change and progress-doc updates.

## 2026-08-05 Mobile Local-Port Fixture Cleanup

- Removed the remaining stale mobile-local `9191` fixtures from Agently-core
  iOS SDK tests. The affected tests are pure client URL-construction tests, so
  the change keeps behavior identical while aligning local mobile evidence with
  the isolated `:9292` Steward lane.
- Rescan command found no remaining `9191`, `localhost:9191`,
  `127.0.0.1:9191`, or `10.0.2.2:9191` references under:
  - `/Users/awitas/go/src/github.com/viant/agently-core/sdk`
  - `/Users/awitas/go/src/github.com/viant/agently/android`
  - `/Users/awitas/go/src/github.com/viant/agently/ios`
- Verification passed:
  - `cd /Users/awitas/go/src/github.com/viant/agently-core/sdk &&
    swift test --package-path ios --filter 'AgentlySDKTests'`
    passed 68 tests.
  - Agently-core `git diff --check` passed.

## 2026-08-05 Forge Target Customization Rescan

- Re-scanned the latest target customization code and Steward window metadata.
  The active pattern is still generic Forge metadata, not SDK/mobile
  hardcoding:
  - Report builder, metric report builder, and Forecasting builder keep web as
    the base/default content and use `targetOverrides` for mobile/tablet/phone.
  - Order uses a workspace `$import` branch under `order/mobile/main.yaml`
    importing shared mobile content.
  - No Steward-specific rendering behavior was found in Forge native resolver
    code or Agently-core SDK target handling.
- Native resolver parity remains covered:
  - Android and iOS both apply broad `mobile`, form factor, platform,
    `mobile:<formFactor>`, exact `platform:<formFactor>`, and compact aliases
    such as `androidPhone` / `iosTablet`, then strip targeting metadata from
    the resolved model.
  - Web/desktop/no-target stays on base content when only mobile overrides are
    present.
- Agently-core workspace metadata handling remains covered:
  - `$import` preserves imported `targetOverrides`, including broad, exact,
    and compact alias keys.
  - HTTP window loading picks exact `ios/phone` branch before mobile fallback,
    uses mobile fallback for Android phone, and uses the shared default when no
    target is requested.
- Verification passed:
  - Forge Android:
    `./gradlew :sdk:testDebugUnitTest --tests '*TargetingTest' --no-daemon
    --console=plain`
  - Forge iOS:
    `swift test --package-path ios --filter 'MetadataResolver|Targeting'`
    passed 5 selected tests.
  - Agently-core import resolver:
    `go test ./workspace/service/meta -run 'ResolveImports' -count=1`
  - Agently-core workspace window target/default branch tests:
    `go test ./adapter/http/ui -run
    'TestWindowHandler_WorkspaceForgeWindow(AppliesTargetBranchToSharedImports|NoTargetUsesSharedDefault)'
    -count=1`
- Local Steward lane was still active on isolated port `:9292` as Agently PID
  `1923` during this rescan. Reporter was not touched.

## 2026-08-05 Mobile Auth, SDK, And Forge Compatibility Rescan 2

- Re-scanned the current worktrees after the target customization pass. Local
  Steward-backed Agently was still listening on isolated port `:9292` as PID
  `1923`.
- Boundary scans remain clean:
  - Forge Android/iOS production sources have no Steward, Forecasting,
    line-id, ad-lookup, or Steward tool special casing.
  - Agently-core SDK production sources have no Steward/Forecasting/line-id
    special casing.
  - Agently Android/iOS production sources have no Forecasting builder,
    line-id, or Steward targeting tool special casing.
  - No stale mobile `9191` references remain under Agently-core SDK, Agently
    Android, or Agently iOS.
- Focused mobile auth/session/restore verification passed:
  - Agently Android app:
    `./gradlew :app:testDebugUnitTest --tests 'com.viant.agently.android.*Auth*'
    --tests 'com.viant.agently.android.*Settings*' --tests
    'com.viant.agently.android.*Session*' --tests
    'com.viant.agently.android.HostedWorkspaceRestoreTest' --no-daemon
    --console=plain`
  - Agently iOS app:
    `swift test --package-path ios --filter
    'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests'`
    passed 19 tests.
  - Agently-core Android SDK:
    `./gradlew testDebugUnitTest --tests '*WorkspaceRestoreTest' --tests
    '*AgentlyClientTest' --tests '*ConversationStreamTrackerTest' --no-daemon
    --console=plain`
  - Agently-core iOS SDK:
    `swift test --package-path ios --filter 'AgentlySDKTests'` passed 68
    tests.
- Focused Forge report-builder/dashboard verification passed:
  - Forge Android:
    `./gradlew :sdk:testDebugUnitTest --tests '*ReportBuilder*' --tests
    '*Dashboard*' --no-daemon --console=plain`
  - Forge iOS:
    `swift test --package-path ios --filter 'ForgeIOSTests'` passed 221
    tests.
- `git diff --check` passed in Agently, Agently-core, Forge, and Steward.
  No runtime patch was needed in this pass, and Reporter was not touched.

## 2026-08-05 Four-Target Native Forecasting Replay Rescan 3

- Re-ran the exact prompt `open forecast builder for line 7288336` against the
  isolated local Steward lane on `:9292`.
- Preflight:
  - Agently was listening on `:9292` as PID `1923`.
  - Android devices were attached:
    `emulator-5554` (`Pixel_Tablet`) and `emulator-5556`
    (`sdk_gphone16k_arm64`).
  - iOS simulators were booted:
    iPhone 17 `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` and iPad Pro 11-inch
    `B2AA0D68-7312-4CC9-85B8-0544341A942D`.
- Android:
  - Refreshed `adb reverse tcp:9292 tcp:9292` for both emulators.
  - Rebuilt/installed the debug APK with
    `AGENTLY_ANDROID_BASE_URL="http://127.0.0.1:9292"
    ./scripts/install-android-oob-debug.sh`. Gradle built and installed on
    both devices; the helper then exited non-zero only because its final
    generic `adb` launch saw more than one attached emulator.
  - Launched both devices explicitly with `adb -s <serial> shell am start -n
    com.viant.agently.android/.MainActivity`.
  - `android-semantic-compose-replay.sh --device emulator-5554 --prompt
    "open forecast builder for line 7288336" --expect "Forecasting" --wait
    120` passed with `verified: Forecasting`.
  - The same replay on `emulator-5556` passed with `verified: Forecasting`.
- iOS:
  - `AgentlyAppLiveUITests`
    `ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer`
    passed on iPad Pro 11-inch with `** TEST SUCCEEDED **` in 161.055s.
    Result bundle:
    `ios/.build/xcode-live-ipad-rescan3/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_14-25-10-+0200.xcresult`.
  - The same live UI test passed on iPhone 17 with `** TEST SUCCEEDED **` in
    141.140s. Result bundle:
    `ios/.build/xcode-live-iphone-rescan3/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_14-25-11-+0200.xcresult`.
- Persisted tool evidence in
  `/Users/awitas/go/src/github.com/viant-internal/steward_ai/deployment/steward/db/agently.db`
  proves each native turn did more than open the pane:
  - Android phone conversation `cb332f8c-c8d9-44f2-b641-46b4302e92bb`,
    payload `38aedc99-dd0c-494d-866a-c07760a648f9`.
  - Android tablet conversation `13313691-6ef9-4b86-a88f-4338c365c8ea`,
    payload `fe810538-8987-4c33-a4de-128db88ac148`.
  - iOS phone conversation `bd253aa8-632b-4f84-8d7f-0ff7f366c0cf`, gzip
    payload `1dff527a-da04-4955-a1bf-8a5bdd24000e`.
  - iOS tablet conversation `20f943a5-7ebf-4665-a3c0-92d8a736ed6c`, gzip
    payload `708424f6-6fcf-4848-9cf1-e32519c710c0`.
- All four conversations completed the same tool chain:
  `steward/AdTargetingProfile`, `steward/ForecastingTargetingConvert`,
  `ui/view/open`, and `ui/window/setFormData`.
- All four completed `ui/window/setFormData` requests contain:
  - `prefill.scope.targetKey = "line:7288336"`
  - `prefill.scope.audienceIds = [7288336]`
  - `prefill.scope.adOrderIds = [2664518]`
  - `includeCountry = ["US"]`
  - `includePostalCodeList = [70731]`
  - all 18 `includeDealsPmp` values:
    `64512, 66016, 76060, 76084, 76105, 76162, 76711, 89531, 90473,
    90476, 90482, 98075, 143925, 143934, 144156, 146708, 148790, 149114`
  - iOS gzip payloads also include `forecastHandoff.sharedIncludeFilters`
    for `location`, `location.postalcode.list`, and `ad.pmp.deal.id`.
- Current caveat remains unchanged: native accessibility does not expose every
  populated chip consistently, so the completed `ui/window/setFormData`
  payloads remain the authoritative proof of filter prefill.

## 2026-08-05 Latest Code Boundary And Contract Rescan

- Re-scanned current Agently, Agently-core, Forge, and Steward worktrees after
  the rescan 3 handoff update. Reporter was not touched.
- Local Steward-backed Agently remains live on `:9292` as PID `1923`.
- Boundary scans passed:
  - Forge Android/iOS production sources have no Agently, Steward,
    Forecasting, line-id, ad-lookup, or `7288336` special casing.
  - Agently Android/iOS production and Agently-core SDK production sources
    have no Forecasting builder, line-id, Steward targeting tool, or ad-lookup
    special casing.
  - Agently mobile app and Agently-core SDK paths have no stale `9191`
    references.
- Focused contract verification passed:
  - Agently-core import/window/target gate:
    `go test ./workspace/service/meta ./protocol/tool/service/ui/view
    ./service/ui/window -run 'Import|Target|ReportBuilder|Window|Prepare'
    -count=1`.
  - Forge Android target/report-builder/dashboard gate:
    `./gradlew :sdk:testDebugUnitTest --tests '*TargetingTest' --tests
    '*ReportBuilder*' --tests '*Dashboard*' --no-daemon --console=plain`.
  - Forge iOS target/report-builder/dashboard gate:
    `swift test --package-path ios --filter 'MetadataResolver|Targeting|ForgeIOSTests'`
    passed 221 tests.
  - Agently-core Android SDK restore/client/stream gate:
    `./gradlew testDebugUnitTest --tests '*WorkspaceRestoreTest' --tests
    '*AgentlyClientTest' --tests '*ConversationStreamTrackerTest' --no-daemon
    --console=plain`.
  - Agently-core iOS SDK gate:
    `swift test --package-path ios --filter 'AgentlySDKTests'` passed 68
    tests.
  - Agently Android app auth/settings/session/hosted-restore gate:
    `./gradlew :app:testDebugUnitTest --tests 'com.viant.agently.android.*Auth*'
    --tests 'com.viant.agently.android.*Settings*' --tests
    'com.viant.agently.android.*Session*' --tests
    'com.viant.agently.android.HostedWorkspaceRestoreTest' --no-daemon
    --console=plain`.
  - Agently iOS app auth/data-source/presentation gate:
    `swift test --package-path ios --filter
    'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests'`
    passed 19 tests.
  - Steward forecast-targeting contract:
    `node skills/forecast-targeting.contract.test.mjs`.
- `git diff --check` passed in Agently, Agently-core, Forge, and Steward
  before this documentation update.

## 2026-08-05 Nested Report-Builder Variant Target Override Fix

- Independent review probing found one real generic Forge gap: iOS typed
  report-builder variants decoded their nested `targetOverrides` too late for
  `MetadataResolver.resolve(WindowMetadata, targetContext)` to preserve and
  apply them reliably. This could drop phone/tablet-specific customization for
  imported report-builder variants such as Forecasting when a host used the
  typed metadata round trip.
- Fixed Forge iOS `DashboardReportBuilderVariantDef` to retain
  `targetOverrides` with explicit default decoding, matching the rest of the
  typed metadata model. Added the same field to the Android variant model for
  native parity.
- Added resolver regression coverage for nested
  `reportBuilders.<ref>.targetOverrides` on web, Android, and iOS. The tests
  assert mobile/phone overrides apply to the selected variant and the
  targeting metadata is stripped after resolution.
- Verification passed:
  - Forge web: `node --no-warnings src/runtime/metadataResolver.test.js` and
    `node --no-warnings src/components/dashboard/reportBuilderVariantModel.test.js`.
  - Forge Android:
    `./gradlew :sdk:testDebugUnitTest --tests '*TargetingTest' --tests
    '*ReportBuilderStateStorageTest' --no-daemon --console=plain`.
  - Forge iOS:
    `swift test --package-path ios --filter 'MetadataResolver'` and
    `swift test --package-path ios --filter 'ForgeIOSTests'`, now 222 tests.

## 2026-08-05 Downstream Native App Verification After Variant Fix

- Rebuilt and tested Agently native app layers against the updated Forge
  report-builder variant model.
- Agently Android passed:
  `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
  --console=plain`. The build compiled the local Forge SDK and app module; it
  emitted only existing Kotlin warnings and finished `BUILD SUCCESSFUL`.
- Agently iOS passed:
  `swift test --package-path ios --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests'`.
  Result: 27 tests, 0 failures.
- Boundary scans remain clean after the Forge fix:
  - no Steward/Forecasting/line-id/ad-lookup special casing in Forge Android
    or iOS production sources;
  - no Forecasting builder, line-id, Steward targeting tool, or ad-lookup
    special casing in Agently Android/iOS production or Agently-core SDK
    production;
  - no stale mobile `9191` references in Agently Android, Agently iOS, or
    Agently-core SDK paths.
- Local Steward-backed Agently remained live on `:9292` as PID `1923`.

## 2026-08-05 Broader Forge And Steward Rescan After Variant Fix

- Re-ran the broad Forge JS suite after the nested report-builder variant
  target override fix:
  `cd /Users/awitas/go/src/github.com/viant/forge && npm test`.
  The run completed successfully after covering reporting fixtures, preview
  runtime helpers, saved-report payloads, and report-builder runtime/component
  tests.
- Re-ran the full Forge Android SDK unit suite:
  `./gradlew :sdk:testDebugUnitTest --no-daemon --console=plain`.
  Result: `BUILD SUCCESSFUL`; warnings only.
- Re-ran Steward workspace Forge/report-builder smoke gates against the local
  code:
  `forecastingCubeBuilder.test.js`,
  `forecastingCubeBuilder.predicates.test.mjs`,
  `metricReportBuilder.test.js`,
  `metricReportBuilder.predicates.test.mjs`,
  `metricReportBuilder.windowParams.test.mjs`,
  `metricReportBuilder.sharedEndpointDatasets.test.mjs`,
  `skills/forecast-targeting.contract.test.mjs`, and
  `reportPresetPrimitiveCoverage.test.mjs`. All passed; only existing Node
  module-type warnings were emitted.
- Re-ran Agently UI host-service tests against the local Steward-backed server
  on `http://127.0.0.1:9292`:
  `APPSERVER_URL=http://127.0.0.1:9292 npm test -- --run
  src/services/reportStoreService.test.js src/services/forgeHostServices.test.js`.
  Result: 2 files / 9 tests passed.
- Confirmed local Steward-backed Agently is still listening on `:9292` as PID
  `1923`. Reporter remains untouched.
- Final hygiene checks passed after this documentation update:
  `git diff --check` in Agently, Agently-core, Forge, and Steward; focused
  production-source leakage scans for Steward/Forecasting/line-specific
  identifiers in Forge, Agently mobile app sources, and Agently-core SDK
  sources; and stale mobile `9191` scans in Agently mobile and Agently-core
  SDK paths.

## 2026-08-05 Fresh Four-Target Native Forecasting Replay

- Re-ran `open forecast builder for line 7288336` against the local
  Steward-backed Agently lane on `:9292` after VPN was restored.
- Android targets:
  - `emulator-5554` / `Pixel_Tablet`: debug APK rebuilt and installed with
    `AGENTLY_ANDROID_BASE_URL="http://127.0.0.1:9292"` plus OOB dev auth.
    The semantic replay initially timed out because the first
    `steward/AdTargetingProfile` call failed transiently while VPN was coming
    back, but the retry completed and the persisted turn succeeded.
    Conversation `5b3902ac-a2c1-4743-a276-5cb10ecda048`, setFormData payload
    `3265971e-15f9-4557-a7a9-1b9bac2824c0`.
  - `emulator-5556` / Android phone: semantic replay passed with
    `verified: Forecasting`. Conversation
    `ac5268b1-4d7f-46b5-817e-d1667d8211d5`, setFormData payload
    `d011227a-5c12-4d30-9c17-2be1a735d2c0`.
- iOS targets:
  - iPad Pro 11-inch (M5) simulator
    `B2AA0D68-7312-4CC9-85B8-0544341A942D`: first live UI attempt failed
    before tool execution with a rejected starter message and no tool calls;
    warm rerun passed the Forecasting UI assertion. Result bundle:
    `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-ipad-fresh/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_16-39-18-+0200.xcresult`.
    Conversation `0743ea0a-be1f-457d-b878-3525969f4007`, setFormData payload
    `2ce33af6-a7e0-464a-9082-53951f9a2f27`.
  - iPhone 17 simulator `59317EFB-ADFE-4A22-817F-4B4F6658AB2E`: fresh live
    UI test passed the Forecasting UI assertion. Result bundle:
    `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-iphone-fresh/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_16-42-33-+0200.xcresult`.
    Conversation `4490ab54-f834-40ae-9c04-4c1fd765c16d`, setFormData payload
    `ed47605c-8d47-4d10-91e9-aad13e4409a8`.
- All four successful turns completed the required tool chain:
  `steward/AdTargetingProfile`, `llm/skills/activate`,
  `steward/ForecastingTargetingConvert`, `ui/window/list`, `ui/view/open`,
  and `ui/window/setFormData`.
- All four completed setFormData requests contain the canonical Forecasting
  prefill:
  - `prefill.scope.targetKey = "line:7288336"`
  - `prefill.scope.audienceIds = [7288336]`
  - `prefill.scope.adOrderIds = [2664518]`
  - `includeCountry = ["US"]`
  - `includePostalCodeList = [70731]`
  - all 18 `includeDealsPmp` values.
- Three of the four fresh successful payloads also carried the shared include
  filter mirror keys for `ad.pmp.deal.id`, `location`, and
  `location.postalcode.list`. The fresh iPhone successful payload carried the
  canonical typed include fields but not the optional shared-filter mirror.
- Final post-replay hygiene passed: `git diff --check` in Agently,
  Agently-core, Forge, and Steward; focused production leakage scans in Forge,
  Agently mobile, and Agently-core SDK; and stale mobile `9191` scans in
  Agently mobile plus Agently-core SDK paths.

## 2026-08-05 Turn Error Durability Fix From iPad Replay

- Root-caused the first failed iPad live UI attempt from the fresh replay:
  turn `b9a0596f-af21-47aa-b855-aae37d3dd0d1` in conversation
  `8bcf749f-5533-40b4-9c70-d198b78be671` did not reach Forge or Steward
  tools. It produced three failed OpenAI model calls, each with
  `dial tcp: lookup api.openai.com: i/o timeout`, then the terminal turn and
  run stored only the generic `no final content produced` error.
- Fixed Agently-core's generic turn finalization path so an empty-output
  failure first recovers the latest failed model-call error from the durable
  transcript/model-call record. The turn now preserves the actionable provider
  error instead of masking it behind the generic empty-output message.
- This stays in Agently-core and does not add Steward, Forge, Forecasting, or
  mobile-specific logic.
- Verification passed:
  - `go test ./service/agent -run 'TestServiceRunPlanAndStatus' -count=1`
  - `go test ./service/agent -count=1`

## 2026-08-05 Steward ENG-54517 Merge And Local Lane Refresh

- Fetched and fast-forwarded the Steward workspace branch
  `ENG-54517` from `origin/ENG-54517` after the push rejection. The local
  branch is no longer behind remote.
- Preserved and reapplied the local Steward mobile/forecasting/report-builder
  edits after the fast-forward. The only overlapping file,
  `agents/steward/prompt/instruction.tmpl`, auto-merged cleanly.
- Rebuilt the local Agently binary with a temporary Go workspace resolving
  `github.com/viant/agently-core` and `github.com/viant/forge` to the local
  checkouts, so the running `:9292` lane includes the local Agently-core turn
  error durability fix without committing a local-only `replace` directive.
- Restarted local Steward-backed Agently on `:9292`; it is now listening as
  PID `49751`.
- Verification passed:
  - `go test ./service/agent -run 'TestServiceRunPlanAndStatus' -count=1`
  - `curl -I http://127.0.0.1:9292/` returned HTTP 200
  - `git diff --check` in Steward and Agently-core

## 2026-08-05 Post-Merge Steward Smoke Verification

- Re-ran the focused Steward contract/smoke suite after the `ENG-54517`
  fast-forward and local edit reapply, with
  `FORGE_ROOT=/Users/awitas/go/src/github.com/viant/forge` so the new audience
  forecast dashboard export contract validates against the actual local Forge
  checkout.
- Passed 10/10:
  - `extension/forge/windows/forecastingCubeBuilder.test.js`
  - `extension/forge/windows/forecastingCubeBuilder.predicates.test.mjs`
  - `extension/forge/windows/metricReportBuilder.test.js`
  - `extension/forge/windows/metricReportBuilder.predicates.test.mjs`
  - `extension/forge/windows/metricReportBuilder.windowParams.test.mjs`
  - `extension/forge/windows/metricReportBuilder.sharedEndpointDatasets.test.mjs`
  - `extension/forge/windows/reportPresetPrimitiveCoverage.test.mjs`
  - `skills/forecast-targeting.contract.test.mjs`
  - `agents/steward/prompt/reportingDelivery.contract.test.mjs`
  - `templates/audienceForecastDashboard.contract.test.mjs`
- Local Steward-backed Agently remained healthy after the suite: PID `49751`
  is still listening on `:9292`, and `curl -I http://127.0.0.1:9292/`
  returned HTTP 200.

## 2026-08-05 Post-Merge Native Mobile SDK Verification

- Re-ran the focused Agently Android app compile/unit gate after the Steward
  merge and local `:9292` refresh:
  `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon --console=plain`.
  Result: `BUILD SUCCESSFUL` in 1m39s.
- Re-ran the focused Agently iOS package test gate:
  `swift test --package-path ios --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests'`.
  Result: 27 tests, 0 failures.
- The iOS slice covered OAuth mobile redirect behavior, developer session
  cookie login, workspace endpoint persistence including local `:9292`,
  composer lookup parsing/selection/focus behavior, hosted workspace
  presentation labels, and Forge datasource input preservation.
- Rechecked production boundary scans:
  - no stale `9191` in Agently Android/iOS production source
  - no Steward/Forecasting/line-id/ad-lookup leakage in Agently mobile
    production source
  - no Steward/Forecasting/line-id/ad-lookup leakage in Forge production
    source outside demo/test fixtures
  - no Steward/Forecasting/line-id/ad-lookup leakage in Agently-core
    production source outside tests/docs/progress
- `git diff --check` passed in Agently, Agently-core, Forge, and Steward.
- Local Steward-backed Agently stayed healthy after mobile verification: PID
  `49751` is listening on `:9292`, and `curl -I http://127.0.0.1:9292/`
  returned HTTP 200.

## 2026-08-05 Post-Merge Web/Forged Target Override Verification

- Re-ran Agently web host bridge tests against the refreshed local Steward
  lane:
  `APPSERVER_URL=http://127.0.0.1:9292 npm test -- --run
  src/services/reportStoreService.test.js src/services/forgeHostServices.test.js`
  in `agently/ui`. Result: 2 files / 9 tests passed.
- Re-ran Forge JS target override regressions directly:
  `node --no-warnings src/runtime/metadataResolver.test.js && node
  --no-warnings src/components/dashboard/reportBuilderVariantModel.test.js`.
  Result: passed.
- Re-ran Forge Android target/report-builder regression slice:
  `./gradlew :sdk:testDebugUnitTest --tests '*TargetingTest' --tests
  '*ReportBuilderStateStorageTest' --no-daemon --console=plain`. Result:
  `BUILD SUCCESSFUL` in 2m44s, with only existing Kotlin warnings.
- Re-ran Forge iOS metadata/report-builder regression slice:
  `swift test --package-path ios --filter 'MetadataResolver|ReportBuilder'`.
  Result: 26 tests, 0 failures, including nested report-builder variant target
  overrides and filter auto-collapse.
- A package-level Forge `npm test -- --run ...` attempt was interrupted after
  many reporting/preview checks had passed because the package script expands
  to the full suite and the hosted Steward render smoke tail ran too long for
  this focused pass. The two intended JS regression files were then run
  directly and passed.
- No stray Node/Vite child test processes remained after interruption.
- Rechecked architecture boundaries and lane health:
  - no stale `9191` in Agently Android/iOS production source
  - no Steward/Forecasting/line-id/ad-lookup leakage in Agently mobile
    production source
  - no Steward/Forecasting/line-id/ad-lookup leakage in Forge production
    source outside demo/test fixtures
  - `git diff --check` passed in Agently, Agently-core, Forge, and Steward
  - local Steward-backed Agently still listened on `:9292` as PID `49751` and
    returned HTTP 200

## 2026-08-05 Android OOB Auth Replay Stabilization

- Root-caused the phone emulator returning to the sign-in card after successful
  OOB bootstrap. Android authenticated against `http://10.0.2.2:9292`, loaded
  Steward workspace metadata and recent conversations, then a stale concurrent
  auth refresh against the alternate emulator alias `10.0.3.2:9292` timed out
  and incorrectly demoted the active session to `Required`.
- Fixed Agently Android auth state handling in local commit `be857252`:
  `fix(android): stabilize workspace auth bootstrap`.
  - Reset the one-shot OOB bootstrap latch when the first-run workspace
    endpoint changes.
  - Stop workspace bootstrap from repeating just because recent conversations
    are empty after metadata is loaded.
  - Treat stale auth-refresh failures after the session ID changes as obsolete.
  - Preserve an established authenticated session on transient connectivity
    failures, while still requiring sign-in for 401/403 credential rejection.
- Verification:
  - Host-side OOB probe against `http://127.0.0.1:9292` returned HTTP 200 and
    the returned `agently_session` cookie authorized `/v1/api/auth/me`,
    `/v1/workspace/metadata?platform=android&formFactor=phone&surface=native`,
    and `/v1/conversations?limit=25`.
  - Android gate passed after cleanup:
    `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon --console=plain`
    finished `BUILD SUCCESSFUL` in 2m49s.
  - Phone emulator replay on `emulator-5556` with
    `AGENTLY_ANDROID_BASE_URL=http://10.0.2.2:9292`,
    `AGENTLY_ANDROID_OOB_SECRET_REF=<local encrypted OOB reference>`,
    and `AGENTLY_ANDROID_AUTO_OOB_SIGN_IN=true` stayed on the Steward
    conversation screen after the old timeout window. UI dump showed
    `Ready for a new conversation`, starter tasks, recent conversations, and
    composer controls. Logcat showed `Ignoring stale auth refresh failure after
    session changed` instead of auth demotion.
- Push status: Agently is one commit ahead locally, but pushing is blocked by
  GitHub credentials on this machine: HTTPS returns 403 for `awitas_viant`, and
  SSH has no accepted public key.

## 2026-08-05 Android Report Builder Predicate Lowering

- Root-caused the latest Android phone Forecasting UI gap. The Steward-backed
  metadata for `forecastingCubeBuilder` now exposes canonical
  `dashboard.reportBuilder.predicates`, `predicateBuckets`, and
  `predicateGroups`, while Android Forge only rendered the older
  `dynamicFilterGroups`/`dynamicFilterFamilies` shape. The result was a
  correctly opened and prefilled Forecasting builder without visible native
  filter controls.
- Implemented generic Android Forge predicate lowering at the report-builder
  config boundary. The new code decodes canonical predicate metadata and
  derives static filters, dynamic filter groups, and dynamic filter families
  without inspecting Steward names, prompt text, line ids, workspace names, or
  window ids.
- Verified the generic Android Forge slice:
  `cd /Users/awitas/go/src/github.com/viant/forge/android &&
  ./gradlew :sdk:testDebugUnitTest --tests '*ReportBuilderPredicatesTest'
  --tests '*ReportBuilderStateStorageTest' --tests '*TargetingTest'
  --no-daemon --console=plain` passed. A narrower rerun of
  `*ReportBuilderPredicatesTest` also passed after warning cleanup.
- Rebuilt and installed the Agently Android debug APK against
  `http://10.0.2.2:9292` with auto-OOB. Gradle's `installDebug` task hung
  during package installation, but installing the generated APK directly with
  `adb install -r` succeeded and the phone relaunched authenticated on the
  Steward home screen.
- Completed-conversation proof before the lowering showed the command path is
  sound: conversation `2dcbde9f-eafb-4bb0-8664-236ac9b30a73` opened
  Forecasting, completed `ui.window.open`, completed `ui.window.setFormData`,
  and set `prefill.scope.targetKey="line:7288336"`. Android screenshots under
  `/tmp/agently-mobile-verify/android-forecasting-line-7288336*.png` show the
  native Forecasting builder but predate the filter-lowering visual proof.
- Latest fresh replay after the lowering created conversation
  `3e0be0d6-00af-4125-8ab4-6845db7fc999` and turn
  `e02ae938-90c4-4142-9c79-2d281f5b4ea9`. Server logs show
  `ui.window.setFormData` returned `ok=true`, but the turn remained running
  behind current Steward MCP discovery timeouts and was canceled to avoid
  leaving the phone in a stuck streaming state.
- Fixed a shared Agently-core Android SDK restore edge that blocked this
  verification path. Completed conversations can retain buffered live execution
  groups after streaming ends; the restore helper was treating any buffered
  live group as an active-turn signal and suppressing durable transcript
  workspace restore. The helper now suppresses durable restore only when an
  active turn id is present, preserving the active-SSE-vs-history separation
  while allowing completed hosted workspaces to rehydrate.
- Verified the restore fix:
  `cd /Users/awitas/go/src/github.com/viant/agently-core/sdk/android &&
  ./gradlew clean testDebugUnitTest --tests '*WorkspaceRestoreTest'
  --no-daemon --console=plain` passed, and
  `cd /Users/awitas/go/src/github.com/viant/agently/android &&
  ./gradlew :app:testDebugUnitTest --tests '*HostedWorkspaceRestoreTest'
  --no-daemon --console=plain` passed with only the existing
  `FeedRuntimeTest` warning.
- Rebuilt and installed the Agently Android debug APK against local `:9292`.
  Reopening completed conversation `2dcbde9f-eafb-4bb0-8664-236ac9b30a73`
  now restores the hosted Forecasting workspace instead of transcript-only.
  Phone evidence:
  `/tmp/agently-mobile-verify/android-phone-completed-forecasting-restored-after-sdk-fix.png`.
- Android phone filter proof is now captured. The restored Forecasting surface
  renders the canonical predicate-derived filter body, including Channels,
  Date Range, Advanced filters, Inventory, Location, and Add line controls.
  Scrolled evidence:
  `/tmp/agently-mobile-verify/android-phone-completed-forecasting-restored-filters-scrolled.png`.
  Tapping Inventory Add line opens a Publisher row, and tapping Publisher opens
  the filter selector with Publisher, Site Type, Site List, Deal / PMP, and
  External Deal options. Evidence:
  `/tmp/agently-mobile-verify/android-phone-forecasting-inventory-add-line.png`
  and
  `/tmp/agently-mobile-verify/android-phone-forecasting-publisher-filter-open.png`.
- Ported the same generic predicate-lowering parity to Forge iOS. Swift now
  decodes `predicates`, `predicateBuckets`, and `predicateGroups` and lowers
  them at the report-builder config boundary, with no Steward, Forecasting,
  line-id, prompt-text, workspace, or window-id routing.
- Verified iOS Forge report-builder parity:
  `cd /Users/awitas/go/src/github.com/viant/forge &&
  swift test --package-path ios --filter
  'ReportBuilder|WindowMetadataDecodesLatestReportBuilderFields'` passed 22
  tests, including the new
  `testReportBuilderPredicateLoweringDerivesFiltersGroupsAndFamilies`.
- Android tablet proof is now captured as well. On the `Pixel_Tablet` AVD,
  the emulator gateway routes to `10.0.2.2` and `10.0.3.2` were unreachable,
  so local Steward on `:9292` was exposed through
  `adb reverse tcp:9292 tcp:9292` and the app was first-run configured to
  `http://localhost:9292`. Auto-OOB completed, the tablet opened completed
  conversation `2dcbde9f-eafb-4bb0-8664-236ac9b30a73`, and the hosted
  Forecasting workspace restored from history with canonical predicate-derived
  filter controls. Evidence:
  `/tmp/agently-mobile-verify/android-tablet-after-clear-auto-oob-poll.png`,
  `/tmp/agently-mobile-verify/android-tablet-localhost-selected-oob-home.png`,
  `/tmp/agently-mobile-verify/android-tablet-completed-forecasting-restored.png`,
  and
  `/tmp/agently-mobile-verify/android-tablet-completed-forecasting-restored-loaded.png`.
- iPhone and iPad predicate-filter proof is now captured through the live UI
  test harness against local Steward on `127.0.0.1:9292`. The
  `ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer`
  flow now waits up to 300 seconds for the hosted Forecasting pane, then
  verifies actual predicate-derived controls from the accessibility tree:
  Date Range, Channels, Inventory, Location, and Add line. It keeps an XCTest
  attachment named `Forecasting predicate filters`.
- iPhone 17 simulator `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` passed with
  `AGENTLY_IOS_LIVE_UI_TESTS=1`,
  `AGENTLY_IOS_UI_TEST_BASE_URL=http://127.0.0.1:9292`, and the encrypted OOB
  secret reference. Result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-iphone/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_20-29-22-+0200.xcresult`.
  The passing run observed Forecasting, Filters, Channels, Date Range,
  Advanced filters, Inventory, Add line, Location, Data, Demographics, Device,
  Quality, and Context.
- iPad Pro 11-inch (M5) simulator `B2AA0D68-7312-4CC9-85B8-0544341A942D`
  passed the same live UI test against `127.0.0.1:9292`. Result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-ipad/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_20-34-56-+0200.xcresult`.
  The passing run observed Forecasting, Filters, Channels, Date Range,
  Advanced filters, Inventory, Add line, Location, Data, Demographics, Device,
  and Quality.
- Current predicate-filter status: Android phone, Android tablet, iPhone, and
  iPad all prove native Forecasting predicate filter rendering. Android phone
  additionally proves filter opening by adding Inventory Publisher and opening
  the selector. Fresh full line-targeting-expression prefill still depends on
  a healthy Steward MCP lane, but the completed proof confirms target-key
  prefill plus native predicate-filter rendering across all four mobile
  targets.

## 2026-08-05 iOS Predicate Verification Hardening

- Independent review found the new iOS live UI assertion was too broad: it
  scanned all app accessibility labels, accumulated matches across scroll
  states, and could theoretically pass against transcript or unrelated hosted
  content.
- Tightened the proof path without adding Steward logic to Agently. Forge iOS
  now exposes generic report-builder accessibility identifiers for filter
  summary, static filters, dynamic filters, dynamic families, groups, and Add
  line buttons. The Agently iOS live UI test now checks exact Forge
  identifiers instead of user-facing copy:
  `forge-report-builder-filter-summary`,
  `forge-report-builder-static-filter-dateRange`,
  `forge-report-builder-dynamic-filters`,
  `forge-report-builder-dynamic-family-inventory`, and
  `forge-report-builder-add-line-inventory`.
- A first live iPhone rerun proved the initial parent-level
  `forge-report-builder` identifier flattened child identifiers in SwiftUI's
  accessibility tree. A second rerun showed section-level identifiers could
  smear onto descendants as well, so the proof identifiers now live on concrete
  leaf text/buttons instead of layout containers. The interrupted reruns should
  not be counted as passing proof for the tightened assertion.
- Verified after the leaf-identifier cleanup:
  `cd /Users/awitas/go/src/github.com/viant/forge &&
  swift test --package-path ios --filter 'ReportBuilder'` passed 22 tests, and
  `git diff --check` passed in Forge, Agently, and Agently-core.
- The hardened exact-identifier live proof now passes on iPhone and iPad
  against local Steward on `127.0.0.1:9292` with OOB bootstrap. iPhone 17
  simulator `59317EFB-ADFE-4A22-817F-4B4F6658AB2E` result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-iphone-identifiers-leaf/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_21-59-17-+0200.xcresult`.
  iPad Pro 11-inch (M5) simulator `B2AA0D68-7312-4CC9-85B8-0544341A942D`
  result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-ipad-identifiers-leaf/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.05_22-05-21-+0200.xcresult`.
  Both bundles report one test, no errors, no failures, and action status
  `succeeded`.
- Push status: `git push origin main` from
  `/Users/awitas/go/src/github.com/viant/agently` was rejected by GitHub with
  `Permission to viant/agently.git denied to awitas_viant`; the branch is
  still only one local commit ahead of `origin/main`, so this is credentials,
  not a merge conflict. SSH is not usable from this machine either:
  `git@github.com: Permission denied (publickey)`.

## 2026-08-05 Android And Boundary Refresh

- Re-ran the Forge Android report-builder predicate slice from
  `/Users/awitas/go/src/github.com/viant/forge/android`:
  `./gradlew :sdk:testDebugUnitTest --tests '*ReportBuilderPredicatesTest'
  --tests '*ReportBuilderStateStorageTest' --tests '*TargetingTest'
  --no-daemon --console=plain`. It finished `BUILD SUCCESSFUL` in 2m11s.
- Re-ran the shared Agently-core Android SDK restore slice from
  `/Users/awitas/go/src/github.com/viant/agently-core/sdk/android`:
  `./gradlew clean testDebugUnitTest --tests '*WorkspaceRestoreTest'
  --no-daemon --console=plain`. It finished `BUILD SUCCESSFUL` in 1m41s.
- Re-ran the Agently Android app integration restore slice from
  `/Users/awitas/go/src/github.com/viant/agently/android`:
  `./gradlew :app:testDebugUnitTest --tests '*HostedWorkspaceRestoreTest'
  --no-daemon --console=plain`. It finished `BUILD SUCCESSFUL` in 4m15s and
  compiled the local Forge and Agently-core SDK modules in the app build.
- Cleaned a local Kotlin warning in the modified Forge Android
  `ReportBuilderRenderer.kt` by giving the nested measure loop an explicit
  lambda label. The focused Forge Android slice still passed afterward
  (`BUILD SUCCESSFUL` in 2m55s), and the Agently Android hosted-restore
  integration was rerun afterward with the local Forge SDK recompiling inside
  the app build (`BUILD SUCCESSFUL` in 3m48s). Remaining Kotlin warnings are
  pre-existing in unrelated Forge UI files.
- Re-ran the Agently-core service-agent status/error recovery slice:
  `go test ./service/agent -run 'TestServiceRunPlanAndStatus_'` from
  `/Users/awitas/go/src/github.com/viant/agently-core`; it passed in 3.506s.
- Production boundary scans remain clean for the strict separation rule:
  Forge Android/iOS production source contains no Steward, Forecasting,
  line-id, stale `9191`, or UI command routing assumptions; Agently-core SDK
  and service production source contain no Steward, Forecasting, line-id,
  stale `9191`, or ad-targeting fixture strings; Agently Android/iOS
  production source contains no Forecasting, line-id, AdTargetingProfile, or
  stale `9191` logic. The mobile apps still intentionally contain workspace
  endpoint presets for Steward production and local `9292` development.
- Cleaned stale Agently-side mobile overlay docs:
  `/Users/awitas/go/src/github.com/viant/agently/ios-app.md` and
  `/Users/awitas/go/src/github.com/viant/agently/android-app.md` now point to
  `agently-core/mobile_sdk-progress/README.md` and
  `agently-core/mobile_sdk-progress/resume.md` as the canonical shared mobile
  handoff. The deleted `mobile_sdk/README.md` absolute path is no longer
  advertised as a runnable instruction.
- Rechecked the shared mobile SDK public-surface parity contract. The only
  active exceptions in `sdk/mobile_parity_exceptions.json` remain the Go-only
  `mode` transport discriminator for Android and iOS, both expiring
  2026-12-31. `go test ./sdk -run
  'TestMobileSDKPublicSurfacesCoverClientContract'` passed, and the broader
  focused SDK slice `go test ./sdk -run
  'TestMobile|TestMetadataTarget|TestWorkspace|TestForge|TestLookups'` also
  passed.

## 2026-08-05 Dashboard Compatibility Refresh

- Refreshed the Forge iOS dashboard/backward-compatibility slice:
  `swift test --package-path /Users/awitas/go/src/github.com/viant/forge/ios
  --filter 'Dashboard|InlineReport|ReportDocument'`. It executed 50 selected
  tests with 0 failures, covering dashboard compatibility blocks, legacy
  forecast categories, report-builder variants, dashboard runtime actions,
  filters, selections, summaries, and inline report compilation.
- Refreshed the Forge Android dashboard/backward-compatibility slice:
  `./gradlew :sdk:testDebugUnitTest --tests '*DashboardModelsTest' --tests
  '*DashboardRuntimeTest' --tests '*DashboardRendererSupportTest' --tests
  '*InlineReportRuntimeCompilerTest' --no-daemon --console=plain` from
  `/Users/awitas/go/src/github.com/viant/forge/android`. It finished `BUILD
  SUCCESSFUL` in 4m11s. Remaining Kotlin warnings are pre-existing in unrelated
  Forge UI files.
- Refreshed Agently-core's SDK dashboard/inline-report normalization and
  transcript hydration slice:
  `go test ./sdk -run
  'Test(NormalizeRenderedContent|Handler_GetTranscript_HydratesPersistedInlineReports|ApplyInlineReportWorkspaceCatalogToState|BackendClient_|HTTPClient_GetForgeWindowMetadata)'`
  from `/Users/awitas/go/src/github.com/viant/agently-core`; it passed in
  8.851s.
- This pass did not require product-code changes. It strengthens the evidence
  that dashboard/report-document backward compatibility remains aligned across
  Forge iOS, Forge Android, and the Agently-core SDK transcript/rendered
  content boundary.

## 2026-08-05 Backend Window Import And Target Refresh

- Rechecked Agently-core's Forge window loader support for `$import(...)`,
  keyed report-builder imports, and broad target override keys such as
  `mobile`, `tablet`, `phone`, `android`, `android:phone`, and `iosTablet`.
  `go test ./service/ui/window/...` passed from
  `/Users/awitas/go/src/github.com/viant/agently-core`.
- Rechecked workspace metadata target/platform coverage with
  `go test ./service/workspace -run
  'Test.*Metadata|Test.*Target|Test.*Platform|Test.*Forge|Test.*Legacy'`;
  it passed in 3.488s.
- No backend code changes were needed. This keeps the current target
  customization model generic in Agently-core while leaving Steward-specific
  window content in the Steward workspace and rendering behavior in Forge.

## 2026-08-05 Current-Diff Boundary Audit

- Reviewed the current changed production diffs in Agently-core, Forge Android,
  Forge iOS, and Agently iOS for Steward leakage, Forecasting/line-id
  hardcoding, stale `9191`, UI-command routing assumptions, and
  fallback/heuristic drift. No new product-code patch was needed from this
  audit.
- The Forge predicate lowering remains generic and config-driven. It lowers
  authored `predicates`, `predicateBuckets`, and `predicateGroups` into the
  existing native report-builder model without Steward, Forecasting, line-id,
  prompt-text, workspace, or window-id routing logic.
- The Agently-core Android restore change remains scoped to active-turn
  separation: only an active live turn suppresses durable hosted-workspace
  restore, so leftover buffered live execution groups after turn completion no
  longer hide historical hosted content.
- Focused symmetry checks passed after the audit:
  `./gradlew :sdk:testDebugUnitTest --tests '*ReportBuilderPredicatesTest'
  --no-daemon --console=plain` from Forge Android finished `BUILD SUCCESSFUL`
  in 45s, and
  `swift test --package-path /Users/awitas/go/src/github.com/viant/forge/ios
  --filter 'testReportBuilderPredicateLoweringDerivesFiltersGroupsAndFamilies'`
  passed 1 selected test with 0 failures.

## 2026-08-05 Steward Contract Refresh

- Rechecked the current Steward workspace on `ENG-54517`; local edits are still
  confined to Steward prompts, skills, intake rules, and Forge window/reporting
  assets. No Agently or Forge code change was needed by this pass.
- Ran the focused Steward/Forge/Forecasting contract set with
  `FORGE_ROOT=/Users/awitas/go/src/github.com/viant/forge`:
  `forecastingCubeBuilder.test.js`,
  `forecastingCubeBuilder.predicates.test.mjs`,
  `metricReportBuilder.test.js`,
  `metricReportBuilder.predicates.test.mjs`,
  `metricReportBuilder.sharedEndpointDatasets.test.mjs`,
  `metricReportBuilder.windowParams.test.mjs`,
  `reportPresetPrimitiveCoverage.test.mjs`,
  `skills/forecast-targeting.contract.test.mjs`,
  `templates/audienceForecastDashboard.contract.test.mjs`, and
  `agents/steward/prompt/reportingDelivery.contract.test.mjs`.
- The contracts passed, including line builder-prefill preserving
  `line:<requested line id>`, Forecasting/Metric unified predicates lowering to
  legacy runtime structures, request parity across canonical and lowered config
  forms, shared-endpoint dataset save/reopen durability, canonical window
  parameters, report primitive coverage, audience forecast dashboard export,
  and reporting delivery checks.
- Node emitted existing module-type warnings while importing Forge ES modules
  from files without package-level `"type": "module"`; no contract failed.

## 2026-08-05 Mobile Endpoint Boundary Fix

- Removed Steward production endpoint presets from generic Agently Android and
  iOS app source. Both apps now keep generic local `:9292` development presets
  in source and accept workspace-specific presets from configuration:
  Android uses Gradle property `agently.android.workspaceEndpointsJson` or
  `AGENTLY_WORKSPACE_ENDPOINTS_JSON`; iOS uses
  `AGENTLY_WORKSPACE_ENDPOINTS_JSON` or launch argument
  `--workspaceEndpointsJSON=...`.
- Steward remains available for local/dev builds by passing the configured
  endpoint JSON, but the generic Agently mobile app source no longer embeds
  `https://steward.agently.viantinc.com`.
- Verification passed:
  `./gradlew :app:testDebugUnitTest --tests '*AppSettingsRuntimeTest'
  --tests '*AppEndpointConfigTest' --no-daemon --console=plain` from Agently
  Android finished `BUILD SUCCESSFUL` in 1m42s, and
  `swift test --package-path /Users/awitas/go/src/github.com/viant/agently/ios
  --filter 'AuthRuntimeTests'` passed 13 tests with 0 failures after the iOS
  endpoint parser and actor-isolation cleanup.
- The Android configured-endpoint compile path was also verified with
  `AGENTLY_WORKSPACE_ENDPOINTS_JSON='[{"title":"Steward","subtitle":"Viant Steward workspace","value":"https://steward.agently.viantinc.com"}]'
  ./gradlew :app:compileDebugKotlin --no-daemon --console=plain`; it finished
  `BUILD SUCCESSFUL` in 2m22s.
- A production-source scan of Agently Android and iOS found no remaining
  Steward production URL, `AdTargetingProfile`, hardcoded `line:7288336`, or
  stale `9191` matches.

## 2026-08-05 Broader Mobile App Verification

- After the endpoint-boundary fix, the broader Agently Android app lane passed:
  `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest --no-daemon
  --console=plain` from `/Users/awitas/go/src/github.com/viant/agently/android`
  finished `BUILD SUCCESSFUL` in 2m23s.
- The focused Agently iOS foundation lane passed:
  `swift test --package-path /Users/awitas/go/src/github.com/viant/agently/ios
  --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests|HostedWorkspacePolicyTests'`.
  It executed 34 selected tests with 0 failures.
- This iOS pass covered configured endpoint injection, mobile OAuth/OOB auth,
  developer session credential paste handling, hosted workspace reuse policy,
  hosted workspace title presentation, Forge datasource input preservation,
  lookup datasource shape, and composer lookup/focus behavior.
- Re-ran mobile production-source leakage scan afterward; Agently Android/iOS
  production source still has no Steward production URL, `AdTargetingProfile`,
  hardcoded `line:7288336`, or stale `9191` matches.

## 2026-08-05 Full Agently iOS Foundation Verification

- Ran the full Agently iOS foundation package after the endpoint-boundary fix:
  `swift test --package-path /Users/awitas/go/src/github.com/viant/agently/ios`.
  It executed 93 tests with 0 failures.
- The full package pass covered endpoint injection/auth, active-turn transcript
  separation, hosted workspace restore and layout policy, Forge transcript
  block adaptation, Forge datasource loading, composer lookup behavior,
  approval callback/editing helpers, elicitation validation, branding, and
  platform target context.
- Re-ran mobile production-source leakage scan and diff hygiene afterward:
  Agently Android/iOS production source still has no Steward production URL,
  `AdTargetingProfile`, hardcoded `line:7288336`, or stale `9191`; `git diff
  --check` passed in Agently, Agently-core, Forge, and Steward.

## 2026-08-05 Agently Merge and Forge Full-Lane Verification

- Fetched Agently origin and merged the incoming `origin/main` version update
  (`v0.3.96`) into local `main`. The merge completed cleanly and preserved the
  uncommitted mobile endpoint-boundary edits.
- Agently local `main` is now ahead of `origin/main` by two commits: the local
  Android auth bootstrap stabilization commit and the merge commit. Pushing is
  blocked by GitHub credentials, not by branch divergence:
  `Permission to viant/agently.git denied to awitas_viant`.
- Ran full Forge iOS package verification:
  `swift test --package-path /Users/awitas/go/src/github.com/viant/forge/ios`.
  It executed 223 tests with 0 failures.
- Ran full Forge Android SDK verification:
  `./gradlew :sdk:testDebugUnitTest --no-daemon --console=plain` from
  `/Users/awitas/go/src/github.com/viant/forge/android`. It finished
  `BUILD SUCCESSFUL` in 1m45s.
- During the full Forge Android pass, the metadata-loader test exposed a
  test-only timing issue: it waited a fixed `50ms` after launching asynchronous
  metadata loading on `Dispatchers.IO`. The test now waits for the metadata
  signal to become non-null with a bounded timeout, preserving production code
  and making the assertion match the runtime contract.
- Re-ran the focused Forge Android metadata-loader test after cleanup:
  `./gradlew :sdk:testDebugUnitTest --tests
  'com.viant.forgeandroid.runtime.ActionHookRuntimeTest.openWindowUsesRegisteredMetadataLoader'
  --no-daemon --console=plain`; it finished `BUILD SUCCESSFUL` in 1m52s.
- Diff hygiene passed afterward in Agently, Agently-core, and Forge with
  `git diff --check`.

## 2026-08-06 Android Native Post-Merge Verification

- Re-ran the default Agently Android compile/unit lane after merging Agently
  `origin/main`: `./gradlew :app:compileDebugKotlin :app:testDebugUnitTest
  --no-daemon --console=plain` from
  `/Users/awitas/go/src/github.com/viant/agently/android`; it finished
  `BUILD SUCCESSFUL` in 2m45s.
- Also ran the configured-endpoint build/install lane with
  `AGENTLY_WORKSPACE_ENDPOINTS_JSON='[{"title":"Local Steward","subtitle":"Local Agently Steward workspace","value":"http://10.0.2.2:9292"}]'
  ./gradlew :app:installDebug --no-daemon --console=plain`. The APK built and
  installed successfully on the attached phone emulator.
- Started the installed app on `emulator-5556` with
  `adb -s emulator-5556 shell am start -n
  com.viant.agently.android/.MainActivity`; Android reported
  `com.viant.agently.android/.MainActivity` as the focused activity and the app
  process was alive.
- Captured native phone evidence at `/tmp/agently-android-5556.png`. The screen
  shows the cleaned authorization prompt with only
  `This workspace requires authorization.`, `Sign in`, and workspace settings;
  a UI hierarchy dump confirmed no visible `OOB` or `session` sign-in noise.
- The attached tablet emulator `emulator-5554` was not trustworthy for this
  pass: Gradle skipped it after adb property fetch timeouts (`Unknown API
  Level`), and direct `adb install` also hung. It needs emulator restart/health
  recovery before tablet native verification can be counted.

## 2026-08-06 iOS Simulator Native Forecast Verification

- Built the iOS app for simulator with
  `xcodebuild -project AgentlyApp.xcodeproj -scheme AgentlyApp -sdk
  iphonesimulator -destination 'generic/platform=iOS Simulator' -derivedDataPath
  .build/xcode-native-20260806 CODE_SIGNING_ALLOWED=NO build`; the build
  succeeded and produced
  `.build/xcode-native-20260806/Build/Products/Debug-iphonesimulator/Agently.app`.
- Installed that app on the booted iPhone 17 and iPad Pro 11-inch simulators.
  Seeded normal persisted workspace selection with `UserDefaults` key
  `agently.ios.settings.apiBaseURL=http://127.0.0.1:9292` and launched with
  `SIMCTL_CHILD_AGENTLY_WORKSPACE_ENDPOINTS_JSON` so the endpoint list remained
  config-driven rather than source-baked.
- iPhone launch evidence: `/tmp/agently-ios-iphone.png` showed the Viant
  Steward mobile shell loading a selected conversation against the local
  workspace. iPad launch evidence: `/tmp/agently-ios-ipad-2.png` showed the
  tablet shell connected to Viant Steward with transcript/composer visible.
- Ran the live iPad UI test for the key prompt:
  `AGENTLY_IOS_LIVE_UI_TESTS=1 AGENTLY_IOS_UI_TEST_BASE_URL=http://127.0.0.1:9292
  xcodebuild -project AgentlyApp.xcodeproj -scheme AgentlyAppLiveUITests
  -destination 'platform=iOS Simulator,id=B2AA0D68-7312-4CC9-85B8-0544341A942D'
  -derivedDataPath .build/xcode-live-ipad-forecast-20260806
  -only-testing:AgentlyAppUITests/ForecastingPrefillUITests/testOpenForecastBuilderPromptCanBeSentFromComposer
  CODE_SIGNING_ALLOWED=NO test`.
- The live UI test passed: 1 test, 0 failures, `** TEST SUCCEEDED **`. It
  tapped New Chat on iPad, typed `open forecast builder for line 7288336`, sent
  the prompt, observed `Forecasting`, and found the required report-builder
  filter-body identifiers:
  `forge-report-builder-filter-summary`,
  `forge-report-builder-static-filter-dateRange`,
  `forge-report-builder-dynamic-filters`,
  `forge-report-builder-dynamic-family-inventory`, and
  `forge-report-builder-add-line-inventory`.
- Test result bundle:
  `/Users/awitas/go/src/github.com/viant/agently/ios/.build/xcode-live-ipad-forecast-20260806/Logs/Test/Test-AgentlyAppLiveUITests-2026.08.06_00-22-13-+0200.xcresult`.

## 2026-08-06 Steward Forecast Builder Prefill Hardening

- Replayed `open forecast builder for line 7288336` from Android phone
  `emulator-5556` against local Steward on `:9292`. The Forecasting workspace
  opened, but the visible builder showed `Filters 0 active`. DB evidence for
  conversation `726c323f-16fe-4d10-a1a8-55a1dd3cdeed` showed the
  `ui/window/setFormData` payload only carried `AdLineId=[7288336]` and
  `prefill.scope.targetKey="line:7288336"`; it did not include normalized
  targeting predicates. This was a Steward orchestration miss, not a Forge or
  mobile renderer miss.
- Hardened Steward-only prompt/intake behavior:
  `intake/activation_rules.yaml` now marks direct line builder-prefill asks with
  `requireTargetingProfile=true` and `requireBuilderPredicatePrefill=true`;
  `prompts/workspace_ui.yaml` now preserves `line:<requested line id>` target
  keys and requires `steward-AdTargetingProfile` before any ID-only
  `setFormData` completion.
- Extended `skills/forecast-targeting.contract.test.mjs` to protect the new
  intake flags, the mandatory profile-read wording, and the no line-to-audience
  target-key rewrite. Verified:
  `node skills/forecast-targeting.contract.test.mjs` and
  `node extension/forge/windows/forecastingCubeBuilder.test.js`.
- Restarted local Agently on `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp` and ran a host-side OOB replay:
  conversation `2a0dfb33-b284-486b-afa0-4655821b52c6`. The corrected tool order
  was `steward/AdTargetingProfile`, `llm/skills/activate(forecast-targeting)`,
  `steward/ForecastingTargetingConvert`, then UI open. The converter response
  resolved `includeCountry=["US"]`, `includePostalCodeList=[70731]`, and
  `includeDealsPmp=[64512,66016,76060,76084,76105,76162,76711,89531,90473,90476,90482,98075,143925,143934,144156,146708,148790,149114]`.
- The host replay correctly failed only at `ui/view/open` because a CLI
  conversation has no attached live UI client. Android post-fix native replay
  is still pending: after clearing emulator data the phone reached the
  one-time workspace selector, but dev OOB bootstrap did not complete before
  the check window. Re-run on an authenticated attached mobile UI before
  counting this as full mobile prefill closure.

## 2026-08-06 Android Native Forecast Prefill Closure

- Rebuilt and installed the Agently Android debug APK on phone emulator
  `emulator-5556` against local Steward on `:9292`:
  `./gradlew :app:assembleDebug --no-daemon --console=plain` followed by
  `adb -s emulator-5556 install -r app/build/outputs/apk/debug/app-debug.apk`.
  The build completed successfully and the install reported `Success`.
- Cleared only the phone app data and launched
  `com.viant.agently.android/.MainActivity`. The one-time workspace selector
  defaulted to `Android Host 9292` (`http://10.0.2.2:9292`). After continuing,
  the auth screen stayed low-noise: `This workspace requires authorization.`,
  `Sign in`, workspace settings, plus the collapsed debug-only
  `Use developer session` helper.
- Generated a fresh local OOB session on the running `:9292` server:
  `POST /v1/api/auth/oob` returned
  `sessionId=5c0b1c8e-5520-493e-9eb6-7ec9b566a262`. Verified
  `POST /v1/api/auth/session/attach` returned `200 OK` for that fresh session
  and for the existing local session used in the Android debug helper.
- Submitted the debug session helper on Android. The app authenticated and
  showed the Steward mobile home/composer against local `:9292`. UI evidence
  showed starter tasks, recent conversations, and a visible new-message
  composer.
- Ran the native semantic replay:
  `ADB="$HOME/Library/Android/sdk/platform-tools/adb"
  ./scripts/android-semantic-compose-replay.sh --device emulator-5556 --prompt
  "open forecast builder for line 7288336" --expect "Forecasting" --wait 120`.
  The wrapper completed with `verified: Forecasting` and `done`.
- Server and DB evidence for conversation
  `a9acab62-b195-4bff-a7f4-046c7fe7e130` showed the full corrected mobile tool
  order completed: `steward/AdTargetingProfile`,
  `llm/skills/activate`, `steward/ForecastingTargetingConvert`,
  `ui/window/list`, `ui/view/open`, and `ui/window/setFormData`.
- Android visible UI evidence showed the Forecasting report builder open with
  `Filters` and `5 active`, not the previous `0 active` state. The persisted
  `ui/window/setFormData` request payload carried the converted predicates:
  `includeCountry=["US"]`, `includePostalCodeList=[70731]`,
  `includeDealsPmp=[64512,66016,76060,76084,76105,76162,76711,89531,90473,90476,90482,98075,143925,143934,144156,146708,148790,149114]`,
  `sharedIncludeFilters` for PMP/location/postal code, and
  `scope.targetKey="line:7288336"`.
- This closes the Android phone native proof gap for the forecast-builder
  predicate prefill path.
- Android tablet native proof is also now closed on `emulator-5554`. The Pixel
  Tablet emulator could not reach the gateway route
  `10.0.2.2:9292` (`Network is unreachable`), so the trusted local path is
  `adb reverse tcp:9292 tcp:9292` plus the app's `Localhost 9292` workspace
  endpoint (`http://127.0.0.1:9292`). After rebuilding and reinstalling the
  debug APK, the quiet auth screen authenticated through the debug-only
  developer session helper and the tablet layout showed
  `Backend http://127.0.0.1:9292`.
- Ran the tablet native semantic replay:
  `ADB="$HOME/Library/Android/sdk/platform-tools/adb"
  ./scripts/android-semantic-compose-replay.sh --device emulator-5554 --prompt
  "open forecast builder for line 7288336" --expect "Forecasting" --wait 120`.
  The wrapper completed with `verified: Forecasting` and `done`.
- Server and DB evidence for tablet conversation
  `24d86d0b-cfa9-4551-b7b3-33294f288dfd` showed the same corrected tool order:
  `steward/AdTargetingProfile`, `llm/skills/activate`,
  `steward/ForecastingTargetingConvert`, `ui/window/list`, `ui/view/open`,
  and `ui/window/setFormData`.
- Android tablet visible UI showed Forecasting open with `Filters` and
  `5 active`. Screenshot evidence:
  `/tmp/agently-tablet-forecasting.png`. The persisted
  `ui/window/setFormData` request payloads
  `54ef70b9-eedb-434a-9073-097d91ef1224` and
  `d5d972b9-6031-488f-bf01-30152e1b40ca` carried the converted predicates:
  `includeCountry=["US"]`, `includePostalCodeList=[70731]`,
  `includeDealsPmp=[64512,66016,76060,76084,76105,76162,76711,89531,90473,90476,90482,98075,143925,143934,144156,146708,148790,149114]`,
  and `scope.targetKey="line:7288336"`.

## 2026-08-06 Native Auth and Boundary Verification Refresh

- Re-ran the focused Agently iOS foundation slice:
  `swift test --package-path ios --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests|HostedWorkspacePolicyTests'`
  from `/Users/awitas/go/src/github.com/viant/agently`. It passed 34 selected
  tests with 0 failures. Coverage included configured workspace endpoints,
  mobile OAuth redirect rejection of web callbacks, OOB/developer-session auth,
  hosted workspace reuse/notice policy, Forge datasource input preservation,
  and composer lookup behavior.
- Re-ran Agently-core shared restore/error durability checks:
  `./gradlew testDebugUnitTest --tests '*WorkspaceRestoreTest' --no-daemon
  --console=plain` from
  `/Users/awitas/go/src/github.com/viant/agently-core/sdk/android`, and
  `go test ./service/agent -run 'TestServiceRunPlanAndStatus_' -count=1` from
  `/Users/awitas/go/src/github.com/viant/agently-core`. Both passed.
- Re-ran production-only boundary scans after the mobile auth/session changes.
  Agently Android/iOS production source, Agently-core production source, and
  Forge production source had no matches for Steward production URL leakage,
  `AdTargetingProfile`, hardcoded `line:7288336` / `7288336`, or stale mobile
  `9191` endpoints. Forge production also had no Agently imports.

## 2026-08-06 Forge and Steward Contract Refresh

- Re-ran focused Forge mobile report-builder/dashboard compatibility checks.
  Forge iOS passed:
  `swift test --package-path ios --filter 'ReportBuilder|MetadataResolver'`
  with 27 selected tests and 0 failures. Forge Android passed:
  `./gradlew :sdk:testDebugUnitTest --tests '*ReportBuilderPredicatesTest'
  --tests '*ReportBuilderStateStorageTest' --tests '*TargetingTest'
  --no-daemon --console=plain`.
- Re-ran Steward forecasting contracts with
  `FORGE_ROOT=/Users/awitas/go/src/github.com/viant/forge`:
  `node extension/forge/windows/forecastingCubeBuilder.test.js`,
  `node extension/forge/windows/forecastingCubeBuilder.predicates.test.mjs`,
  and `node skills/forecast-targeting.contract.test.mjs`. The suite passed
  and continues to require the Steward-owned targeting-profile and predicate
  prefill path before forecast-builder form population is counted complete.
- Re-ran Steward report-builder/window contracts:
  `node extension/forge/windows/metricReportBuilder.test.js`,
  `node extension/forge/windows/metricReportBuilder.predicates.test.mjs`,
  `node extension/forge/windows/metricReportBuilder.windowParams.test.mjs`,
  and
  `node extension/forge/windows/metricReportBuilder.sharedEndpointDatasets.test.mjs`.
  These checks passed for predicate lowering, seam-only prefills, canonical
  and alias parameters, and shared-endpoint dataset save/reopen behavior.
- Re-ran Steward report preset/dashboard/prompt contracts with exported
  `FORGE_ROOT`: `node extension/forge/windows/reportPresetPrimitiveCoverage.test.mjs`,
  `node templates/audienceForecastDashboard.contract.test.mjs`, and
  `node agents/steward/prompt/reportingDelivery.contract.test.mjs`.
  The dashboard export contract and reporting delivery contract passed. Node
  still emits existing module-type warnings while importing Forge ES modules;
  no runtime contract failed.
- Publishing the committed Agently mobile auth bootstrap changes remains
  blocked by GitHub credentials rather than branch state. `git fetch origin
  main` left Agently at `0` behind and `2` ahead, but `git push origin main`
  returned `Permission to viant/agently.git denied to awitas_viant`.
- Cleaned the untracked mobile helper scripts before they become part of the
  handoff. `install-android-oob-debug.sh` now defaults to the local Android
  emulator lane `http://10.0.2.2:9292`, `launch-ios-oob-sim.sh` defaults to
  `http://127.0.0.1:9292`, and both require the OOB secret reference through
  environment variables instead of embedding a personal secret path or a
  Steward production endpoint. Verified with `bash -n` for all three scripts,
  `./scripts/android-semantic-compose-replay.sh --self-test`, and a production
  leakage scan over Agently app/script paths.
- Re-ran the focused Agently Android settings/auth slice after the script and
  endpoint cleanup:
  `./gradlew :app:testDebugUnitTest --tests '*AppSettingsRuntimeTest'
  --tests '*AuthRuntimeTest' --tests '*HostedWorkspaceRestoreTest'
  --no-daemon --console=plain`. It passed; the only output noise was existing
  Forge Kotlin warnings.
- Scrubbed remaining exact local secret references from Agently mobile
  verification helpers. The iOS live Forecasting UI test now requires
  `AGENTLY_IOS_UI_TEST_OOB_SECRET` instead of defaulting to a personal OOB
  reference, and `ui/tmp-report-builder-probe.mjs` now requires
  `AGENTLY_PROBE_OOB_SECRET_REF` plus `AGENTLY_PROBE_AUTH_CONFIG_REF` instead
  of embedding local credential paths. A scan for the exact local secret file
  names across Agently, this progress folder, and Forge native production
  sources returned no matches.
- Re-ran iOS verification after the scrub:
  `swift test --package-path ios --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests|HostedWorkspacePolicyTests'`
  passed 34 selected tests with 0 failures, and
  `xcodebuild -quiet -project ios/AgentlyApp.xcodeproj -scheme AgentlyApp
  -configuration Debug -destination 'generic/platform=iOS Simulator'
  -derivedDataPath ios/.build/xcode-scrub build-for-testing
  CODE_SIGNING_ALLOWED=NO` exited with code 0. `node --check
  ui/tmp-report-builder-probe.mjs` also passed.
- Classified the untracked Agently historical app-plan notes
  (`android-app.md`, `ios-app.md`, `froge-fence.md`) as non-authoritative
  context only; the active handoff remains this folder. Verified the untracked
  Agently Go contract tests before packaging decisions:
  `go test . -run 'TestWorkspaceReporting' -count=1` and
  `go test ./tools/system/platform -run 'TestService_' -count=1` both passed.
- Rechecked the untracked Forge report-builder preview/runtime assets. Running
  every `src/demos/reportBuilder/*.test.js` directly passed, including preview
  metrics, semantic validation/model behavior, runtime interaction/session,
  saved-payload, authored-report, detail-target, and forecast drill ladder
  coverage. The project runner
  `npm run test:authored-runtime -- --no-color` also passed end to end,
  including hosted Steward builder render smoke, report-builder loading icon
  fixed-position rotation coverage, authored runtime semantic sections,
  export/import fixture families, and runtime preview helpers.
- Packaging note: the untracked Forge report-builder preview/runtime files are
  required assets, not scratch. Tracked files already reference them:
  `package.json`, `scripts/run-authored-runtime-unit-tests.mjs`,
  `scripts/verify-semantic-preview-phase1.mjs`,
  `src/demos/reportBuilder/ReportBuilderPreview.jsx`,
  `src/components/dashboard/DashboardReportRuntime.test.js`, and
  `src/reporting/fixtures/capacityPreviewSavedReportRecordBuilder.js` import
  or execute these helpers/tests. Before creating the Forge change, include the
  36 untracked candidates under `src/demos/reportBuilder`, plus native
  `ReportBuilderPredicates` source/tests. Hygiene checks found no personal
  secret refs, stale mobile `9191`, or Agently imports in those untracked
  assets; Steward refs are fixture model/target identifiers in demo tests.
  `node --check` passed for every untracked JS asset.

## 2026-08-06 Agently and Agently-Core Packaging Refresh

- Re-ran the exact Agently-core regressions covering the changed service and
  Android SDK behavior. `go test ./service/agent -run
  'TestServiceRunPlanAndStatus_(RecoversDurableFinalAssistantContent|EmptyResultUsesLastFailedModelCallError)'
  -count=1` passed, proving durable assistant-content recovery still works
  and empty final-output failures now surface the last failed model-call error
  instead of collapsing to `no final content produced`. Android SDK restore
  coverage also passed with `./gradlew testDebugUnitTest --tests
  '*WorkspaceRestoreTest' --no-daemon --console=plain` from
  `agently-core/sdk/android`.
- Classified Agently untracked packaging items. `ios-app.md` is referenced by
  tracked `ios/README.md`, so it must either be included with the Agently
  package or the tracked reference must be updated. `android-app.md` and
  `froge-fence.md` are historical context docs and remain non-authoritative.
  The untracked scripts under `agently/scripts/` are verified local mobile
  launch/replay helpers referenced by this handoff; include them if the
  handoff is packaged. The untracked Go tests
  `reporting_registry_test.go` and `tools/system/platform/service_test.go`
  are verified contract tests and should be included if preserving this test
  coverage is desired.
- Hygiene for Agently untracked items passed: no exact local secret refs, stale
  mobile `9191`, hardcoded forecast line id, or Steward targeting tool names
  were found. `bash -n` passed for every untracked shell script, and
  `node --check ui/tmp-report-builder-probe.mjs` passed.
- Agently-core untracked documentation classification: include
  `mobile_sdk-progress/README.md` with the current mobile/Forge package because
  `mobile_sdk-progress/resume.md` now delegates to it as the authoritative
  handoff. Keep `doc/mcp-2026-extension-upgrade.md` and `doc/mcp-2006/` out of
  this mobile package unless the MCP 2026 workstream is intentionally packaged
  at the same time; those files are a separate proposed protocol-extension
  rollout plan spanning `mcp-protocol`, `mcp`, `mcp-ext`, `agently-core`,
  `agently`, and `forge`, not proof of the mobile SDK / Forge parity closure.
- Agently-core doc hygiene rescan found no exact personal secret references in
  `mobile_sdk-progress/README.md`, `mobile_sdk-progress/resume.md`,
  `doc/mcp-2026-extension-upgrade.md`, or `doc/mcp-2006/`. Remaining `9191`
  references are confined to historical progress notes; the current mobile
  verification lane remains the isolated `:9292` Steward server described in
  the latest status table and native verification sections.
- Agently-core TypeScript SDK test fixtures no longer embed a personal OOB
  secret path. The OOB login fixture now uses a generic example path, an exact
  rescan over `mobile_sdk-progress`, `doc/mcp-*`, `sdk`, and `service` found
  no local user secret file references, and `npm test` from `sdk/ts` passed
  all 366 Vitest tests.

## 2026-08-06 Steward Packaging Refresh

- Classified Steward's untracked `skills/forecast-targeting.contract.test.mjs`
  as required contract coverage for the prompt/intake changes. It asserts that
  line requests stay `AdLineId`, do not masquerade as audience requests, require
  `steward-AdTargetingProfile` before builder prefill, and require resolved
  predicate fields before a successful Forecasting builder population.
- Re-ran the compact Steward verification set with
  `FORGE_ROOT=/Users/awitas/go/src/github.com/viant/forge`:
  `node skills/forecast-targeting.contract.test.mjs`,
  `node extension/forge/windows/forecastingCubeBuilder.test.js`,
  `node extension/forge/windows/forecastingCubeBuilder.predicates.test.mjs`,
  `node extension/forge/windows/metricReportBuilder.predicates.test.mjs`,
  `node extension/forge/windows/reportPresetPrimitiveCoverage.test.mjs`, and
  `node agents/steward/prompt/reportingDelivery.contract.test.mjs`. The suite
  passed; only existing Forge ES-module type warnings were emitted.
- Steward boundary scan over changed and untracked files found no exact local
  secret refs, stale mobile `9191`, or Agently app/source references. The
  report-builder `targetOverrides` stanza is pre-existing, generic, and
  data-driven; the current diff only adds the Forecasting builder via
  `$import(../../forecastingCubeBuilder/shared/content.yaml)`.

## 2026-08-06 Local Packaging and Push Status

- Agently mobile auth/bootstrap and verification-helper changes were packaged
  in local commit `719460dc` on `viant/agently`. The branch remains ahead of
  `origin/main` because GitHub rejects the current HTTPS credentials with
  `Permission to viant/agently.git denied to awitas_viant`.
- Agently-core restore/run durability and mobile progress changes were
  packaged in local commit `320207d` on `viant/agently-core`; this commit also
  carries the TypeScript SDK OOB fixture scrub. Its push is likewise blocked by
  `Permission to viant/agently-core.git denied to awitas_viant`.
- Forge native predicate lowering and report-builder preview/runtime assets
  were packaged in local commit `1cec1ca` on `viant/forge`. Its push is blocked
  by `Permission to viant/forge.git denied to awitas_viant`.
- Steward forecasting/report-builder contract changes were packaged in local
  commit `b262af1`, merged with remote SendGrid commits, verified with the
  compact Steward contract suite, and pushed to `origin/ENG-54517` as merge
  commit `17e2457`.
- Remaining untracked files are intentionally not part of the mobile/Forge
  package: Agently's historical `android-app.md`, `ios-app.md`, and
  `froge-fence.md`; Agently-core's MCP 2026 planning docs under
  `doc/mcp-2026-extension-upgrade.md` and `doc/mcp-2006/`.

## 2026-08-06 Post-Commit Verification Refresh

- Agently post-commit focused verification passed on local commit `719460dc`:
  Android auth/settings/restore unit slice
  `./gradlew :app:testDebugUnitTest --tests '*AppSettingsRuntimeTest'
  --tests '*AuthRuntimeTest' --tests '*HostedWorkspaceRestoreTest'
  --no-daemon --console=plain`, Go reporting registry contract
  `go test . -run 'TestWorkspaceReporting' -count=1`, platform service
  contract `go test ./tools/system/platform -run 'TestService_' -count=1`,
  and the iOS foundation slice `swift test --package-path ios --filter
  'AuthRuntimeTests|HostedWorkspacePresentationTests|ForgeAgentlyDataSourceLoaderTests|ComposerRuntimeTests|HostedWorkspacePolicyTests'`
  all passed. The iOS slice executed 34 selected tests with 0 failures.
- Agently-core post-commit verification passed on local commit `320207d` plus
  progress docs commits: `go test ./service/agent -run
  'TestServiceRunPlanAndStatus_(RecoversDurableFinalAssistantContent|EmptyResultUsesLastFailedModelCallError)'
  -count=1`, Android SDK restore coverage `./gradlew testDebugUnitTest
  --tests '*WorkspaceRestoreTest' --no-daemon --console=plain` from
  `sdk/android`, and the full TypeScript SDK `npm test` from `sdk/ts`. The TS
  suite passed 22 files and 366 tests.
- Forge post-commit focused verification passed on local commit `1cec1ca`:
  Android `./gradlew :sdk:testDebugUnitTest --tests
  '*ReportBuilderPredicatesTest' --tests '*ReportBuilderStateStorageTest'
  --tests '*TargetingTest' --no-daemon --console=plain`, followed by iOS
  `swift test --package-path ios --filter 'ReportBuilder|MetadataResolver'`.
  The iOS slice executed 27 selected tests with 0 failures. Android emitted
  only existing Kotlin warnings.
- Steward post-merge compact verification passed before push to
  `origin/ENG-54517`: `node skills/forecast-targeting.contract.test.mjs`,
  `node extension/forge/windows/forecastingCubeBuilder.test.js`,
  `node extension/forge/windows/forecastingCubeBuilder.predicates.test.mjs`,
  `node extension/forge/windows/metricReportBuilder.predicates.test.mjs`,
  `node extension/forge/windows/reportPresetPrimitiveCoverage.test.mjs`, and
  `node agents/steward/prompt/reportingDelivery.contract.test.mjs` with
  `FORGE_ROOT=/Users/awitas/go/src/github.com/viant/forge`.

## 2026-08-06 Android Forecasting Verification Refresh

- Fixed Agently-core MCP discovery so non-strict tool-surface discovery treats
  transport failures from optional/unrelated MCP servers as an empty optional
  surface and records cooldown, instead of returning a warning/error into the
  turn. Strict discovery and direct tool execution remain unchanged.
- Added regression coverage in
  `internal/tool/registry/registry_discovery_scope_test.go` for the first
  transport failure path and the follow-up cooldown path.
- Verification passed:
  `go test ./internal/tool/registry -run 'TestListServerTools_|TestWithDiscoveryTimeout' -count=1`,
  `go test ./internal/tool/registry -count=1`, and
  `go test ./service/agent -run 'TestServiceRunPlanAndStatus_(RecoversDurableFinalAssistantContent|EmptyResultUsesLastFailedModelCallError)' -count=1`.
- Rebuilt `/tmp/agently-mobile-verify/agently` with a temporary Go workspace
  pointing at the local Agently and Agently-core checkouts, then restarted
  Steward on `:9292` with
  `STEWARD_MCP_URL=http://127.0.0.1:5002/mcp`.
- Android emulator `emulator-5556` verified the prompt
  `open forecast builder for line 7288336` using
  `scripts/android-semantic-compose-replay.sh --expect Forecasting`. Server
  logs showed `ui.window.open` for
  `forecastingCubeBuilder__bd2cb435-4ebd-47d7-b537-c20e08259eb4`,
  `ui.data.fetch`, and successful `ui.window.setFormData`; the Android UI tree
  showed `Forecasting`, `Forecast Inventory`, and `Filters`.
- Added Agently Android bridge coverage for the exact Forecasting-shaped
  `ui.window.setFormData` payload: `reportBuilderRef=forecastingCubeBuilder`,
  `prefill.includeCountry`, PMP deals, postal codes, and scoped ad-order /
  audience ids survive into Forge `peekWindowForm()`. Verification passed with
  `./gradlew :app:testDebugUnitTest --tests '*QueryRuntimeTest' --no-daemon
  --console=plain` from `/Users/awitas/go/src/github.com/viant/agently/android`.
- Added matching Agently iOS bridge coverage for the same Forecasting-shaped
  `ui.window.open` + `ui.window.setFormData` contract. Verification passed with
  `swift test --package-path ios --filter AppleUIBridgeControllerTests`, which
  executed 2 selected tests with 0 failures.
- Remaining verification gap: rerun the live iOS phone/tablet flow after the
  local Agently-core patch is packaged into Agently's dependency path.
