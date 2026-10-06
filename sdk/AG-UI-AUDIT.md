# AG-UI SDK protocol audit

The additive SDKs use the pinned AG-UI1.0 vocabulary (31 events) and preserve protocol history/state as the authority for follow-up inputs. The canonical shared reference remains `protocol/agui/testdata/conformance.json`: actual upstream HttpAgent1.0.1 produced 45 normalized events and 10 complete messages. Native mirrors/schema have byte-for-byte synchronization tests. This audit does not claim production shell migration or every Agently feature UI.

| Behavior | TypeScript | Swift | Kotlin |
| --- | --- | --- | --- |
| POST SSE, chunked UTF-8, transport/error outcomes | Real HttpAgent HTTP fixture | URLSession streamed fixture and live backend | OkHttp/MockWebServer and live backend |
| Full roles/media/state, RFC6902, snapshots, opaque continuation | Official reducer with complete-history comparisons | Native store vs shared official projection | Native store vs shared official projection |
| Client definitions/handlers/results/continuation | Explicit registry dispatch, immediate result retention, standard follow-up POST | Explicit actor dispatcher, typed media results, URLSession continuation POST and live native continuation | Explicit suspend dispatcher, typed media results, actual HTTP continuation POST and live native continuation |
| Human interrupts | Exact response IDs, expiry checks, optional synchronous application schema validator | Exact IDs, supported-schema validation or application validator | Exact IDs, supported-schema validation or application validator |
| Nested client tools | Explicit version1 client-tool interrupt dispatch; human/unknown reasons remain unanswered | Same profile; cached validated results and explicit combined resume | Same profile; cached validated results and explicit combined resume |
| Disconnect vs backend cancellation | `abortTransport` vs independent `cancelRun` command while original stream is active | Consuming-task cancellation vs independent `cancelRun` POST | Collection cancellation vs independent `cancelRun` POST |
| Recovery | Defensive `lastPostedInput`, `reconnectFull` identical original POST, fresh initial projection | Re-run `snapshot.input`; fresh verifier/store, full replay | Re-run `snapshot.input`; fresh verifier/store, full replay |
| Capabilities/extensions | Explicit opt-in discovery, version checks, generic command envelope | Explicit envelope/run, validated supported capability snapshot, generic command envelope | Same |
| Standard external-agent transport | No Agently requirement; fixtures have no native-route fallback | Explicit endpoint; standard tool roundtrip has no Agently forwarded properties | Explicit endpoint; standard tool roundtrip has no Agently forwarded properties |

Client-tool dispatchers retain successful validated handler outputs within their lifetime. They do not guarantee external-effect idempotence after an application process restart or a handler that performs an effect and throws. Clear retained outputs only after durable continuation acceptance. Unknown client-tool profile versions are not automatically executed.

Native response/argument validation supports the pinned schema vocabulary plus local `$defs`, `anyOf`, type unions and string/array/numeric bounds. Unsupported schema keywords or references fail closed; applications can inject their own full JSON Schema validator. TypeScript offers synchronous application validation hooks and relies on server validation for authoritative answer checks. JavaScript numeric values follow the official upstream Number representation; the native JSON trees retain exact numeric tokens.

Full replay requires a server that retains accepted runs and the same authenticated principal. Neither transport accepts an arbitrary Last-Event-ID checkpoint with a fresh open-stream verifier. The new live native tests cover actual standard frontend definition/handler/multipart-result continuation and identical-input replay against a fresh mock-backed assembly. Their explicit generated anonymous-cookie header keeps the same scope on loopback HTTP; it does not prove deployed TLS/authentication. Android tests separately cover fallback cookie retention, explicit header precedence and secure/domain/expiry policy. A URLProtocol interceptor is insufficient to prove default Foundation HTTPS cookie-header injection.

Interactive MCP Apps app-scope consent and production web/mobile/CLI shell integration remain separate work. No SDK protocol code falls back to retired native endpoints.

Final verification for this audit: Swift `swift test` with the current mock-backed live URL executed 112 tests, 2 existing deployment tests skipped, 0 failures. Kotlin `testDebugUnitTest` with JDK17/Android35 and that live URL executed 120 tests, 2 existing deployment tests skipped, 0 failures. TypeScript typecheck/build passed; Vitest reported 424 passed and 5 existing deployment tests skipped across 35 files. The owned loopback backend18197/mock18098 were stopped after proof; older processes were not touched.

Semantic-parity follow-up adds native mirrors of the 35-case `reducer-semantics.json` reference, with exact accepted message/state/normalized-event comparisons and all expected rejections asserted. It fixed opener-only chunk metadata producing an extra empty content event, and matched the pinned consumer's one allowed corrective RUN_ERROR after RUN_FINISHED. Event ownership/history verification was retained; this consumer correction allowance does not change backend producer policy. Mirror bytes are checked against the shared canonical file.

After semantic parity fixes, the full offline native suites pass: Swift114 tests/4 existing opt-in skips, Android122 tests/4 existing opt-in skips, zero failures. Both suites compare all 20 accepted cases and require all 15 rejected cases to fail. The previous current-backend live proofs remain separately recorded above; no live server was restarted for this bounded normalizer/verifier follow-up.

Integer follow-up consumes 66 raw-JSON cases from shared `integer-semantics.json` in both native SDKs. Existing exact integer logic accepts integral decimal/exponent notation, rejects fractions/out-of-safe-range values and keeps opaque numeric lexemes. Final offline native suites: Swift116 tests/4 opt-in skips, Android124 tests/4 opt-in skips, zero failures. No integer implementation fix was required.

Backward compatibility rerun, 2026-10-03: complete existing TypeScript SDK
typecheck/build and Vitest pass (424 tests, 5 opt-in skips); complete Swift suite
passes (116 tests, 4 opt-in skips); Android testDebugUnitTest passes (124 tests,
4 opt-in skips). These include legacy client/store/upload/session tests alongside
the additive AG-UI modules. The web/mobile production shells are not migrated.
Logs: /tmp/agui-sdk-ts-backward-20261003.log,
/tmp/agui-sdk-ios-backward-20261003.log,
/tmp/agui-sdk-android-backward-20261003.log.

The current full core compatibility run passes 180 packages, 6,487 test/subtest
records, 52 skips, zero failures (/tmp/agui-core-compat-final-20261003.jsonl).
Two existing test setups were corrected: concurrent async goal fixtures now
share native conversation/goal/queue storage and verify a committed queue; UI
datasource fixtures establish polling heartbeats before requesting commands.
Their original timing/identity assertions and production behavior remain intact.

Authenticated Steward compatibility checks passed on 2026-10-03 through the
existing Go HTTP SDK's AuthLocalOOBSession (PKCE and the deployment's complete
scope set). The supplied Scy credential/client references work. The standalone
helper must register scy/kms/blowfish; the installed CLI attempt also supplied
comma-separated scopes as one scope. Those failed attempts do not indicate an
invalid credential or an AG-UI regression.

The current assembled backend loaded an isolated copy of Steward metadata with
fresh runtime/database/state; original deployment data was excluded. Legacy
session creation/identity, public agents, conversation listing and workspace
metadata for web/iOS/Android targets passed. On the same cookie session,
capabilities, workspace.metadata.get, workspace.publicagents.list and
workspace.models.list passed with nine schema-valid AG-UI events; legacy
identity still passed afterward. Reproducible helper:
../agently-ag-ui/dev/ag-ui/steward-compat/main.go. Evidence:
/tmp/agui-steward-sdk-oob-compat-20261003.log. Tokens stay in memory and no
remote business mutations or live model calls were performed. This proves
shared backend/session/metadata compatibility, not native device UI or every
remote MCP business operation.
