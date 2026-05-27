# Approval system

This document is the canonical overview of tool approval in Agently Core.
It focuses on the current source-of-truth contract across:

- tool definitions and bundles
- model-visible tool schema
- prompt review elicitation
- queue approval
- backend review transforms
- transcript / queue persistence

When behavior and this document disagree, the code is the bug owner and this
document should be updated after the fix.

## Goals

- One approval contract for all tools.
- Workspace-owned review semantics, runtime-owned transport.
- No UI invention of missing backend state.
- No downstream heuristics to guess review rows.
- Exact reviewed args must be visible in `llm.request` before we expect the
  model to use them.

## Core concepts

### Approval config

Approval behavior is declared on `llm.ApprovalConfig` in
[genai/llm/tool.go](genai/llm/tool.go).

Important fields:

- `mode`: `none` | `prompt` | `queue`
- `review`: schema-driven review contract
- `queueBehavior`: `wait` | `detach`

### Review contract


The important pieces are:

- `requestedSchema`
- `seeds`
- `xform`

The runtime uses these to:

1. expose review fields on the model-visible tool schema
2. seed review defaults from the emitted tool args
3. transform approved review rows into final executable tool args

### Review-shaped tool args

For prompt/review tools, the model must emit review-shaped args, not final
execution-shaped args.

Example for `workspace-RecordPatch`:

- top-level `rows`
- top-level read-only `intent`
- `Recommendation` metadata only

The model must not pre-collapse reviewed rows into final grouped
`Recommendation.proposed_value` before approval. That is owned by the backend
review transform.

## Prompt review

Prompt review uses the elicitation system.

Relevant code:

- [service/agent/approval_elicitor.go](service/agent/approval_elicitor.go)
- [service/shared/toolapproval/edit.go](service/shared/toolapproval/edit.go)
- [doc/elicitation-system.md](doc/elicitation-system.md)

Flow:

1. model emits reviewed tool call
2. runtime builds `tool_approval` elicitation from `review.requestedSchema`
3. seeded defaults populate the review form
4. user approves / rejects / edits
5. backend applies `ApplyReview(...)`
6. final executable tool args are derived from approved rows

For `workspace-RecordPatch`, this is how grouped review rows become the
final patch payload.

## Queue approval

Queue approval persists pending approvals instead of opening inline review.

Relevant code:

- [service/shared/toolexec/tool_executor.go](service/shared/toolexec/tool_executor.go)
- [sdk/embedded_queue.go](sdk/embedded_queue.go)
- canonical queue row read contract:
  [pkg/agently/toolapprovalqueue/read/queue_rows/queue_rows.sql](pkg/agently/toolapprovalqueue/read/queue_rows/queue_rows.sql)
- exact queue count contract:
  [pkg/agently/toolapprovalqueue/count/queue_total/queue_total.sql](pkg/agently/toolapprovalqueue/count/queue_total/queue_total.sql)
- terminal outcome replay contract:
  [pkg/agently/toolapprovalqueue/outcome/outcome_rows/outcome_rows.sql](pkg/agently/toolapprovalqueue/outcome/outcome_rows/outcome_rows.sql)

Persisted queue rows carry:

- `arguments`
- `metadata.approval`
- `metadata.review`
- `metadata.queueBehavior`

That means queue approval can use the same backend review transform as prompt
approval. The queue row is not a lossy fallback path.

### Queue behaviors

Two queue semantics exist:

- `wait`: enqueue and keep the turn in `waiting_for_user`
- `detach`: enqueue and let the turn finish immediately

Current default:

- queue mode defaults to `detach` unless explicitly set to `wait`

That behavior is implemented in:

- [genai/llm/tool.go](genai/llm/tool.go)
- [service/agent/run_turn.go](service/agent/run_turn.go)

Queue rows are persisted with the effective `queueBehavior`, not just the raw
bundle field, so downstream readers do not need to guess.

Read ownership:

- `pkg/agently/toolapprovalqueue/read` is the runtime Datly source of truth
  for queue rows
- `pkg/agently/toolapprovalqueue/pending` is a compatibility shim and should
  not diverge from `read`

## Model-visible tool schema

Reviewed fields must be present in the actual `llm.request` tool schema before
the model can emit them.

Relevant code:

- [service/agent/tools.go](service/agent/tools.go)
- [service/agent/binding_tools.go](service/agent/binding_tools.go)

For reviewed tools like `workspace-RecordPatch`, the final model-visible
schema must include:

- `Recommendation`
- `intent`
- `rows`

If those fields are missing from `llm.request`, prompt changes alone are not a
real fix.

## State ownership

Approval state owners are:

- tool bundle: review schema and xform declaration
- backend runtime: review seeding, queue persistence, transform execution,
  turn status
- transcript / queue store: durable truth for past turns and pending approvals
- UI: render what the backend already emitted

The UI must not:

- invent reviewed rows from raw JSON
- infer queue behavior
- guess missing rationale or labels
- attach the wrong queue item by fuzzy matching

## Verified review path

The most fully verified reviewed tool path so far is the generic review flow
for:

- `workspace-RecordPatch`

Verified backend truths:

- review-shaped args reached real `llm.request`
- prompt mode seeded real review rows
- queue mode persisted real `rows` + `intent`
- queue mode with `detach` finalized the parent turn as `succeeded`
- newest queue item ordering now surfaces the latest pending approval first

## Current live gaps

These are the known remaining gaps after the latest verified fixes:

1. Queue review checkbox interaction in the live browser is still not proven
   end to end.
   - real rows render in the queue review modal
   - but partial deselection has not yet been verified through an approved queue
     decision payload

2. Some live Steward reviewed rows still omit `site_name` and `rationale`.
   - the prompt fragments were tightened so they are required
   - the compiled Steward `system.tmpl` must be regenerated before that
     producer contract becomes live

These are not reasons to add fallback heuristics. They are explicit producer or
UI interaction bugs.

## Verification rules

When debugging approval behavior, compare:

1. `llm.request`
2. queue row truth / elicitation truth in storage
3. transcript truth
4. live UI render

If they disagree:

- fix the producer first
- then fix the canonical transport / projection layer
- only then fix the UI consumer

Never call the problem fixed just because one surface looks better.

## Related docs

- [doc/elicitation-system.md](doc/elicitation-system.md)
- [doc/tool-system.md](doc/tool-system.md)
- [doc/async.md](doc/async.md)
- [doc/streaming-events.md](doc/streaming-events.md)
