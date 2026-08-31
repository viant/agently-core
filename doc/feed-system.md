# Feed system

A **Tool Feed** is a declarative, conversation-scoped projection of tool JSON
into a Forge UI. A feed declaration selects tool calls, resolves one canonical
payload, derives named datasources, chooses a presentation target, and may
declare local or submitted interactions.

The feed system is domain-neutral. Workspaces own business fields, lookup
sources, callback names, write policy, and domain tool selection.

## Guarantees

- One canonical tool payload supplies all feed sections and placements.
- Existing feed declarations remain valid.
- Missing or unknown presentation targets use legacy `auto` placement.
- Feed state is isolated by conversation and feed ID.
- Preview patches do not invoke domain tools.
- Visual and agent edits use one patch-operation contract.
- Web, TypeScript, Android, and iOS expose compatible feed models.
- Local selection callbacks do not create chat turns.
- Stale replay or delayed fetches cannot overwrite a newer dirty preview.
- The generic runtime contains no workspace-specific business policy.

## Package map

| Path | Responsibility |
|---|---|
| [`sdk/api/feed.go`](../sdk/api/feed.go) | Feed declaration, presentation, match, activation, and state models |
| [`sdk/feed.go`](../sdk/feed.go) | Workspace registry, normalization, tool-name matching, SSE emission |
| [`sdk/feed_resolver.go`](../sdk/feed_resolver.go) | Resolve active feed data from tool/transcript history |
| [`sdk/embedded_feeds.go`](../sdk/embedded_feeds.go) | Canonical feed materialization for embedded clients |
| [`internal/feedextract/`](../internal/feedextract/) | Projection, derivation, merge, and unique-key behavior |
| [`sdk/handler_feeds.go`](../sdk/handler_feeds.go) | Feed discovery and data HTTP APIs |
| [`runtime/streaming/event.go`](../runtime/streaming/event.go) | Feed lifecycle SSE fields |
| [`sdk/api/canonical.go`](../sdk/api/canonical.go), [`sdk/canonical_reducer.go`](../sdk/canonical_reducer.go) | Canonical active-feed state |
| [`protocol/tool/service/ui/feed/service.go`](../protocol/tool/service/ui/feed/service.go) | Live `ui/feed:get` and `ui/feed:update` service |
| [`sdk/ts/src/`](../sdk/ts/src/) | TypeScript models, tracker, patch helper, client methods |
| [`sdk/android/`](../sdk/android/) | Android models and client methods |
| [`sdk/ios/`](../sdk/ios/) | iOS models and client methods |
| Workspace `feeds/*.yaml` | Feed declarations and business-owned UI |

The Agently web implementation is in `viant/agently/ui`; reusable visual
primitives are in `viant/forge`.

## Declaration

```yaml
id: editable-catalog
title: Editable catalog
developerOnly: false

presentation:
  icon: applications
  accent: '#3857d6'
  target: inline
  suppressReportIds: [legacy_catalog_report]

match:
  service: catalog
  method: '*'

activation:
  kind: history
  scope: last

dataSource:
  result:
    source: output

  record:
    dataSourceRef: result
    selectors: { data: record }

  items:
    dataSourceRef: record
    selectors: { data: items }
    selectionMode: multi
    uniqueKey: [{ field: id }]
    paging: { enabled: true, size: 20 }

ui:
  title: Editable catalog
  containers:
    - id: itemTable
      kind: dashboard.editableTable
      dataSourceRef: items
      columns:
        - { key: name, label: Name, editor: { type: text } }
        - { key: amount, label: Amount, editor: { type: number } }
```

`dataSource` is singular in the persisted feed declaration. Clients receive
the normalized map as `dataSources` in the feed-data response.

## Tool matching

Feed matching accepts display and canonical tool names:

- `service/method_name`;
- `service:method_name`;
- canonical `service_path-method_name`.

Only the service segment normalizes underscores/colons into `/`. Method
underscores are preserved. `method: '*'` matches all methods of the selected
service.

Matching is independent of whether the tool is internal or provided by MCP.

## Activation

### `history`

Scans recorded tool calls and projects matching payloads. Use this for a feed
representing the result that produced the current conversation state.

- `scope: last` uses the newest match.
- `scope: all` merges all applicable matches according to datasource rules.

### `tool_call`

Resolves data on demand by executing the declared activation tool. Use only
for safe reads; the feed layer does not make a mutating tool safe.

## Projection

Named datasources form a dependency graph.

- `source` selects the canonical root, normally `output` or `input`.
- `dataSourceRef` selects a parent datasource.
- `selectors.data` selects a dot/numeric-index path from the parent.
- `fields` projects form/display fields and transforms.
- `flatten` expands nested parent/child collections.
- `exclude` removes sentinel or aggregate rows.
- `aggregate` derives values such as row counts.
- `derive` builds computed fields from templates.
- `uniqueKey` defines row identity across merge/refresh.
- `merge` supports `append`, `union`, `merge_object`, and `replace_last`.
- `paging` is client presentation metadata; it does not truncate canonical
  state.

Selectors support dot paths and numeric indices, for example
`record.items.0.details`. No XML XPath or feed-specific query language is
used.

The projection implementation is shared with the datasource/lookup system in
[`internal/feedextract/`](../internal/feedextract/).

## Presentation

`FeedPresentation` fields:

| Field | Meaning |
|---|---|
| `icon` | Client visual hint |
| `accent` | Client accent-color hint |
| `target` | `auto`, `inline`, `workspace`, or `detached` |
| `suppressReportIds` | Legacy report IDs omitted when this feed owns the visual result |

Target behavior:

- `auto`: legacy client-selected behavior.
- `inline`: render once after the final assistant/iteration row representing
  the owning turn. A newer preview can move the same feed to the current turn;
  unrelated later turns do not move or duplicate it.
- `workspace`: render in the current conversation's Tool Feed workspace.
- `detached`: render through an independent launcher/drawer.

Explicit inline and detached feeds are excluded from workspace tabs. Feed
visibility does not depend on developer mode; only `developerOnly: true` does.

Presentation travels through feed-list/data HTTP responses,
`tool_feed_active`, canonical replay, and SDK models.

## Lifecycle and HTTP APIs

SSE event types:

- `tool_feed_active` — feed has data; includes feed ID/title, presentation,
  item count, conversation/turn/message identity, and optional inline data.
- `tool_feed_inactive` — feed no longer has active data.

HTTP:

- `GET /v1/feeds` lists declarations, match metadata, and presentation.
- `GET /v1/feeds/{id}/data?conversationId={conversationId}` returns:
  `feedId`, `title`, `developerOnly`, `presentation`, `data`, `dataSources`, and
  `ui`.

Feed JSON containing normalized redaction markers is repaired before response
serialization. Invalid unrecoverable feed JSON is not passed to the renderer.

## Live draft service

The server registers an internal `ui/feed` service backed by the attached
Forge UI bridge.

### `ui/feed:get`

Input:

```json
{
  "clientId": "optional",
  "feedId": "editable-catalog",
  "dataSourceRefs": ["record", "items"]
}
```

Constraints:

- conversation ID is taken from execution context;
- an attached UI client for that conversation is required;
- preferred client ID is used when available;
- 1–32 datasource refs are required.

Output:

```json
{
  "clientId": "selected-client",
  "data": {
    "conversationId": "conversation-id",
    "feedId": "editable-catalog",
    "dataSources": {
      "record": { "form": {}, "collection": [], "selection": {} },
      "items": { "form": {}, "collection": [], "selection": {} }
    }
  }
}
```

### `ui/feed:update`

Input:

```json
{
  "clientId": "optional",
  "feedId": "editable-catalog",
  "operations": [
    {
      "dataSourceRef": "items",
      "op": "replace",
      "path": "/collection/2/amount",
      "value": 30
    }
  ]
}
```

Constraints:

- 1–64 operations;
- each operation has a datasource ref;
- op is `add`, `replace`, or `remove`;
- path begins with `/`.

Output contains `clientId`, `ok`, and optional `error`.

The service sends `ui.feed.get`/`ui.feed.update` to the selected client. The
update includes the current turn ID so the client can move an inline preview
without adding a second feed.

## Patch semantics

`FeedPatchOperation` is:

```ts
type FeedPatchOperation = {
  dataSourceRef: string;
  op: 'add' | 'replace' | 'remove';
  path: string;
  value?: unknown;
};
```

Paths are JSON Pointers relative to a datasource view:

- `/collection/0/name` — first collection row;
- `/collection/2/name` — middle collection row;
- `/collection/4/name` — last row of a five-row collection;
- `/form/title` — form field;
- `/selection/selection/0/name` — selected-row view.

The web adapter maps the view-relative path through `source`,
`dataSourceRef`, and `selectors.data` to the canonical payload. Equivalent
patches from two views are deduplicated.

After patching, every dependent datasource is recomputed and all Forge signals
are rewired. Array changes are applied once to avoid shifted-index double
removal. Dirty refs are restored after rewiring.

## Concurrency and replay

Clients must preserve local intent across asynchronous feed events:

- cache keys combine conversation ID and raw feed ID;
- a preview records its owning turn;
- older or same-turn active events cannot replace dirty preview data;
- delayed feed-data responses merge presentation/UI metadata but retain the
  latest dirty data;
- a later authoritative turn may replace the preview;
- pending draft snapshots may be restored after a failed submit;
- selection is reconciled by `uniqueKey` when data refreshes.

These rules prevent preview flicker, stale rollback, and cross-conversation
feed leakage.

## Interaction and callbacks

A selectable datasource may declare:

```yaml
selectionMode: multi
uniqueKey: [{ field: id }]
selection:
  initial: all
  field: selected
  feedbackDataSourceRef: selectionStatus
  callback:
    type: local
    eventName: catalog_selection_changed
```

Selection payloads contain:

- `feedId`, `conversationId`, and `dataSourceRef`;
- `eventName` and action (`selected`/`unselected`);
- changed row;
- selected, unselected, and changed rows;
- final datasource snapshot;
- optional callback metadata/context.

Callback types:

- `local` — UI-only feedback/audit state, no chat turn;
- `llm_event` — structured foreground/background conversational event;
- workspace/custom callback — dispatched through the existing Forge action
  router.

Submit handlers can combine one form datasource with multiple collection/
selection datasources into one snapshot. Business identifiers and tool names
must come from declarations or data, never ambient UI heuristics.

## Generic Forge primitives

Feeds may use the normal Forge container vocabulary, including:

- section tabs with optional print-time `expandAll`;
- summary, KPI, chart, composition, timeline, status, detail, and table blocks;
- `dashboard.editableTable` with add/remove, quick filter, pagination, text,
  number, select, tags, time, and structured frequency editors;
- `dashboard.lookupChips` with static or datasource lookup, provider choices,
  duplicate suppression, table/chip selection presentation, and drill-down;
- quantitative table visuals such as data/progress bars;
- toolbar actions enabled or disabled by declared dirty datasource refs.

Signal-backed primitives must call `useSignals()` when they read Forge
signals; otherwise external `ui/feed:update` operations will not rerender.

### Shared declaration and platform presentation

Web, Android, and iOS consume the same `ui` and normalized `dataSources` from
one workspace feed declaration. A workspace must not fork the YAML merely to
change mobile layout. Platform renderers may choose compact native
presentation while preserving field identity, editor semantics, callbacks,
and patch paths.

Compact native presentation includes:

- a report-style section navigator for larger tab sets: previous, current
  title/count/menu, and next;
- compact summary/menu values as readable pills rather than fixed desktop
  cards;
- adaptive form grids, native boolean switches, and a native two-ended
  `dateRange` picker that preserves `{start, end}` data;
- editable collections as tables with one horizontally scrolling remainder;
- the declared `frozen: true` identifying column (or first column fallback)
  fixed on the leading edge and capped at 40% of the visible table width;
- semantic column widths: IDs/codes narrow, numeric fields moderate, names
  wider, and narrative fields widest;
- multiline narrative editors whose row height follows actual content within
  compact bounds;
- icon-only add/remove controls with accessible declaration-owned labels;
- generic pastel header/alternating-row surfaces selected by the Forge table
  primitive, never by workspace name or field vocabulary.

Lookup add/search belongs above the selected collection so it remains
reachable when the collection is large. `Filter selected rows` filters the
current collection; it is intentionally distinct from the lookup search that
queries available candidates.

Datasource declarations are decoded independently. Projection-only sources
receive non-fetching local Forge contexts. Lookup and drill datasource refs
found in UI metadata receive standard remote contexts when they are not
already declared. Nested `inputs`, provider-specific inputs, query paths, and
`inputBindings` are resolved before the standard datasource fetch. Candidate
labels normalize scalar or path-array `label`, `displayPath`, `path`, and
`value` fields.

This compact behavior is a Forge capability. Workspace-specific plan,
advertiser, deal, audience, or campaign policy remains in workspace YAML and
tools.

## Backend PDF export

`feed.print` materializes the current feed container/data map into reporting
`reportSpec`, `reportFill`, and `reportPrint` primitives. The client submits a
report-export request, waits for the backend job, retrieves the artifact, and
downloads the returned PDF bytes.

This is backend rendering. It is not browser `window.print` or DOM capture.
Interactive-only blocks are converted to printable report blocks; tabbed
sections can expand for print.

The authored toolbar action uses the semantic `pdf` icon token and an
`Export PDF` label. Native renderers map that token to their platform PDF
symbol. A mobile host must not substitute a generic print action or client-side
screen capture. Android and iOS both compile the current feed metadata plus
form/collection snapshots before submitting the same reporting export job.

## Mobile interaction events

Forge exposes a domain-neutral interaction observer. It emits user intent only
from native controls, without importing Agently or workspace policy:

- `feed.tab_changed` with container/tab identity, title, and index;
- `feed.form_changed` with datasource ref, field, scope, control type, and the
  current value.

Agently mobile hosts accept those events only for rendered `feed-*` windows,
attach conversation/window identity, debounce repeated text edits, and call
`ui/events:record`. Agents inspect:

- the current authoritative preview snapshot through `ui/feed:get`;
- recent user interaction intent through `ui/events:list`.

Interaction events are observational. They do not submit a chat message,
publish a draft, or invoke a domain mutation. Workspace-specific meaning stays
in the feed declaration and tools.

## SDK support

TypeScript, Android, and iOS expose:

- `FeedPresentation` and presentation target;
- active/canonical feed state;
- `FeedPatchOperation`;
- get/update feed draft inputs and outputs;
- `getFeedData`;
- `getFeedDraft`;
- `updateFeedDraft`.
- `recordUIEvent` and `listUIEvents` for conversation-scoped interaction
  inspection.

The mobile client methods execute the same internal `ui/feed` tools with an
explicit conversation ID. Presentation decisions remain client-specific while
the data/patch contract stays identical.

### Android host implementation

Android pairing is layered across the three generic projects:

| Project | Android responsibility |
|---|---|
| `viant/forge` | Datasource snapshot and JSON-Pointer view patch primitives; conversation-aware inline windows |
| `viant/agently-core` | Feed/presentation models, `feedTarget` SSE decoding, canonical `turnId`, get/update SDK facade |
| `viant/agently` | Placement routing, inline/drawer surfaces, report suppression, and native UI-bridge commands |

The Android SSE model must retain `feedTarget`, and canonical hydration must
retain `ActiveFeedState.turnId`. Without both fields, a client cannot place a
feed before the data fetch or bind an inline feed after transcript replay.

Native placement follows the same contract with an Android compatibility
default:

- `inline` renders after the assistant message whose `turnId` owns the feed;
- `workspace` renders in the Tool Feed launcher/drawer;
- `detached` renders in an independent Feed Apps launcher/drawer;
- `auto`, absent, and unknown targets use the legacy Android workspace surface;
- `developerOnly` feeds are excluded from ordinary user surfaces;
- canonical `suppressReportIds` removes duplicate transcript reports.

The native UI bridge accepts `ui.feed.get` and `ui.feed.update` only for a
rendered feed window matching both raw feed ID and conversation ID. Get returns
the requested `form`, `collection`, and `selection` views. Update applies the
same `FeedPatchOperation` shape to Forge signals and fails closed for missing
feeds, datasource refs, invalid operations, relative pointers, missing replace/
remove targets, and out-of-range array indices.

The Android host maps declared datasource views back to canonical roots,
recomputes dependent projections, reconciles selection by `uniqueKey`, and
retains dirty same-turn previews across delayed payload refreshes. Native dirty
state is currently in-memory; process-recreation persistence and complete Forge
visual primitive coverage remain separate gates.

### iOS host implementation

iOS uses the same three-project layering:

| Project | iOS responsibility |
|---|---|
| `viant/forge` | Actor-safe datasource snapshot/patch primitives, observable signal updates, and conversation-aware inline windows |
| `viant/agently-core` | Swift feed models and draft facade, `feedTarget` SSE decoding, canonical turn/presentation retention |
| `viant/agently` | SwiftUI placement, generic Forge feed materialization, report suppression, and Apple UI-bridge commands |

Placement is identical to Android:

- `inline` renders once after the last assistant transcript entry for the
  owning `turnId`;
- `workspace` renders in the Tool Feeds launcher/sheet;
- `detached` renders in an independent Feed Apps launcher/sheet;
- `auto`, absent, and unknown targets use the legacy workspace placement;
- `developerOnly` and `suppressReportIds` have the same visibility semantics.

The Swift feed host decodes content-shaped or single-container `ui`, decodes
declared datasources independently into Forge models, opens an exact
`feed-{feedId}-{conversationId}` window, hydrates datasource views, and renders
generic Forge containers. Its UI bridge accepts get/update only when that exact
conversation-scoped window exists.

Independent decoding is required because local projection declarations may
contain `fields`, `flatten`, `derive`, and other feed-only keys rather than a
remote transport definition. Such sources receive `autoFetch: false`; a local
projection must not erase its hydrated collection by attempting a transport
refresh. Inline feed-data loading also survives SwiftUI view recreation during
conversation bootstrap and can fall back to the canonical data carried by the
active feed snapshot.

The iOS `StreamPayload` must decode `feedTarget` even though
`FeedPresentation.target` exists in the public model; otherwise live placement
differs from canonical replay until the next state refresh.

For coordinated uncommitted development, Agently's Swift package accepts
`AGENTLY_IOS_SDK_PACKAGE_PATH`. The supplied path must have the package identity
`AgentlySDKPackage`. Release builds continue to use the pinned dependency and
must advance that dependency together with the host changes.

The iOS host uses the same canonical-root mapping, dependent projection
recomputation, selection reconciliation, and same-turn dirty replay rules as
Android. Its cache is actor-isolated and scoped by Forge runtime identity plus
window ID.

### Native canonical synchronization

Native hosts maintain a canonical payload beside each rendered feed window.
For every update they:

1. resolve the datasource's canonical path through its declaration ancestry;
2. merge current target-view edits into canonical state;
3. translate projected or flattened view paths to storage paths;
4. apply add/replace/remove once;
5. recompute every declared datasource;
6. update Forge form/collection signals;
7. reconcile selected rows by `uniqueKey`.

Writes to aggregate, derived, parent-projected, or constant-projected fields
fail closed. Selection membership changes that do not mutate row data remain
selection-view operations.

Cache identity is `(Forge runtime identity, window ID)`. Same-turn or missing-
turn payload refreshes cannot replace a dirty preview; a different non-empty
turn is authoritative. Closing the window removes the cache entry. This is
in-memory replay protection, not durable process-recreation persistence.

### Native projection parity

Android and iOS implement dot/bracket/numeric and `$` selectors, field
projection, date/boolean transforms, flatten sources, parent/constant fields,
exclusion rules, `aggregate.countAs`, `uniqueKey` deduplication, and derived
string templates. Projection runs both at initial hydration and after canonical
patches so mobile get/update snapshots use the same logical datasource shapes
as web.

## Mutation boundary

The feed system owns preview state only. A domain write requires an explicit
workspace action or explicit user instruction. A safe workspace flow is:

1. read the live feed draft;
2. read current authoritative domain state;
3. reconcile version and desired state;
4. execute one domain write;
5. let the new tool result replace/clean the feed.

Publish, campaign creation, version-list navigation, and domain recomputation
are not generic feed operations.

## Compatibility, persistence, and security

- No feed-specific database table is required.
- Existing YAML without new fields remains valid.
- Unknown targets become `auto`.
- New callback snapshot fields are additive.
- Preview patches never include OAuth credentials by design.
- Attached-client and conversation scoping prevent arbitrary cross-session UI
  mutation.
- Required provider tool bundles should fail closed when discovery returns no
  definitions.
- Workspace-specific names, fields, and rules must not be added to Agently
  Core, Agently UI, or Forge implementations/tests.

## Verification checklist

For a new feed:

1. Verify each triggering tool-name form and wildcard behavior.
2. Verify feed target across SSE, HTTP, canonical replay, and client tracker.
3. Verify every datasource path against a real payload.
4. Verify first, middle, last, add, and remove collection patches.
5. Verify parent patches refresh derived child datasources.
6. Verify dirty state survives same-turn replay and delayed fetches.
7. Verify select/unselect/all/reset with full snapshot payloads.
8. Verify local callbacks create no chat turn.
9. Verify explicit submit performs the expected read-before-write policy.
10. Verify inline, workspace, detached, desktop, and compact layouts.
11. Verify backend PDF artifact creation/download if print is exposed.
12. Verify no workspace vocabulary leaked into generic repositories.
13. Verify switching to another conversation removes the previous
    conversation-owned workspace/feed surfaces and updates route, selection,
    and main-chat parameters together.
14. Verify native tab and reversible form edits produce acknowledged
    `feed.tab_changed` / `feed.form_changed` events without creating a turn.

## Related documentation

- [`doc/lookups.md`](lookups.md)
- [`doc/streaming-events.md`](streaming-events.md)
- [`doc/tool-system.md`](tool-system.md)
- [`toolfeed-ext.md`](../toolfeed-ext.md) — transient implementation/history
