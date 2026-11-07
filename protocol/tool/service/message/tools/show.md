When a response contains overflow information:
- It will have a line: `overflow:true`.
- It will also include: `nextRange: X-Y`.
OR
When a tool result contains continuation information:
- It will have a line: `continuation.hasMore:true`
- It will also include: `continuation.nextRange: X-Y`.

To read the another part of the content:
- Call message-show again with:
  - messageId: the same id you just received in the response.
  - byteRange.from = X
  - byteRange.to   = Y

Read the evidence needed for the user's question, not unrelated parts of a
large response. For JSON, prefer `transform.selector` with `format: ndjson`
to read a known subtree, or `format: csv` and `fields` to select known columns.
Transforms operate on the original full payload before byte-range clipping.
Do not invent selectors or treat a partial result as a complete inventory.

When the question requires the complete result and `continuation.hasMore:true`,
continue with `continuation.nextRange` until `hasMore:false`.
For a narrow question, stop once the relevant evidence is available and state
any remaining limitation. A continuation marker alone does not require reading
every unrelated field or row.

Do NOT call message-show with byteRange starting at 0
when a nextRange is provided. Always use the X-Y values from nextRange.
