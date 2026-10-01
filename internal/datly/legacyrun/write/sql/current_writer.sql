SELECT r.id, r.schedule_id, r.created_at, r.updated_at, r.status, r.error_message, r.lease_owner, r.lease_until, r.precondition_ran_at, r.precondition_passed, r.precondition_result, r.conversation_id, r.conversation_kind, r.scheduled_for, r.started_at, r.completed_at FROM (SELECT data_rows.id, data_rows.should_delete, data_rows.schedule_id, data_rows.created_at, data_rows.updated_at, data_rows.status, data_rows.error_message, data_rows.lease_owner, data_rows.lease_until, data_rows.precondition_ran_at, data_rows.precondition_passed, data_rows.precondition_result, data_rows.conversation_id, data_rows.conversation_kind, data_rows.scheduled_for, data_rows.started_at, data_rows.completed_at FROM  (
    SELECT 0 AS should_delete, id, schedule_id, created_at, updated_at,
           status, error_message, lease_owner, lease_until,
           precondition_ran_at, precondition_passed, precondition_result,
           conversation_id, conversation_kind, scheduled_for,
           started_at, completed_at
    FROM schedule_run
)  data_rows) r WHERE $criteria.CompositeIn("r", $WriterKeys)