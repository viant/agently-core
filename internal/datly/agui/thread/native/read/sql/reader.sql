SELECT identities.* FROM  (SELECT c.id,c.created_by_user_id FROM conversation c WHERE c.id=$NativeID
)  identities