SELECT r.id, r.user_id, r.status, r.decision, r.approved_by_user_id, r.approved_at, r.updated_at, r.metadata, r.expires_at, r.timed_out_at FROM (SELECT t.* FROM  (SELECT q.id,q.user_id,q.status,q.decision,q.approved_by_user_id,q.approved_at,q.updated_at,q.metadata,q.expires_at,q.timed_out_at
FROM tool_approval_queue q
WHERE q.user_id = :Principal
)  t) r WHERE $criteria.CompositeIn("r", $WriterKeys)