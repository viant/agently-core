SELECT t.* FROM  (SELECT q.id,q.user_id,q.status,q.decision,q.approved_by_user_id,q.approved_at,q.updated_at,q.metadata,q.expires_at,q.timed_out_at
FROM tool_approval_queue q
WHERE q.user_id = :Principal
)  t