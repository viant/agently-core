# Uploaded assets through existing resources and scratchpad

Status: the core resource pathway is implemented, with unit/integration coverage. The historical analysis below records the original behavior and rationale; implementation details and supported formats are documented in `protocol/tool/service/resources/doc/resources_spec.md`.

Implemented: native scratchpad resolution and lenient absolute paths; shared publication for authenticated uploads/reporting; resource discovery, inspect/read/export, existing readImage presentation, explicit native file ingestion, preserved native PDF intent, full-history fallback for binary content, and credential-scoped OpenAI upload reuse. Anonymous uploads retain their legacy behavior. Optional installed Poppler backends provide PDF rendering/extraction; OCR, archive extraction, and spreadsheet visual rendering remain unsupported rather than silently approximated. Existing compatible MCP tools accept the references; no universal remote upload adapter or additional MCP authorization layer was added.

## Decision

Extend and adapt the existing pathway:

- **Scratchpad / AFS** stores and resolves uploaded and generated assets.
- **Resources** exposes URI-based inspection, reading, image presentation, and export.
- **Existing attachment persistence, history binding, and provider adapters** deliver explicitly requested native content to the model.
- **Existing MCP consumers** receive the same resource reference; transport adaptation is needed only when a consumer cannot resolve it.

Do not introduce a second artifact storage service, an `artifact:*` tool family, or a duplicate `viewImage` implementation. Keep `scratchpad://artifact/<id>`; a new scheme is unnecessary for the functionality requested.

The resource layer is lenient for absolute inputs: absolute filesystem paths and recognized absolute resource URIs bypass configured resource-root allowlists and containment checks. Roots are used for relative-path resolution and discovery. Scratchpad ownership checks, MCP authentication, and host/OS permissions remain independent of resource-root checks.

The model receives a small upload descriptor, then chooses whether to inspect, extract, view natively, export, or forward the asset. Uploading does not automatically invoke provider attachment handling. Provider coupling is appropriate when presenting native content, not when registering or forwarding a resource.

**Allowing a full URI without a resource root is necessary, but is not the only change.** The existing resource API already accepts `uri`. Normalization, identity propagation, provider initialization, upload publication, and native presentation through continuation are the actual integration work.

## Findings from the current pathway

### 1. Full URI input already exists; normalization loses the scratchpad scheme

`protocol/tool/service/resources/read.go:resolveReadTarget` uses `input.URI` before attempting `newRootContext`. Both `ReadInput` and `ReadImageInput` already have `uri`. Thus `root`/`rootId` is not required when this field is supplied.

The call chain is:

```text
resources.read/readImage
  → resolveReadTarget
  → normalizeFullURI
  → normalizeUserRoot
  → configured-root comparison
  → downloadResource
```

`normalizeUserRoot` in `resources/roots.go` handles workspace, file, absolute filesystem paths, GitHub shorthand, and MCP. It has no scratchpad branch. An unrecognized URI falls through to workspace-relative normalization.

The source analysis confirmed the existing normalization bug. Replace that behavior with native scratchpad resolution:

```text
Input: scratchpad://artifact/a123
Result: scratchpad://artifact/a123 → AFS scratchpad resolver
Resource-root checks: bypassed, whether roots are configured or not
Authorization: authenticated scratchpad ownership
```

`downloadResource` already uses AFS for non-MCP inputs. Once the correct URI and identity reach it, no new binary transport abstraction is needed for scratchpad reads.

A related input distinction matters: an absolute path in `uri` already takes the direct branch; an absolute path supplied only in `path` still enters root resolution first. Accept absolute `path` directly as well, bypassing root lookup and resource-root checks. Relative `path` continues to require a root or the existing unambiguous implicit root.

### 2. Root validation cannot be reused unchanged as artifact authorization

`normalizeFullURI` applies `agentAllowed` resource-root restrictions to the normalized target. An uploaded artifact need not be located under an agent's configured knowledge directory. Its authorization must come from authenticated scratchpad ownership.

`isAllowedWorkspace` currently compares lowercased string prefixes. A probe confirmed that `/tmp/knowledge-private/a` passes the prefix check for `/tmp/knowledge`. This is a concrete weakness of that helper, not a proof of every filesystem operation's effective permissions. Under the proposed lenient policy, this helper is not applied to absolute inputs. For relative/root-scoped operations that still use it, use scheme/authority-aware, path-segment containment and filesystem-appropriate case rules. Host/OS permissions remain independent.

Absolute inputs bypass configured resource-root checks. This does not imply arbitrary scheme support: unknown schemes fail explicitly instead of being interpreted as workspace paths. Recognized schemes retain their own authentication and ownership semantics.

### 3. AFS resolution needs explicit initialization and identity bridging

The production `afsscratchpad.Register` call found in this checkout is inside the reporting initialization branch of `app/executor/builder.go`. An upload resource must work with reporting disabled and with an externally supplied reporting service.

The AFS scratchpad dependency reads its own `ContextWithUserID` value, an optional configured `UserIDProvider`, or a fixed configured user. It does not automatically recognize Agently's auth context.

A probe with `auth.WithUserInfo(...Subject: "alice")` produced:

```text
Agently EffectiveUserID: alice
AFS scratchpad without bridge: scratchpad requires an effective user id
AFS with WithUserIDProvider(auth.EffectiveUserID): resolves alice
```

The existing scratchpad note tool explicitly supplies `WithUserID(authctx.EffectiveUserID(ctx))`; reporting sets AFS owner context. Resource reads need equivalent bridging. Share scratchpad root/template/macros/identity configuration across notes, reporting, uploads, and resource reads. Do not let process-global AFS registration change per request or fix a particular user's ID globally. Validate conflicting configuration if multiple runtimes share one process.

### 4. Upload storage is separate from scratchpad publication

`sdk/embedded_resources.go:UploadFile` currently writes an inline attachment payload and optionally registers a generated-file entry. `sdk/handler_files.go` constructs the download URI. Staged uploads write temporary files. None of those operations constitutes scratchpad artifact publication today.

`service/reporting/scratchpad.go` already implements the publication pattern: store bytes, write private artifact metadata under `artifact/<id>`, and return a logical scratchpad URI. Factor that mechanism into reusable scratchpad artifact operations instead of copying it into each producer.

### 5. Image presentation already exists end to end in the full-history path

`resources:readImage` resolves/downloads the image and uses shared `imageio` to resize/re-encode it. Defaults are 2048×768 and 4 MB encoded output. These are output defaults, not an input-download cap or fixed server-enforced maxima.

It returns metadata and `encodedURI`; `includeData=true` optionally returns base64. The executor recognizes the tool name and performs these operations:

```text
resources:readImage result
  → persistToolImageAttachmentIfNeeded
  → load encodedURI or decode dataBase64
  → addToolAttachment
  → inline model_request payload + user/control attachment carrier
  → next runPlan iteration rebuilds binding
  → buildHistory merges the carrier into its parent user message
  → binding creates binary LLM content
  → provider adapter builds native image input
```

Relevant code: `service/shared/toolexec/image_persistence.go`, `tool_executor.go`, `service/agent/binding_history.go`, `binding_history_payloads.go`, `protocol/binding/binding.go`, and `service/agent/run_query.go`.

The executor redacts base64 from the normal tool result after persisting the attachment. However, debug tracing currently receives the result before this redaction. The default encoded URI is a physical temporary-file URL, and `destURL` controls output storage without going through the resource read-target checks. Reuse the image pipeline, but adapt generated output storage and access rather than exposing arbitrary write destinations for uploaded assets.

### 6. Anchored continuation can omit a newly read image

The existing image carrier is associated with the original user task. `BuildContinuationRequest` selects tool-call/result messages for the last provider response and newer content messages. It does not detect an image added to a previously transmitted user message.

An overlay probe constructed that exact request shape:

```text
Full request: an old user message now contains image content
Continuation: assistant tool call + tool result; zero image content parts
```

This confirms a gap in continuation selection for that shape. It is not a live-provider test. Full-history attachment tests alone cannot prove the next anchored model request sees the image.

### 7. Native PDF needs preserved presentation intent

The OpenAI adapter already uploads supported input files or inlines their data and maps them to `input_file`; images map to `input_image`. See `genai/llm/provider/openai/adapter.go` and `responses.go`.

But `protocol/binding/binding.go:attachmentToLLMContent` extracts text from PDFs before binary provider conversion whenever extraction succeeds. Reusing attachment persistence alone therefore does not guarantee native PDF presentation. Add explicit native intent that survives persistence and bypasses this extraction for an explicitly requested native read. Preserve existing legacy PDF behavior otherwise.

There is another reason not to blindly copy Codex's tool-output arrangement: the current OpenAI Responses serializer emits textual `function_call_output` and can continue before emitting multimodal items on that tool message. Reuse/adapt Agently's attachment-message pathway; merely adding image/file JSON to a tool result is insufficient.

The OpenAI upload helper is currently called with `context.Background()` from request conversion. Native-ingest adaptation should propagate request cancellation and authenticated context through upload preparation. Provider upload handles are transport details, not public resource identities.

## Proposed resource resolution contract

Keep existing fields and semantics, with one shared resolver used by read, readImage, inspect, export, and native-file presentation:

| Input | Resolution | Authorization |
| --- | --- | --- |
| `uri: scratchpad://artifact/<id>` | Preserve and parse scheme/authority/ID; open through AFS scratchpad | Effective user and scratchpad ownership |
| `uri: file:///...` or absolute filesystem path in `uri`/`path` | Normalize as a file resource; no root lookup | No resource-root checks; host/OS permissions still apply |
| `uri: workspace://...` | Existing workspace mapping; no resource-root check | Host/OS permissions and applicable workspace identity |
| MCP URI / existing GitHub shorthand | Existing MCP route; no configured resource-root allowlist | MCP authentication and server-side resource authorization |
| Relative `path` with root/rootId | Existing root-relative resolution | Root policy and containment |
| Relative `path` without resolvable root | Reject ambiguous input | No implicit broad filesystem access |
| Unknown absolute URI scheme | Return unsupported-scheme error | Add schemes only with explicit policy |

`uri` retains precedence over root/path for compatibility. New schemas should describe mutually exclusive input forms. An absolute value in `path` takes the same direct resolver before root lookup, even when root/rootId is supplied. A supplied root does not constrain an absolute target. Relative paths keep existing root resolution and checks.

A conceptual internal resolved resource holds the canonical URI, resource kind, authorized read context, descriptor, and an open operation. This can extend the existing `readTarget`; it does not require a public service. All readers use the resulting authorization decision rather than reopening an unvalidated arbitrary URI.

Do not expose physical scratchpad roots. Require the artifact authority and valid ID for uploaded-file access; do not accidentally expose arbitrary scratchpad note bodies. Enforce ownership even if the agent has no configured resource roots. Artifacts are user-scoped and reusable across that user’s conversations. Conversation associations support discovery and provenance, not access restrictions. Existing MCP client isolation and scratchpad ownership checks remain in place; no additional conversation-level MCP authorization layer is required.

## Upload and resource availability

Extend upload responses additively with a resource descriptor; retain existing ID/download URI fields for compatibility:

```json
{
  "id":"upload-file-id",
  "uri":"/v1/files/upload-file-id?conversationId=conv1",
  "resource":{
    "uri":"scratchpad://artifact/a123",
    "name":"customers.xlsx",
    "mimeType":"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    "sizeBytes":183042,
    "kind":"workbook"
  }
}
```

Use the existing generated-file ID as the artifact ID where possible, avoiding redundant identity mapping; preserve existing report IDs. The example shows distinct IDs only to illustrate compatibility fields, not to require two identities.

Add a reference-only query field such as `resourceURIs`, distinct from existing provider `Attachments`. The host resolves descriptors and persists message/conversation associations. It supplies a bounded availability fragment:

```text
User uploaded customers.xlsx (XLSX, 183042 bytes).
Resource: scratchpad://artifact/a123
Use resources:inspect to discover its sheets, resources:read to extract content,
or pass this URI directly to a compatible tool.
```

No model/provider lookup or binary attachment creation occurs at this stage. Legacy attachments retain their existing automatic presentation behavior. Register once if a client supplies the same file through both compatibility and new paths.

Publish the bytes and private manifest before returning a ready resource. Stage/claim operations must be owner-bound and idempotent. Failed publication leaves recoverable pending state; it must not return a usable reference with missing content.

For the first implementation, publishing existing inline upload bytes into configured scratchpad AFS storage is acceptable and makes references usable by existing shared-storage consumers. Treat this as compatibility duplication, with an explicit cleanup/ownership policy. Later allow the generated-file/download layer and scratchpad to share the same backing object. Do not invent a payload URI that downstream AFS consumers cannot open.

Keep content immutable once published. An export or changed file receives a new identity. Private manifests include source location, ownership, expiry, size/digest, and provenance. Reserve runtime artifact keys against ordinary note mutations and avoid revealing manifests through note fetch/list. Use persistent configured storage for durable assets; the current in-memory default is not restart persistence.

## One resource tool family

| Tool | Behavior |
| --- | --- |
| `resources:roots` | Existing configured-root discovery; unchanged |
| `resources:list` | Existing directory/MCP listing; optionally extend with an explicit conversation-upload listing scope |
| `resources:inspect` | New metadata and structural discovery |
| `resources:read` | Existing text reads; add format-aware text/table extraction and explicit native-file representation |
| `resources:readImage` | Existing image read and provider presentation; adapt URI resolution and continuation handling |
| `resources:export` | New conversion/extraction into reusable scratchpad resources |

Prefer `resources:read(..., representation:"native")` for provider-native files over adding `nativeIngest`, `readPDF`, `readWorkbook`, and a second `viewImage`. A `readPDF` convenience method can delegate to it later if usage demonstrates a need. This keeps native PDF support without multiplying implementations.

`mode` already means head/tail/signatures in text reads; do not overload it with native presentation. Add an explicit `representation` field. Existing calls that omit it keep current behavior. New format capabilities are advertised by inspect and validated against the selected handler/provider.

### Inspect: know the structure before choosing content

```json
{"uri":"scratchpad://artifact/a123"}
```

A workbook result contains bounded metadata and usable selectors:

```json
{
  "uri":"scratchpad://artifact/a123",
  "kind":"workbook",
  "components":[
    {"id":"sheet-1","name":"Customers","kind":"sheet","usedRange":"A1:H4200"},
    {"id":"sheet-2","name":"Orders","kind":"sheet","usedRange":"A1:M18000"}
  ],
  "capabilities":{
    "read":["table"],
    "export":[{"operation":"convert","formats":["csv","json"]}]
  },
  "complete":true
}
```

Native-file support is a separate presentation capability evaluated for the active provider/model. Content kind or a local adapter allowlist alone does not establish remote acceptance.

A component inspection uses the returned ID or exact name:

```json
{"uri":"scratchpad://artifact/a123","select":{"componentId":"sheet-1"}}
```

| Format | Initial metadata | Deeper inspection |
| --- | --- | --- |
| Workbook | Sheets, named ranges, tables | Column positions, header candidates, formulas, merged cells |
| PDF | Page count, text/render/extraction capabilities | Page dimensions, embedded-image IDs, text availability |
| Image | MIME, dimensions, supported frames | Selected-frame details |
| CSV/text | Encoding, delimiter/columns when known | Column details or bounded text sample |
| Archive, later | Paginated entry names/types/sizes | Selected entry metadata |

Component IDs remain stable for an immutable resource. Used ranges are coordinates, not a count of business records. Inferred types/headers, estimated counts, and incomplete metadata are labeled. Expensive scans are bounded, and component lists are paginated. Cache by digest, handler version, and options, after authorization.

Inspect first is the normal reasoning sequence when structure is unknown. It is not a mandatory extra call if metadata is already known, and is unnecessary for forwarding an original asset.

### Read: deterministic extraction or native presentation

Read selected spreadsheet cells as a table:

```json
{
  "uri":"scratchpad://artifact/a123",
  "select":{"sheet":"Customers","range":"A1:H100"},
  "representation":"table",
  "options":{"values":"displayed","headerRow":1},
  "limits":{"maxRows":100,"maxOutputBytes":32768}
}
```

Extract PDF text:

```json
{
  "uri":"scratchpad://artifact/pdf123",
  "select":{"pages":[1,2]},
  "representation":"text",
  "options":{"ocr":"auto"}
}
```

Present a whole PDF/file through the selected provider:

```json
{"uri":"scratchpad://artifact/pdf123","representation":"native"}
```

View an image using the existing tool:

```json
{"uri":"scratchpad://artifact/image123","maxWidth":2048,"maxHeight":768}
```

The last object is input to `resources:readImage`; it must trigger actual native image input, not just return a URI. No separate LLM call is needed: the normal next call receives the content.

Extraction output returns columns/rows or text, requested/actual coverage, truncation, warnings, and continuation. Reuse existing read paging fields for legacy text calls; format-aware results add structured coverage without changing their meaning. Selectors and options are typed per format. Reject conflicts, unknown sheets, and ambiguous whole-workbook table reads. Never silently choose the first sheet/page.

Native reads have different semantics: the provider interprets the original file under its own capabilities and limits. They do not promise full-sheet extraction or exact page selection. For deterministic selection, use extraction/export. Initially require a whole resource for native input; export a subset first when needed. Unsupported native requests fail with available alternatives instead of silently switching to text.

### Export: derive another resource for reading or handoff

```json
{
  "uri":"scratchpad://artifact/a123",
  "select":{"sheet":"Customers"},
  "operation":"convert",
  "output":{"format":"csv"},
  "options":{"values":"displayed","headerRow":1,"encoding":"utf-8"}
}
```

```json
{
  "uri":"scratchpad://artifact/pdf123",
  "select":{"pages":[2]},
  "operation":"render",
  "output":{"format":"png","dpi":144}
}
```

```json
{
  "uri":"scratchpad://artifact/pdf123",
  "select":{"pages":[1,2]},
  "operation":"extractImages",
  "output":{"format":"original"}
}
```

Return resource descriptors with new scratchpad URIs and provenance. Rendering a PDF page and extracting embedded images are separate operations. Rendered spreadsheet regions can be handled by the same export/readImage composition when a renderer exists.

Export processes the complete declared selection; read-preview limits must never silently truncate its output. One operation may return multiple images with a bounded/paginated descriptor list. Long conversions use existing async job machinery. Failures and partial outputs are explicit, with no success claim for incomplete exports.

## Adapt the existing presentation pipeline

### Preserve provider intent

Generalize the internal attachment-result bridge behind `persistToolImageAttachmentIfNeeded` so the existing image tool and a native read can request presentation. Keep existing string tool results and compatibility behavior; a wholesale typed registry rewrite is not a prerequisite.

Use an internal result capability or host-owned dispatch metadata to mark native presentations. Do not let arbitrary untrusted JSON from unrelated tools trigger file loading. A plain status or provider file ID in tool text is not native content.

Persist the source URI, digest/payload, originating tool call ID, and presentation intent (`image` or `native_file`). This intent must survive binding and history replay. Explicit `native_file` bypasses the PDF-to-text shortcut; legacy attachments continue using their existing default.

Share policy checks across direct attachments and tool-triggered presentations. The existing readImage path does not enter `processAttachments`, so its encoded byte cap is not equivalent to provider attachment budgeting. Apply provider/MIME/size checks at presentation, without restricting resource storage or MCP forwarding.

### Make delivery correct for continuation

Minimum reliable adaptation: if new native content was attached to a previously transmitted message, mark the pending presentation and force full-history replay instead of sending an incomplete anchored continuation. Clear the pending state only after the provider accepted that content. A failed or canceled request does not count as delivery.

For efficient anchored delivery, add a stable, appended presentation carrier after the originating tool result. Track its trace/time/ID independently of the old user message, and include it in continuation selection. Retain user/task provenance separately from model-message chronology. Existing multimodal provider serialization can map that carrier to an appropriate message without relying on multimodal `function_call_output` support.

Choose the full-replay fallback for the first increment unless the appended carrier is implemented and proven end to end. Do not keep appending new duplicate images each iteration. Use presentation identity (tool call/resource digest/options) for retries and cached results. Track delivery per applicable provider-response lineage; switching provider or replaying full history must not reuse an unrelated delivered flag.

### Reuse provider upload/inline preparation

The OpenAI adapter already supports inline image/file data and uploaded handles. Reuse those methods, with context-aware preparation. Keep provider handles private and scope any reuse cache to provider endpoint, credential identity, digest, purpose, and expiry. Uploading a file alone is insufficient: the next request must include `input_file`/`input_image` or the corresponding provider-native content.

Validate native capability by provider endpoint/model/MIME. A text-only model can still inspect/extract/forward resources. Native ingestion may be unavailable for a resource that remains valid for every other operation.

### Bound bytes and protect generated outputs

Current reads download the whole resource before output clipping. Current readImage loads/decodes the original before enforcing encoded output size. Add input-byte, decoded-pixel, decompression, elapsed-time, and cumulative presentation limits. Caller options cannot raise hard server caps.

Publish new encoded/exported outputs through scratchpad or use a private temporary handle that never becomes an arbitrary writable user destination. Preserve legacy `encodedURI` consumers during migration. Only delete temporary content after persistence and consumers finish. Persisted inline image payloads permit replay independent of temporary-file lifetime.

Redact inline data before debug tracing as well as normal result persistence. Report presentation persistence failures as tool failures, not successful reads that the model never sees.

## Format processing stays inside resources

Add a small handler registry used by inspect/read/export. Handlers consume authorized readers from the shared resource resolver and publish via scratchpad. They do not own another asset catalog.

Use the existing Excelize dependency for XLSX and standard CSV parsing. Preserve coordinates and distinguish raw, displayed, formula, and cached values. Never silently recalculate formulas or fetch external workbook links. Workbook subset exports must state whether references/formulas are preserved or flattened.

Existing PDF text support does not prove page rendering, OCR, or embedded-image extraction is available. Select and validate backends during implementation; advertise only installed operations. `ocr:auto` does not hide a missing OCR backend or claim complete extraction from scanned pages.

File extensions and caller MIME are hints; identify/validate the actual format. Enforce limits before expensive decoding and on outputs. Treat extracted text as asset content, not as policy or instructions.

## Tool-to-tool handoff remains independent

```text
Upload → scratchpad://artifact/a123

A. bulkUpload(sourceURL=a123 URI) → original file, no inspect/read needed
B. inspect(a123) → export Customers as CSV → a456 URI → bulkUpload
C. readImage(image URI) / read(PDF URI, native) → next model call sees content
```

Preserve the existing email attachment `sourceURL` contract. Consumer-specific schemas may accept an artifact ID, but normalize it to a scoped resource reference. Original and exported assets use the same contract.

An AFS-aware consumer with matching storage and authenticated identity can open the reference directly. A remote consumer without those capabilities needs a declared adapter: upload-session/file ID, short-lived download grant, bounded inline data, or a private local file when filesystem visibility is explicitly configured. Do not rewrite every URI-looking argument.

Keep stable resource references/digests in logical arguments for approvals, deduplication, and audit; materialized transport handles are private execution details. Reauthorize queued work and lease content while asynchronous consumers use it. Rotating URLs must not change import identity. Ambiguous bulk-import outcomes require existing execution protection or downstream idempotency/status handling to prevent duplicate side effects.

Root-free resource reads do not make scratchpad storage reachable from an arbitrary remote MCP server. That is a separate deployment/transport concern.

## Final implementation contracts

### Filesystem trust domain

Absolute file access intentionally uses the Agently runtime’s OS privileges. This design does not claim isolation between mutually untrusted users sharing that filesystem identity. Deployments requiring such isolation must supply it at the execution/storage boundary; it is not provided by resource-root checks. This limitation does not change the accepted lenient absolute-input policy.

### Mutable sources and versioning

Scratchpad artifacts are immutable. Local files and MCP resources can change. Inspection returns a version token when structure or selectors depend on content. A selected read/export can supply `expectedVersion`; a mismatch returns `resource_changed`. Omitting it explicitly reads the latest available version.

For conversion, native presentation, or transfer that needs digest-based identity, snapshot the consumed bytes through existing scratchpad storage or use an equivalent backend version-pinned read. Compute the digest from those same bytes; do not hash a path and later reopen it assuming the contents are unchanged. Use the consumed version in provenance, provider-handle caching, and transport deduplication.

### Retention and presentation copies

Source expiry or source deletion prevents new resource reads, exports, and new native-ingestion operations. Previously authorized content copied into conversation history is a conversation snapshot and follows conversation retention. Removing a discovery association does not revoke a user-owned resource or erase an existing snapshot.

Conversation deletion/retention cleanup removes its local presentation payloads and delivery state when no retained association requires them. Provider handles follow their configured expiry and supported deletion mechanism. Deleting a scratchpad manifest alone does not recall content already sent to a provider or an external MCP consumer. Pending operations recheck source availability unless a valid retention lease pins the snapshot.

## Implementation increments

1. **Direct scratchpad reads.** Add scheme-aware resolution and authorization; centralize scratchpad initialization/configuration and identity bridging. Bypass configured resource-root checks for absolute paths and recognized absolute URIs; preserve scheme-specific authorization and root-relative behavior. Correct containment checks only where relative/root-scoped operations still require them. Verify existing readImage reaches native provider input in the next call, including continuation fallback.
2. **Uploads become resources.** Reuse publication for uploads/reporting; return descriptors; persist reference-only associations and availability metadata. Protect manifests and handle staging/partial publication. No automatic provider upload for the new contract.
3. **Inspect and extract.** Add resources:inspect and structured read selectors, starting with workbook/CSV/PDF text. Paginate structure and output; advertise actual capabilities.
4. **Native file presentation.** Add read representation=native, reuse attachment persistence and provider preparation, preserve native intent through PDF binding, and verify continuation/replay/model switching. Keep existing readImage.
5. **Export and consumers.** Publish CSV/text/rendered/extracted results to scratchpad; exercise email and one actual bulk importer. Add only the transport adapter that consumer requires. Broader rendering/OCR/archive support follows installed-backend needs.

No production change should be described as complete until upload → resource tool → next provider request is verified. Resource availability, presentation, and external delivery have separate acceptance criteria.

## Verification performed and remaining proof

Existing focused tests passed for resource normalization, resources image reading, image-result persistence, attachment-carrier history, binding history messages, OpenAI binary inline/upload request conversion, and OpenAI file support classification. Tests used local fakes; no live model or external email/import action was invoked.

Temporary Go-overlay probes, without adding repository source files, confirmed scratchpad normalization failure, the missing Agently-to-AFS identity bridge, the sibling-prefix containment result, and omission of content on an old user message from the constructed anchored continuation. These characterize current behavior; they are not passing acceptance tests for the proposed fixes.

Required regression and integration tests:

| Scenario | Required result |
| --- | --- |
| Scratchpad URI with no root, with unrelated knowledge roots, and with reporting disabled | Authorized resource opens unchanged; no workspace reinterpretation |
| Missing/wrong user, malformed URI, unknown scheme | Denied without private-path/existence disclosure |
| Same user accesses an artifact from another conversation | Resource reading and compatible MCP handoff remain permitted; conversation association is not an ACL |
| Absolute filesystem URI/path outside configured roots, with no root or an unrelated supplied root | Direct resolution succeeds subject to host/OS permissions; no resource-root rejection |
| Relative path, sibling prefix, traversal/symlink cases in root-scoped resolution | Relative-path root policy and containment remain correct |
| Upload and report publication, staging retry, partial write, persistent restart | Stable resolvable reference; idempotent claim; no ready-but-missing content |
| readImage with default encodedURI and optional base64 | Native image appears in the immediately following model request |
| Same image read with anchored continuation, retry, cache hit, resume, provider switch | Content is delivered with correct identity and no accidental duplication |
| Native PDF with extractable text versus legacy PDF | Native request remains binary; legacy extraction behavior stays compatible |
| OpenAI inline/upload and another configured provider | Appropriate native representation, preserved call association, explicit unsupported errors |
| Text-only model / resource larger than provider cap | Reference/extraction/forwarding work within their own limits; native request fails specifically |
| Inspect workbook/PDF then read selected component | Real selectors, explicit coverage, bounded output, no guessed first component |
| Export beyond preview and multiple extracted images | Complete declared selection or explicit failure; reusable output references |
| Original and derived asset sent to compatible MCP consumer | Correct bytes/digest; no model transcription; authorization and retry protection retained |
| Debug/result redaction and temporary output cleanup | No binary/credentials leaked; no premature deletion |
| Mutable source changes after inspection or during transfer preparation | Expected-version conflict or exact pinned snapshot; digest identifies consumed bytes |
| Source expiry/deletion followed by history replay and conversation cleanup | New resource operations fail; prior conversation snapshots follow documented retention |

## Boundaries to keep explicit

- Absolute paths and recognized absolute URIs deliberately bypass resource-root checks under the current lenient policy. Host/OS permissions and scheme-specific authentication/ownership checks still apply; relative paths remain root-scoped.
- Reusing scratchpad does not automatically supply conversation associations or persistent storage; those must be configured/recorded.
- Reusing provider adapters does not mean storing every upload as a provider attachment.
- Native file interpretation is not a substitute for deterministic extraction or full-file transfer.
- No new service or scheme is needed to implement this design; the work is extending the existing resource contract and correcting its integration boundaries.
