# Overflow and Continuation

This document describes how Agently handles large tool results and message
continuation. It exists to keep the contract explicit and to avoid duplicate
overflow logic across request assembly, providers, and UI.

## Principles

- There are **two valid continuation mechanisms**:
  - native contract continuation
  - generic overflow recovery
- There must be **one canonical decision point** that chooses between them.
- Provider adapters must serialize requests, not reinterpret overflow.
- Transcript snapshots may redact payloads, but must not invent new overflow
  semantics.
- `llm.request` is a primary debugging artifact. When behavior is wrong, inspect
  what the model actually saw before changing prompts or UI.

## Mechanisms

### 1. Native contract continuation

Use native continuation when the tool itself exposes structured paging:

- output includes a `continuation` object
- input accepts a compatible range selector

Examples:

- `message-show`
- `resources/read`
- `template-get` replayed through `message-show`

The model should continue with the same tool using the explicit range contract.

### 2. Generic overflow recovery

Use generic overflow recovery when:

- the tool result is too large for replay preview
- there is no compatible native input range contract

In that case Agently exposes `message-show` only when overflow is actually
detected and emits an overflow wrapper with the exact continuation args the
model needs.

## Canonical owner

The canonical overflow owner is:

- [service/agent/overflow.go](../service/agent/overflow.go)

That layer is responsible for:

- deciding whether continuation is native or generic
- annotating native continuation payloads
- generating generic overflow wrappers
- preserving source `messageId`
- emitting exact `nextArgs` for `message-show`

Other layers must not duplicate that policy.

## Runtime preview budget

The replay preview budget comes from request metadata:

- `Options.Metadata["toolResultPreviewLimit"]`

It is propagated into runtime request context by:

- [service/core/generate.go](../service/core/generate.go)
- [runtime/requestctx/context.go](../runtime/requestctx/context.go)

This allows recovery tools such as `message-show` to honor the same effective
budget as generic request replay.

## `message-show`

`message-show` is both:

- a native continuation tool
- the generic overflow recovery tool

Its contract lives in:

- [protocol/tool/service/message/show.go](../protocol/tool/service/message/show.go)

Important rules:

- `ShowOutput.messageId` must carry the original source message id.
- Continuation must stay anchored to the original source message, not the tool
  message row created for the `message-show` call.
- When a preview budget is present, `message-show` must clamp its returned chunk
  so its own output does not recursively overflow.

## Producer flow

1. Tool executes and persists its raw response payload.
2. Request assembly builds replayable tool-result messages.
3. Canonical overflow shaping runs in `service/agent/overflow.go`.
4. The resulting `llm.GenerateRequest` is persisted as `llm.request`.
5. Provider adapters serialize the already-shaped request.

## Non-owners

These layers should not own overflow semantics:

- provider adapters such as:
  - [genai/llm/provider/openai](../genai/llm/provider/openai)
- transcript/request redaction:
  - [service/core/modelcall/redact.go](../service/core/modelcall/redact.go)
- UI renderers

If these layers rewrite overflow independently, contracts drift.

## Debugging checklist

When continuation or overflow looks wrong, compare:

1. persisted tool response payload
2. tool message content in transcript
3. canonical replayed tool result in `llm.request`
4. provider request payload
5. rendered UI

Typical failure modes:

- `message-show` missing from tool list
- overflow wrapper present but no `messageId`
- native continuation present but no `nextArgs`
- continuation rebased to tool message ids instead of source message id
- recovery tool recursively overflowing itself

## Current behavior

As of the latest fixes:

- native continuation and generic overflow both exist and are valid
- `message-show` is exposed only on actual overflow
- generic overflow wrappers include native `message-show` args
- native continuation annotation uses typed union detection for bytes/lines
- `message-show` preserves source `messageId`
- `message-show` clamps its output to the replay preview budget

Under an artificially tiny preview budget, the remaining issue is efficiency,
not correctness: the recovery loop may require many stable `message-show`
iterations to finish.
