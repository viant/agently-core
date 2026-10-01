# Conversation graph checkpoint

The generated conversation reader and writer now have 18 real legacy/new
`useCase{desc,input,expect}` comparisons plus caller-owned commit/rollback tests.
Reader and writer plumbing remain transcribed. Authored code contains creation,
visibility and activity defaults; transcript/message sorting; stage/status rules;
elicitation hydration; and the JSON business codec's Scan/Value contract.

Evidence includes empty/unknown conversations, insertion defaults, sparse status
and metadata patches, explicit NULL/zero/empty values, nil/empty collections,
missing identity, late-failure rollback, transcript inclusion, inclusive since
filtering, model/tool calls and payloads, usage aggregates and elicitation status.
Successful writer bodies and full nested reader outputs are compared recursively.
Only generated clock values and JSON key casing are normalized.

The draft outer `preamble` projection lost the legacy `Narration` field name.
DQL now aliases the Go field to narration and retains the physical SQLX preamble
mapping. No generated file was edited by hand. The JSON codec handles NULL
logical elicitation values before relation hooks hydrate their payloads.

Datly's builder needed a basic binding correction: evaluated template arguments
are named before independently owned relation-key placeholders are injected.
Existing SQLX parsing/binding preserves final SQL order. Ambiguous combinations
without relation metadata still fail. SQLite tests cover scalar/composite keys,
enabled/disabled predicates and exclusion of unrelated parent rows. Builder and
reader package checks pass; this fix is committed in original Datly as 8991695a.

## Canonical get/list reader

Get and list now share the conversation reader. A required host-owned
`conversationaccess/list` provider selects the mode; the required `visibility/subject`
provider carries the verified subject, including an explicitly empty anonymous
subject. List mode preserves public/owner visibility, orphan-child filtering,
status/search/cursor predicates, ordering and legacy SQL stage calculation.
The existing query selector provider selects root columns without transcript or
usage relations. List projection and full legacy fields/order are compared with
the original reader; missing visibility providers fail closed. Get mode keeps
transcript hooks and validates its required conversation identity in the business
input hook. Host mode must never be supplied by an untrusted query parameter.

The separate list DQL/generated package was removed after focused parity passed.
Caller/API cutover, tree deletion, fact cubes and whole-codebase SQL removal
remain outstanding. Application/Core changes remain uncommitted for review;
these comparisons do not prove full app migration.
