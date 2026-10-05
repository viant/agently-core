SELECT t.* FROM  (SELECT c.id, 0 AS should_delete FROM run c
 WHERE COALESCE(c.run_kind,'execution')='execution'
)  t