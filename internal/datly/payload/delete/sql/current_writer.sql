SELECT r.id FROM (SELECT t.* FROM  (SELECT c.id,
       0 AS should_delete
FROM call_payload c
)  t) r WHERE $criteria.CompositeIn("r", $WriterKeys)