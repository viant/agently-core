# Schedule Deletion

`DELETE /v1/api/agently/scheduler/schedule/{id}` deletes the schedule and the scheduled conversation history it owns.

## Backend Flow

The scheduler service delegates to the data-layer cascade delete:

```go
DeleteScheduleCascade(ctx context.Context, scheduleID string) error
```

The delete runs in one SQL transaction:

1. Load the schedule and verify `created_by_user_id` when present.
2. Collect connected conversations from `conversation.schedule_id`, `run.schedule_id -> conversation_id`, and deprecated `schedule_run` when present.
3. Start from oldest root conversations first.
4. Reuse conversation tree deletion for child and linked conversations.
5. Block recent active conversations or runs, with the same 48-hour stale exception as conversation deletion.
6. Delete remaining schedule runs without conversations.
7. Delete deprecated `schedule_run` rows when present.
8. Delete the schedule row.

Conversation rows are still deleted child-before-parent. Within each independent depth group, rows are deleted oldest-to-newest.

## Error Mapping

- `204 No Content`: delete succeeded.
- `403 Forbidden`: current user does not own the schedule or every connected conversation.
- `404 Not Found`: schedule does not exist.
- `409 Conflict`: connected conversation or schedule run is still active and not stale.
- `500 Internal Server Error`: unexpected failure.

## Payloads

Payload cleanup is inherited from conversation deletion: collect connected payload IDs before deleting dependents, then delete only collected payload rows that have no remaining references. Object-backed payload physical storage cleanup is still out of scope.
