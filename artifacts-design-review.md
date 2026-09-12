# Architecture and security review

Reviewed: `artifacts-design.md`, 2026-09-11.

Verdict: the reuse strategy is sound. Keep scratchpad/AFS, the resources tool family, and existing provider adapters. The final design now records the trust-domain, versioning, and retention contracts identified below. Finding 2 is withdrawn after clarifying that artifacts are user-scoped. The lenient absolute-path policy is an accepted requirement; this review does not propose restoring resource-root checks for absolute inputs.

This is a design and source review. No exploit against a deployed system or live-provider test was performed. The design's earlier test/probe results are useful evidence of current implementation behavior, not evidence that the proposed changes are already secure.

## Findings

### 1. [P1] Define the trust boundary of lenient absolute-file access

Design locations: lines 16, 136–147, and 406.

The document bypasses resource-root checks while relying on host/OS permissions and promising user isolation for scratchpad artifacts. In the current resource reader, file access is performed through AFS in the Agently process. The inspected path has no per-request OS impersonation or filesystem sandbox hook. OS permissions therefore describe the server process's access, not the requesting user's access.

In a shared server with filesystem-backed artifacts, a caller who knows another user's backing file path could request it directly through `file://`, bypassing scratchpad authorization. Hiding physical paths reduces exposure but is not an isolation mechanism. Reporting publication currently requests 0755 directories and 0644 files; changing those modes alone does not isolate two users served by the same OS process.

This finding is conditional on a shared trust environment. Broad file reads can be entirely intentional in a trusted single-user runtime. The missing part is declaring which deployment promise the design makes.

Required resolution: state that unrestricted absolute-file access uses the runtime's filesystem trust domain. For mutually untrusted users, require per-user execution/filesystem isolation or a privileged content broker with storage inaccessible to ordinary file reads. Keep absolute inputs root-free. Distinguish resource roots from the actual isolation boundary. If shared-process isolation is out of scope, explicitly exclude it rather than promising that scratchpad checks protect bytes accessible through another scheme.

Verification: two users with known fixture paths must either be isolated by the execution/storage boundary or be explicitly classified as the same trusted filesystem domain. Test direct file reads as well as scratchpad URI reads.

Evidence: `protocol/tool/service/resources/read.go:265`; `service/reporting/scratchpad.go:61`.

### 2. Withdrawn: conversation-level MCP authorization

The MCP manager uses user + conversation + server pooling when its user-ID extractor is configured. Scratchpad artifacts are user-owned and intentionally reusable across that user’s conversations. Conversation associations are discovery/provenance metadata, not ACLs.

The earlier P1 finding assumed a conversation restriction that is not required. No additional MCP authorization layer is recommended. Preserve existing client/credential isolation and scratchpad ownership validation. The final design and acceptance criteria have been corrected accordingly.

### 3. [P2] Define a stable content version for mutable resource URIs

Design locations: lines 145, 241, 330–344, and 380.

Scratchpad content is specified as immutable, but the unified resource interface also accepts mutable filesystem and MCP resources. The design uses digest-based component selectors, provider-handle reuse, provenance, and transfer deduplication without defining when a mutable source is snapshotted or how a later open is pinned to the inspected version.

For example, inspect a workbook at a local path, then replace that file before export or native ingestion. The same selector may now address another sheet or dataset. Computing a digest and subsequently reopening a path can also race with replacement. The documented resolved resource's open operation alone does not establish version stability.

Required resolution: define one version contract across inspect/read/export/native delivery. Inspect returns a version token; selected operations either consume that version or explicitly request latest content. Pin the bytes used for digesting and processing through a snapshot/stable handle or backend conditional read. A bare filesystem descriptor may still be changed in place, so distinguish stable path resolution from stable bytes. Snapshot mutable resources into existing scratchpad storage where needed. Use the exact consumed version for provider caches, exports, and MCP dispatch deduplication.

Verification: replace and modify a source between inspect and read/export, and while transfer preparation runs. The operation must consume the pinned snapshot or return resource-changed, never silently relabel different bytes as the inspected version.

### 4. [P2] Expiry/revocation behavior is undefined for persisted presentation copies

Design locations: lines 180–184, 330–352, and 405.

The reviewed draft gave artifacts expiry and association checks, while the reused presentation path copies bytes into conversation payloads and can upload additional copies to a provider. History replay prefers inline payload bytes and therefore need not reopen the scratchpad URI. Expiring the source manifest or removing its association does not automatically stop later native replay or remove provider copies.

That behavior may be intentional: a conversation can retain a snapshot of something legitimately read earlier. But the document must choose that contract rather than imply that source expiry applies to all representations.

Required resolution: distinguish reference expiry, access revocation, conversation retention, and provider-copy retention. Recommended baseline: ordinary source expiry prevents new resource operations while existing authorized conversation snapshots follow conversation retention. Explicit access revocation/deletion must have a separately defined effect on future replay and queued presentations. If it forbids future presentation, invalidate local delivery state/handles and suppress relevant payloads before constructing the next request. Remote copies already delivered require provider-supported cleanup and cannot be recalled by deleting an AFS manifest.

Apply the chosen rules to both inline upload duplication and native provider handles. Record the source association/version on presentation copies so cleanup and replay decisions are implementable.

Verification: read an image/PDF, expire the source, revoke access, resume, switch provider, and replay full history. Assert the explicitly chosen behavior for each event; also test provider-handle expiry and pending jobs.

Evidence: `service/shared/toolexec/image_persistence.go:96` creates an inline model-request payload; `service/agent/binding_history_payloads.go` reads inline bytes before considering the stored URI.

## Architectural assessment

The main separation is appropriate: resource identity and storage do not depend on model capabilities; extraction and native presentation are distinct operations; original and derived content use the same handoff reference.

Reusing resources:readImage and extending resources:read with representation=native avoids duplicating adapters. Keeping source-specific authorization in the resolver is appropriate, provided alternate access paths and downstream consumers obey the declared trust model.

The continuation and native-PDF issues already documented are real integration blockers and have concrete mitigation directions. They do not require a new registry or a wholesale tool-result rewrite. Prefer a stable appended presentation carrier long term; treat full-history replay as a bounded compatibility fallback.

Implementation should also preserve the distinction between cached conversion bytes and presentation delivery state. Existing resources read/readImage methods are marked cacheable (supersession-eligible). Adding native behavior must ensure history projection cannot discard a pending presentation or its necessary provenance. The existing retry/cache/continuation acceptance tests should explicitly include supersession and context compaction.

## Final disposition

Keep the existing services and lenient absolute-path policy. Finding 2 is withdrawn. Findings 1, 3, and 4 are addressed at the design-contract level in the final design’s filesystem trust-domain, mutable-source/versioning, and retention sections. Their implementation and acceptance tests remain required; this review does not certify unimplemented changes.
