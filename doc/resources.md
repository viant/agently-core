# Uploaded assets and resource tools

Uploaded files and generated exports are available through user-owned
`scratchpad://artifact/<id>` URIs. The agent can inspect their structure, extract
selected content, present images/files to the model, export another format, or
pass the original URI to a compatible tool.

Uploading makes a resource available without automatically attaching its bytes
to an LLM request. Use `resourceURIs` for this behavior; existing `attachments`
retain their automatic presentation behavior.

## Upload and use a file

| Entry point | Conversation required? | Authenticated response |
| --- | --- | --- |
| `POST /upload` | No | `uri` is a scratchpad URI; `resource` contains its descriptor |
| `POST /v1/files` | Yes, in multipart `conversationId` | Existing file ID/download URI plus `resource` |
| Go SDK `UploadFile` | Yes | Existing file ID plus `Resource` and file metadata |
| Swift/Kotlin `uploadFile` | Optional | `resource` descriptor; route selected from the optional conversation ID |

Both HTTP upload routes accept multipart field `file`, with optional `name` and
`contentType`. Uploads need an authenticated effective user to publish a
scratchpad resource. Anonymous callers retain legacy upload/staging behavior and
receive no `resource` descriptor.

The descriptor includes `uri`, `id`, `name`, `mimeType`, `sizeBytes`, and `sha256`.
Use the returned `resource.uri` in `POST /v1/agent/query`:

```json
{
  "agentId": "orchestrator",
  "conversationId": "conv_1",
  "query": "Inspect the Customers sheet and export it as CSV",
  "resourceURIs": ["scratchpad://artifact/a123"]
}
```

The runtime supplies compact resource metadata to the model. Queries with
`resourceURIs` add the resource tools unless an explicit `tools` allowlist
restricts the tool surface. To configure tools independently of an upload, use
the `resources` tool bundle or expose individual methods.

Resources belong to the user and can be reused across that user's conversations.
Rediscover them with `resources:list`:

```json
{"scope":"artifacts","maxItems":20}
```

Pass the returned `nextCursor` as `cursor` to continue listing. This lists
user-owned uploaded and generated artifacts, rather than only the current
conversation's files.

## Resource addresses

`resources:read`, `readImage`, `inspect`, and `export` accept `uri` or `path`.
Absolute filesystem paths and recognized absolute URIs do not require a resource
root or pass configured resource-root allowlist checks. Examples:

```json
{"uri":"scratchpad://artifact/a123"}
```

```json
{"path":"/data/imports/customers.csv"}
```

Relative paths use `rootId` from `resources:roots`, or an unambiguous implicit
root. `uri` takes precedence over `path`/root fields. Supported URI routes include
`file://`, `workspace://`, MCP URIs, and scratchpad artifact URIs. Unsupported
schemes fail explicitly. Scratchpad ownership, MCP authentication, and host/OS
permissions still apply; absolute-file access uses the runtime's OS identity.

## Discover structure with inspect

Call `resources:inspect` before selecting unfamiliar content:

```json
{"uri":"scratchpad://artifact/a123","limit":20}
```

| Format | Inspection result |
| --- | --- |
| XLSX workbook | Sheet names and IDs, stored used ranges; first-row column candidates on sheet inspection |
| PDF | Page count and page components; installed extraction/rendering capabilities |
| Image | Dimensions and detected MIME type |
| CSV/text | Detected content kind, MIME type, size, and supported operations |

Results include a content `version`, `capabilities`, and paginated `components`.
Workbook ranges marked `usedRangeEstimated` come from stored metadata; candidates
marked `columnsInferred` are not a declared schema. Native-file support depends
on the selected provider/model, as indicated by `nativeRequiresProviderSupport`.

For deeper workbook inspection, select an exact sheet name or returned component:

```json
{"uri":"scratchpad://artifact/a123","select":{"componentId":"sheet-1"}}
```

## Extract text and tables with read

Read selected workbook cells:

```json
{
  "uri":"scratchpad://artifact/a123",
  "representation":"table",
  "select":{"sheet":"Customers","range":"A1:H1000"},
  "options":{"values":"displayed","headerRow":1},
  "limits":{"maxRows":100,"maxOutputBytes":32768}
}
```

The result includes `table.rows`, original `rowNumbers`, `startColumn`, and
`coverage`. When truncated, pass the returned `cursor` with the same URI,
selection, and options to read the next portion. CSV supports the same table
representation and cell-range selection, without a sheet selector.

`values` accepts `displayed` (default) or `raw`. Formulas use workbook caches and
are not recalculated. `headerRow` labels the original header row; it does not add
or remove rows. Supported encoding is UTF-8; `delimiter` can specify a single
character for CSV parsing/export.

Extract selected PDF pages as text:

```json
{
  "uri":"scratchpad://artifact/pdf123",
  "representation":"text",
  "select":{"pages":[1,2]}
}
```

PDF text extraction uses the existing Go `ledongthuc/pdf` library and does not
require Poppler. PDF page numbers are one-based. Text results use the existing byte/line paging
fields and `continuation` metadata. Calls without `representation` keep legacy
text-read behavior, including binary-content omission. OCR is unavailable;
scanned documents may need native presentation or rendered images.

For mutable files, copy `version` from inspection into `expectedVersion` on
read/export. A mismatch returns `resource_changed`. Omitting it reads the latest
available bytes. Table cursors also validate the version and selection.

## Present native images and files

Use the existing `resources:readImage` tool to show an image to the model:

```json
{"uri":"scratchpad://artifact/image123","maxWidth":2048,"maxHeight":768}
```

Use `resources:read` to present an entire file through provider-native input:

```json
{"uri":"scratchpad://artifact/pdf123","representation":"native"}
```

The normal next model call receives actual image/file content. A native PDF stays
a PDF instead of being automatically extracted as text. Native reads snapshot
the source; no separate LLM call is made by the resource tool.

Native reads accept a whole resource, without `select`, extraction `options`, or
a table cursor. Export a subset first if needed. Provider/model incompatibility
and presentation-size failures are surfaced rather than silently converted to
another representation. The OpenAI adapter supports its existing inline/upload
paths and reuses uploaded handles within their identity, content, and expiry
scope. See [LLM providers](llm-providers.md).

## Export and pass to other tools

Use `resources:export` to create reusable derived artifacts:

```json
{
  "uri":"scratchpad://artifact/a123",
  "select":{"sheet":"Customers"},
  "operation":"convert",
  "output":{"format":"csv"}
}
```

| Source | Operation | Output formats |
| --- | --- | --- |
| XLSX | `convert` | `csv`, `json`, `xlsx` |
| CSV | `convert` | `csv`, `json`, `xlsx` |
| PDF/text/CSV text | `convert` | `txt` |
| Image | `convert` | `png`, `jpeg` |
| PDF, with `pdftoppm` installed | `render` | `png` |
| PDF, with `pdfimages` installed | `extractImages` | `original`, `png` |

Exports process the complete selected content within processing limits,
independently of read-preview limits. Results contain `resources[]` descriptors,
`sourceVersion`, `complete`, and applicable warnings. Workbook exports preserve
selected values, not formulas, formatting, or external references. JSON exports
contain the table object with rows and coordinates.

Render a PDF page for later `resources:readImage`:

```json
{
  "uri":"scratchpad://artifact/pdf123",
  "select":{"pages":[2]},
  "operation":"render",
  "output":{"format":"png","dpi":144}
}
```

To extract embedded images instead, use `operation: "extractImages"` and
`output.format: "original"`. Both operations require an explicit page selection.
Inspection advertises these capabilities only when their executables are
installed. OCR and archive extraction are not supported by the current handlers.

Spreadsheet cell data is supported through table reads and CSV/JSON/XLSX exports.
“Spreadsheet visual rendering” means producing an image of a cell range with its
layout, fonts, fills, borders, and merged cells; that operation is not currently
available. Embedded workbook pictures are separate from a rendered worksheet.
The pinned Excelize library can locate and extract those pictures with
`GetPictureCells` / `GetPictures`, but the current resource tools do not yet
expose workbook picture extraction.

Pass an original URI or an exported `resources[].uri` to a compatible consumer:

```json
{"sourceURL":"scratchpad://artifact/a123"}
```

The field name belongs to the consumer's schema; email attachments already use
`sourceURL`. Inspection and model presentation are unnecessary for forwarding
original bytes. The receiving tool needs compatible AFS resolution, storage
access, and identity, or its own transport adapter. See [MCP integration](mcp-integration.md).

## Storage and limits

For durable assets, configure scratchpad storage with a user placeholder:

```sh
export AGENTLY_SCRATCHPAD_URI='file:///var/lib/agently/scratchpad/${userID}'
```

The default `mem://localhost/scratchpad/${userID}` is ephemeral. Artifact
manifests are private to resource publication and cannot be edited or fetched
through ordinary scratchpad note tools.

| Operation | Current limits |
| --- | --- |
| Upload/resource input | 64 MiB |
| Inspection component page | 50 by default; at most 100 |
| Artifact listing page | 50 by default; at most 100 |
| Table processing | 100,000 rows; one million cells |
| Table read response | 100 rows / 32 KiB by default; at most 1,000 rows / 64 KiB of row content |
| Image decode | 40 million pixels |
| Image read encoding | 2048×768 / 4 MiB by default; requested maxima 4096 per dimension / 8 MiB |
| PDF text extraction | At most 1,000 selected pages |
| PDF media export | At most 16 selected pages, 64 outputs, and 64 MiB output; 30-second timeout |
| PDF rendering | 36–200 DPI, default 144; rendered dimensions capped at 4096 |

Native presentation also respects its configured byte limit and the active
provider's constraints. Publication retries with the same ID require identical
content. The same-ID publication guard is process-local; independent producers
must use unique IDs or external coordination.

## Implementation reference

| Path | Responsibility |
| --- | --- |
| [resources/](../protocol/tool/service/resources/) | URI access and list/inspect/read/readImage/export methods |
| [scratchpad/artifact.go](../protocol/tool/service/scratchpad/artifact.go) | User-owned publication and descriptors |
| [sdk/embedded_resources.go](../sdk/embedded_resources.go) | SDK upload registration |
| [service/agent/resource_inputs.go](../service/agent/resource_inputs.go) | Resource availability in conversation context |
| [service/shared/toolexec/](../service/shared/toolexec/) | Persist native presentation results |
| [OpenAI provider](../genai/llm/provider/openai/) | Native request preparation and uploaded-handle reuse |

Related: [SDK](sdk.md), [internal tools](internal-tools.md),
[LLM providers](llm-providers.md), [MCP integration](mcp-integration.md),
[augmentation](augmentation.md).
