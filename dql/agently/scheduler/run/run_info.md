# Scheduler run visibility

Scheduler run reads are still filtered through `*run.Filter`.

- Only runs linked to public conversations are visible to anonymous callers.
- Authenticated callers can also see runs for their own private conversations.
- Runs without a linked conversation are excluded from public listing.

Runtime reads now come from the unified `run` table rather than legacy `schedule_run`.
