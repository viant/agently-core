SELECT modelCall.* FROM (
 SELECT m.* FROM model_call m JOIN message mm ON mm.id=m.message_id
 JOIN turn t ON t.id=mm.turn_id JOIN conversation c ON c.id=t.conversation_id
 WHERE $Detail=1 AND $Allowed=1 AND ($OwnerID='' OR (c.created_by_user_id=$OwnerID AND HEX(c.created_by_user_id)=HEX($OwnerID)))
 AND ($OwnerIDContains='' OR INSTR(LOWER(c.created_by_user_id),LOWER($OwnerIDContains))>0)
 AND ($ConversationID='' OR c.id=$ConversationID) AND ($TurnID='' OR t.id=$TurnID)
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
) modelCall
