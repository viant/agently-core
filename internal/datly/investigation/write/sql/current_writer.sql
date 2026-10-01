SELECT r.id, r.title, r.created_by, r.conversation_id, r.summary, r.ad_order_id, r.verdict, r.created FROM (SELECT data_rows.id, data_rows.should_delete, data_rows.title, data_rows.created_by, data_rows.conversation_id, data_rows.summary, data_rows.ad_order_id, data_rows.verdict, data_rows.created FROM  (
    SELECT 0 AS should_delete, id, title, created_by,
           conversation_id, summary, ad_order_id, verdict, created
    FROM investigation
)  data_rows) r WHERE $criteria.CompositeIn("r", $WriterKeys)