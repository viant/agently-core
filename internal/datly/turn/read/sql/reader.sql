SELECT turn_rows.cleanup_status, turn_rows.id, turn_rows.conversation_id, turn_rows.created_at, turn_rows.queue_seq, turn_rows.status, turn_rows.error_message, turn_rows.started_by_message_id, turn_rows.retry_of, turn_rows.agent_id_used, turn_rows.agent_config_used_id, turn_rows.model_override_provider, turn_rows.model_override, turn_rows.model_params_override, turn_rows.run_id FROM  (
SELECT
        t.id,
        t.conversation_id,
        t.created_at,
        t.queue_seq,
        t.status,
        LOWER(TRIM(COALESCE(t.status,''))) AS cleanup_status,
        t.error_message,
        t.started_by_message_id,
        t.retry_of,
        t.agent_id_used,
        t.agent_config_used_id,
        t.model_override_provider,
        t.model_override,
        t.model_params_override,
        t.run_id
    FROM turn t
    WHERE 1=1
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    AND ($ReadMode <> 'queued' OR t.status='queued')
    AND ($ReadMode <> 'active' OR t.id=(
      SELECT t.id FROM turn t WHERE t.status IN ('running','waiting_for_user')
      ${predicate.Builder().CombineOr($predicate.FilterGroup(0,"AND")).Build("AND")}
      ORDER BY t.created_at DESC,t.id DESC LIMIT 1))
    AND ($ReadMode <> 'nextQueued' OR t.id=(
      SELECT t.id FROM turn t WHERE t.status='queued'
      ${predicate.Builder().CombineOr($predicate.FilterGroup(0,"AND")).Build("AND")}
      ORDER BY COALESCE(t.queue_seq,-1),t.created_at,t.id LIMIT 1))
    AND ($ReadMode <> 'byId' OR t.id=(
      SELECT t.id FROM turn t WHERE 1=1
      ${predicate.Builder().CombineOr($predicate.FilterGroup(0,"AND")).Build("AND")}
      LIMIT 1))
    ORDER BY CASE WHEN $LockRows THEN t.id END ASC, CASE WHEN $ReadMode IN ('queued','nextQueued') THEN COALESCE(t.queue_seq,-1) END ASC,
      CASE WHEN $ReadMode IN ('queued','nextQueued') THEN t.created_at END ASC,
      CASE WHEN $ReadMode IN ('queued','nextQueued') THEN t.id END ASC,
      CASE WHEN $ReadMode NOT IN ('queued','nextQueued') THEN t.created_at END DESC,
      CASE WHEN $ReadMode NOT IN ('queued','nextQueued') THEN t.id END DESC
    #if($LockRows) ${View.ForUpdate()} #end  
)  turn_rows