SELECT q.*
FROM tool_approval_queue q
WHERE q.id IN ($Unsafe.Selector($CurIDs<[]string>(body/Queues).Values, 'id').Prefix("'").Suffix("'").JoinBy(","))
