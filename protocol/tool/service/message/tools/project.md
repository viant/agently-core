Update the request-scoped context projection for the current run.

Use this tool to hide prior turns or specific messages from active prompt
history without mutating transcript truth.

Guidelines:

- Prefer `turnIds` when hiding a whole prior turn.
- Use `messageIds` for precise suppression of specific messages or tool results.
- Provide a short `reason` when useful for observability.
- This tool affects prompt construction only for the current request/run.
- This tool does not archive, delete, or edit transcript rows.
