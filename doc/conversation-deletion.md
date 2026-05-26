# Conversation Deletion

This document describes the hard-delete path for conversations and the history panel.

## Public API

`DELETE /v1/conversations/{id}` deletes a conversation tree owned by the current user.

Expected responses:

- `204 No Content`: delete succeeded.
- `403 Forbidden`: current user does not own every conversation in the tree.
- `404 Not Found`: root conversation does not exist.
- `409 Conflict`: the tree is still in progress and is not stale.
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

History-panel deletion calls this through the public endpoint. Schedule deletion should reuse this primitive directly when schedule cleanup is implemented.

## Ownership

Deletion is owner-scoped. The effective user ID must match `conversation.created_by_user_id` for every conversation in the collected tree. Empty legacy owners are rejected for normal user deletion.

## Tree Scope

The delete tree includes:

- Root conversations requested by ID.
- Descendants via `conversation.conversation_parent_id`.
- Linked conversations via `message.linked_conversation_id`, recursively.

Conversation rows are deleted deepest-child-first, with roots last.

## In-Progress Guard

Deletion is blocked when active markers exist and the newest active timestamp is within 48 hours.

Active markers include conversation, message, turn, turn queue, run, model call, tool call, and tool approval statuses that represent queued/running/waiting work.

If active markers are older than 48 hours, deletion is allowed. This handles conversations stuck in a broken active state.

## Transaction Order

The data service performs the DB cleanup in a single SQL transaction:

1. Build the conversation tree.
2. Collect conversation, message, turn, run, tool approval, deprecated `schedule_run`, and payload IDs.
3. Check owner permissions for every conversation.
4. Check in-progress markers with the 48-hour stale exception.
5. Set `investigation.conversation_id = NULL` when the table exists.
6. Delete deprecated `schedule_run` rows when the table exists.
7. Delete explicit `tool_approval_queue` rows.
8. Delete explicit `run` rows connected by `conversation_id` or `turn_id`.
9. Delete `turn_queue`, `model_call`, `tool_call`, `generated_file`, `message`, and `turn` rows for the tree.
10. Delete conversations deepest-first.
11. Delete collected `call_payload` rows only if no remaining table references them.
12. Commit.

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

After deleting the tree, only those collected payload IDs are eligible for deletion, and only when no remaining table references them. There is no global orphan cleanup.

Object-backed payloads currently delete only the DB row when it is unreferenced. Physical object deletion is intentionally deferred until Agently-owned storage can be distinguished from user/external paths.

## Retained Rows

`investigation` rows are retained. When present, their `conversation_id` is set to `NULL` before the conversation tree is removed.
