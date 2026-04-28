# Conversation model

Every observable thing an agent does is persisted into a relational schema so
the UI can replay transcripts, the scheduler can resume work, and the reactor
can recover after a restart. The model is Datly-backed.

## Core entities

| Table (logical) | Package | Role |
|---|---|---|
| conversation | [pkg/agently/conversation/](../pkg/agently/conversation/) | Top-level thread, visibility, title, owner, counters |
| turn | [pkg/agently/turn/](../pkg/agently/turn/) | One request/response cycle; holds status + reactor phase |
| message | [pkg/agently/message/](../pkg/agently/message/) | User/assistant/tool/system messages; append-only |
| tool_call | [pkg/agently/tool/](../pkg/agently/tool/) | Individual tool invocation + result |
| run | [pkg/agently/run/](../pkg/agently/run/) | Long-lived execution record (async ops, schedules) |
| turn_queue | [pkg/agently/turnqueue/](../pkg/agently/turnqueue/) | Pending turns awaiting a slot |
| tool_approval_queue | [pkg/agently/toolapprovalqueue/](../pkg/agently/toolapprovalqueue/) | Pending approvals |
| payload | [pkg/agently/payload/](../pkg/agently/payload/) | Large JSON blobs kept out of the hot table |
| generated_file | [pkg/agently/generatedfile/](../pkg/agently/generatedfile/) | Per-conversation uploaded/produced files |

## Datly DAO

All tables are exposed through generated Datly readers/writers under
[pkg/agently/<table>/{read,write,list,byId}](../pkg/agently/). Service layer
composes them; `sdk/canonical_*.go` reduces them into client-friendly state
envelopes.

## Canonical state reducer

`sdk/canonical_transcript.go` + `sdk/canonical_reducer.go` build a **single
snapshot** per conversation that the UI can diff/patch. It:

- Folds persisted tables + in-flight streaming events into one shape.
- Excludes ephemeral reducer state from the wire surface.
- Powers `GET /v1/conversations/{id}/transcript` and the SSE live state.

See [doc/streaming-events.md](streaming-events.md) for how events feed the reducer.

## Append-only semantics

- Messages and tool calls are never mutated after write — corrections append new records with `supersedes` pointers.
- Context pruning (see [doc/context-management.md](context-management.md)) changes what the **model** sees, not the transcript.
- Delete only happens via admin `/v1/conversations/{id}/prune`, which uses an LLM to pick low-value messages and hard-deletes them.

## Turn-level assistant ownership

One turn may legitimately contain **multiple user messages** and
**multiple assistant outputs**. The UI must not assume either
"one user message per turn" or "one assistant message per turn".

Same-turn extra user messages happen during steering / follow-up updates while
the turn is still active.

What matters is not just the stored message rows, but the **bubble ownership
contract**:

- a live narration stream owns the **next assistant bubble slot**
- a persisted standalone assistant message such as `message:add` owns its **own
  dedicated bubble**
- the final assistant response may reuse the live narration-owned bubble, but
  must not absorb earlier standalone assistant bubbles

### 1. Narration

Narration is the live, transient progress surface for the next assistant
response:

- live reasoning / wait-mode progress / async parked-status updates
- usually represented by assistant rows with `interim = 1`
- may be updated many times in place

Narration rules:

- narration is **not** a standalone durable conclusion
- narration creates or updates the bubble slot for the **next** assistant
  response
- when the real assistant response arrives, it reuses that narration-owned
  bubble
- narration must not steal or overwrite an earlier standalone assistant bubble

Identity rule:

- the narration-owned bubble is keyed by the message id / page identity of that
  live assistant response path

See also:
- [doc/async.md](async.md) — parked status narration slot semantics

### 2. Standalone assistant messages

Examples:

- `message:add` preliminary findings
- persisted assistant checkpoints
- non-final explanatory assistant notes inserted mid-turn

Properties:

- `role = assistant`
- `interim = 0`
- they always render as **their own dedicated bubbles**
- they are never just placeholders for a later final response

Ownership rules:

- a standalone assistant message keeps its own bubble identity
- later narration must not reuse it
- later final assistant content must not overwrite it
- if a standalone assistant bubble already exists and narration for a later
  response begins, narration must create a **new** bubble slot for that later
  response

### 3. Final assistant response

The final assistant response may do one of two things:

- if narration already created the next assistant bubble slot, the final
  response reuses that bubble
- if there is no narration-owned bubble slot yet, the final response creates a
  new assistant bubble

It must **not** reuse a standalone `message:add` bubble.

## Rendering invariants

- A turn may contain:
  - one or more user messages
  - multiple assistant messages
  - multiple tool rows
  - one or more transient narration updates
- The UI must not assume a single user bubble or a single assistant bubble per
  turn.
- Later same-turn user messages are real user bubbles, not annotations on the
  original user message.
- A later same-turn user message does not collapse earlier assistant notes or
  narration; ordering is still by message identity / sequence, not by turn id
  alone.
- `message:add` notes stay standalone even when they occur in the same turn as:
  - active narration
  - later tool calls
  - the final assistant answer
- Narration only owns the bubble slot for the **next** assistant response.
- Final assistant content reuses the last narration-created bubble when one
  exists; otherwise it creates a new bubble.
- Bubble reuse should be keyed by message id / canonical page identity, not by
  turn id alone.
- Past turns are transcript-owned.
- Active turns are SSE-owned.
- Transcript must not reshape an active SSE-owned turn while it is live.

## Lifecycle operations

| HTTP | Effect |
|---|---|
| `POST /v1/conversations` | Create a conversation |
| `POST /v1/conversations/{id}/terminate` | Cancel all active turns |
| `POST /v1/conversations/{id}/compact` | Summarize and archive old messages |
| `POST /v1/conversations/{id}/prune` | LLM-driven low-value pruning |
| `DELETE /v1/conversations/{id}/turns/{turnID}` | Drop a queued turn |

## Extensibility

- **New column**: add in the Datly view YAML + regenerate readers; include in the canonical reducer if client-visible.
- **New reducer fact**: add a pure fn `(state, event) → state` in `sdk/canonical_*.go`.

## Related docs

- [doc/streaming-events.md](streaming-events.md)
- [doc/context-management.md](context-management.md)
- [doc/async.md](async.md) — `run` entity used for async operations.
