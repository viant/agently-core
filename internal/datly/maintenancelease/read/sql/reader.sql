SELECT snapshot.db_now, snapshot.lease_key, snapshot.owner_id, snapshot.lease_token, snapshot.lease_until, snapshot.created_at, snapshot.updated_at FROM  (
    SELECT clock.db_now, lease.lease_key, lease.owner_id, lease.lease_token,
           lease.lease_until, lease.created_at, lease.updated_at
    FROM (SELECT ${criteria.UTCNow()} AS db_now) clock
    LEFT JOIN (
        SELECT l.lease_key, l.owner_id, l.lease_token, l.lease_until,
               l.created_at, l.updated_at
        FROM maintenance_lease l
        WHERE 1=1
        ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    ) lease ON $Mode <> 'clock'
    WHERE $Trusted
    ORDER BY lease.lease_until, lease.lease_key
)  snapshot