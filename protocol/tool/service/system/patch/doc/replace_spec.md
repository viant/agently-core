Stages an exact string replacement in a file under workdir. The replacement is recorded in the active patch session until the host commits or rolls it back.

Input

- `workdir` is required.
- `path` is required. Relative paths resolve against workdir; absolute paths are accepted only when they already point inside workdir.
- `old` is required and must be non-empty exact text already present in the file.
- `new` is the replacement text.
- `replaceAll` replaces every exact occurrence when true.
- `expectedOccurrences` optionally pins the exact occurrence count before replacing.

Rules

- The target file must have been read first with `resources:read` in the current turn.
- If the observed file changed after `resources:read`, read it again before replacing.
- The target file must already exist.
- No fuzzy matching, path guessing, quote normalization, or similar-file fallback is performed.
- If `replaceAll` is false, `old` must occur exactly once.
- If `expectedOccurrences` is set, the observed occurrence count must match it exactly.
- Paths that escape workdir by traversal or sibling-prefix tricks are rejected.
- The method stages the change only; commit and rollback remain host-owned controls.

Output

- Returns the resolved path, replacement count, status, and line-change stats.
