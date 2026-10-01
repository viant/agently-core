# Elicitation message presentation

`ElicitRequestParams.message` is human-facing Markdown; plain text remains valid.
The backend persists and streams its source without converting it to HTML.
Root-proxy and authoritative child events carry the same message.

Web uses the agently-core UI SDK Markdown renderer. Native clients use Forge's
Markdown renderers. Headings, emphasis, lists, inline code, fenced examples, and
links are presentation only. Code blocks are not executed or interpreted as
Forge UI/report documents. Raw HTML is not executable content.

`requestedSchema`, `callbackURL`, conversation/elicitation IDs, and the structured
response payload remain separate contracts. Message formatting never changes the
accept/decline/cancel action or determines submitted field values.

Covered surfaces:

- Agently web elicitation overlay, including out-of-band prompts.
- Agently web chat elicitation form.
- Agently iOS elicitation overlay (Forge form and fallback editor paths).
- Agently Android elicitation overlay.

This extends message presentation. It does not claim every advanced JSON Schema
constraint or provider authorization UI is identical across platforms.

## Verification

- Core elicitation service tests, including proxied Markdown message preservation: passed.
- Core Go SDK elicitation reducer tests: passed.
- Shared TypeScript SDK: 397 tests passed.
- Agently web elicitation tests: 19 passed; production build passed.
- Android app elicitation tests and Forge Markdown tests: passed using sibling SDK sources.
- iOS elicitation overlay and its five validation tests: passed in an isolated
  package with the real JSON/approval support files and real SDK dependencies.
  The full app build remains blocked by existing actor-isolation errors in
  unrelated workspace/composer/platform support code.

The preceding widget backfill did not certify elicitation's full form behavior.
This change pairs Markdown message presentation while leaving request schemas,
response payloads, approval semantics, and cancellation behavior unchanged.
