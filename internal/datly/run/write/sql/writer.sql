SELECT t.* FROM  (SELECT c.* , NULL AS `condition`, 0 AS should_delete FROM run c WHERE COALESCE(c.run_kind,'execution')='execution'
)  t