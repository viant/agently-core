# Context & memory management

The runtime tracks every message, tool call, and plan step inside a turn, but
the *model-visible* slice must fit the LLM's context window. This doc covers
how that slice is built, how token budgets are enforced, and how the runtime
recovers from overflow without losing the durable transcript.

## Layers

| Layer | Role | Where |
|---|---|---|
| Request context | Per-request identity, turn meta, conversation id, user-ask; flows via `context.Context`. | [runtime/requestctx/](../runtime/requestctx/) |
| Memory (deprecated shim) | Back-compat accessor for turn metadata that used to live in a global map. | [runtime/memory/memory.go](../runtime/memory/memory.go) |
| Turn-level context limits | Computes the "model slice" per call: what messages + tool payloads go into the next LLM invocation. | [service/reactor/service_context_limit.go](../service/reactor/service_context_limit.go) |
| Overflow recovery | On `context_length_exceeded` errors: prune aged tool outputs, retry in the same turn. | [service/reactor/overflow.go](../service/reactor/overflow.go) |

## Budget model

Limits are declared per-agent in YAML and read at turn start:

- `conversationTokenLimit` — cap on total conversation history sent to the model.
- `toolResultTokenLimit` — cap on a single tool call's output; oversized outputs are truncated with a tail marker.
- `ageForSummarization` — tool calls older than N reactor steps become candidates for replacement by their summary.

Defaults flow from workspace config ([bootstrap](../bootstrap/)) and from agent YAML. See the steward_ai workspace `config.yaml` for a reference example.

## Overflow path

1. LLM returns a `context_length_exceeded`-style error (provider-specific classifier in `genai/llm/`).
2. [service/reactor/overflow.go](../service/reactor/overflow.go) is invoked; it:
   - Ranks messages by age × size × type.
   - Replaces tool-call payloads beyond `ageForSummarization` with short summaries.
   - Keeps the final assistant responses intact.
   - Re-issues the LLM call with the trimmed slice.
3. **The persisted transcript is not mutated.** Only the in-memory "prompt" slice changes. Transcript replay on reload rehydrates every message.

## Invariants

- Transcript (what's saved) is append-only; what the model sees is derived per call.
- No message is ever deleted to make room — pruning replaces bodies with summaries.
- User messages always survive pruning; only tool-call outputs are candidates.
- Token-count estimates use the model's tokenizer when available and a conservative character-based fallback otherwise.

## Extensibility

- **Custom prune strategy**: satisfy `reactor.ContextSlicer` and inject via executor runtime builder.
- **Per-tool result cap**: tool YAML under `tools/` can set `result_token_limit`; the tool executor applies it on the result path before the reactor sees it.
- **Skip-pruning flag**: messages with metadata `pin: true` are never summarized; useful for few-shot anchors.

## Related docs

- [doc/conversation-model.md](conversation-model.md) — transcript persistence.
- [doc/streaming-events.md](streaming-events.md) — how pruning decisions are surfaced (as `context_pruned` events when enabled).
