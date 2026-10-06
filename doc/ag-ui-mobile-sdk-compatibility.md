# Mobile SDK compatibility evidence, 2026-10-04

The web migration leaves the existing Swift SDK and mobile UI defaults unchanged.
Swift package tests run natively on macOS; no simulator or iOS app is required.
`Package.swift` targets Swift tools 5.9, iOS 17 and macOS 14, with an SDK library
and one resource-backed test target. This checkout has no iOS SDK README. The
run used Apple Swift 6.3.3 on arm64 macOS.

## Validation

From `sdk/ios`:

```sh
swift test
AGENTLY_AGUI_LIVE_URL=http://127.0.0.1:20541/v1/ag-ui/run \
  swift test --filter 'AgUiTests.testLive'
```

The complete package suite executed 116 tests: 112 passed and four explicitly
configured live contract tests skipped; there were no failures. Log:
`/tmp/agui-ios-sdk-suite-20261004.log`.

The two AG-UI live tests were then enabled against the owned deterministic
assembly fixture, without changing Swift sources or expectations. Both passed:
standard native endpoint/discovery/reduced assistant response, and actual
frontend-tool dispatch followed by a successor and identical durable replay.
Log: `/tmp/agui-ios-sdk-live-fixed-20261004.log`.

The first live attempt exposed a backend regression introduced by the native
approval coordination path: `findAGUIContinuation` attempted to JSON-decode an
absent resume array after completing approval receipts. A standard frontend-tool
result successor legitimately has no resume entries. Initial admission returned
200 with pending frontend tool IDs; the second POST returned 409 with
`unexpected end of JSON input`, before replay. The core implementation now guards
resume decoding when no entries exist. The unchanged Swift live regression
verifies the same successor/replay contract and passes after the
owned backend is rebuilt. Initial diagnostic logs are
`/tmp/agui-ios-sdk-live-20261004.log` and
`/tmp/agui-ios-sdk-live-traced-20261004.log`; they are retained as failure evidence,
not reported as passing checks. The temporary trace proxy omitted credentials.

## Approval HTTP compatibility

Swift `AgentlyClient.decideToolApproval` retains
`POST /v1/tool-approvals/{id}/decision`. Its existing Codable output contains
optional `status` and `message`; extra `protocol` references and real `outcome`
fields are ignored by the decoder. A temporary program linked against the
unchanged compiled SDK successfully decoded the additive current response shape,
including the original status/message. Log:
`/tmp/agui-ios-approval-additive-decode.log`.

Android retains the same endpoint and optional status/message DTO. Its default
JSON configuration sets `ignoreUnknownKeys = true`. Additive response compatibility is supported by the existing serializer
configuration; Android build and JVM validation are recorded below. The mobile DTOs do not expose the new protocol references or
full host outcomes. This evidence does not claim that a mobile app has migrated
to the new web orchestration or supports the new standard MCP Apps host workflow.

The two external actual-server/reporting contract tests still require their
separate ready-file environment variables and were not enabled. Authenticated
mobile UI, iOS simulator/device behavior, Android device/emulator behavior, and
full remote backend mobile UI acceptance remain later gates.

## Android build and actual JVM tests

The Android checkout contains a real Gradle Android-library module, despite its
older extraction README listing a module as future work. The current toolchain is
Gradle wrapper 9.4.1, Android Gradle Plugin 8.4.2, Kotlin 1.9.24, JDK 17.0.18,
compile SDK 35, minimum SDK 26 and JVM target 17. The configured JDK and Android
SDK already exist on this machine; no SDK/toolchain installation was required.

The initial wrapper invocation failed because no SDK location environment variable
was set. Pointing `ANDROID_HOME` to the installed Android SDK resolved it without
modifying sources, build files or local properties. Fresh debug and release JVM
unit tests were explicitly rerun, and both debug/release AARs assembled:

```sh
cd sdk/android
ANDROID_HOME=/Users/awitas/Library/Android/sdk ./gradlew \
  testDebugUnitTest --rerun testReleaseUnitTest --rerun \
  assembleDebug assembleRelease --no-daemon
```

Each variant executed 124 tests: 120 passed, four opt-in live tests skipped, and
zero failures/errors. These are fresh test reports rather than prior cached test
results. Log: `/tmp/agui-android-sdk-fresh-suite-20261004.log`. Preserved report
counts: `/tmp/agui-android-sdk-full-counts-20261004.json`. AARs:
`sdk/android/build/outputs/aar/agently-android-sdk-debug.aar` and
`sdk/android/build/outputs/aar/agently-android-sdk-release.aar`.

The standard native AG-UI endpoint test and actual frontend-tool dispatcher,
successor and identical replay test were then enabled against the owned rebuilt
assembly fixture for **both** debug and release variants. Both tests passed in
each variant (four executions total), with no skips or failures. Log:
`/tmp/agui-android-sdk-live-20261004.log`.

```sh
ANDROID_HOME=/Users/awitas/Library/Android/sdk \
AGENTLY_AGUI_LIVE_URL=http://127.0.0.1:20541/v1/ag-ui/run ./gradlew \
  testDebugUnitTest \
  --tests 'com.viant.agentlysdk.agui.AgUiTest.liveStandardEndpointWhenConfigured' \
  --tests 'com.viant.agentlysdk.agui.AgUiLiveActionsTest.liveHandlerContinuationAndIdenticalReplay' \
  --rerun testReleaseUnitTest \
  --tests 'com.viant.agentlysdk.agui.AgUiTest.liveStandardEndpointWhenConfigured' \
  --tests 'com.viant.agentlysdk.agui.AgUiLiveActionsTest.liveHandlerContinuationAndIdenticalReplay' \
  --rerun --no-daemon
```

No Android SDK source, mobile UI or test expectations were changed. The separate
actual JSON/auth/reporting ready-file tests remain opt-in and were not enabled;
these checks do not substitute for Android emulator/device UI acceptance.
