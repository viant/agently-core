
SELECT '' AS rule_id, '' AS action, 0 AS priority, '' AS table_name, '' AS record_id,
       '' AS reference_table, '' AS reference_id, '' AS observed_at_raw
WHERE 1=0

UNION ALL
SELECT 'tool_approval_queue.missing_conversation' AS rule_id, 'safe-detach' AS action, 30 AS priority,
       'tool_approval_queue' AS table_name, CAST(taq.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(taq.conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(taq.updated_at, taq.created_at) AS CHAR) AS observed_at_raw
FROM tool_approval_queue taq
WHERE (TRIM(COALESCE(taq.conversation_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = taq.conversation_id)) AND COALESCE(taq.updated_at, taq.created_at) IS NOT NULL AND COALESCE(taq.updated_at, taq.created_at) <= $OlderThan

UNION ALL
SELECT 'tool_approval_queue.missing_message' AS rule_id, 'safe-detach' AS action, 30 AS priority,
       'tool_approval_queue' AS table_name, CAST(taq.id AS CHAR) AS record_id,
       'message' AS reference_table, COALESCE(CAST(taq.message_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(taq.updated_at, taq.created_at) AS CHAR) AS observed_at_raw
FROM tool_approval_queue taq
WHERE (TRIM(COALESCE(taq.message_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM message orphan_parent WHERE orphan_parent.id = taq.message_id)) AND COALESCE(taq.updated_at, taq.created_at) IS NOT NULL AND COALESCE(taq.updated_at, taq.created_at) <= $OlderThan

UNION ALL
SELECT 'tool_approval_queue.missing_turn' AS rule_id, 'safe-detach' AS action, 30 AS priority,
       'tool_approval_queue' AS table_name, CAST(taq.id AS CHAR) AS record_id,
       'turn' AS reference_table, COALESCE(CAST(taq.turn_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(taq.updated_at, taq.created_at) AS CHAR) AS observed_at_raw
FROM tool_approval_queue taq
WHERE (TRIM(COALESCE(taq.turn_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM turn orphan_parent WHERE orphan_parent.id = taq.turn_id)) AND COALESCE(taq.updated_at, taq.created_at) IS NOT NULL AND COALESCE(taq.updated_at, taq.created_at) <= $OlderThan

UNION ALL
SELECT 'model_call.missing_provider_request_payload' AS rule_id, 'safe-detach' AS action, 40 AS priority,
       'model_call' AS table_name, CAST(mc.message_id AS CHAR) AS record_id,
       'call_payload' AS reference_table, COALESCE(CAST(mc.provider_request_payload_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(mc.completed_at, mc.started_at) AS CHAR) AS observed_at_raw
FROM model_call mc
WHERE (TRIM(COALESCE(mc.provider_request_payload_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM call_payload orphan_parent WHERE orphan_parent.id = mc.provider_request_payload_id)) AND COALESCE(mc.completed_at, mc.started_at) IS NOT NULL AND COALESCE(mc.completed_at, mc.started_at) <= $OlderThan

UNION ALL
SELECT 'model_call.missing_provider_response_payload' AS rule_id, 'safe-detach' AS action, 40 AS priority,
       'model_call' AS table_name, CAST(mc.message_id AS CHAR) AS record_id,
       'call_payload' AS reference_table, COALESCE(CAST(mc.provider_response_payload_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(mc.completed_at, mc.started_at) AS CHAR) AS observed_at_raw
FROM model_call mc
WHERE (TRIM(COALESCE(mc.provider_response_payload_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM call_payload orphan_parent WHERE orphan_parent.id = mc.provider_response_payload_id)) AND COALESCE(mc.completed_at, mc.started_at) IS NOT NULL AND COALESCE(mc.completed_at, mc.started_at) <= $OlderThan

UNION ALL
SELECT 'model_call.missing_request_payload' AS rule_id, 'safe-detach' AS action, 40 AS priority,
       'model_call' AS table_name, CAST(mc.message_id AS CHAR) AS record_id,
       'call_payload' AS reference_table, COALESCE(CAST(mc.request_payload_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(mc.completed_at, mc.started_at) AS CHAR) AS observed_at_raw
FROM model_call mc
WHERE (TRIM(COALESCE(mc.request_payload_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM call_payload orphan_parent WHERE orphan_parent.id = mc.request_payload_id)) AND COALESCE(mc.completed_at, mc.started_at) IS NOT NULL AND COALESCE(mc.completed_at, mc.started_at) <= $OlderThan

UNION ALL
SELECT 'model_call.missing_response_payload' AS rule_id, 'safe-detach' AS action, 40 AS priority,
       'model_call' AS table_name, CAST(mc.message_id AS CHAR) AS record_id,
       'call_payload' AS reference_table, COALESCE(CAST(mc.response_payload_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(mc.completed_at, mc.started_at) AS CHAR) AS observed_at_raw
FROM model_call mc
WHERE (TRIM(COALESCE(mc.response_payload_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM call_payload orphan_parent WHERE orphan_parent.id = mc.response_payload_id)) AND COALESCE(mc.completed_at, mc.started_at) IS NOT NULL AND COALESCE(mc.completed_at, mc.started_at) <= $OlderThan

UNION ALL
SELECT 'model_call.missing_run' AS rule_id, 'safe-detach' AS action, 40 AS priority,
       'model_call' AS table_name, CAST(mc.message_id AS CHAR) AS record_id,
       'run' AS reference_table, COALESCE(CAST(mc.run_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(mc.completed_at, mc.started_at) AS CHAR) AS observed_at_raw
FROM model_call mc
WHERE (TRIM(COALESCE(mc.run_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM run orphan_parent WHERE orphan_parent.id = mc.run_id AND COALESCE(orphan_parent.run_kind,'execution')='execution')) AND COALESCE(mc.completed_at, mc.started_at) IS NOT NULL AND COALESCE(mc.completed_at, mc.started_at) <= $OlderThan

UNION ALL
SELECT 'model_call.missing_stream_payload' AS rule_id, 'safe-detach' AS action, 40 AS priority,
       'model_call' AS table_name, CAST(mc.message_id AS CHAR) AS record_id,
       'call_payload' AS reference_table, COALESCE(CAST(mc.stream_payload_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(mc.completed_at, mc.started_at) AS CHAR) AS observed_at_raw
FROM model_call mc
WHERE (TRIM(COALESCE(mc.stream_payload_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM call_payload orphan_parent WHERE orphan_parent.id = mc.stream_payload_id)) AND COALESCE(mc.completed_at, mc.started_at) IS NOT NULL AND COALESCE(mc.completed_at, mc.started_at) <= $OlderThan

UNION ALL
SELECT 'model_call.missing_turn' AS rule_id, 'safe-detach' AS action, 40 AS priority,
       'model_call' AS table_name, CAST(mc.message_id AS CHAR) AS record_id,
       'turn' AS reference_table, COALESCE(CAST(mc.turn_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(mc.completed_at, mc.started_at) AS CHAR) AS observed_at_raw
FROM model_call mc
WHERE (TRIM(COALESCE(mc.turn_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM turn orphan_parent WHERE orphan_parent.id = mc.turn_id)) AND COALESCE(mc.completed_at, mc.started_at) IS NOT NULL AND COALESCE(mc.completed_at, mc.started_at) <= $OlderThan

UNION ALL
SELECT 'tool_call.missing_request_payload' AS rule_id, 'safe-detach' AS action, 50 AS priority,
       'tool_call' AS table_name, CAST(tc.message_id AS CHAR) AS record_id,
       'call_payload' AS reference_table, COALESCE(CAST(tc.request_payload_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(tc.completed_at, tc.started_at) AS CHAR) AS observed_at_raw
FROM tool_call tc
WHERE (TRIM(COALESCE(tc.request_payload_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM call_payload orphan_parent WHERE orphan_parent.id = tc.request_payload_id)) AND COALESCE(tc.completed_at, tc.started_at) IS NOT NULL AND COALESCE(tc.completed_at, tc.started_at) <= $OlderThan

UNION ALL
SELECT 'tool_call.missing_response_payload' AS rule_id, 'safe-detach' AS action, 50 AS priority,
       'tool_call' AS table_name, CAST(tc.message_id AS CHAR) AS record_id,
       'call_payload' AS reference_table, COALESCE(CAST(tc.response_payload_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(tc.completed_at, tc.started_at) AS CHAR) AS observed_at_raw
FROM tool_call tc
WHERE (TRIM(COALESCE(tc.response_payload_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM call_payload orphan_parent WHERE orphan_parent.id = tc.response_payload_id)) AND COALESCE(tc.completed_at, tc.started_at) IS NOT NULL AND COALESCE(tc.completed_at, tc.started_at) <= $OlderThan

UNION ALL
SELECT 'tool_call.missing_run' AS rule_id, 'safe-detach' AS action, 50 AS priority,
       'tool_call' AS table_name, CAST(tc.message_id AS CHAR) AS record_id,
       'run' AS reference_table, COALESCE(CAST(tc.run_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(tc.completed_at, tc.started_at) AS CHAR) AS observed_at_raw
FROM tool_call tc
WHERE (TRIM(COALESCE(tc.run_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM run orphan_parent WHERE orphan_parent.id = tc.run_id AND COALESCE(orphan_parent.run_kind,'execution')='execution')) AND COALESCE(tc.completed_at, tc.started_at) IS NOT NULL AND COALESCE(tc.completed_at, tc.started_at) <= $OlderThan

UNION ALL
SELECT 'tool_call.missing_turn' AS rule_id, 'safe-detach' AS action, 50 AS priority,
       'tool_call' AS table_name, CAST(tc.message_id AS CHAR) AS record_id,
       'turn' AS reference_table, COALESCE(CAST(tc.turn_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(tc.completed_at, tc.started_at) AS CHAR) AS observed_at_raw
FROM tool_call tc
WHERE (TRIM(COALESCE(tc.turn_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM turn orphan_parent WHERE orphan_parent.id = tc.turn_id)) AND COALESCE(tc.completed_at, tc.started_at) IS NOT NULL AND COALESCE(tc.completed_at, tc.started_at) <= $OlderThan

UNION ALL
SELECT 'generated_file.missing_message' AS rule_id, 'safe-detach' AS action, 60 AS priority,
       'generated_file' AS table_name, CAST(gf.id AS CHAR) AS record_id,
       'message' AS reference_table, COALESCE(CAST(gf.message_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(gf.updated_at, gf.created_at) AS CHAR) AS observed_at_raw
FROM generated_file gf
WHERE (TRIM(COALESCE(gf.message_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM message orphan_parent WHERE orphan_parent.id = gf.message_id)) AND COALESCE(gf.updated_at, gf.created_at) IS NOT NULL AND COALESCE(gf.updated_at, gf.created_at) <= $OlderThan

UNION ALL
SELECT 'generated_file.missing_payload' AS rule_id, 'safe-detach' AS action, 60 AS priority,
       'generated_file' AS table_name, CAST(gf.id AS CHAR) AS record_id,
       'call_payload' AS reference_table, COALESCE(CAST(gf.payload_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(gf.updated_at, gf.created_at) AS CHAR) AS observed_at_raw
FROM generated_file gf
WHERE (TRIM(COALESCE(gf.payload_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM call_payload orphan_parent WHERE orphan_parent.id = gf.payload_id)) AND COALESCE(gf.updated_at, gf.created_at) IS NOT NULL AND COALESCE(gf.updated_at, gf.created_at) <= $OlderThan

UNION ALL
SELECT 'generated_file.missing_turn' AS rule_id, 'safe-detach' AS action, 60 AS priority,
       'generated_file' AS table_name, CAST(gf.id AS CHAR) AS record_id,
       'turn' AS reference_table, COALESCE(CAST(gf.turn_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(gf.updated_at, gf.created_at) AS CHAR) AS observed_at_raw
FROM generated_file gf
WHERE (TRIM(COALESCE(gf.turn_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM turn orphan_parent WHERE orphan_parent.id = gf.turn_id)) AND COALESCE(gf.updated_at, gf.created_at) IS NOT NULL AND COALESCE(gf.updated_at, gf.created_at) <= $OlderThan

UNION ALL
SELECT 'message.missing_attachment_payload' AS rule_id, 'safe-detach' AS action, 100 AS priority,
       'message' AS table_name, CAST(m.id AS CHAR) AS record_id,
       'call_payload' AS reference_table, COALESCE(CAST(m.attachment_payload_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(m.updated_at, m.created_at) AS CHAR) AS observed_at_raw
FROM message m
WHERE (TRIM(COALESCE(m.attachment_payload_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM call_payload orphan_parent WHERE orphan_parent.id = m.attachment_payload_id)) AND COALESCE(m.updated_at, m.created_at) IS NOT NULL AND COALESCE(m.updated_at, m.created_at) <= $OlderThan

UNION ALL
SELECT 'message.missing_elicitation_payload' AS rule_id, 'safe-detach' AS action, 100 AS priority,
       'message' AS table_name, CAST(m.id AS CHAR) AS record_id,
       'call_payload' AS reference_table, COALESCE(CAST(m.elicitation_payload_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(m.updated_at, m.created_at) AS CHAR) AS observed_at_raw
FROM message m
WHERE (TRIM(COALESCE(m.elicitation_payload_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM call_payload orphan_parent WHERE orphan_parent.id = m.elicitation_payload_id)) AND COALESCE(m.updated_at, m.created_at) IS NOT NULL AND COALESCE(m.updated_at, m.created_at) <= $OlderThan

UNION ALL
SELECT 'message.missing_linked_conversation' AS rule_id, 'safe-detach' AS action, 100 AS priority,
       'message' AS table_name, CAST(m.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(m.linked_conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(m.updated_at, m.created_at) AS CHAR) AS observed_at_raw
FROM message m
WHERE (TRIM(COALESCE(m.linked_conversation_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = m.linked_conversation_id)) AND COALESCE(m.updated_at, m.created_at) IS NOT NULL AND COALESCE(m.updated_at, m.created_at) <= $OlderThan

UNION ALL
SELECT 'message.missing_parent' AS rule_id, 'safe-detach' AS action, 100 AS priority,
       'message' AS table_name, CAST(m.id AS CHAR) AS record_id,
       'message' AS reference_table, COALESCE(CAST(m.parent_message_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(m.updated_at, m.created_at) AS CHAR) AS observed_at_raw
FROM message m
WHERE (TRIM(COALESCE(m.parent_message_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM message orphan_parent WHERE orphan_parent.id = m.parent_message_id)) AND COALESCE(m.updated_at, m.created_at) IS NOT NULL AND COALESCE(m.updated_at, m.created_at) <= $OlderThan

UNION ALL
SELECT 'message.missing_superseded_by' AS rule_id, 'safe-detach' AS action, 100 AS priority,
       'message' AS table_name, CAST(m.id AS CHAR) AS record_id,
       'message' AS reference_table, COALESCE(CAST(m.superseded_by AS CHAR),'') AS reference_id,
       CAST(COALESCE(m.updated_at, m.created_at) AS CHAR) AS observed_at_raw
FROM message m
WHERE (TRIM(COALESCE(m.superseded_by, '')) <> '' AND NOT EXISTS (SELECT 1 FROM message orphan_parent WHERE orphan_parent.id = m.superseded_by)) AND COALESCE(m.updated_at, m.created_at) IS NOT NULL AND COALESCE(m.updated_at, m.created_at) <= $OlderThan

UNION ALL
SELECT 'message.missing_turn' AS rule_id, 'safe-detach' AS action, 100 AS priority,
       'message' AS table_name, CAST(m.id AS CHAR) AS record_id,
       'turn' AS reference_table, COALESCE(CAST(m.turn_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(m.updated_at, m.created_at) AS CHAR) AS observed_at_raw
FROM message m
WHERE (TRIM(COALESCE(m.turn_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM turn orphan_parent WHERE orphan_parent.id = m.turn_id)) AND COALESCE(m.updated_at, m.created_at) IS NOT NULL AND COALESCE(m.updated_at, m.created_at) <= $OlderThan

UNION ALL
SELECT 'run.missing_checkpoint_message' AS rule_id, 'safe-detach' AS action, 110 AS priority,
       'run' AS table_name, CAST(r.id AS CHAR) AS record_id,
       'message' AS reference_table, COALESCE(CAST(r.checkpoint_message_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(r.completed_at, r.updated_at, r.created_at) AS CHAR) AS observed_at_raw
FROM run r
WHERE COALESCE(r.run_kind,'execution')='execution' AND (TRIM(COALESCE(r.checkpoint_message_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM message orphan_parent WHERE orphan_parent.id = r.checkpoint_message_id)) AND COALESCE(r.completed_at, r.updated_at, r.created_at) IS NOT NULL AND COALESCE(r.completed_at, r.updated_at, r.created_at) <= $OlderThan

UNION ALL
SELECT 'run.missing_conversation' AS rule_id, 'safe-detach' AS action, 110 AS priority,
       'run' AS table_name, CAST(r.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(r.conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(r.completed_at, r.updated_at, r.created_at) AS CHAR) AS observed_at_raw
FROM run r
WHERE COALESCE(r.run_kind,'execution')='execution' AND (TRIM(COALESCE(r.conversation_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = r.conversation_id)) AND COALESCE(r.completed_at, r.updated_at, r.created_at) IS NOT NULL AND COALESCE(r.completed_at, r.updated_at, r.created_at) <= $OlderThan

UNION ALL
SELECT 'run.missing_resumed_from' AS rule_id, 'safe-detach' AS action, 110 AS priority,
       'run' AS table_name, CAST(r.id AS CHAR) AS record_id,
       'run' AS reference_table, COALESCE(CAST(r.resumed_from_run_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(r.completed_at, r.updated_at, r.created_at) AS CHAR) AS observed_at_raw
FROM run r
WHERE COALESCE(r.run_kind,'execution')='execution' AND (TRIM(COALESCE(r.resumed_from_run_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM run orphan_parent WHERE orphan_parent.id = r.resumed_from_run_id AND COALESCE(orphan_parent.run_kind,'execution')='execution')) AND COALESCE(r.completed_at, r.updated_at, r.created_at) IS NOT NULL AND COALESCE(r.completed_at, r.updated_at, r.created_at) <= $OlderThan

UNION ALL
SELECT 'run.missing_schedule' AS rule_id, 'safe-detach' AS action, 110 AS priority,
       'run' AS table_name, CAST(r.id AS CHAR) AS record_id,
       'schedule' AS reference_table, COALESCE(CAST(r.schedule_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(r.completed_at, r.updated_at, r.created_at) AS CHAR) AS observed_at_raw
FROM run r
WHERE COALESCE(r.run_kind,'execution')='execution' AND (TRIM(COALESCE(r.schedule_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM schedule orphan_parent WHERE orphan_parent.id = r.schedule_id)) AND COALESCE(r.completed_at, r.updated_at, r.created_at) IS NOT NULL AND COALESCE(r.completed_at, r.updated_at, r.created_at) <= $OlderThan

UNION ALL
SELECT 'run.missing_turn' AS rule_id, 'safe-detach' AS action, 110 AS priority,
       'run' AS table_name, CAST(r.id AS CHAR) AS record_id,
       'turn' AS reference_table, COALESCE(CAST(r.turn_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(r.completed_at, r.updated_at, r.created_at) AS CHAR) AS observed_at_raw
FROM run r
WHERE COALESCE(r.run_kind,'execution')='execution' AND (TRIM(COALESCE(r.turn_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM turn orphan_parent WHERE orphan_parent.id = r.turn_id)) AND COALESCE(r.completed_at, r.updated_at, r.created_at) IS NOT NULL AND COALESCE(r.completed_at, r.updated_at, r.created_at) <= $OlderThan

#if($MySQLContract)
UNION ALL
SELECT 'schedule_run.missing_conversation' AS rule_id, 'safe-detach' AS action, 120 AS priority,
       'schedule_run' AS table_name, CAST(sr.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(sr.conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(sr.completed_at, sr.updated_at, sr.created_at) AS CHAR) AS observed_at_raw
FROM schedule_run sr
WHERE (TRIM(COALESCE(sr.conversation_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = sr.conversation_id)) AND COALESCE(sr.completed_at, sr.updated_at, sr.created_at) IS NOT NULL AND COALESCE(sr.completed_at, sr.updated_at, sr.created_at) <= $OlderThan
#end

UNION ALL
SELECT 'turn.missing_goal' AS rule_id, 'safe-detach' AS action, 130 AS priority,
       'turn' AS table_name, CAST(t.id AS CHAR) AS record_id,
       'goal' AS reference_table, COALESCE(CAST(t.goal_id AS CHAR),'') AS reference_id,
       CAST(t.created_at AS CHAR) AS observed_at_raw
FROM turn t
WHERE (TRIM(COALESCE(t.goal_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM goal orphan_parent WHERE orphan_parent.id = t.goal_id)) AND t.created_at IS NOT NULL AND t.created_at <= $OlderThan

UNION ALL
SELECT 'turn.missing_retry_of' AS rule_id, 'safe-detach' AS action, 130 AS priority,
       'turn' AS table_name, CAST(t.id AS CHAR) AS record_id,
       'turn' AS reference_table, COALESCE(CAST(t.retry_of AS CHAR),'') AS reference_id,
       CAST(t.created_at AS CHAR) AS observed_at_raw
FROM turn t
WHERE (TRIM(COALESCE(t.retry_of, '')) <> '' AND NOT EXISTS (SELECT 1 FROM turn orphan_parent WHERE orphan_parent.id = t.retry_of)) AND t.created_at IS NOT NULL AND t.created_at <= $OlderThan

UNION ALL
SELECT 'turn.missing_run' AS rule_id, 'safe-detach' AS action, 130 AS priority,
       'turn' AS table_name, CAST(t.id AS CHAR) AS record_id,
       'run' AS reference_table, COALESCE(CAST(t.run_id AS CHAR),'') AS reference_id,
       CAST(t.created_at AS CHAR) AS observed_at_raw
FROM turn t
WHERE (TRIM(COALESCE(t.run_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM run orphan_parent WHERE orphan_parent.id = t.run_id AND COALESCE(orphan_parent.run_kind,'execution')='execution')) AND t.created_at IS NOT NULL AND t.created_at <= $OlderThan

UNION ALL
SELECT 'turn.missing_started_by_message' AS rule_id, 'safe-detach' AS action, 130 AS priority,
       'turn' AS table_name, CAST(t.id AS CHAR) AS record_id,
       'message' AS reference_table, COALESCE(CAST(t.started_by_message_id AS CHAR),'') AS reference_id,
       CAST(t.created_at AS CHAR) AS observed_at_raw
FROM turn t
WHERE (TRIM(COALESCE(t.started_by_message_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM message orphan_parent WHERE orphan_parent.id = t.started_by_message_id)) AND t.created_at IS NOT NULL AND t.created_at <= $OlderThan

UNION ALL
SELECT 'report_export_job.missing_conversation' AS rule_id, 'safe-detach' AS action, 140 AS priority,
       'report_export_job' AS table_name, CAST(rej.job_id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(rej.conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(rej.completed_at, rej.started_at, rej.submitted_at) AS CHAR) AS observed_at_raw
FROM report_export_job rej
WHERE (TRIM(COALESCE(rej.conversation_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = rej.conversation_id)) AND COALESCE(rej.completed_at, rej.started_at, rej.submitted_at) IS NOT NULL AND COALESCE(rej.completed_at, rej.started_at, rej.submitted_at) <= $OlderThan

UNION ALL
SELECT 'report_run.missing_conversation' AS rule_id, 'safe-detach' AS action, 160 AS priority,
       'report_run' AS table_name, CAST(rr.report_run_id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(rr.conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(rr.updated_at, rr.created_at) AS CHAR) AS observed_at_raw
FROM report_run rr
WHERE (TRIM(COALESCE(rr.conversation_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = rr.conversation_id)) AND COALESCE(rr.updated_at, rr.created_at) IS NOT NULL AND COALESCE(rr.updated_at, rr.created_at) <= $OlderThan

UNION ALL
SELECT 'schedule.missing_conversation' AS rule_id, 'safe-detach' AS action, 170 AS priority,
       'schedule' AS table_name, CAST(s.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(s.conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(s.updated_at, s.created_at) AS CHAR) AS observed_at_raw
FROM schedule s
WHERE (TRIM(COALESCE(s.conversation_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = s.conversation_id)) AND COALESCE(s.updated_at, s.created_at) IS NOT NULL AND COALESCE(s.updated_at, s.created_at) <= $OlderThan

UNION ALL
SELECT 'schedule.missing_goal' AS rule_id, 'safe-detach' AS action, 170 AS priority,
       'schedule' AS table_name, CAST(s.id AS CHAR) AS record_id,
       'goal' AS reference_table, COALESCE(CAST(s.goal_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(s.updated_at, s.created_at) AS CHAR) AS observed_at_raw
FROM schedule s
WHERE (TRIM(COALESCE(s.goal_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM goal orphan_parent WHERE orphan_parent.id = s.goal_id)) AND COALESCE(s.updated_at, s.created_at) IS NOT NULL AND COALESCE(s.updated_at, s.created_at) <= $OlderThan

UNION ALL
SELECT 'conversation.missing_parent_conversation' AS rule_id, 'safe-detach' AS action, 190 AS priority,
       'conversation' AS table_name, CAST(c.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(c.conversation_parent_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(c.last_activity, c.updated_at, c.created_at) AS CHAR) AS observed_at_raw
FROM conversation c
WHERE (TRIM(COALESCE(c.conversation_parent_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = c.conversation_parent_id)) AND COALESCE(c.last_activity, c.updated_at, c.created_at) IS NOT NULL AND COALESCE(c.last_activity, c.updated_at, c.created_at) <= $OlderThan

UNION ALL
SELECT 'conversation.missing_parent_turn' AS rule_id, 'safe-detach' AS action, 190 AS priority,
       'conversation' AS table_name, CAST(c.id AS CHAR) AS record_id,
       'turn' AS reference_table, COALESCE(CAST(c.conversation_parent_turn_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(c.last_activity, c.updated_at, c.created_at) AS CHAR) AS observed_at_raw
FROM conversation c
WHERE (TRIM(COALESCE(c.conversation_parent_turn_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM turn orphan_parent WHERE orphan_parent.id = c.conversation_parent_turn_id)) AND COALESCE(c.last_activity, c.updated_at, c.created_at) IS NOT NULL AND COALESCE(c.last_activity, c.updated_at, c.created_at) <= $OlderThan

UNION ALL
SELECT 'conversation.missing_schedule' AS rule_id, 'safe-detach' AS action, 190 AS priority,
       'conversation' AS table_name, CAST(c.id AS CHAR) AS record_id,
       'schedule' AS reference_table, COALESCE(CAST(c.schedule_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(c.last_activity, c.updated_at, c.created_at) AS CHAR) AS observed_at_raw
FROM conversation c
WHERE (TRIM(COALESCE(c.schedule_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM schedule orphan_parent WHERE orphan_parent.id = c.schedule_id)) AND COALESCE(c.last_activity, c.updated_at, c.created_at) IS NOT NULL AND COALESCE(c.last_activity, c.updated_at, c.created_at) <= $OlderThan

#if($MySQLContract)
UNION ALL
SELECT 'conversation.missing_schedule_run' AS rule_id, 'safe-detach' AS action, 190 AS priority,
       'conversation' AS table_name, CAST(c.id AS CHAR) AS record_id,
       'run|schedule_run' AS reference_table, COALESCE(CAST(c.schedule_run_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(c.last_activity, c.updated_at, c.created_at) AS CHAR) AS observed_at_raw
FROM conversation c
WHERE (TRIM(COALESCE(c.schedule_run_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM run current_run WHERE current_run.id = c.schedule_run_id AND COALESCE(current_run.run_kind,'execution')='execution') AND NOT EXISTS (SELECT 1 FROM schedule_run legacy_run WHERE legacy_run.id = c.schedule_run_id)) AND COALESCE(c.last_activity, c.updated_at, c.created_at) IS NOT NULL AND COALESCE(c.last_activity, c.updated_at, c.created_at) <= $OlderThan
#end
#if(!$MySQLContract)
UNION ALL
SELECT 'conversation.missing_schedule_run' AS rule_id, 'safe-detach' AS action, 190 AS priority,
       'conversation' AS table_name, CAST(c.id AS CHAR) AS record_id,
       'run' AS reference_table, COALESCE(CAST(c.schedule_run_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(c.last_activity, c.updated_at, c.created_at) AS CHAR) AS observed_at_raw
FROM conversation c
WHERE (TRIM(COALESCE(c.schedule_run_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM run orphan_parent WHERE orphan_parent.id = c.schedule_run_id AND COALESCE(orphan_parent.run_kind,'execution')='execution')) AND COALESCE(c.last_activity, c.updated_at, c.created_at) IS NOT NULL AND COALESCE(c.last_activity, c.updated_at, c.created_at) <= $OlderThan
#end

UNION ALL
SELECT 'tool_execution_claim.missing_turn' AS rule_id, 'safe-delete' AS action, 1010 AS priority,
       'tool_execution_claim' AS table_name, CAST(tec.claim_key AS CHAR) AS record_id,
       'turn' AS reference_table, COALESCE(CAST(tec.turn_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(tec.updated_at, tec.created_at) AS CHAR) AS observed_at_raw
FROM tool_execution_claim tec
WHERE ((TRIM(COALESCE(tec.turn_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM turn orphan_parent WHERE orphan_parent.id = tec.turn_id))) AND COALESCE(tec.updated_at, tec.created_at) IS NOT NULL AND COALESCE(tec.updated_at, tec.created_at) <= $OlderThan

UNION ALL
SELECT 'turn_queue.missing_conversation' AS rule_id, 'safe-delete' AS action, 1020 AS priority,
       'turn_queue' AS table_name, CAST(tq.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(tq.conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(tq.updated_at, tq.created_at) AS CHAR) AS observed_at_raw
FROM turn_queue tq
WHERE ((TRIM(COALESCE(tq.conversation_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = tq.conversation_id))) AND COALESCE(tq.updated_at, tq.created_at) IS NOT NULL AND COALESCE(tq.updated_at, tq.created_at) <= $OlderThan

UNION ALL
SELECT 'turn_queue.missing_message' AS rule_id, 'safe-delete' AS action, 1020 AS priority,
       'turn_queue' AS table_name, CAST(tq.id AS CHAR) AS record_id,
       'message' AS reference_table, COALESCE(CAST(tq.message_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(tq.updated_at, tq.created_at) AS CHAR) AS observed_at_raw
FROM turn_queue tq
WHERE ((TRIM(COALESCE(tq.message_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM message orphan_parent WHERE orphan_parent.id = tq.message_id))) AND COALESCE(tq.updated_at, tq.created_at) IS NOT NULL AND COALESCE(tq.updated_at, tq.created_at) <= $OlderThan

UNION ALL
SELECT 'turn_queue.missing_turn' AS rule_id, 'safe-delete' AS action, 1020 AS priority,
       'turn_queue' AS table_name, CAST(tq.id AS CHAR) AS record_id,
       'turn' AS reference_table, COALESCE(CAST(tq.turn_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(tq.updated_at, tq.created_at) AS CHAR) AS observed_at_raw
FROM turn_queue tq
WHERE ((TRIM(COALESCE(tq.turn_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM turn orphan_parent WHERE orphan_parent.id = tq.turn_id))) AND COALESCE(tq.updated_at, tq.created_at) IS NOT NULL AND COALESCE(tq.updated_at, tq.created_at) <= $OlderThan

UNION ALL
SELECT 'model_call.missing_message' AS rule_id, 'safe-delete' AS action, 1040 AS priority,
       'model_call' AS table_name, CAST(mc.message_id AS CHAR) AS record_id,
       'message' AS reference_table, COALESCE(CAST(mc.message_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(mc.completed_at, mc.started_at) AS CHAR) AS observed_at_raw
FROM model_call mc
WHERE ((TRIM(COALESCE(mc.message_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM message orphan_parent WHERE orphan_parent.id = mc.message_id))) AND COALESCE(mc.completed_at, mc.started_at) IS NOT NULL AND COALESCE(mc.completed_at, mc.started_at) <= $OlderThan

UNION ALL
SELECT 'tool_call.missing_message' AS rule_id, 'safe-delete' AS action, 1050 AS priority,
       'tool_call' AS table_name, CAST(tc.message_id AS CHAR) AS record_id,
       'message' AS reference_table, COALESCE(CAST(tc.message_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(tc.completed_at, tc.started_at) AS CHAR) AS observed_at_raw
FROM tool_call tc
WHERE ((TRIM(COALESCE(tc.message_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM message orphan_parent WHERE orphan_parent.id = tc.message_id))) AND COALESCE(tc.completed_at, tc.started_at) IS NOT NULL AND COALESCE(tc.completed_at, tc.started_at) <= $OlderThan

UNION ALL
SELECT 'generated_file.missing_conversation' AS rule_id, 'safe-delete' AS action, 1060 AS priority,
       'generated_file' AS table_name, CAST(gf.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(gf.conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(gf.updated_at, gf.created_at) AS CHAR) AS observed_at_raw
FROM generated_file gf
WHERE ((TRIM(COALESCE(gf.conversation_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = gf.conversation_id))) AND COALESCE(gf.updated_at, gf.created_at) IS NOT NULL AND COALESCE(gf.updated_at, gf.created_at) <= $OlderThan

UNION ALL
SELECT 'report_export_artifact.missing_job' AS rule_id, 'safe-delete' AS action, 1070 AS priority,
       'report_export_artifact' AS table_name, CAST(rea.artifact_id AS CHAR) AS record_id,
       'report_export_job' AS reference_table, COALESCE(CAST(rea.job_id AS CHAR),'') AS reference_id,
       CAST(rea.created_at AS CHAR) AS observed_at_raw
FROM report_export_artifact rea
WHERE ((TRIM(COALESCE(rea.job_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM report_export_job orphan_parent WHERE orphan_parent.job_id = rea.job_id))) AND rea.created_at IS NOT NULL AND rea.created_at <= $OlderThan

#if($MySQLContract)
UNION ALL
SELECT 'conversation_report_context.missing_active_report_run' AS rule_id, 'safe-delete' AS action, 1080 AS priority,
       'conversation_report_context' AS table_name, CAST(CONCAT(COALESCE(CAST(crc.owner_id AS CHAR), ''), CHAR(31), COALESCE(CAST(crc.conversation_id AS CHAR), '')) AS CHAR) AS record_id,
       'report_run' AS reference_table, COALESCE(CAST(CONCAT(COALESCE(CAST(crc.owner_id AS CHAR), ''), CHAR(31), COALESCE(CAST(crc.active_report_run_id AS CHAR), '')) AS CHAR),'') AS reference_id,
       CAST(crc.updated_at AS CHAR) AS observed_at_raw
FROM conversation_report_context crc
WHERE (TRIM(COALESCE(crc.active_report_run_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM report_run orphan_parent WHERE orphan_parent.owner_id = crc.owner_id AND orphan_parent.report_run_id = crc.active_report_run_id)) AND crc.updated_at IS NOT NULL AND crc.updated_at <= $OlderThan
#end
#if(!$MySQLContract)
UNION ALL
SELECT 'conversation_report_context.missing_active_report_run' AS rule_id, 'safe-delete' AS action, 1080 AS priority,
       'conversation_report_context' AS table_name, CAST(printf('%s%s%s', COALESCE(CAST(crc.owner_id AS CHAR), ''), CHAR(31), COALESCE(CAST(crc.conversation_id AS CHAR), '')) AS CHAR) AS record_id,
       'report_run' AS reference_table, COALESCE(CAST(printf('%s%s%s', COALESCE(CAST(crc.owner_id AS CHAR), ''), CHAR(31), COALESCE(CAST(crc.active_report_run_id AS CHAR), '')) AS CHAR),'') AS reference_id,
       CAST(crc.updated_at AS CHAR) AS observed_at_raw
FROM conversation_report_context crc
WHERE (TRIM(COALESCE(crc.active_report_run_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM report_run orphan_parent WHERE orphan_parent.owner_id = crc.owner_id AND orphan_parent.report_run_id = crc.active_report_run_id)) AND crc.updated_at IS NOT NULL AND crc.updated_at <= $OlderThan
#end

#if($MySQLContract)
UNION ALL
SELECT 'conversation_report_context.missing_conversation' AS rule_id, 'safe-delete' AS action, 1080 AS priority,
       'conversation_report_context' AS table_name, CAST(CONCAT(COALESCE(CAST(crc.owner_id AS CHAR), ''), CHAR(31), COALESCE(CAST(crc.conversation_id AS CHAR), '')) AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(crc.conversation_id AS CHAR),'') AS reference_id,
       CAST(crc.updated_at AS CHAR) AS observed_at_raw
FROM conversation_report_context crc
WHERE (TRIM(COALESCE(crc.conversation_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = crc.conversation_id)) AND crc.updated_at IS NOT NULL AND crc.updated_at <= $OlderThan
#end
#if(!$MySQLContract)
UNION ALL
SELECT 'conversation_report_context.missing_conversation' AS rule_id, 'safe-delete' AS action, 1080 AS priority,
       'conversation_report_context' AS table_name, CAST(printf('%s%s%s', COALESCE(CAST(crc.owner_id AS CHAR), ''), CHAR(31), COALESCE(CAST(crc.conversation_id AS CHAR), '')) AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(crc.conversation_id AS CHAR),'') AS reference_id,
       CAST(crc.updated_at AS CHAR) AS observed_at_raw
FROM conversation_report_context crc
WHERE (TRIM(COALESCE(crc.conversation_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = crc.conversation_id)) AND crc.updated_at IS NOT NULL AND crc.updated_at <= $OlderThan
#end

#if($MySQLContract)
UNION ALL
SELECT 'investigation.missing_conversation' AS rule_id, 'safe-delete' AS action, 1090 AS priority,
       'investigation' AS table_name, CAST(i.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(i.conversation_id AS CHAR),'') AS reference_id,
       CAST(i.created AS CHAR) AS observed_at_raw
FROM investigation i
WHERE (TRIM(COALESCE(i.conversation_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = i.conversation_id)) AND i.created IS NOT NULL AND i.created <= $OlderThan
#end

UNION ALL
SELECT 'message.missing_conversation' AS rule_id, 'safe-delete' AS action, 1100 AS priority,
       'message' AS table_name, CAST(m.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(m.conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(m.updated_at, m.created_at) AS CHAR) AS observed_at_raw
FROM message m
WHERE ((TRIM(COALESCE(m.conversation_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = m.conversation_id))) AND COALESCE(m.updated_at, m.created_at) IS NOT NULL AND COALESCE(m.updated_at, m.created_at) <= $OlderThan

#if($MySQLContract)
UNION ALL
SELECT 'schedule_run.missing_schedule' AS rule_id, 'safe-delete' AS action, 1120 AS priority,
       'schedule_run' AS table_name, CAST(sr.id AS CHAR) AS record_id,
       'schedule' AS reference_table, COALESCE(CAST(sr.schedule_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(sr.completed_at, sr.updated_at, sr.created_at) AS CHAR) AS observed_at_raw
FROM schedule_run sr
WHERE ((TRIM(COALESCE(sr.schedule_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM schedule orphan_parent WHERE orphan_parent.id = sr.schedule_id))) AND COALESCE(sr.completed_at, sr.updated_at, sr.created_at) IS NOT NULL AND COALESCE(sr.completed_at, sr.updated_at, sr.created_at) <= $OlderThan
#end

UNION ALL
SELECT 'turn.missing_conversation' AS rule_id, 'safe-delete' AS action, 1130 AS priority,
       'turn' AS table_name, CAST(t.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(t.conversation_id AS CHAR),'') AS reference_id,
       CAST(t.created_at AS CHAR) AS observed_at_raw
FROM turn t
WHERE ((TRIM(COALESCE(t.conversation_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = t.conversation_id))) AND t.created_at IS NOT NULL AND t.created_at <= $OlderThan

UNION ALL
SELECT 'goal.missing_conversation' AS rule_id, 'safe-delete' AS action, 1180 AS priority,
       'goal' AS table_name, CAST(g.id AS CHAR) AS record_id,
       'conversation' AS reference_table, COALESCE(CAST(g.conversation_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(g.updated_at, g.created_at) AS CHAR) AS observed_at_raw
FROM goal g
WHERE ((TRIM(COALESCE(g.conversation_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = g.conversation_id))) AND COALESCE(g.updated_at, g.created_at) IS NOT NULL AND COALESCE(g.updated_at, g.created_at) <= $OlderThan

UNION ALL
SELECT 'call_payload.unused' AS rule_id, 'safe-delete' AS action, 1210 AS priority,
       'call_payload' AS table_name, CAST(cp.id AS CHAR) AS record_id,
       'payload_consumers' AS reference_table, COALESCE(CAST(cp.id AS CHAR),'') AS reference_id,
       CAST(cp.created_at AS CHAR) AS observed_at_raw
FROM call_payload cp
WHERE (NOT EXISTS (SELECT 1 FROM message m WHERE m.attachment_payload_id = cp.id) AND NOT EXISTS (SELECT 1 FROM message m WHERE m.elicitation_payload_id = cp.id) AND NOT EXISTS (SELECT 1 FROM model_call mc WHERE mc.request_payload_id = cp.id) AND NOT EXISTS (SELECT 1 FROM model_call mc WHERE mc.response_payload_id = cp.id) AND NOT EXISTS (SELECT 1 FROM model_call mc WHERE mc.provider_request_payload_id = cp.id) AND NOT EXISTS (SELECT 1 FROM model_call mc WHERE mc.provider_response_payload_id = cp.id) AND NOT EXISTS (SELECT 1 FROM model_call mc WHERE mc.stream_payload_id = cp.id) AND NOT EXISTS (SELECT 1 FROM tool_call tc WHERE tc.request_payload_id = cp.id) AND NOT EXISTS (SELECT 1 FROM tool_call tc WHERE tc.response_payload_id = cp.id) AND NOT EXISTS (SELECT 1 FROM generated_file gf WHERE gf.payload_id = cp.id) AND NOT EXISTS (SELECT 1 FROM run payload_run WHERE payload_run.id = cp.run_id)) AND cp.created_at IS NOT NULL AND cp.created_at <= $OlderThan

UNION ALL
SELECT 'report_export_job.missing_artifact' AS rule_id, 'report-only' AS action, 2140 AS priority,
       'report_export_job' AS table_name, CAST(rej.job_id AS CHAR) AS record_id,
       'report_export_artifact' AS reference_table, COALESCE(CAST(rej.artifact_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(rej.completed_at, rej.started_at, rej.submitted_at) AS CHAR) AS observed_at_raw
FROM report_export_job rej
WHERE (TRIM(COALESCE(rej.artifact_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM report_export_artifact orphan_parent WHERE orphan_parent.artifact_id = rej.artifact_id)) AND COALESCE(rej.completed_at, rej.started_at, rej.submitted_at) IS NOT NULL AND COALESCE(rej.completed_at, rej.started_at, rej.submitted_at) <= $OlderThan

UNION ALL
SELECT 'report_export_job.missing_report_run' AS rule_id, 'report-only' AS action, 2140 AS priority,
       'report_export_job' AS table_name, CAST(rej.job_id AS CHAR) AS record_id,
       'report_run' AS reference_table, COALESCE(CAST(rej.report_run_id AS CHAR),'') AS reference_id,
       CAST(COALESCE(rej.completed_at, rej.started_at, rej.submitted_at) AS CHAR) AS observed_at_raw
FROM report_export_job rej
WHERE (TRIM(COALESCE(rej.report_run_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM report_run orphan_parent WHERE orphan_parent.report_run_id = rej.report_run_id AND orphan_parent.owner_id = rej.owner_id)) AND COALESCE(rej.completed_at, rej.started_at, rej.submitted_at) IS NOT NULL AND COALESCE(rej.completed_at, rej.started_at, rej.submitted_at) <= $OlderThan

