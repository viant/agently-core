# Tool Feed extension: implementation record

Status: implemented and live-verified on 2026-08-30

This is the transient engineering record for the Tool Feed extension. It
captures the implementation across repositories, the problems encountered
while building the first rich editable feed, and the verification evidence.
The durable, product-neutral contract is in
[`doc/feed-system.md`](doc/feed-system.md).

## Outcome

One tool result can now drive a conversation-scoped Forge application in one
of three declared placements:

- `inline` in the owning assistant turn;
- `workspace` in the Tool Feed workspace;
- `detached` in its own launcher and drawer.

The placements reuse one canonical feed payload. A workspace can project it
into forms, collections, charts, editable tables, lookups, selections, tabs,
status banners, and backend-produced PDF output. User and agent edits share
the same preview state. Preview changes remain local until a workspace-defined
submit action invokes a domain tool.

The verified Media Planner flow is:

```text
Media Planner read
  -> tool_feed_active(media-plan)
  -> inline tabbed Forge application
  -> visual edit or ordinary chat instruction
  -> ui/feed:update preview patch
  -> Update draft enabled / Publish disabled
  -> optional explicit Update draft
  -> ui/feed:get + authoritative read + one domain edit
```

No database migration was required.

## Android mobile pairing started

The first native mobile pairing pass was implemented on 2026-08-30 in the
requested dependency order: `viant/forge`, `viant/agently-core`, then
`viant/agently`. This pass closes the protocol/host seam needed for an Android
client to receive placement metadata, render the correct surface, and
participate in the live `ui/feed` draft bridge.

### `viant/forge`: native draft-state foundation

`android/sdk/src/main/java/com/viant/forgeandroid/runtime/FeedDraftRuntime.kt`
adds domain-neutral Android runtime types and functions:

- `FeedDataSourceSnapshot(form, collection, selection)` captures all three
  datasource views used by `ui/feed:get`;
- `FeedPatchOperation(dataSourceRef, op, path, value)` mirrors the generic
  feed patch contract without importing Agently/Core types;
- `snapshotFeedDataSources(...)` fails closed on an unknown datasource ref;
- `applyFeedPatchOperations(...)` supports `add`, `replace`, and `remove`
  against `/form`, `/collection`, and `/selection` JSON Pointer roots;
- pointer unescaping handles `~0` and `~1`, collection append accepts `/-`,
  and missing/out-of-bounds paths fail instead of silently corrupting state;
- operations update Forge signals once and return the changed datasource refs.

`ForgeRuntime.openWindowInline(...)` now accepts optional `conversationId` and
`presentation`. This makes an inline feed window discoverable by the native UI
bridge without changing existing callers.

Coverage is in
`android/sdk/src/test/java/com/viant/forgeandroid/runtime/FeedDraftRuntimeTest.kt`.
It verifies first/middle/last collection operations, form and multi-selection
patches, snapshot shape, relative-path rejection, and bounds checking.

### `viant/agently-core`: Android protocol and tracker parity

The Android SDK already exposed `getFeedDraft` and `updateFeedDraft`, but the
live stream path discarded two fields required by placement:

- `SSEEvent.feedTarget` was absent, even though Core emits `feedTarget` on
  `tool_feed_active`;
- canonical hydration rebuilt an SSE-like event without the feed's `turnId`.

The Android stream model now includes `feedTarget`. `FeedTracker` carries it
into `FeedPresentation.target`, and canonical hydration carries both target
and owning turn. The regression in
`sdk/android/src/test/java/com/viant/agentlysdk/stream/ConversationStreamTrackerTest.kt`
checks live `inline` and hydrated `detached` feeds plus their turn ownership.

Relevant wire fields are:

| Layer | Field | Purpose |
|---|---|---|
| SSE | `feedTarget` | Select native placement without fetching feed data first |
| Canonical feed | `turnId` | Bind an inline feed to its assistant turn after replay |
| Presentation | `target` | `auto`, `inline`, `workspace`, or `detached` |
| Presentation | `suppressReportIds` | Remove legacy report duplicates when canonical metadata is available |

### `viant/agently`: Android placement and bridge host

Android now normalizes feed placement in `FeedInteractionRuntime.kt`:

- missing, `auto`, and unknown targets preserve the legacy workspace surface;
- `inline` feeds are selected by exact owning `turnId`;
- `workspace` feeds remain in the Tool Feed card/drawer;
- `detached` feeds use a separate `Feed apps` launcher/drawer;
- developer-only feeds remain excluded from normal user surfaces;
- `suppressReportIds` is collected from visible canonical feed metadata and
  filters duplicate transcript reports.

`TranscriptScreen.kt` renders inline feeds after the owning assistant bubble,
at conversation width rather than inside the message surface.
`FeedComponents.kt` supplies the inline surface and parameterized workspace /
detached launchers. Phone and larger workspace layouts pass the same merged
canonical/live feed collection into all three placements.

`UIBridgeClient.kt` now handles `ui.feed.get` and `ui.feed.update`:

- it resolves a rendered feed window by exact feed ID and conversation ID;
- get returns `conversationId`, `feedId`, and requested datasource snapshots;
- update decodes the operation list and delegates validation/mutation to the
  Forge Android feed runtime;
- an absent/unrendered feed and unknown datasource fail closed;
- the existing bridge poll acknowledges success/failure and republishes a UI
  snapshot after the command.

`FeedWindowState.kt` records conversation and presentation on each Forge feed
window, removing the ambiguity that previously made a native feed impossible
to address safely in a multi-conversation client.

### Android issues found and addressed

| Symptom/risk | Cause | Resolution |
|---|---|---|
| Every feed appeared in one Android bottom sheet. | Native host ignored `presentation.target`. | Added normalized placement routing and separate inline/workspace/detached surfaces. |
| An inline feed could not be attached after replay. | Canonical hydration dropped `turnId`. | Preserve the canonical owning turn in the Android tracker. |
| Live placement disagreed with web. | Android `SSEEvent` omitted `feedTarget`. | Carry `feedTarget` into `FeedPresentation.target`. |
| Agent draft calls reported no usable native target. | Android UI bridge did not implement `ui.feed.get/update`. | Added conversation-scoped bridge commands over rendered Forge feed windows. |
| Patches could be applied inconsistently by each host. | Forge native had no reusable view-patch primitive. | Added one generic snapshot/patch runtime with JSON Pointer validation. |
| Feed windows were indistinguishable in bridge snapshots. | Inline Forge windows lacked conversation/presentation state. | Extended `openWindowInline` and set both fields from the feed payload. |
| Feed plus legacy report duplicated output. | Android transcript did not consume `suppressReportIds`. | Filter canonical reports using visible feed presentation metadata. |
| The first Core tracker regression reported canonical `detached` as live `inline`. | The test sent a live event before first hydration; the tracker correctly replays pre-hydration events after canonical state, so live state won. | Split live-event and canonical-hydration assertions into independent trackers; production precedence was left unchanged. |
| The first Core Android test command rejected `--tests`. | The root `test` task is only an aggregate and does not accept test filtering. | Run the concrete `testDebugUnitTest` task. |

### Android pairing boundary after this pass

This is the start of native pairing, not a claim that every web visual is
already implemented on Android. Transport, placement, bridge, canonical-root
mapping, dependent recomputation, selection reconciliation, and same-turn
dirty replay protection are implemented. Remaining parity work is:

1. add/verify native renderers for the complete web feed vocabulary, notably
   editable tables, lookup chips, dirty-ref toolbar enablement, and all
   print-time tab behavior;
2. persist dirty-preview ownership across Android process recreation;
3. visually verify inline, workspace, and detached placement on phone and
   tablet;
4. extend the stable Android contract to iOS (completed in the next section).

## iOS mobile pairing

The iOS pass follows the same dependency order and wire contract as Android.
It was implemented after the Android target/bridge seam was verified, so
`workspace`, `inline`, and `detached` have one meaning across both native
clients.

### `viant/forge`: Swift datasource draft runtime

`ios/Sources/ForgeIOSRuntime/FeedDraftRuntime.swift` adds the Swift equivalents
of the Android runtime types:

- `FeedDataSourceSnapshot` exposes `form`, `collection`, and `selection`;
- `FeedPatchOperation` carries datasource ref, operation, JSON Pointer, and
  optional value without depending on Agently SDK models;
- `snapshotFeedDataSources(...)` validates the window and datasource graph;
- `applyFeedPatchOperations(...)` supports add/replace/remove, pointer
  unescaping, array append, first/middle/last updates, and strict missing-path /
  array-bound failures;
- form, collection, and selection updates are written to both the actor-owned
  datasource runtime and their observable Forge signals exactly once.

`ForgeRuntime.openWindowInline(...)` now accepts optional `conversationID` and
`presentation`, matching the Android window identity contract while preserving
source compatibility through defaults.

Coverage is in
`ios/Tests/ForgeIOSTests/FeedDraftRuntimeTests.swift`.

### `viant/agently-core`: Swift stream target parity

The public Swift models and `getFeedDraft` / `updateFeedDraft` facade already
contained the new feed fields. The missing seam was live SSE decoding:

- `Streaming.StreamPayload` now decodes `feedTarget`;
- `applyFeedEvent` constructs `FeedPresentation` when target is the only visual
  field and preserves target alongside icon/accent;
- live `turnID` remains on `ActiveFeedState`;
- canonical hydration continues to retain the full feed state directly rather
  than rebuilding a lossy event.

The regression extends
`sdk/ios/Tests/AgentlySDKTests/AgentlySDKTests.swift` with an inline target and
owning-turn assertion.

### `viant/agently`: SwiftUI placement, Forge rendering, and bridge

`ToolFeedsSection.swift` now owns platform-neutral iOS placement helpers:

- absent, `auto`, `workspace`, and unknown targets use the legacy Tool Feed
  workspace launcher;
- `inline` binds by exact `turnID`;
- `detached` uses an independent `Feed apps` launcher and sheet;
- developer-only feeds remain hidden;
- visible canonical feeds contribute `suppressReportIds`.

The same file now decodes both content-shaped and single-container feed UI,
decodes datasource declarations into Forge models, materializes a
conversation-scoped Forge inline window, hydrates every datasource, and renders
generic containers through `ContainerRenderer`. Existing specialized terminal
and file-preview paths remain available.

`TranscriptScreen.swift` renders an inline feed after the last assistant entry
for its owning turn, never once per fragment, and filters duplicate canonical
reports. `WorkspaceScreen.swift` and `PhoneWorkspaceScreen.swift` pass the same
merged live/canonical feed collection and Forge runtime to transcript,
workspace, and detached surfaces.

`AppleUIBridgeController.swift` implements `ui.feed.get` and `ui.feed.update`
with exact raw feed ID plus conversation matching. It translates Agently JSON
values into Forge values, delegates snapshot/patch validation to Forge, and
returns the same response shape as Android/web.

`ios/Package.swift` accepts `AGENTLY_IOS_SDK_PACKAGE_PATH` for coordinated
multi-repository verification. The override path must retain the
`AgentlySDKPackage` package identity; production continues to use the pinned
package/submodule until that pointer is advanced.

### iOS issues found and addressed

| Symptom/risk | Cause | Resolution |
|---|---|---|
| Live iOS feeds ignored `inline`/`detached`. | Swift `StreamPayload` did not decode `feedTarget`. | Decode and carry target into `FeedPresentation`. |
| All user feeds appeared in one sheet. | SwiftUI host filtered only `developerOnly`. | Add target normalization and three placement surfaces. |
| Inline replay could render on every assistant fragment. | Placement considered only turn equality. | Attach once after the last assistant entry for the owning turn. |
| Generic editable feed UI fell back to text. | iOS feed host special-cased terminal/file/summary and did not create a Forge window. | Decode generic content/dataSources, hydrate a scoped window, and render `ContainerRenderer`. |
| Agent feed commands could not reach iOS state. | Apple UI bridge lacked `ui.feed.get/update`. | Add exact conversation/feed lookup and Forge-backed snapshot/patch handling. |
| Feed windows were ambiguous in snapshots. | Swift inline windows did not retain conversation/presentation. | Extend `openWindowInline` and populate both values. |
| First Forge patch test lost its form title before the patch. | The normal selection setter intentionally seeds form from selection. | Seed selection first, then the independent form fixture; runtime behavior was not weakened. |
| Direct sibling Swift package verification formed an identity cycle. | The sibling folder basename is `ios`, which collides in SwiftPM resolution; Agently normally consumes an `AgentlySDKPackage`-named symlink. | Add a package-path override and verify through a temporary `AgentlySDKPackage`-named alias. |
| Swift test could not `await` inside `XCTAssertEqual` autoclosure. | XCTest autoclosures are synchronous. | Await the collection first, then assert the captured value. |
| Every iOS section rendered with empty cards and blank form fields. | Datasource decoding was all-or-nothing; projection-only declarations contain keys that are not transport fields, so one decode failure discarded the entire map. | Decode declarations independently, create non-fetching local contexts for projection-only sources, and synthesize remote lookup contexts from UI references. |
| A feed-data request was canceled during conversation bootstrap. | SwiftUI recreated the inline surface while the active-feed snapshot settled, canceling the view-owned URL task. | Fetch feed data in a detached child task and hydrate with live canonical data as a fallback. |
| Publisher and other large collections emptied after initial hydration. | Local projection contexts inherited fetch behavior and replaced manually projected rows. | Force `autoFetch: false` for datasource declarations without a transport service. |
| General repeated section titles and exposed oversized inputs. | Both `ContainerRenderer` and `SchemaBasedFormRenderer` rendered the same title, and the form used standard-density one-column controls. | Keep title ownership in the container, use compact adaptive fields, native boolean controls, and a two-ended native date range picker. |
| Frozen tables looked tall, grey, and fragmented. | One narrative column imposed a fixed tall height on every row; pickers lacked cell chrome and the frozen seam used a heavy shadow. | Compute height per row from actual narrative content, cap the frozen identifier at 40%, add consistent rounded editors/dividers, soften the seam, and apply deterministic generic pastel headers/row surfaces. |
| Lookup add was hidden after dozens of selected rows. | iOS rendered the lookup action after the table. | Move the icon-only add/close action and search UI above the table; live publisher and governed Deal lookups were verified. |
| Canonical lookup values displayed `null` or array syntax. | Targeting-tree labels can be path arrays rather than scalar labels. | Normalize label/displayPath/path/value fallbacks and join path segments for display/mapping. |

### iOS verification

- Forge iOS: `swift test` passed 272/272, including feed draft and generic
  renderer/runtime coverage.
- Agently Core iOS: `swift test` passed 79/79; the stream target and feed draft
  facade tests passed 2/2 when run alone.
- Agently iOS: `swift test` passed 159/159; the focused Tool Feed suite passes
  13/13, including remote lookup dependency synthesis.
- Simulator visual QA covered all seven sections, hydrated forms/summaries,
  compact report-style navigation/pills, schedule and collection tables,
  frozen identifier columns, icon add/close controls, publisher lookup, and a
  live governed Deal search returning `Coke_SamsClub_xAd_CPM$5.10`.

### Mobile visual/interaction issues found during Media Plan QA

| Symptom | Root cause | Generic resolution |
|---|---|---|
| Android content overlapped or appeared as a narrow vertical strip near 340 dp. | Responsive grid/split fallbacks kept sibling children in a `Box`, so they occupied the same coordinates. | Narrow layouts now use a `Column`; spacing and surface padding are compact-density values. |
| Toolbar text such as Print/PDF rendered one character per line. | Desktop action widths were retained beside two full-width mobile actions. | Mobile actions wrap and use icon-first compact controls with accessible labels. |
| Entity annotations crashed Android with an ICU regex error. | Escaped-brace patterns were interpreted differently by Android's regex engine. | Replace fragile entity parsing with Android-safe matching and test malformed/escaped annotations. |
| Raw `@{type:id "label"}` text leaked into assistant bubbles. | Native markdown did not consume the shared entity annotation syntax. | Render entity annotations as their business label while retaining the opaque identity. |
| Adding the first row to an empty flattened collection failed. | Reverse mapping assumed an existing projected row/parent identity. | Resolve the declared flatten source/parent and permit validated `/-` append without an existing child. |
| Publisher, audience, advertiser, and Deal lookups were absent on native clients. | Only datasources explicitly declared in the transport map got runtime contexts; lookup/drill refs live in UI metadata. | Recursively discover lookup/drill refs and synthesize standard `agentlyAPI` datasource contexts. |
| The first Deal lookup used `deal_lookup` and returned no authoritative result. | That datasource/model was not exposed by the active MCP surface. | Use the platform targeting-tree datasource with `Field: PMP_DEAL`, nested query inputs, channel bindings, and advertiser binding. |
| Deal/audience candidates showed `null` labels. | Targeting-tree rows may expose `path` arrays with no scalar `label`. | Resolve label, displayPath, path, then value; normalize arrays into readable path text. |
| All rows became cards and long rationale text clipped. | Native hosts lacked an editable table primitive and treated collections as generic objects. | Add generic editable tables, multiline narrative cells, semantic widths, horizontal scrolling, and a frozen identifying column capped at 40%. |
| Date range showed `{start=..., end=...}` and did not open a picker. | The schema widget was classified as generic object/text. | Add a native `dateRange` field type and start/end picker while keeping the stored object shape. |
| Tapping the collapsed Android composer edited the last focused feed field. | Feed focus was not cleared before expanding/requesting composer focus. | Clear feed focus, request composer focus explicitly, show the IME, and collapse/clear focus after send. |
| Fast ADB text entry dropped/reordered characters. | Samsung IME key injection is not atomic. | Use slow character-level entry and verify the focused field/text before sending; this was a test-harness artifact, not accepted product behavior. |
| Plain-English mutations were recorded but produced no assistant update. | The configured `media-planner-tools` bundle was unavailable for those turns. | Preserve the messages as failed turns, report the backend dependency failure, and do not claim the feed mutation succeeded. No Update/Publish action was invoked. |
| A shared YAML change risked desktop regression. | Tool Feed UI metadata is shared by web, Android, and iOS. | Keep domain declarations single-source; implement compact/frozen/pastel behavior in generic platform renderers and rerun all seven web tabs after shared changes. |

### Shared native boundary

Android and iOS now agree on transport fields, target placement, exact inline
turn ownership, conversation/feed bridge identity, snapshot shape, direct view
patch validation, canonical-root mapping, dependent recomputation, unique-key
selection reconciliation, same-turn dirty replay protection, projection
vocabulary, report suppression, compact tabs/forms, editable collections,
native date/boolean controls, lookup dependency loading, frozen columns, and
generic pastel table presentation. Android was verified on a connected
physical device and iOS in Simulator. Persistent dirty state across process
recreation remains follow-up work; the Android-only “add iOS pairing” item is
satisfied by this section.

## Native canonical synchronization and projection closure

The second mobile pairing pass closed the gap between a direct Forge signal
patch and the web canonical-cache algorithm.

### Canonical patch mapping

Android `FeedCanonicalRuntime.kt` and iOS `FeedCanonicalRuntime.swift` now:

1. keep one canonical payload per Forge runtime plus window ID;
2. resolve datasource ancestry through `source`, `dataSourceRef`, and
   `selectors.data`;
3. treat a default `output` selector as identity when the parent value has no
   `output` member;
4. translate projected field names back through `fields.path`/string mappings;
5. translate date-range `start`/`end` edits through `startPath`/`endPath`;
6. resolve filtered/deduplicated collection rows back by `uniqueKey`;
7. reverse flattened rows through parent index, flatten source path, child
   index, source fields, parent fields, and constant values;
8. reject writes to aggregate, derived, flattened-parent, and flattened-
   constant fields because they do not own canonical storage;
9. apply each canonical array mutation once, then recompute every datasource;
10. reconcile selections to the new rows by `uniqueKey`.

Before an agent patch, the target datasource's current form/collection is
merged into canonical state so visual edits on that surface are not lost.
Projected forms are synchronized field-by-field rather than replacing the
domain object with a display projection.

### Replay and isolation

The native cache is keyed by Forge runtime identity plus window ID, not window
ID alone. A dirty same-turn or missing-turn payload refresh keeps the preview;
a payload owned by a different non-empty turn replaces it as authoritative.
Closing a feed window clears its cache entry.

This protects delayed feed-data responses and same-turn canonical replay in
memory. Persistence across app process recreation remains follow-up work.

### Native projection vocabulary

Android and iOS now compute the same feed projection vocabulary used by the web
adapter:

- dot, bracket/numeric, direct `output`/`input`, and `$` selectors;
- `fields` projection;
- `dateRange`, `dateRangeLabel`, `dateParts`, and `boolean` transforms;
- `flatten.sources`, child exclusions, parent fields, constant values, and
  primitive children;
- datasource `exclude.equals` and `exclude.equalsIgnoreCase`;
- `aggregate.countAs`;
- `uniqueKey` deduplication;
- `${path}` derived string fields.

Regression fixtures cover first/middle/last and append/remove operations,
parent-to-child recomputation, projected form/date-range writes, flattened
collections, exclusion, aggregation, derivation, numeric selectors, selection
identity, same-turn replay retention, newer-turn replacement, and runtime-cache
isolation.

### Additional issues found and addressed

| Symptom/risk | Cause | Resolution |
|---|---|---|
| A new runtime inherited another runtime's dirty feed in tests. | Cache key used only `windowId`; deterministic IDs can repeat. | Key by runtime object identity plus window ID on Android and iOS. |
| Projected form update could erase unprojected domain fields. | Current visual form was written over the entire canonical object. | Synchronize declared projected fields individually, including date-range endpoints. |
| Child datasource with no selector mapped to a nonexistent `.output`. | Default `output` means identity when the parent object has no `output` member. | Inspect the canonical parent value before extending the path. |
| Append at `/collection/-` failed during reverse row lookup. | `-` has no existing projected row to match. | Allow direct append for unprojected collections and validate at canonical application. |
| Filtered or flattened row indexes targeted the wrong canonical element. | View index and canonical index differ after projection. | Reverse-map by unique keys and flatten source/parent/child coordinates. |
| Primitive `$` flatten sources produced no native row. | Field projection required a map/object child. | Permit field projection from primitives and treat `$` as the child root. |
| Android compilation could not smart-cast a captured JSON value. | A mutable value was inspected inside a lambda. | Use an explicit loop and stable local value. |
| Full Android feed tests leaked state across methods. | The isolation bug above survived focused single-test execution. | Runtime-scoped cache keys fixed production and test isolation. |

## Final behavioral contract

### Canonical data and projection

- The tool response is canonical. UI sections do not repeat the domain call.
- Workspace YAML declares named datasources with `source`, `dataSourceRef`,
  `selectors.data`, `fields`, `flatten`, `exclude`, `aggregate`,
  `selectionMode`, `uniqueKey`, and `paging`.
- Dot paths and numeric indices project JSON. XML XPath and a new query
  language were intentionally not introduced.
- Tool-name matching preserves method underscores. Only the service portion
  is normalized from canonical underscore/colon syntax.
- A wildcard method can bind one feed to every method of a service.

### Presentation

`presentation.target` accepts `auto`, `inline`, `workspace`, or `detached`.
Unknown values normalize to `auto`.

- `auto` preserves legacy client-selected placement.
- `inline` is owned by one turn, uses the full conversation width, and renders
  once after the final assistant/iteration row representing that turn.
- `workspace` participates in the Tool Feed tab strip and compact drawer.
- `detached` is excluded from the workspace strip and gets an independent
  launcher/drawer.
- `presentation.icon`, `presentation.accent`, and
  `presentation.suppressReportIds` travel through HTTP, SSE, canonical state,
  replay, and SDK models.
- Feed visibility is independent of developer-mode execution details.

### Live draft API

The internal `ui/feed` service has two generic methods:

```json
{
  "method": "ui/feed:get",
  "input": {
    "clientId": "optional-client",
    "feedId": "feed-name",
    "dataSourceRefs": ["form", "items"]
  }
}
```

The response contains the selected attached client and current form,
collection, and selection snapshots for every requested datasource.

```json
{
  "method": "ui/feed:update",
  "input": {
    "clientId": "optional-client",
    "feedId": "feed-name",
    "operations": [
      {
        "dataSourceRef": "items",
        "op": "replace",
        "path": "/collection/2/value",
        "value": 30
      }
    ]
  }
}
```

Rules:

- `feedId` identifies the feed declaration, not a domain entity.
- `dataSourceRef` identifies a declared logical datasource.
- `op` is `add`, `replace`, or `remove`.
- `path` is an absolute JSON Pointer.
- `/collection/...`, `/form/...`, and `/selection/selection/...` address the
  collection, form, and multi-selection views.
- View paths resolve back to the datasource's canonical root before patching.
- Update accepts 1–64 operations; read accepts 1–32 datasource refs.
- Update is preview-only and moves the feed to the current turn.
- A patch never invokes a domain write.

`FeedPatchOperation` and get/update models are exposed by TypeScript, Android,
and iOS SDKs so mobile and web share the same contract.

### Draft synchronization

The final synchronization order is important:

1. Patch the canonical cached payload once.
2. Resolve the target datasource to its canonical root.
3. Deduplicate equivalent operations emitted for multiple views.
4. Recompute all dependent datasources.
5. Rewire Forge signals from the recomputed map.
6. Restore dirty markers after rewiring.
7. Reject stale/same-turn payloads that would overwrite a dirty preview.
8. Preserve dirty data when a delayed metadata/data request completes.

This supports first, middle, and last collection changes without index shifts,
double removal, or stale-response rollback.

### Selection and callbacks

- Multi-selection uses Forge's `{selection: []}` shape.
- `uniqueKey` controls identity across refreshes.
- Select-all/reset operate on the full collection, not one page.
- A toggle computes `selectedRows`, `unselectedRows`, `changedRows`, and
  `finalDataSourceSnapshot` atomically.
- `callback.type: local` updates feed-local feedback without a chat turn.
- `llm_event` submits a structured conversational action.
- Workspace callbacks receive the same complete snapshot.
- UI callbacks and agent calls use the same patch-operation semantics.

### Preview, draft update, and publish

Three transitions are separate:

1. **Preview update** — local UI/feed state only.
2. **Update draft** — workspace-defined submit; normally reads the live feed,
   performs a fresh authoritative domain read, and issues one edit.
3. **Publish** — a separate domain transition allowed only from a clean draft.

The generic runtime does not define domain recomputation, versioning,
publishing, or campaign creation. Those belong to workspace/tool contracts.

## Project and file map

### `viant/agently-core`: protocol, resolver, SDK, canonical state

Core feed model and matching:

- `sdk/api/feed.go` — `FeedSpec`, `FeedPresentation`, `FeedMatch`,
  `FeedActivation`, and `FeedState`.
- `sdk/feed.go` — workspace registry, hot reload, target normalization,
  service/method parsing, wildcard matching, and active SSE emission.
- `sdk/feed_resolver.go` and `sdk/embedded_feeds.go` — transcript/tool-result
  resolution into one active payload.
- `internal/feedextract/` — projection, derive, merge, and unique-key logic.
- `sdk/handler_feeds.go` — `/v1/feeds` and
  `/v1/feeds/{id}/data?conversationId=...`.

Canonical/live contract:

- `runtime/streaming/event.go` — `feedTarget` on `tool_feed_active`.
- `sdk/api/canonical.go`, `sdk/canonical_reducer.go`, and
  `sdk/canonical_helpers.go` — feeds in canonical conversation state.
- `sdk/feed_notifier.go` — activation/inactivation publication.

Interactive service:

- `protocol/tool/service/ui/feed/service.go` — `ui/feed:get` and
  `ui/feed:update`, conversation/client resolution, validation, and bridge
  dispatch to `ui.feed.get`/`ui.feed.update`.

Cross-platform SDK:

- `sdk/ts/src/types.ts`, `sdk/ts/src/feedPatch.ts`,
  `sdk/ts/src/feedTracker.ts`, and `sdk/ts/src/client.ts`.
- `sdk/android/src/main/java/com/viant/agentlysdk/Models.kt` and `Client.kt`.
- `sdk/ios/Sources/AgentlySDK/Models.swift` and `AgentlyClient.swift`.

Supporting tool/MCP changes:

- `service/agent/tools.go` and `service/agent/binding_tools.go` — fail-closed
  required-bundle resolution without unrelated tools masking an unavailable
  provider.
- `service/agent/intake_query.go` — submit tool-bundle/profile guidance.
- `go.mod` — `github.com/viant/mcp v0.21.0`; no local MCP replace.

### `viant/agently`: server wiring and web client

Server/CLI:

- `serve.go` — registers the generic internal `ui/feed` service.
- `cmd/agently/mcp_list.go`, `mcp_run.go`, and `auth_tools.go` — discovery and
  execution with separate workspace OOB and provider `--mcp-oob` auth.

Placement and rendering:

- `ui/src/components/ToolFeedDetail.jsx` — Forge/fallback rendering, inline
  ownership, expansion, and report-export adapter.
- `ui/src/components/ToolFeedWorkspace.jsx` — workspace tabs, collapse/expand,
  and compact drawer.
- `ui/src/components/ToolFeedDetached.jsx` — detached launcher/drawer.
- `ui/src/components/chat/IterationBlock.jsx` — turn-owned inline placement and
  suppression of declared legacy report blocks.
- `ui/src/components/Root.jsx` — feed surfaces independent of developer mode.
- `ui/src/services/toolFeedTarget.js` — target normalization/filtering.

State and interaction:

- `ui/src/services/toolFeedBus.js` — conversation-scoped cache/tracker,
  canonical patch mapping, dependent datasource rewiring, preview protection,
  and stale-fetch protection.
- `ui/src/services/feedForgeWiring.js` — projection into Forge signals,
  selection reconciliation, and field/flatten/exclude/aggregate transforms.
- `ui/src/services/feedForgeContext.js` — form/collection/selection handlers,
  pagination, lookup transport, callbacks, full submit snapshots, and print.
- `ui/src/services/feedDraftState.js` — draft restoration after failed submit.
- `ui/src/services/forgeUIActions.js` — structured callback dispatch.
- `ui/src/services/feedReportExport.js` and `reportExportService.js` — submit a
  reference-first Forge view plus current datasource overrides to the shared
  backend compiler/export path.
- `ui/src/styles/shell.css` — full-width inline layout, responsive workspace,
  drawers, and overlap fixes.

### `viant/forge`: reusable visual primitives

- `src/components/FormPanel.jsx` — shared section tabs and print-time
  `expandAll` materialization.
- `src/components/dashboard/DashboardBlocks.jsx` — generic
  `dashboard.editableTable`, `dashboard.lookupChips`, composition/detail/status
  blocks, frequency editor, add/remove, filtering, pagination, and reactivity.
- `src/components/dashboard/DashboardTableContent.jsx` and
  `tableCellVisuals.js` — report-strength tables and quantitative cell visuals.
- `src/components/table/basic/Toolbar.jsx` — actions controlled by declared
  dirty datasource refs.
- `src/components/Container.jsx`, `Container.css`, `Dashboard.css`, and
  `FormPanel.jsx` — responsive sizing, tab styling, and overlap corrections.
- `src/core/ui/commands.js` — generic `ui.feed.get`/`ui.feed.update` handling.
- `src/components/dashboard/dashboardEditableTableSignals.test.js` — guards
  external signal reactivity with neutral fixtures.

Forge contains no workspace, advertiser, channel, or MCP-specific policy.

### `viant-internal/steward_ai`: workspace adoption

Domain-specific behavior stays here:

- `deployment/v2/steward/feeds/media-plan.yaml` — literal feed ID,
  presentation, tabs, projections, editable collections, lookups, global
  actions, and print action.
- `deployment/v2/steward/prompts/media_plan_management.yaml` — preview versus
  update versus publish, datasource paths, read-before-write, and business copy.
- `deployment/v2/steward/agents/steward/prompt/parts/routing.md` — direct
  Steward routing; no child feed forwarding.
- `deployment/v2/steward/intake/activation_rules.yaml` — create/read/edit and
  draft-update activation; editable routes append `workspace-ui`.
- `deployment/v2/steward/tools/bundles/` — Media Planner, publish, and generic
  workspace UI tool surfaces.
- `deployment/v2/steward/oauth/providers/media-planner-oauth.yaml` and
  `mcp/mediaplanner.yaml` — provider/server metadata.
- `deployment/v2/steward/agents/media-planner/mediaPlanner.contract.test.mjs`
  — workspace contract coverage.

## Media Planner workspace feature set

The inline application provides:

- one wide feed above the latest assistant acknowledgement;
- a business-name chip retaining the opaque plan ID as identity;
- version/status display;
- General, Channels, Schedule, Supply, Targeting, Deals, and Audiences tabs;
- goal, budget, flight, objective, advertiser/product, and targeting forms;
- channel allocation visuals and editable table;
- frequency count, interval, and hour/day/week controls;
- editable dayparts;
- publisher/site, deal, audience, blocklist, geography, and segment collections;
- search/add/remove controls, duplicate suppression, paging, and filtering;
- lookup-backed table/chip selection and sitelist drill-down declarations;
- one global Update draft and one global Publish plan action;
- backend PDF export through reporting primitives;
- ordinary chat instructions staging the same preview as visual edits.

Current external discovery exposes create, get, and edit. Publication UI is
declared, but live publication depends on external `publish_media_plan`.
List/search and authoritative version-list operations are also absent, so the
UI uses a latest-version chip rather than a fake selector.

## Issues encountered and resolutions

| Symptom | Root cause | Resolution/evidence |
|---|---|---|
| Feed did not activate for underscored methods. | Parser normalized method underscores. | Normalize only service; generic naming/wildcard tests pass. |
| Old treemap chunk failed dynamic import. | Browser referenced a superseded Vite asset. | Rebuild and reload current hashed assets. |
| Inline feed was narrow/clipped. | Inline inherited rail constraints. | Explicit inline feeds force full width/height. |
| Description text did not reclaim width. | Bubble reserved execution-detail space. | Feed layout separated from developer chrome. |
| Inputs/checkboxes overlapped. | Shared table/form sizing did not reserve control width. | Generic Forge sizing and checkbox layout corrected. |
| Forge crashed setting indexed CSS properties. | Non-object style reached React style assignment. | Style normalization keeps CSS object-shaped. |
| Tabs looked inconsistent or did not switch. | Two tab implementations diverged. | Unified section-tab primitive with active-only render and print expansion. |
| An inline feed could render before the final assistant row, or more than once when a turn projected multiple iteration rows. | Every web `IterationBlock` mounted its own exact-turn feed renderer. | Move inline anchoring to the projected chat-row host and render once after the final assistant/iteration row for the feed's owning `turnId`; Android/iOS already select the last assistant row for that turn. |
| Web appeared unable to switch from an open Media Plan conversation to troubleshooting. | Conversation-owned workspace state can outlive the visible conversation if selection cleanup regresses. | Live-tested Media Plan → troubleshooting URL/content replacement and added a generic regression that removes the previous workspace, focuses main chat, updates route, and replaces conversation parameters. |
| Duplicate report documents appeared below the feed. | Legacy report and feed rendered the same result. | `suppressReportIds` suppresses only workspace-declared legacy reports. |
| Feed appeared tied to developer mode. | Placement lived near execution chrome. | Feed surfaces mounted independently; `developerOnly` is explicit. |
| Large lists duplicated chips and looked weak. | Chip-only/basic-table presentation did not scale. | Table presentation, counts, paging, filtering, lookup and duplicate omission. |
| Callback JSON leaked into chat. | Local events fell through to LLM dispatch. | `callback.type: local` is consumed without a chat turn. |
| Unselected rows were wrong. | Selection reconstructed after mutation using object identity/page state. | Atomic full-collection payload plus `uniqueKey`. |
| Removal shifted indices or ran twice. | Cache and signal both applied the operation. | Patch canonical cache once, then recompute/wire. |
| View-relative patches missed nested data. | `/collection`/`form`/`selection` applied without datasource ancestry. | Map view paths to canonical roots and dedupe. |
| Parent patches did not update child collections. | Only direct signals were touched. | Recompute and wire every dependent datasource. |
| Preview appeared then reverted. | Delayed backend response overwrote dirty cache. | Async merges prefer latest dirty data; race test added. |
| Same-turn active event replaced preview. | `createdAt` was treated as authority. | Same-turn/missing-turn replay cannot replace dirty preview. |
| Editable values stayed visually unchanged. | `DashboardEditableTable` did not subscribe to signals. | Added generic `useSignals()` subscription and regression test. |
| Update draft became disabled after staging. | Dirty markers were lost during overwrite/rewire. | Preserve dirty refs and restore them after wiring; live verified. |
| Agent used domain plan ID as feed ID. | Domain and UI identities were conflated. | Workspace contract requires literal `feedId: "media-plan"`. |
| “the current media plan” missed routing. | Regex accepted `the` or `current`, not both. | Workspace rule and contract test updated. |
| Edit route lacked `ui/feed:update`. | Provider bundle alone was exposed. | Editable routes explicitly append `workspace-ui`. |
| Draft surface appeared unavailable. | Stale binary lacked `ui/feed`, then wrong feed ID was queried. | Rebuilt current binary, corrected ID, tested in a clean conversation. |
| MCP tools disappeared after restart. | Active user lacked delegated provider credential. | Re-linked with separate workspace/MCP OOB flows; no credentials copied/logged. |
| Rebuilt MCP client failed negotiation. | `viant/mcp v0.20.0` lacked final streamable headers/fallback. | Upgraded to published v0.21.0; no local replace. |
| Failed submission erased preview. | Authoritative rerender replaced local state. | Pending draft snapshot is restored on failure. |
| Opaque IDs dominated copy. | Storage identity was rendered as prose. | Entity chips retain ID and display business name/version. |
| Multiple publish buttons confused state. | Actions were section-local. | One global Update and one global Publish use dirty refs. |

## Verification

### 2026-08-30 mobile visual, PDF, and interaction completion

The final mobile pass reused Forge primitives and the shared Media Plan feed
declaration; no Android- or iOS-specific workspace YAML was introduced.

- Android and iOS use the report-style compact section navigator, adaptive
  pills/forms, compact tables, lookup tinting, and shared pastel action
  palette established by the reporting engine.
- `Export PDF` now declares `icon: pdf`. Android maps it to
  `PictureAsPdf`; iOS maps it to the native PDF/document symbol. Android, iOS,
  and web route `feed.print` through
  `reporting:compile_and_export_forge_ui`. They send `feed://media-plan`, target
  context, declared datasource refs, and only current unsaved snapshots as
  overrides. The backend owns Forge-UI lowering, report compilation, export,
  and artifact creation; clients retrieve and open the artifact.
- Forge now exposes a host-neutral `ForgeInteraction` observer. Native tab
  selection emits `feed.tab_changed`; user form edits emit
  `feed.form_changed`. Forge does not import Agently, SDK, conversation, or
  workspace policy.
- Agently mobile hosts accept only rendered `feed-*` windows, debounce text
  edits by window/kind/field, and record sanitized structured events through
  `ui/events:record`. Current state remains available through `ui/feed:get`;
  recent intent is inspectable through `ui/events:list`.
- Shared `editor.type: frequency` now renders count, interval, and unit controls
  on Android and iOS instead of falling back to one text field. Editable-table
  patches emit the same inspectable `feed.form_changed` event as ordinary form
  fields. iOS uses pastel row/header surfaces without internal grid dividers.
- Mobile composer lookup chips open their selection table directly. The commit
  control remains an arrow, is disabled while a required lookup is unresolved
  or the composer is empty, and enables after lookup selection or ordinary text.
- The Android and iOS Automation work uncovered reusable Forge table defects:
  authored empty-state pagination, icon token rendering, conditional toolbar
  visibility, and selection/visibility ordering. Those fixes remain generic.

Device evidence:

- physical Samsung Android: `Export PDF` glyph/label visually verified;
  General → Channels produced `feed.tab_changed recorded=true`; a reversible
  total-budget edit produced two `feed.form_changed recorded=true`
  acknowledgements and restored `250000`;
- authenticated Pixel 10 Pro emulator: centralized `feed://media-plan` export
  opened the multi-page `Media Plan.pdf` in the native PDF viewer without an
  app crash; lookup-chip, clear, empty-send, and plain-text-send states were
  visually verified;
- authenticated iPhone 17 Pro Simulator: PDF glyph/label and inline placement
  visually verified; General ↔ Channels produced two
  `feed.tab_changed recorded=true` acknowledgements;
- Automation E2E on both platforms created/shared `Hello World Mobile Test`,
  manually ran it, opened run history/conversation, and verified assistant
  output `Hello world.`

Regression coverage includes Android/iOS `ForgeInteraction` observer tests,
the Android Agently app unit suite, Forge iOS 276/276, Agently iOS 161/161,
and successful Android/iOS application builds after integration.

Automated:

- Forge Android:
  `./gradlew :sdk:testDebugUnitTest --tests com.viant.forgeandroid.runtime.FeedDraftRuntimeTest`
  passed, including JSON Pointer and first/middle/last patch coverage.
- Agently Core Android:
  `./gradlew testDebugUnitTest --tests com.viant.agentlysdk.stream.ConversationStreamTrackerTest --tests com.viant.agentlysdk.AgentlyClientTest`
  passed (76 tests in the filtered task run).
- Agently Android with live sibling Core/Forge sources:
  `./gradlew :app:testDebugUnitTest --tests com.viant.agently.android.FeedRuntimeTest -Pagently.android.useSiblingSources=true`
  passed.
- The full Agently Android unit suite with sibling sources passed 242/242
  after canonical/projection/isolation coverage was added.
- Agently Core Go:
  `go test ./sdk ./protocol/tool/service/ui/feed` passed.
- Steward contract: `GREEN Media Planner workspace contract`.
- Agently UI: 117/117 focused integration tests; final feed/wiring/context/draft
  suites 34/34 after race fixes.
- Web conversation-window regression suite passed 37/37, including switching
  from a workspace-backed conversation to a plain troubleshooting conversation.
- Forge iOS passed 280/280 and Agently iOS passed 161/161 after compact table,
  lookup dependency, form/date, and hydration fixes.
- Agently Core: `go test ./sdk ./service/agent` passed.
- Forge signal regression and production build passed.
- Agently UI production build passed.
- Boundary audit found no newly added workspace terms in Agently or Forge;
  domain-specific Core fixtures were replaced by catalog/record fixtures.

Live browser:

- workspace: `viant-internal/steward_ai/deployment/v2/steward`;
- application: `http://localhost:8787`;
- conversation: `cb19a864-c587-4a8e-bee1-7192c73c6077`.

Starting from version 4, ordinary chat staged:

- first row CTV: `7 per 1 day`;
- middle row Display: `5 per 1 day`;
- last row DOOH: `3 per 1 week`;
- unmentioned Video: unchanged at `3 per 1 day`.

The rendered inputs showed those values. Update draft was enabled, Publish was
disabled, and no Media Planner edit/publish occurred; persisted version stayed
4.

## Compatibility and security

- No database/table migration.
- Omitted presentation fields retain `auto` behavior.
- Callback snapshot fields are additive.
- Feed state is scoped by conversation plus raw feed ID.
- `ui/feed` requires an attached client in the same conversation.
- Preferred UI client is honored when supplied.
- Patch counts, operation names, refs, and absolute paths are validated.
- Preview patches carry no OAuth token or client credential.
- Required provider bundles fail closed.
- Domain policy stays in workspace declarations.

## Known gaps and follow-up work

1. **Implicit active-feed routing.** “Change the current media plan” is
   verified. Bare “Change CTV frequency…” still needs generic active-feed
   follow-up context rather than a broad workspace regex. Core follow-up state
   currently understands active windows, not active feeds.
2. **Probe-free updates.** Guidance requests direct `ui/feed:update`, but a
   model may still issue feed/context/event reads. Deterministic active-feed
   binding should remove that variance and latency.
3. **Plan list/search and version navigation.** External MCP does not expose
   them; do not guess a latest plan or fabricate a selector.
4. **Publish.** Workspace action exists; external `publish_media_plan` is still
   required for live completion.
5. **Process recreation.** Dirty preview protection is in-memory on native
   clients; Android/iOS process recreation does not yet restore an unsaved
   preview.
6. **Backend PDF release smoke.** iOS completed an authenticated Simulator
   export and Android completed an authenticated emulator export through the
   centralized backend compiler. The physical-device artifact-open retry and
   deployment-worker release smoke remain release-environment checks.
7. **Future server-owned feed materialization.** Feed declarations and initial
   request/result extraction are already server-side, while named Forge views
   and dirty draft state are materialized in the attached client. A future
   design may persist a feed instance keyed by conversation, owning turn/tool
   call, and feed ID, then resolve datasource views on the backend. This pass
   deliberately does not change that ownership model.
