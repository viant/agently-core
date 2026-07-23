# Agently Reporting

This is the canonical feature guide for reports, report-builder dashboards,
progressive inline reports, persistence, execution, and export in Agently.

All reporting sources converge on the same canonical Forge report model:

```text
preset | saved report | inline report | dashboard import
  -> ReportDocument
  -> ReportSpec
  -> ReportFill
  -> ReportPrint
  -> web / iOS / Android / PDF
```

There is no separate dashboard renderer or inline-only reporting stack.

## Feature Model

### Report sources

Users can work with:

- **Presets**: workspace-owned, read-only report starters with names and
  descriptions.
- **Saved reports**: user-owned report definitions stored by Agently Core.
- **Inline reports**: one-off reports assembled from assistant fences.
- **Dashboard imports**: legacy dashboard metadata adapted to canonical report
  primitives.

Opening a preset creates an editable report instance without mutating the
preset. A clean inline report may be saved when all live datasets reference
registered workspace data sources.

### Multi-dataset reports

A report may declare several datasets. Every dataset has a stable id and either:

- a registered workspace `dataSourceRef`, or
- materialized JSON/CSV rows for an ephemeral inline report.

Every dataset-backed block identifies its dataset explicitly with
`datasetRef`. Reports may combine KPI, narrative, chart, table, map, timeline,
collection, callout, tab, and other canonical primitives without introducing a
second dashboard schema.

Live dataset declarations may include portable request, result-contract,
field, format, and capability metadata. Provider-specific details remain
optional capability metadata; generic report behavior must not require Datly,
Steward, or another provider.

### Filters and scope

Runtime scope is layered:

1. entry context, such as order, campaign, line, or audience
2. per-dataset scope and local date windows
3. block refinements, drill state, and user selections

The layers merge into one provider-neutral request per dataset. Provider
adapters translate that request to the backing tool or MCP server. Preview,
reopened runtime, and backend export use the same persisted dataset envelope so
filters cannot silently diverge between table, chart, and PDF.

### Builder and runtime

The visual builder supports:

- adding, editing, removing, and labeling datasets
- binding every block to a dataset
- arranging blocks on the shared 12-column grid
- selecting measures, dimensions, breakdowns, and filters
- importing/exporting report metadata
- saving an editable report under the effective authenticated user

Executed reports show report content, filters, and runtime refinements rather
than design controls. Materialized inline reports already contain data and do
not require a Run action.

### Persistence and listing

Agently Core is the durable reporting boundary. Saved definitions, export
jobs, export artifacts, shared artifacts, and audit events are persisted by the
configured reporting store. Workspace files contain presets and datasource
metadata, not user reports.

All saved-report reads and mutations are gated by the effective user derived
from `JWT.subject`. A user request such as "list my reports" may return both
workspace presets and reports visible to that user, while preserving the
read-only distinction for presets.

### Execution and export

Execution and export use a unified source selector:

- `preset`
- `report`
- `inline`

Backend execution resolves registered datasets with the current auth context.
Export accepts the canonical report artifacts, runs asynchronously when data
must be materialized, and returns an artifact id plus a downloadable target.
The download must have a reader-facing filename and the correct MIME type.

The UI emits report lifecycle events through its configured report-event
handler:

- `report.run_start`
- `report.run`
- `report.export_start`
- `report.export_complete`

Events identify the report, normalized source kind, effective filters,
conversation/window context, and available run/job/artifact identifiers.
Export completion includes `artifactId` and `targetUrl` when available.

## Progressive Inline Reports

### Goal

Allow an assistant response to build one inline report progressively:

1. publish a datasource,
2. publish the report blocks that use it,
3. publish another datasource,
4. append or update more report blocks,
5. finish with one coherent interactive report.

The authoring experience should retain the concise original dashboard grammar
while lowering to the canonical Forge `ReportDocument` model. The assembled
report must therefore support the same web, mobile, execution, persistence, and
PDF paths as a report created in the visual builder.

### Existing behavior

The current fenced-content contract supports:

- one or more `forge-data` fences,
- followed by a complete `forge-ui` fence,
- with one UI tree rendered from the datasource snapshot available to it.

Existing `forge-data` modes are `replace`, `append`, and JSON-object `patch`.
Existing `forge-ui` dashboard blocks are registry-backed and can be converted
to canonical report primitives by Forge's dashboard-to-report adapter.

This remains valid and unchanged. In particular, repeated `forge-ui` fences do
not silently become fragments of one report; changing that meaning would break
existing transcripts and make ownership ambiguous.

### Dedicated fence

Add a third registered fence type:

```text
forge-report
```

`forge-report` is an assembly instruction for one inline report. It is not a
new renderer or a second report schema. The runtime assembles its source
definition, then Forge lowers and validates it as one canonical
`ReportDocument`.

#### Why not overload `forge-ui`?

- `forge-ui` currently represents a complete render tree.
- Multiple complete UI trees in one response are valid today.
- Progressive composition needs stable identity, ordering, idempotency, and
  explicit completion semantics.
- A separate fence makes partial state visible to the parser without changing
  old transcript behavior.

### Core envelope

Every `forge-report` fence uses this envelope:

```json
{
  "version": 1,
  "scope": "campaign_analysis",
  "id": "delivery_brief",
  "sequence": 1,
  "mode": "start",
  "grammar": "dashboard-v1",
  "target": { "kind": "report", "ref": "root" },
  "title": "Delivery Brief",
  "blocks": []
}
```

Fields:

| Field | Required | Meaning |
| --- | --- | --- |
| `version` | yes | Envelope schema version. Initially `1`. |
| `scope` | no | Stable namespace shared by related data and report instances. Defaults to the assistant message scope. |
| `id` | yes | Stable report instance/assembly id within the scope. |
| `mode` | yes | `start`, `append`, `patch`, `replace`, or `commit`. |
| `grammar` | no | `dashboard-v1` (default) or `report-document-v1`. Immutable after start. |
| `target` | no | Report root or nested block/slot receiving the operation. Defaults to the report root. |
| `sequence` | yes | Monotonic transaction number shared by data and report fences for this assembly. |
| `title` | no | Reader-facing title. May be patched later; omitted titles fall back to the report id or the host's localized inline-report label. |
| `subtitle` | no | Reader-facing subtitle. |
| `description` | no | Reader-facing report description. |
| `theme` | no | Canonical constrained report theme tokens. |
| `blocks` | by operation | Dashboard blocks or canonical report blocks. |
| `layout` | no | Shared 12-column layout metadata. |
| `removeBlockIds` | patch only | Explicit block removals. |
| `fallback` | no | Short bounded Markdown shown by clients that cannot render progressive reports. |
| `metadata` | no | Safe provenance and generation metadata. |

The canonical identity is in the JSON body. Fence-header attributes may be
accepted as shorthand, but body values win and all SDKs normalize to the body
shape.

### Datasource association

Version progressive report data as `forge-data` version `2`. Version 2 adds
`scope`, `reportRef`, and `sequence`; existing version-1 parsers remain strict
and unchanged.

```json
{
  "version": 2,
  "scope": "campaign_analysis",
  "id": "delivery_summary",
  "reportRef": "delivery_brief",
  "sequence": 1,
  "format": "json",
  "mode": "replace",
  "data": [
    { "spend": 120000, "impressions": 8400000 }
  ]
}
```

Rules:

1. A datasource participating in progressive assembly MUST use version `2` and
   MUST declare `scope`, `reportRef`, and `sequence`.
2. `scope` uses the existing Forge dashboard scope concept. A datasource is
   registered as `scope + reportRef + datasource id`, so multiple report
   instances in one message cannot collide even when they reuse local ids.
3. `reportRef` assigns the data transaction to one report instance. Shared data
   is represented by repeating an idempotent version-2 data transaction for
   each report in version 1 of this proposal; cross-instance mutable data is
   deliberately deferred.
4. A version-1 `forge-data` fence without `reportRef` remains compatible with
   existing `forge-ui` behavior and is never implicitly claimed by a report.
5. Datasource ids are unique within one report assembly. A later fence with the same id
   applies its declared `replace`, `append`, or `patch` operation.
6. A block resolves an unqualified `dataSourceRef` in its own report assembly.
   Cross-scope references are rejected in version 1.
7. Static data remains transcript-scoped. A live datasource declaration is a
   workspace reference, not embedded credentials or executable code.

Namespace rules are explicit:

- legacy UI data key: `messageId + datasource id`,
- progressive report data key: `messageId + scope + reportRef + datasource id`,
- there is no visibility between the two namespaces,
- identical local ids in different report instances never collide.

A version-2 data transaction may arrive before `forge-report start`. It creates
only a bounded pending assembly slot; it does not render or execute anything
until the matching report start is accepted.

A pending data-only assembly counts toward both the reports-per-message and
data-size limits. If the assistant message ends without a matching report
`start`, it becomes terminal `orphaned`, emits a stable diagnostic, and is not
eligible for implicit commit, save, execution, or export.

## Scope, Instance, and Target

Progressive reports reuse the dashboard runtime's scoped-instance model rather
than creating global transcript state.

### Scope

`scope` is the logical namespace for related report instances, datasource
identities,
filters, selections, and events. It is normalized with the same safe segment
rules as the existing Forge dashboard scope.

Examples:

- `scope: "campaign_analysis"`
- `scope: "order_2680567"`
- omitted scope -> the current assistant message scope

### Instance

`id` is the report instance id inside the scope. Two reports may use the same
workspace datasource declarations or equivalent static snapshots while
retaining independent filters, selections, progressive assembly state, and
export state:

```text
forge-report:scope:campaign_analysis:delivery_brief
forge-report:scope:campaign_analysis:inventory_brief
```

The runtime derives a report instance key equivalent to the dashboard key:

```text
conversationId + messageId + normalized scope + report id
```

Every report event includes that key. In version 1, a selection or filter
change in one instance cannot affect another instance. Cross-instance mutable
filter or selection binding is deliberately unsupported.

If several instances originate from one reusable definition, optional
`metadata.definitionRef` records provenance; it does not replace the instance
`id`.

For example, one response may start both of these independent instances:

```json
{ "version": 1, "scope": "order_2680567", "id": "delivery", "sequence": 1, "mode": "start", "grammar": "dashboard-v1", "title": "Delivery" }
```

```json
{ "version": 1, "scope": "order_2680567", "id": "inventory", "sequence": 1, "mode": "start", "grammar": "dashboard-v1", "title": "Inventory" }
```

Each instance has its own sequence space because the assembly key includes the
report id.

### Target

`target` routes one report operation without introducing positional JSON paths:

```json
{ "kind": "report", "ref": "root" }
```

```json
{
  "kind": "block",
  "ref": "inventory_group",
  "slot": "childBlockIds",
  "position": "append"
}
```

Supported version-1 target fields:

| Field | Values | Meaning |
| --- | --- | --- |
| `kind` | `report`, `block` | Target the report root or one existing parent block. |
| `ref` | `root` or block id | Stable target identity. |
| `slot` | `childBlockIds`, `sectionIds` | Canonical reference slot accepted by `compositeBlock` or `tabGroupBlock` in `report-document-v1`. |
| `position` | `append` | Append new root blocks and register their ids with the target container. |

Rules:

- omitted target means `{ "kind": "report", "ref": "root", "position": "append" }`,
- the report root accepts only `position: "append"`; replacing the complete
  report uses envelope `mode: "replace"`,
- target resolution uses the valid snapshot from before the fragment; a block
  created in the same fragment cannot also be its target,
- the target primitive must advertise the requested slot in the registry,
- `dashboard-v1` accepts only the report root target because the existing
  dashboard grammar and adapter expose a flat 12-column block list,
- nested block targets and registered child slots are available only in
  `report-document-v1`,
- target routing is structural only; it cannot escape the report instance,
- patch and remove operations still identify affected blocks by stable id,
- a failed target operation leaves the previous report snapshot unchanged.

This enables a large canonical report to be authored in stable groups. For
example, one `report-document-v1` fragment may establish an
`inventory_group` composite, and later fragments may target its
`childBlockIds` slot with a chart, table, narrative, and detail panel. The new
blocks remain canonical root blocks; the target operation adds their ids to
the composite. A `tabGroupBlock` similarly accepts appended `sectionBlock`
ids through `sectionIds`. Stepper steps and kanban columns are block content,
not report primitives, and are updated with block patch operations. A large
`dashboard-v1` report appends stable-id blocks to the root.

## Progressive Example

The following response renders one report, not three separate dashboards:

````text
```forge-data
{
  "version": 2,
  "scope": "campaign_analysis",
  "id": "delivery_summary",
  "reportRef": "delivery_brief",
  "sequence": 1,
  "format": "json",
  "mode": "replace",
  "data": [{ "spend": 120000, "impressions": 8400000 }]
}
```

```forge-report
{
  "version": 1,
  "scope": "campaign_analysis",
  "id": "delivery_brief",
  "sequence": 2,
  "mode": "start",
  "grammar": "dashboard-v1",
  "title": "Delivery Brief",
  "blocks": [
    {
      "id": "summary",
      "kind": "dashboard.summary",
      "dataSourceRef": "delivery_summary",
      "metrics": [
        { "field": "spend", "label": "Spend", "format": "currency" },
        { "field": "impressions", "label": "Impressions", "format": "compactNumber" }
      ]
    }
  ]
}
```

```forge-data
{
  "version": 2,
  "scope": "campaign_analysis",
  "id": "daily_delivery",
  "reportRef": "delivery_brief",
  "sequence": 3,
  "format": "csv",
  "mode": "replace",
  "data": "date,channel,spend\n2026-07-17,CTV,43000\n2026-07-18,Display,31000"
}
```

```forge-report
{
  "version": 1,
  "scope": "campaign_analysis",
  "id": "delivery_brief",
  "sequence": 4,
  "mode": "append",
  "blocks": [
    {
      "id": "daily_spend",
      "kind": "dashboard.timeline",
      "title": "Daily Spend",
      "dataSourceRef": "daily_delivery",
      "chart": {
        "type": "line",
        "xAxis": { "dataKey": "date" },
        "series": {
          "nameKey": "channel",
          "valueKey": "spend"
        }
      },
      "columnSpan": 8
    },
    {
      "id": "daily_table",
      "kind": "dashboard.table",
      "title": "Delivery Detail",
      "dataSourceRef": "daily_delivery",
      "columns": [
        { "key": "date", "label": "Date" },
        { "key": "channel", "label": "Channel" },
        { "key": "spend", "label": "Spend", "format": "currency" }
      ],
      "columnSpan": 12
    }
  ]
}
```

```forge-report
{
  "version": 1,
  "scope": "campaign_analysis",
  "id": "delivery_brief",
  "sequence": 5,
  "mode": "commit"
}
```
````

While streaming, sequence 1 stages data and sequence 2 renders the summary.
Sequences 3 and 4 add another datasource plus its chart and table without
replacing the summary. Sequence 5 marks the report complete and enables final
compile/export status.

## Grammar Modes

### `dashboard-v1`

This is the recommended LLM-facing authoring grammar. It accepts all existing
dashboard kinds supported by the Forge dashboard adapter:

- `dashboard.summary`
- `dashboard.kpiTable`
- `dashboard.compare`
- `dashboard.timeline`
- `dashboard.composition`
- `dashboard.dimensions`
- `dashboard.geoMap`
- `dashboard.status`
- `dashboard.filters`
- `dashboard.feed`
- `dashboard.table`
- `dashboard.report`
- `dashboard.detail`
- `dashboard.messages`
- `dashboard.badges`

The assembler retains the complete source-level dashboard definition and
reruns the adapter after every accepted fragment. This is important: adapting
each fragment independently would lose cross-block filter, selection, detail,
and layout context.

The adapter MUST derive every canonical block id deterministically from the
source dashboard block id plus a stable semantic role suffix. It must not use a
mutable array ordinal. When one dashboard block lowers to several canonical
nodes, each node receives a distinct deterministic suffix. Web, iOS, Android,
and PDF consume those canonical ids for state retention and diagnostics.

### `report-document-v1`

This advanced mode accepts canonical authored report blocks directly:

- `markdownBlock`
- `filterBarBlock`
- `refinementBarBlock`
- `kpiBlock`
- `badgesBlock`
- `chartBlock`
- `tableBlock`
- `geoMapBlock`
- `sectionBlock`
- `tabGroupBlock`
- `compositeBlock`
- `stepperBlock`
- `infoPanelBlock`
- `calloutBlock`
- `kanbanBlock`
- `timelineBlock`
- `collectionBlock`

It is intended for report-builder-aware agents and exact round trips. The two
grammars cannot be mixed inside one report id.

## Merge Semantics

### `start`

- transitions a pending data-only assembly into a renderable report assembly,
- requires an id that has not already accepted a report `start`,
- establishes grammar and report metadata,
- defaults an omitted grammar to `dashboard-v1`,
- may include initial blocks and layout.

### `append`

- adds new blocks in source order at the resolved `target`,
- requires every appended block id to be new,
- rejects accidental duplicate ids rather than silently replacing content,
- may add layout items for the new blocks.

### `patch`

- updates report metadata or existing blocks by id,
- uses RFC 7386 JSON Merge Patch semantics for block fields,
- supports `removeBlockIds` for explicit deletion,
- never changes the report grammar,
- never treats an omitted field as deletion,
- treats an explicit `null` property as deletion,
- replaces arrays as whole values; updating one column, metric, or series means
  resending that complete array,
- rejects `null` block entries; `removeBlockIds` is the only block-deletion
  mechanism.

### `replace`

- replaces the assembled source definition for the report id,
- retains the report id and transcript ownership,
- requires grammar and a complete valid source definition,
- must restate the established grammar; a mismatch is rejected,
- atomically frees all replaced block ids, so the replacement payload may
  reuse them,
- treats every replaced block as removed for interaction-state purposes, even
  when the replacement reuses the same id.

### `commit`

- marks the assembly complete,
- runs final source validation, adapter validation, `ReportDocument` compile,
  and export-readiness validation,
- does not itself persist the report to the user's saved-report store,
- transitions the assembly to a terminal committed state.

End-of-message acts as an implicit commit when no explicit `commit` fence was
emitted. Explicit and implicit commit apply identical validation. If commit
fails, the last valid snapshot remains visible with terminal state `incomplete`;
save and export stay disabled. Any later data or report fragment addressed to a
committed assembly is rejected as `REPORT_ALREADY_COMMITTED` without changing
the committed snapshot.

## Ordering and Layout

- Block order is first appearance unless explicitly changed by a patch.
- `append` adds blocks after the current last block by default.
- For large nested reports, envelope `target` is preferred over repeating
  placement fields on every block.
- Version 1 does not support relative `before`/`after` insertion. Reordering is
  deferred until a stable explicit order-patch contract is defined.
- Dashboard `columnSpan` continues to use the existing 12-column layout.
- Canonical report layout items use the same 12-column spans.
- Mobile stacks blocks in source order while retaining desktop spans as
  metadata.
- A fragment cannot leave a layout item referencing an unknown block after the
  fragment transaction commits.

## Streaming and Transaction Model

Each closed fence is one atomic transaction:

1. parse the envelope,
2. validate fence-local shape,
3. clone the current assembly,
4. apply the data or report operation,
5. validate references available at that sequence,
6. adapt/lower the complete assembly,
7. publish the new immutable snapshot through the canonical ReportDocument
   runtime.

If a fragment fails, the prior valid snapshot remains visible. Diagnostics are
attached to the failed sequence; partially applied blocks or rows are never
shown.

An open trailing fence renders a progress placeholder. A closed valid report
fragment renders the latest report snapshot immediately. The UI should not
render a separate card for each fragment.

Publishing a new snapshot MUST preserve user-held interaction state for stable
ids, including filter values, report selection, active tab/step, table sort,
and locally selected measure or breakdown. State for removed ids is discarded.
For `dashboard-v1`, stable ids are the deterministic canonical ids derived by
the adapter from source block ids and semantic role suffixes. A full `replace`
discards prior interaction state as specified above.
The same state-retention fixtures must pass on web, iOS, and Android.

## Forward References

Default rule: a block's datasource must exist when that report fragment closes.
This gives immediate, useful validation and avoids flicker.

An optional future `deferReferences: true` mode may allow forward references,
but export, execution, and explicit commit must remain disabled until all
references resolve. It is not required for version 1.

## Identity, Replay, and Idempotency

Assembly key:

```text
conversationId + messageId + normalized scope + report.id
```

This prevents two messages using the same human-friendly report id from
mutating each other.

Every progressive version-2 data transaction and every `forge-report`
transaction MUST use one monotonic sequence space per assembly:

- a repeated sequence with identical canonical JSON content is an idempotent
  replay; object keys are sorted and whitespace is ignored,
- a repeated sequence with different content is a conflict,
- lower sequences arriving after higher sequences are ignored as stale,
- a received but rejected transaction consumes its sequence and retains a
  diagnostic, so correction must use a new sequence,
- a gap means a sequence was never received,
- gaps are allowed while streaming but both explicit and implicit commit fail
  until resolved.

The reducer records accepted, rejected, and missing sequence slots. This makes
stream retry deterministic and prevents duplicate `forge-data append`
transactions from duplicating rows.

## Runtime and Export

After every valid fragment, the assembler produces the same runtime inputs as
the visual report builder:

```text
assembled source
  -> dashboard adapter when needed
  -> ReportDocument
  -> ReportSpec
  -> ReportFill
  -> ReportPrint / PDF
```

Progressive snapshots MUST always render through the lowered canonical
ReportDocument runtime. `dashboard-v1` source must not take a separate legacy
dashboard render path, even before commit.

Consequences:

- table and chart blocks use the same resolved datasets,
- filter values appear in runtime and PDF,
- macros and narration resolve through the canonical report macro context,
- all supported primitives have one web/mobile/PDF interpretation,
- inline reports can be saved through the existing authenticated report-store
  contract without inventing inline-specific persistence.

During progressive assembly:

- interactive filtering may be enabled for the latest valid snapshot,
- export is disabled until explicit or implicit commit succeeds,
- saving is disabled until the report compiles cleanly,
- design mode is not shown inline by default,
- measure, breakdown, and filter controls may be exposed when declared by the
  assembled report metadata.

## Live Datasources

Version 1 supports two datasource forms:

1. static `forge-data` payloads (`json` or `csv`),
2. references to workspace-owned datasource metadata.

A report fragment may declare a reference without embedding workspace logic:

```json
{
  "id": "delivery_live",
  "kind": "workspaceRef",
  "dataSourceRef": "metrics_ad_cube_report"
}
```

Forge interprets the generic declaration. The host workspace owns endpoint,
query, semantic model, credentials, and customization metadata. No Steward
logic belongs in Forge or Agently Core.

`workspaceRef` is resolved only from the host-provided datasource catalog
available to the current agent/workspace. A model-authored datasource id that
is absent from that allowlist is rejected before execution.

Live execution remains a backend responsibility. The effective auth token is
propagated to the datasource MCP/tool call, and saved-report access remains
gated by the effective authenticated user id derived from `JWT.subject`.

## Persistence Boundary

An inline assembly is ephemeral transcript/runtime state by default. It is not
the saved-report database.

When the user chooses **Save report**:

- the committed canonical `ReportDocument`, `ReportSpec`, `ReportFill`, and
  `ReportPrint` use the existing report-store contract,
- ownership is assigned by the authenticated backend, never trusted from fence
  content,
- the saved report can later be listed, executed, exported, or reopened like a
  visually authored report.

Scratchpad may hold temporary large payloads during one execution, but it is
not the durable report store.

## Validation and Diagnostics

Validation has four levels:

1. **Fence**: JSON, version, id, operation, format.
2. **Assembly**: sequence, unique ids, legal merge, datasource references.
3. **Grammar**: dashboard or canonical report block schema.
4. **Compile**: ReportDocument/Spec/Fill/Print compatibility and export parity.

Diagnostics must include:

- report id,
- sequence when available,
- fence type,
- block or datasource id,
- stable error code,
- JSON path,
- actionable suggested fix.

Unknown block kinds are errors, never silently discarded. Unsupported future
fields remain strict-schema errors unless explicitly namespaced under
`metadata.extensions`.

## Security

- Only registered block kinds and formatter helpers are allowed.
- No raw HTML, JavaScript, SQL, credentials, or arbitrary executable callbacks.
- Markdown uses the existing safe renderer.
- Workspace host actions cross the existing explicit host-action boundary.
- Static payload size, row count, block count, nesting depth, and total report
  size are bounded.
- Backend datasource execution propagates effective auth and enforces the
  datasource's own authorization.
- Live datasource ids must exist in the host-provided allowlist for the current
  workspace and effective user.
- Fence-provided owner ids, auth headers, DSNs, or secret references are
  rejected.

## Suggested Limits for Version 1

| Limit | Initial value |
| --- | ---: |
| Reports per assistant message | 4 |
| Report fragments per report | 64 |
| Blocks per assembled report | 100 |
| Datasources per report | 32 |
| Static rows per datasource | 10,000 |
| Uncompressed static data per report | 5 MB |
| Uncompressed static data per assistant message | 10 MB |
| Assembled report source | 5 MB |
| Composite nesting depth | 8 |

Host deployments may lower these limits. Increasing them should require an
explicit server policy rather than model-authored metadata.

## Compatibility

- Existing `forge-ui` and `forge-data` responses render unchanged.
- Existing one-shot dashboards can be imported into the report builder through
  the current adapter.
- A one-shot `forge-report start` followed by message end is valid.
- SDKs that do not understand `forge-report` should preserve the fence as
  ordinary code rather than partially interpreting it.
- Web, iOS, and Android must decode the same normalized transcript-envelope
  fixture before the feature is considered complete. Agently Core owns the
  authoritative transaction reducer; native clients consume its canonical
  snapshots and MUST NOT independently reassemble report transactions. The web
  raw-fence fallback reducer shares the server's assembly fixtures because it
  may render local streaming text before a canonical snapshot arrives.

## Contract Rules

- live datasource declarations belong in `forge-report.datasets`; data values
  belong in `forge-data`,
- a later assistant turn cannot mutate an earlier inline assembly; save the
  report first, then update it through the authenticated report API,
- valid snapshots may display and accept local interaction before commit,
- explicit or implicit clean commit is mandatory for save/export,
- version 1 supports target `append`, not target replacement or relative reordering,
- dashboard-v1 targets only the report root; nested targets require
  report-document-v1,
- cross-instance mutable filter and selection binding is not supported,
- progressive report data uses strict `forge-data` version 2,
- report association is always explicit; no datasource claiming heuristic.
