# Mobile SDK Resume

Resume from `mobile_sdk.md` and `mobile_sdk-progress.md`, not from the older
Android/iOS plan snapshots.

Current task: complete real-device Steward parity for the generic Forge
transcript path without changing Steward data or conversation semantics. The
source migration is complete: canonical `renderedContent` is additive on
transcript and live reducer state, exposed by Go, TypeScript, Android, and iOS
SDK models, and projected by native hosts into Forge's typed canonical parts.

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
