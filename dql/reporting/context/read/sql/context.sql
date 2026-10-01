
    SELECT c.owner_id, c.conversation_id, c.active_report_run_id,
           c.revision, c.activation_source, c.actor_id, c.updated_at
    FROM conversation_report_context c
    WHERE ($Internal OR c.owner_id = $OwnerSubject)
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    ORDER BY c.owner_id, c.conversation_id
