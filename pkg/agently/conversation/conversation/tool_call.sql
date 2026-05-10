SELECT t.*,
          m.sequence AS message_sequence
 FROM tool_call t
 JOIN message m ON m.id = t.message_id
 WHERE (m.type = 'tool_op' OR m.role = 'tool')
 ${predicate.Builder().CombineOr($predicate.FilterGroup(3, "AND")).Build("AND")}
