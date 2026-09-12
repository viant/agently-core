# Resource access

`resources` reads absolute file paths, `file://`, `workspace://`, MCP URIs, and
user-owned `scratchpad://artifact/<id>` references. Absolute inputs do not
require a resource root or pass resource-root allowlist checks. Relative paths
still resolve under an explicit or unambiguous configured root. Scratchpad
ownership and MCP authentication remain enforced.

Use the `resources` tool bundle, or expose individual methods. Queries containing
`resourceURIs` automatically add resource tools unless an explicit `tools` list
restricts the tool surface. Conversation associations are discovery metadata;
the same user can reuse an artifact in other conversations.

## Uploads

Authenticated `POST /upload` supports uploads before a conversation exists and
returns a scratchpad URI and `resource` descriptor. Authenticated
`POST /v1/files` / SDK `UploadFile` retains the existing file ID/download contract
and additionally returns `resource`. Anonymous legacy uploads retain the previous
behavior and do not publish into a shared anonymous scratchpad.

Pass uploaded references in the query:

```json
{"query":"Inspect this workbook","resourceURIs":["scratchpad://artifact/a123"]}
```

Only descriptors enter the initial context. Original bytes are not automatically
sent to the model. `resources:list` with `scope: "artifacts"` rediscovers the
current user's published resources, with `maxItems` and `cursor` pagination.

## Inspect and extract

```json
{"uri":"scratchpad://artifact/a123"}
```

`resources:inspect` returns format, version, sheets/pages, and supported
operations. Workbook ranges from stored metadata are labeled estimated; first-row
column candidates are labeled inferred. Native support still depends on the
selected provider/model. Use `select.sheet` or `select.componentId` for deeper
workbook inspection.

```json
{"uri":"scratchpad://artifact/a123","representation":"table","select":{"sheet":"Customers","range":"A1:H100"},"limits":{"maxRows":50},"expectedVersion":"<inspect version>"}
```

`resources:read` extracts workbook/CSV tables or PDF text. Table responses include
original row coordinates, coverage, and a cursor tied to the source version and
selection. Text uses the existing byte/line pagination fields. Calls without
`representation` keep legacy text-read behavior.

## Native presentation

`resources:readImage` opens and presents an image through the provider:

```json
{"uri":"scratchpad://artifact/image123"}
```

`resources:read` can present an entire file natively:

```json
{"uri":"scratchpad://artifact/pdf123","representation":"native"}
```

Native reads snapshot content and persist presentation intent. The normal next
model request receives image/file content. PDF native intent bypasses automatic
PDF-to-text extraction. Full-history replay is used when binary content is
present, so anchored continuation cannot omit newly added content. OpenAI uploaded
handles are reused within the credential/user/content/expiry scope.

Native selection is deliberately whole-file. Export a selection first when
needed. Provider/model rejection is surfaced; unsupported native formats are not
silently extracted as text. Configured attachment byte limits apply to native
presentation; resource storage has its own limit.

## Export and handoff

```json
{"uri":"scratchpad://artifact/a123","select":{"sheet":"Customers"},"operation":"convert","output":{"format":"csv"}}
```

`resources:export` returns new scratchpad descriptors. CSV/JSON/XLSX conversions
process the complete selected table, not just its preview. Workbook exports are
value-only and report that formulas/formatting are not preserved. PDF text export
uses the existing Go text extractor and `format: "txt"` (no Poppler required); image conversion supports PNG/JPEG.

Optional installed Poppler tools enable PDF `render` (PNG) and `extractImages`
(original/PNG). Select up to 16 pages. Missing backends return explicit errors and
are not advertised by inspection. OCR, archive extraction, and spreadsheet visual
rendering are not provided by the initial handlers.

Pass original or exported URIs directly to compatible tools, e.g. an email
attachment's `sourceURL`. Remote tools need their own AFS resolver/shared storage
or an existing file-transport adapter. This change does not create a universal
MCP upload transport.

## Storage and limits

Configure `AGENTLY_SCRATCHPAD_URI` with `${userID}` for persistent storage. The
in-memory default remains suitable for ephemeral use. Publication reuses the
existing scratchpad service, including reporting exports, and protects artifact
manifests from ordinary note tools.

Input content is capped at 64 MiB; image decoding at 40 million pixels; image
output at 4096 per dimension / 8 MiB requested encoding. Table processing is
bounded by 100,000 rows and one million cells; reads return at most 1,000 rows and
64 KiB of row content. Limits fail explicitly. PDF conversion uses private
files, a timeout, a page-count bound, and an output budget.

A source version is the SHA-256 of the consumed bytes. Supply `expectedVersion`
to reject changed mutable files. Native reads pin a scratchpad snapshot. Published
artifacts are immutable within the publisher; same-ID retries require identical
content. Publication's concurrent same-ID guard is process-local; independently
running producers must use unique IDs or external coordination.

## Verification

The implementation adds 32 named Go tests, with table-driven cases covering URI
validation, ownership, publication/retry, format inspection and selection, export
completeness, limits, version conflicts, native intent, prompt expansion, provider
serialization, configured upload endpoints, handle reuse, and failure reporting.
Affected Go package suites and targeted race tests pass. The TypeScript SDK suite
passes 398 tests. Existing workspace bootstrap failure (missing bundled
playwright-cli) and TypeScript compiler diagnostics were reproduced without these
additions; they are not resolved by this change. No live provider or external
email/bulk-import call was performed.
