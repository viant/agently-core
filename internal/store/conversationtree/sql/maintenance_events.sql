DELETE FROM call_payload
WHERE id IN (/*IDS*/)
  AND kind = 'agui.event'
  AND run_id IN (/*RUNS*/)
  AND EXISTS (
    SELECT 1 FROM run r
    WHERE r.id = call_payload.run_id
      AND r.run_kind = 'agui'
      AND r.conversation_id IN (/*THREADS*/)
  )
