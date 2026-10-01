
    SELECT CAST(a.occurred_at AS CHAR) AS maintenance_observed_at, a.event_id, a.event_type, a.artifact_ref, a.version,
           a.job_id, a.artifact_id, a.actor_id, a.actor_ref,
           a.occurred_at, a.metadata_json
    FROM report_audit_event a
    WHERE $Trusted
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    ORDER BY a.occurred_at, a.event_id
