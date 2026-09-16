# Schedule Deletion

`DELETE /v1/api/agently/scheduler/schedule/{id}` deletes the schedule and the scheduled conversation history it owns.

## Backend Flow

The scheduler service delegates to the data-layer cascade delete:

```go
DeleteScheduleCascade(ctx context.Context, scheduleID string) error
```

The delete runs in one SQL transaction:

1. Load the schedule and verify `created_by_user_id` when present.
2. Load the schedule, lock its row on MySQL, and reject deletion while its lease is current.
3. Collect connected conversations from `conversation.schedule_id`, `run.schedule_id -> conversation_id`, and deprecated `schedule_run` when present.
4. Start from oldest root conversations first and reuse conversation graph deletion for parent, child, parent-turn, and linked relationships.
5. Verify ownership of every collected conversation and reject protected inbound references or active report exports.
6. Block a run in `pending`, `prechecking`, `queued`, or `running` while its lease is current or its heartbeat is fresh. An expired lease with a stale or missing heartbeat does not block deletion merely because the persisted status is active-looking.
7. Delete the conversation graphs, remaining current runs without conversations, and deprecated `schedule_run` rows.
8. Delete the schedule row.

Conversation rows are still deleted child-before-parent. Within each independent depth group, rows are deleted oldest-to-newest.

## Error Mapping

- `204 No Content`: delete succeeded.
- `403 Forbidden`: current user does not own the schedule or every connected conversation.
- `404 Not Found`: schedule does not exist.
- `409 Conflict`: connected conversation or schedule run is still active and not stale.
- `500 Internal Server Error`: unexpected failure.

This endpoint is a user-authorized deletion of the schedule itself. Periodic
scheduled retention is different: it removes old scheduler runs and their
contained conversation graphs without treating historical owner columns as an
authorization boundary, but preserves the schedule for future occurrences.
See [database-maintenance.md](database-maintenance.md).

## Payloads

Payload cleanup is inherited from conversation deletion: collect connected payload IDs before deleting dependents, then delete only collected payload rows that have no remaining references. Object-backed payload physical storage cleanup is still out of scope.
