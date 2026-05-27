SELECT * FROM tool_approval_queue
WHERE $criteria.In("id", $CurIDs.Values)
