SELECT * FROM run
WHERE $criteria.In("id", $CurIDs.Values)
