# Conversation Deletion

This document describes the hard-delete path for conversations and the history panel.

## Public API

`DELETE /v1/conversations/{id}` deletes a conversation tree owned by the current user.

Expected responses:

- `204 No Content`: delete succeeded.
- `403 Forbidden`: current user does not own every conversation in the tree.
- `404 Not Found`: root conversation does not exist.
- `409 Conflict`: the tree has a live run, an unknown nonterminal state, or another protected dependency.
- `500 Internal Server Error`: unexpected failure.

The Go and TypeScript SDKs expose this as:

```go
DeleteConversation(ctx context.Context, id string) error
```

```ts
deleteConversation(id: string): Promise<void>
```

## Backend Primitive

The reusable data-layer primitive is:

```go
DeleteConversationTree(ctx context.Context, rootConversationIDs ...string) error
```

History-panel deletion calls this through the public endpoint. Schedule deletion reuses the same cleanup rules through `DeleteScheduleCascade`; see [schedule-deletion.md](schedule-deletion.md).

## Ownership

Deletion is owner-scoped. The effective user ID must match `conversation.created_by_user_id` for every conversation in the collected tree. Empty legacy owners are rejected for normal user deletion.

## Tree Scope

The delete tree includes:

- Root conversations requested by ID.
- Descendants via `conversation.conversation_parent_id`.
- Descendants whose `conversation_parent_turn_id` belongs to a turn in the graph.
- Linked conversations via `message.linked_conversation_id`, recursively.

Conversation rows are deleted deepest-child-first, with roots last.

## In-Progress Guard

Known conversation lifecycle states, including active-looking states such as `running` and `waiting_for_user`, are validated using their runs rather than conversation, turn, message, model-call, or tool-call timestamps.

A run is live when its status is `pending`, `prechecking`, `queued`, or `running` and either:

- its lease is still current (an unparsable non-empty lease is treated conservatively as current), or
- its last heartbeat is no older than twice `heartbeat_interval_sec`, with a minimum grace period of 15 seconds.

An active-status run with a missing or invalid heartbeat is considered stale when its lease is missing or expired. Unknown nonterminal conversation statuses remain blocked when the conversation has stored activity. This lets old stuck conversations be removed without allowing deletion of a worker that still owns a valid lease or is sending heartbeats.

## Transaction Order

The data service performs the DB cleanup in a single SQL transaction:

1. Build the conversation tree.
2. Collect conversation, message, turn, run, tool approval, deprecated `schedule_run`, and payload IDs.
3. Check owner permissions for every conversation.
4. Collect goals, internal goal-wakeup schedules, report runs, export jobs, and export artifacts associated with the graph.
5. Reject inbound graph references, user schedules, cross-owner report data, active report exports, live schedules, live runs, and unknown nonterminal conversation states.
6. Delete `investigation` rows whose `conversation_id` belongs to the graph when
   the table exists.
7. Delete conversation-owned report audit events, export artifacts, export jobs, report contexts, and report runs. Durable shared reports are not included.
8. Delete deprecated `schedule_run`, tool approval, execution claim, and current `run` rows.
9. Delete `turn_queue`, `model_call`, `tool_call`, `generated_file`, `message`, and `turn` rows for the graph.
10. Delete internal goal-wakeup schedules and their goals.
11. Delete conversations deepest-first.
12. Delete collected `call_payload` rows only if no remaining table references them, then commit.

The implementation does not rely on FK cascade for correctness because local SQLite and deployed MySQL differ in table coverage and connection-level FK behavior.

## Payloads

Payload IDs are collected before dependent rows are deleted from:

- `message.attachment_payload_id`
- `message.elicitation_payload_id`
- `generated_file.payload_id`
- `model_call.request_payload_id`
- `model_call.response_payload_id`
- `model_call.provider_request_payload_id`
- `model_call.provider_response_payload_id`
- `model_call.stream_payload_id`
- `tool_call.request_payload_id`
- `tool_call.response_payload_id`

After deleting the tree, only those collected payload IDs are eligible for
deletion, and only when no remaining table references them.

The periodic maintenance worker also has a global `call_payload.unused` orphan
rule. It applies only after its configured grace period and independently
rechecks that no supported consumer references the payload. See
[database-maintenance.md](database-maintenance.md).

Object-backed payloads currently delete only the DB row when it is unreferenced. Physical object deletion is intentionally deferred until Agently-owned storage can be distinguished from user/external paths.

## Related and Retained Rows

`investigation` rows attached to the graph are deleted in the same transaction,
before the conversation rows. SQLite's schema does not contain this table, so
that driver skips the step through its static schema contract.

`report_shared_artifact` rows are also retained. They are durable saved-report
definitions and are not owned by the lifecycle of a single conversation.
