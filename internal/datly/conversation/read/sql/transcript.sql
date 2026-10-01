SELECT transcript.elapsedInSec, transcript.stage, transcript.id, transcript.conversation_id, transcript.created_at, transcript.queue_seq, transcript.origin, transcript.goal_id, transcript.status_reason, transcript.status, transcript.error_message, transcript.started_by_message_id, transcript.retry_of, transcript.agent_id_used, transcript.agent_config_used_id, transcript.model_override_provider, transcript.model_override, transcript.model_params_override, transcript.run_id FROM (
SELECT
      t.*,
      0 elapsedInSec,
      '' AS stage
       FROM turn t
      ${predicate.Builder().CombineOr($predicate.FilterGroup(1, "AND")).Build("WHERE")}
) transcript