# Optional proactive context compaction

An agent activates proactive compaction only by explicitly setting
`contextCompactionPercent`. Omitting it preserves the existing reactive recovery
and performs no new request preparation, token counting, or compaction.

```yaml
# Agent
contextCompactionPercent: 80
# Selected model's options
options:
  contextWindow: 400000
```

The percentage must be finite, greater than zero, and at most 100. The selected
model must explicitly declare a positive `options.contextWindow`. Output
`maxTokens` and `safeEffectiveInputTokens` are not used as context capacity.

Before the next agent-loop model invocation, the engine prepares the request
once and computes `100 * input_tokens / contextWindow`. OpenAI Responses models
count the exact input fields of the prepared Responses payload through `/responses/input_tokens`
using their existing HTTP client and credentials. This includes instructions,
message roles, tool definitions, structured outputs, tool inputs/outputs, and
supported image/file content. Generation-only settings such as output limits, streaming and cache keys are excluded according to the count endpoint schema. Counting is not recorded as a model invocation.
Unsupported counting, endpoint failures, or invalid count responses stop the
opted-in request with an actionable error; there is no character-count fallback.
See the [OpenAI token-counting guide](https://developers.openai.com/api/docs/guides/token-counting).

At or above the configured percentage, one pass uses the existing LLM compaction
and `message-remove` path. The percentage is a trigger, not a hard cap. The engine
rebuilds and recounts history, then continues even if usage still meets the
threshold. Immutable overhead and a latest-user-only history skip compaction.
Normal provider context-limit recovery remains available.

The eligible-history signature excludes the latest user, existing summaries,
recovery removal operations, archived messages, and pending operations. A state
already attempted cannot repeatedly compact itself; newly completed original
history rearms it. Every removal tuple is validated against the authoritative
conversation and eligibility snapshot before any summary insertion or archival.
Tool calls and outputs are projected together from one tool-result message.
Proactive recovery requires the removal tool. Each handoff also carries exact
finished-operation identities, tool names, and statuses from the validated
removed tool rows. This authoritative metadata overrides conflicting prose and
preserves successful-operation identity without relying on summary transcription.
Archived completed tool results are removed from replay; pending and unpersisted
operations survive. Both provider continuation mechanisms are disabled until the
first successful invocation using the rebuilt full history.
Native summary and full-history response markers preserve that requirement
across failures and restarts. Only a completed full-history model call with a
provider response ID, started after the handoff, clears the durable barrier.

The `e2e/query/testdata/proactive_compaction/` fixtures are isolated test inputs,
not a default Steward profile. They select the available low-cost nano-tier
`gpt-5-nano` with its 400000-token capacity and a 1% trigger. No parameter-count
claim is made. Import them only into a private test workspace. Seed more than
4000 input tokens of completed history, retain a known fact and completed tool
operation, continue through AG-UI, and verify the native summary message,
`proactive compaction` before/after token log, preserved fact, and one execution
per operation. A summary message uses the existing native conversation events;
no separate compaction wire protocol is introduced.

The opt-in live regression can also exercise the application's Datly 1.0
conversation API against an isolated temporary SQLite file:

```sh
AGENTLY_PROACTIVE_LIVE=1 AGENTLY_PROACTIVE_NATIVE=1 go test ./service/agent \
  -run '^TestProactiveCompactionLiveNano$' -count=1 -timeout=180s -v
```

The verified native run counted 10629 input tokens before compaction and 311
afterward, archived two messages, created one summary, preserved the latest
user message, and executed removal once. Both real follow-up answers retained
the exact seeded fact and completed operation ID. The successful full-history
response cleared the durable continuation barrier; removing the percentage
made no additional count requests. This test uses real database persistence
and the reactor, but does not prove the HTTP AG-UI/application transport path.

The production HTTP AG-UI gate is also verified separately:

```sh
AGENTLY_PROACTIVE_LIVE=1 go test ./sdk \
  -run '^TestProactiveCompactionHTTPLiveNano$' -count=1 -timeout=300s -v
```

The real handler/agent/Datly run counted 10696 input tokens before recovery and
812 afterward, persisted one marked summary, and archived the two seeded
history rows plus an older interim model row. Two fresh HTTP requests retained
the exact fact and operation ID; standard SSE text/tool identities stayed
paired. Durable replay repeated the saved events without provider or counting
calls. A third request with the percentage absent performed no extra counts.
The deterministic failed-removal HTTP regression verifies `RUN_ERROR`, the
original validation error, unchanged history and no continued generation or
replay retry.

These integration checks also cover canonical removal-tool naming and isolated
recovery options: forced removal selection must not modify a cached agent's
options or leak into the next user turn.
