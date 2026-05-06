# scratchpad

`scratchpad` stores small user-scoped notes for the agent.

- `memorize(key, description, body)` creates or replaces a note by exact key.
- `append(key, body, description?)` appends to a note by exact key. The
  description is note-level metadata for `list()`, not an appended entry.
  Existing notes preserve their description unless a new non-empty description
  is provided. New notes default the description to the key when omitted.
  Appended bodies are separated with a visible `---` delimiter.
- `list()` returns keys, descriptions, and update times for the current user only.
- `fetch(key)` returns the note body for the exact key.

Storage is backed by `viant/afs`. The root URI comes from `AGENTLY_SCRATCHPAD_URI`
or defaults to:

```text
mem://localhost/scratchpad/${userID}
```

The URI template must contain `${userID}` or `${user}`. The runtime expands that
macro from the effective user id in context and sanitizes it as a single path
segment. This keeps the default scratchpad user-bounded and prevents a bad
override from accidentally sharing one pad across users.

Resolved storage URIs are host-side implementation details. Tool outputs and
tool-visible errors must never return absolute filesystem paths or resolved
storage roots.

Supported template macros:

- `${userID}` / `${user}`: sanitized effective user id.
- `${workspaceRoot}`: Agently workspace root.
- `${runtimeRoot}`: Agently runtime root.

The note key is an exact identity, not a file path. The service stores notes in
hashed JSON filenames under the user root, so key lookup does not depend on
fuzzy matching, slug matching, or path traversal.
