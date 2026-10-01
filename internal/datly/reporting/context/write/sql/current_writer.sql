SELECT r.revision, r.owner_id, r.conversation_id, r.active_report_run_id, r.activation_source, r.actor_id, r.updated_at FROM (SELECT data_rows.owner_id, data_rows.conversation_id, data_rows.should_delete, data_rows.active_report_run_id, data_rows.revision, data_rows.activation_source, data_rows.actor_id, data_rows.updated_at FROM  (
    SELECT 0 AS should_delete, owner_id, conversation_id, active_report_run_id,
           revision, activation_source, actor_id, updated_at
    FROM conversation_report_context
)  data_rows) r WHERE $criteria.CompositeIn("r", $WriterKeys)