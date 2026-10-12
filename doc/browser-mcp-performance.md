# Browser MCP performance verification

Tool schemas are registered with the conversation's browser catalog. Each
invocation checks a compact catalog identity projection against the current
configuration of that registered server. It does not retransmit immutable tool
schemas or reload unrelated server descriptors. Ownership, expiry, connection
identity and revocation remain checked; this is not an authorization TTL cache.

## Timing boundaries

Use the saved tool call's `startedAt` and `completedAt` for its complete tool
lifecycle. Model generation before dispatch and model continuation after the
result are separate costs. Do not report a native handler benchmark as the
whole proxy's latency.

The browser records bounded UserTiming measurements under
`agently.browserMCP`:

- `handoff.pendingToHandler`, `handoff.terminalToHandler`, `handoff.handlers`
  and `handoff.continuation` measure scheduling and continuation.
- `tool.catalogCurrent`, `tool.nativeRequest`, `tool.resultProjection` and
  `tool.total` measure the tool handler.
- `transport.queue` and `transport.roundTrip` separate waiting for a usable
  connection from a posted request's roundtrip.

Catalog responses include `Server-Timing` entries for `catalog` and `ownership`.
Backend logs contain fixed phase names and numeric durations for deferral,
deferred-boundary waiting, result persistence and continuation. These timings
contain no tool arguments, credentials, image bytes or website content.
Continuation may include the next model call; it is not pure transport time.

## Live verification, 2026-10-12

Two distinct read-only skill-list requests from the embedded Agently UI through
the Chrome extension completed successfully:

| Measurement | First request | Repeat request |
| --- | ---: | ---: |
| Saved tool lifecycle | 233 ms | 438 ms |
| Backend deferral | 35 ms | 32 ms |
| Result persistence | 13 ms | 92 ms |

Earlier comparable discovery calls took about 8.7 seconds. This establishes an
improvement in the updated live configuration, but does not isolate the causal
contribution of compact validation, the refreshed server/page and the rebuilt
bundle. Two samples are not a general latency percentile or a guarantee for
navigation, screenshots or mutations.

The provider response and saved transcript each contain one tool call per
request. Count actual call IDs and persisted outcomes rather than the model's
narrative description. Distinct user-requested calls are not deduplicated by
arguments; replay handling applies only to the same original request identity.

Synthetic tests independently exercise compact validation, immediate
revocation, browser transport and Datly result persistence/continuation. Use
synthetic public data for these tests; live user artifacts are not fixtures.
