# Scheduler run visibility

Scheduler run reads are filtered directly in the query.

- Only runs linked to schedules are listed.
- Runs on public schedules are visible to all callers.
- Runs on private schedules are visible only to the schedule owner.
- Pagination is applied at the Datly view level, and total count comes from a matching filtered Datly count query.

Runtime reads now come from the unified `run` table rather than legacy `schedule_run`.
