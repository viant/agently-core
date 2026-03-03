SELECT q.id AS values
FROM turn_queue q
WHERE q.id IN ($Unsafe.Selector($CurIDs<[]string>(body/Queues).Values, 'id').Prefix("'").Suffix("'").JoinBy(","))
