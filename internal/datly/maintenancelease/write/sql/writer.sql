SELECT data_rows.lease_key, data_rows.should_delete, data_rows.owner_id, data_rows.lease_token, data_rows.lease_until, data_rows.created_at, data_rows.updated_at FROM  (
    SELECT 0 AS should_delete, lease_key, owner_id, lease_token,
           lease_until, created_at, updated_at
    FROM maintenance_lease
)  data_rows