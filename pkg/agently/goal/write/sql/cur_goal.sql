SELECT * FROM goal
WHERE $criteria.In("id", $CurGoalIDs.Values)
