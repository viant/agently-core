Wire schema: AG-UI protocol 1.0, matching `@ag-ui/core` 1.0.1.

`schema-1.0.json` is copied unchanged from:
https://github.com/ag-ui-protocol/ag-ui/blob/e60019d258cf43ecc5ad19e8d374f1c31cbf2b94/spec/1.0/schema.json

The upstream package manifest at that same immutable commit reports version
1.0.1 and protocolVersion 1.0. The schema is the wire authority, including
`cancelled` outcomes and optional input tools/context.

The `Wire*` model is generated from every definition in the pinned schema,
including all 31 event variants, all message/content/media-source variants,
interrupts, resume entries, outcomes, JSON Patch operations, and capability
categories. `DecodeEvent`, `DecodeInput`, and `DecodeCapabilities` validate the
exact schema before decoding. Optional pointer fields preserve absent versus
explicit false, zero, and empty values. Open JSON values use `json.RawMessage`;
RFC 6902 operation extension members are retained in `Extra`. Unions expose
one typed pointer for each variant and reject multiple variants on encoding.

Regenerate using `go generate ./protocol/agui` (Python 3 and gofmt). The tracked
`wire-schema.json` preserves the original definitions and names inline unions
for validator reuse. `wire_generated_test.go` and
`testdata/wire-all-definitions.json` exercise every named and generated shape,
including every optional property, with schema validation and lossless round
trips. Additional tests cover null, empty deltas, explicit false/zero, open patch
members, unknown fields, required fields, and invalid union construction.

The legacy `Event`, `Message`, and `RunAgentInput` models retain source
compatibility for the existing adapter. Use the `Wire*` models for complete
consumer storage or wire forwarding; the legacy models alone do not retain
all fields. Schema validation is structural; run/message/tool sequence and
ownership verification, event reduction, transport support, and backend feature
execution are separate acceptance gates.

The existing translator implements a producer subset: run lifecycle, assistant text,
tool calls and results, and versioned queue/progress custom events. It does
not implement all protocol 1.0 events or claim history/state synchronization,
interrupt continuation, client tools, multimodal input, or full Agently parity.

`forwardedProps.agently` is the supported operation envelope, with version
`"1"`, operation `"chat"` or `"capabilities"`, optional requestId and optional
payload containing agentId/model. The HTTP adapter owns input validation,
run/turn correlation, authorization, and payload hydration. Translator callers
must hydrate referenced tool result payloads before emitting results.

Custom names `agently.queue` and `agently.progress`
carry typed values whose version is `"1"`. Queue sequence is a decimal string
to preserve precision. A non-append snapshot or offset gap ends the run with RUN_ERROR because
this profile cannot synchronize text replacements.
Narration/reasoning remains progress so replacing interim narration with a
persisted answer at the same message ID does not concatenate the two.

MCP Apps host results use the middleware vocabulary pinned at the same AG-UI
commit: `forwardedProps.__proxiedMCPRequest`, `ACTIVITY_SNAPSHOT` with activity
`mcp-apps`, and full MCP response envelopes in `RUN_FINISHED.result`. App instance
scope must be authorized separately from server hashes; no proxy capability is
advertised solely because the wire parser exists.

The MCP dependency currently uses the isolated sibling worktree
`../mcp-protocol-ag-ui` based on `github.com/viant/mcp-protocol v0.19.0`. Its focused
wire fix preserves arbitrary result `_meta` entries, number precision in open
content/structured values, and resource-result text/blob alternatives, including
explicit empty values. The published v0.19.0 DTO discarded arbitrary `_meta` keys.
That sibling replacement must remain available until an immutable fixed module
version is published and pinned. The canonical registry captures host results
through a request-scoped `runtime/mcpapps` hook before its model-safe projection;
raw results are never substituted into model message text.
