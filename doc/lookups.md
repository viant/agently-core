# DataSources & Lookups: Generic MCP-backed pickers for any form

**Status:** Proposal
**Author:** awitas
**Date:** 2026-04-22
**Scope:** agently-core (framework), agently (UI), forge (reuse), any workspace (steward_ai is the first viable use case)

---

## 1. Problem

Scenarios, prompts, tools, and elicitation forms often need entity references (e.g. ids for campaigns, advertisers, customers, tickets, assets — whatever domain the workspace serves). Today these are **free-text fields with dummy placeholders**; the user has to know or guess the id, and there is no name-based discovery.

We need a mechanism, generic for **any MCP server** exposing tabular/hierarchical data, that:

1. Lets the user **search and pick** an entity by name.
2. **Displays** a friendly label; **stores/normalizes** an id (or any declared canonical value).
3. Supports **flat lists** and **hierarchical trees** (tree is parameterized fetches from the same datasource, not a special kind).
4. **Caches per user** with configurable TTL (default 30 min) so pickers don't hammer the MCP server. User identity is inherited from `context.Context` on the existing MCP call path — not a declared field.
5. Can be attached to a form via **elicitation overlays** (no change to the form schema language).
6. Can be triggered inline in free-text via a **`/<name>` hotkey** that inserts a normalizing token.
7. Can also appear **pre-authored** — a scenario or starting-prompt body containing literal `/<name>` renders as an inline picker at that position.
8. Can be **pre-warmed / refreshed** by the scheduler or a scenario's own MCP tool call.

Design principle: nothing in core may be domain-, MCP-server-, or widget-specific. Domain knowledge lives entirely in workspace YAML.

---

## 2. Existing Building Blocks (what we are reusing)

| Capability | Where | Reuse |
|---|---|---|
| `DataSource` with `Selectors`, `Parameters`, `Paging`, `FilterSet`, `UniqueKey` | forge [types/model.go:850](../../forge/backend/types/model.go) | Base datasource model — we embed it, extend with `Backend` + `Cache` |
| Forge `DataSource` embedding + path projection | [internal/feedextract/extractor.go:9](../internal/feedextract/extractor.go) | Projection engine — unchanged, reused by `Fetch` |
| Elicitation schema refinement (`x-ui-widget`, `x-ui-order`, defaults for type/format) | [service/elicitation/refiner/refiner.go:8-52](../service/elicitation/refiner/refiner.go) | Invocation site for a **new** `overlay.Apply` pass — see §5.0 for scope of new work |
| Workspace kind loader (generic `Repository[T]`) | [workspace/workspace.go:40-65](../workspace/workspace.go), [workspace/repository/base/repository.go](../workspace/repository/base/repository.go) | Register new kinds `datasources`, `lookups/overlays` |
| Streaming bus (`tool_started/completed`) | [sdk/stream_tracker.go](../sdk/stream_tracker.go), [sdk/feed_notifier.go](../sdk/feed_notifier.go) | Internal `datasource/fetch` MCP tool fires the same events |
| MCP **tool** invocation (the actual tool dispatch path, not resource expansion) | [internal/tool/registry/registry.go:662](../internal/tool/registry/registry.go) `Registry.Execute(ctx, name, args)` → [protocol/mcp/proxy/proxy.go:26](../protocol/mcp/proxy/proxy.go) `CallTool(ctx, …)` with auth options attached by [protocol/mcp/manager/auth_token.go:36](../protocol/mcp/manager/auth_token.go) `WithAuthTokenContext`. Caller is [service/shared/toolexec/tool_executor.go:501](../service/shared/toolexec/tool_executor.go). | The `mcp_tool` backend invokes via the same seam. |
| Forge picker primitive (**form-metadata only**; no JSON-schema overlay, no authored-text parsing) | forge [Item.Lookup](../../forge/backend/types/model.go), [TextLookup.jsx](../../forge/src/packs/blueprint/TextLookup.jsx), [utils/lookup.js](../../forge/src/utils/lookup.js), `openLookup` defaults | Reused as-is for Activation (a); Activations (b)+(c) and the schema→Lookup translator are **new** work. |
| Per-turn context | [runtime/memory/memory.go](../runtime/memory/memory.go) | Turn-scoped only — datasource cache is new per-user storage (§6) |
| Missing in forge: `/name` hotkey in text input | — | New component `HotkeyLookupInput` (§9) |

---

## 3. High-Level Design — three layers on top of what already exists

Rather than introduce a parallel "lookup service", we stack three thin layers. Each reuses something that already works.

```
                          ╔═══════════════════════════════════════════╗
  Layer 3 — Activation    ║  (a) elicitation overlay: attach to schema ║
  (how it enters a form)  ║  (b) inline '/name' hotkey in text fields  ║
                          ╚════════════════════╤══════════════════════╝
                                               │ references by name
                          ╔════════════════════▼══════════════════════╗
  Layer 2 — Dictionary    ║  forge Item.Lookup (already exists)        ║
  (picker contract)       ║  + optional tree / breadcrumb variant      ║
                          ╚════════════════════╤══════════════════════╝
                                               │ points to
                          ╔════════════════════▼══════════════════════╗
  Layer 1 — DataSource    ║  forge types.DataSource (already exists)   ║
  (MCP-backed data)       ║  + agently extension: MCP tool binding,    ║
                          ║    pinned args, user-auth, per-user cache  ║
                          ╚════════════════════╤══════════════════════╝
                                               │
                                         MCP tool call
                                   (user's auth context)
```

### 3.1 Layer 1 — DataSource (reuse forge; add MCP binding + cache)

Forge [types.DataSource](../../forge/backend/types/model.go:850) already has everything we need to describe *how to turn an upstream payload into rows a widget can consume*: `Selectors.Data`, `Parameters`, `Paging`, `FilterSet`, `UniqueKey`, `SelectionMode`, `Cardinality`. Agently-core already embeds it in [internal/feedextract/extractor.go:9](../internal/feedextract/extractor.go).

What's missing for our use case:

- A way to declare a datasource whose **backend is an MCP tool** (today forge DataSource only knows `Service` = HTTP endpoint).
- **Pinned parameters** on that MCP call — fixed inputs the workspace author sets (e.g. `tenant=current`, `limit=50`), distinct from caller-supplied inputs.
- **Per-user cache** with configurable TTL (default 30m).

> **Auth is not a datasource concern** — it's a side effect of `context.Context` propagation on the existing tool-call path: [internal/tool/registry/registry.go:712](../internal/tool/registry/registry.go) injects the server's auth token into `ctx` via [WithAuthTokenContext](../protocol/mcp/manager/auth_token.go), which [proxy.go:26 CallTool](../protocol/mcp/proxy/proxy.go) pulls out as a `RequestOption` at dispatch time. `Fetch(ctx, …)` hooks in at the same seam. No `auth:` field in YAML, no override knob. A workspace that wants a service-account backend routes the datasource to a different MCP server wired to that identity. **Caveat:** the end-to-end behaviour is an architectural expectation until the integration test in §10 item 3 is green; don't rely on `scope: user` isolation until then.

We model the extensions as an optional `backend:` and `cache:` section on a datasource declaration, plus a new workspace kind `datasources/`.

```yaml
# <workspace>/datasources/<name>.yaml
id: <string>
# forge DataSource fields (subset; everything forge supports is allowed)
cardinality: collection
selectors:
  data: "<path>"
parameters:                        # caller-provided (mapped from :form / :query)
  - { from: :form, to: :args, name: q }
paging: { enabled: true, size: 50 }

# NEW — agently extension
backend:
  kind: mcp_tool                   # mcp_tool | mcp_resource | feed_ref | inline
  service: <mcp-service>
  method:  <mcp-tool-name>
  pinned:                          # fixed args, never caller-overridable
    limit: 50
cache:
  scope: user                      # user | conversation | global — affects cache key only
  ttl: 30m                         # configurable; default 30m
  maxEntries: 5000
  key: [ args.q ]                  # which param paths contribute to the key (default: all)
  refreshPolicy: stale-while-revalidate
```

The framework's only new responsibility: when a datasource has a `backend`, route its fetch through the MCP invocation path (preserving `ctx`), apply the existing projection, then cache under `(scope-id, datasourceID, paramsHash)`. Everything downstream — forge selectors, unique keys, paging — is unchanged.

`scope: user` is purely a **cache-key** concern — it decides that the cached rows for user A aren't served to user B. It's not an auth policy; the auth policy is whatever `ctx` carried when the miss happened.

### 3.2 Layer 2 — Dictionary / Lookup (reuse forge `Item.Lookup`)

**Yes, forge defines the dialog end-to-end.** We do not invent a dialog schema, column list, label rendering, or selection-wiring — all of that already exists in forge as `Item.Lookup` + a forge Container/Window/Dialog:

- [Item.Lookup](../../forge/backend/types/model.go:791) declares the picker button and the round-trip contract: `DialogId`, `Inputs` (seed parameters sent into the dialog), `Outputs` (parameters extracted from the selected row back into the caller's form), `Title`, `Size`, `Footer`.
- The **dialog itself** is a plain forge Container — its DataSource, its table columns (including which column is the display label), its quick filters, its paging — are all standard forge metadata.
- Runtime: [TextLookup.jsx](../../forge/src/packs/blueprint/TextLookup.jsx) and [utils/lookup.js](../../forge/src/utils/lookup.js) already implement the open-dialog-and-map-outputs behavior.

So for any lookup, the author writes:

1. A **datasource** YAML (§4.1) — describes *how to fetch and project rows*.
2. A **forge Dialog** (standard forge metadata, no extension) — describes *how the picker looks*: title, which columns show, which column is the label, which is the id, any filter toolbar. Its `dataSource` ref points to the datasource from step 1.
3. An **overlay binding** (§4.3) — wires a form field to the dialog via forge `Inputs/Outputs`:
   - `outputs` decides what gets written back (e.g. `id → <formField>`, `name → <displayField>`).
   - `inputs` decides what the caller's form pre-seeds (e.g. `${form.q} → :query`).
   - `display` is a convenience string for chip rendering when the value is already selected.

Concrete example. Note the forge Parameter semantics precisely: `From` / `To` are `[dataSourceRef]:store` sentinels (`:form`, `:query`, `:output`, `:args`, …); `Location` is the selector on the source (defaults to `Name` if omitted); `Name` is the selector on the destination. Inputs default to `from: :form, to: :query`; outputs default to `from: :output, to: :form`. Source: [backend/types/model.go:928](../../forge/backend/types/model.go), [utils/lookup.js:13](../../forge/src/utils/lookup.js), [hooks/window.js:78](../../forge/src/hooks/window.js).

```yaml
# extension/forge/datasources/advertiser.yaml   (hybrid: forge DS + agently backend)
id: advertiser
cardinality: collection
selectors:
  data: "results"
parameters:
  - { from: ":form", to: ":args", name: q }   # caller form.q → MCP args.q
paging: { enabled: true, size: 50 }
backend:
  kind: mcp_tool
  service: platform
  method: advertiser_search
  pinned: { limit: 50 }
cache: { scope: user, ttl: 30m }
```

```yaml
# extension/forge/dialogs/advertiserPicker.yaml   (pure forge — no agently extension)
id: advertiserPicker
type: dialog
title: "Select Advertiser"
size: { width: 720, height: 560 }
dataSource: advertiser                       # refs forge-datasources/advertiser
view:
  type: table
  columns:
    - { field: id,     header: "ID",     width: 100 }
    - { field: name,   header: "Name" }                       # the display column
    - { field: region, header: "Region", width: 120 }
  quickFilter:
    - { field: q, placeholder: "Search…" }
footer:
  ok:     { label: "Select", requireSelection: true }
  cancel: { label: "Cancel" }
```

```yaml
# extension/forge/lookups/site_list_planner.yaml   (agently overlay; uses forge Parameter syntax)
target: { kind: template, id: site_list_planner }
bindings:
  - path: $.properties.advertiser_id
    lookup:
      dialogId: advertiserPicker
      # inputs: omitted → forge default is {from: :form, to: :query, name: <Name>}.
      # outputs: required; location defaults to name if omitted.
      outputs:
        - { location: id,   name: advertiser_id }    # :output.id   → :form.advertiser_id
        - { location: name, name: advertiser_name }  # :output.name → :form.advertiser_name
      display: "${advertiser_name} (#${advertiser_id})"
```

**Caveats worth knowing** (from forge source, not speculation):

- `:output` is the **entire** selected row, not a field — always use `location` to project a column.
- `to: :form` (no dataSource prefix) writes to the caller's default datasource. Use `to: "caller:form"` to disambiguate when needed.
- No built-in pre-commit validation hook; to intercept before the form is written, use `footer.ok.handler` ([model.go:816](../../forge/backend/types/model.go)).
- Spread syntax (`name: "[]ids"`) works in both inputs and outputs; useful when a binding needs to write an array.
- The dialog must call `dialog.commit(payload)` to return a value — forge does not auto-pick the selected row; the dialog's OK action is the commit trigger.

**Division of labor**:

| Concern | File | Owner |
|---|---|---|
| Rows source + projection (MCP tool, pinned args, selectors, cache) | `extension/forge/datasources/*.yaml` | hybrid |
| Dialog title, size, table columns, which column is label, quick filter, footer | `extension/forge/dialogs/*.yaml` | pure forge |
| Round-trip wiring (schema path ↔ dialog, Inputs/Outputs, named tokens) | `extension/forge/lookups/*.yaml` | agently |

Tree variant: same three files, with the dialog's `view.type` set to `tree` and a caller-supplied `parent` input driving the datasource. No new agently concept.

### 3.3 Layer 3 — Activation (how a lookup reaches a form)

Three entry paths. (b) and (c) use the same client component and the same resolution pipeline as (a); the distinction is *where the `/name` token comes from*.

**(a) Elicitation overlay.** An overlay file maps a JSON Schema path (on a template / tool / elicitation request) to a `lookup:` block. The existing elicitation [Refiner](../service/elicitation/refiner/refiner.go) is extended to inject forge-compatible `Item.Lookup` metadata onto the property. The submitted form value is the output mapping (typically the id). Nothing else about the form changes.

**(b) Inline `/name` hotkey (live).** In free-text inputs (prompt box, long-text form fields, chat composer), typing `/` (configurable) opens an autocomplete of **lookup names the current context exposes**. The user continues typing to filter (`/advertiser `), hits Tab/Enter, which pops the same lookup dialog (or an inline mini-popover for small result sets), and on selection inserts a **token** like `@{advertiser:123 "Acme Corp"}` — rendered as a chip displaying the label, stored as an id, and flattened to the id-only form for the model. This widget does **not** exist in forge today (§1 finding 3); it's the one new front-end component we build. It shares 100% of the resolution path with (a) — same datasource, same cache, same output mapping.

**(c) Authored `/name` in starting prompts / templates.** A scenario or template author can write `/advertiser` directly in the prompt body. When the starting-prompt UI (scenario launcher, "starting task" form, message template) renders the body, it **parses** any `/name` occurrence where `name` resolves to a known lookup in the current context and **substitutes an inline picker widget** at that position — same component as (b) in "unresolved" state. The author controls where the picker lands by placement in the text. Examples:

```
Analyze performance for /advertiser over the last /window in /region.
```

renders as a paragraph with three inline pickers. Submission produces the text with the tokens flattened (e.g. `Analyze performance for 123 over the last 14d in NA.` or whatever `modelForm` each binding declares). Until the user fills a picker, the token is in `unresolved` state; the form blocks submission if `required` (declared per-lookup or per-binding).

This unifies authoring: the same `/name` grammar works for (i) authored prompts, (ii) live chat composer typing, (iii) ad-hoc text inputs. The client detects `/name` uniformly; only the *source* of the token differs.

**Discovery of available lookup names** for any of (a)/(b)/(c) goes through a single endpoint:

- `GET /v1/api/lookups/registry?context=<target-kind>:<target-id>` → `[{ name, datasource, display, required? }, ...]`

The registry is composed from all overlays that match the current context.

### 3.3.1 Two representations: stored vs. sent

A resolved lookup value has two lives:

| When | Representation | Contains | Who sees it |
|---|---|---|---|
| **While authoring / editing / stored in form state / persisted in transcript** | **Rich token** — e.g. `@{advertiser:123 "Acme Corp"}` | id **and** label (and optionally extra fields the binding declared) | the user (as a chip), the form runtime, persistence |
| **When the message/tool-args reach the LLM** | **Flattened** via binding's `modelForm` (default: just the id) | id only | the model |

Rules:

1. **Re-openable.** The chip stays a live picker. Clicking it reopens the same forge dialog, pre-selecting the current row (the stored id is fed to the dialog as an input; the dialog's DataSource can fetch-by-id or highlight-by-id). The user can pick a different row or clear the selection.
2. **Send-time flattening, not storage-time.** The rich token is what gets saved (in form state, in the transcript, in conversation history). Flattening to `modelForm` happens once, at the moment a payload leaves for the LLM or a tool call. The stored representation is never downgraded.
3. **Label comes from the token, not a re-fetch.** When a conversation is reloaded, chips rehydrate from the stored token without another MCP round-trip. Optional: a binding can set `revalidateOnMount: true` to re-fetch the label by id (useful if the entity might have been renamed).
4. **Clearing the picker** removes the token and leaves whatever the binding declared as the empty-state text (commonly nothing; or a placeholder like `[advertiser?]`).
5. **Dialog-mode vs. named-token mode use the same pair.** Schema-bound (§3.3a) fields also store `{id, label}` alongside each other (`advertiser_id` + `advertiser_name` via forge `Outputs`) — re-opening the picker reads both, highlights the current selection, and overwrites both on change. Only `advertiser_id` (or whatever the binding declared as the canonical value) is what the LLM ultimately sees.

Storage format for named tokens is opaque to core — proposal: `@{<name>:<id> "<label>"}` with `<label>` shell-escaped. Alternative Markdown form `[label](lookup://name/id)` also works; core roundtrips whatever the binding emits.

### 3.4 What is and isn't hardcoded

Hardcoded in the framework:

- A `Backend` contract on datasources (kinds: `mcp_tool`, `mcp_resource`, `feed_ref`, `inline`) — adding a kind is one interface implementation.
- Cache key formula: `(scope-id, datasourceID, paramsHash)`.
- Overlay placement: JSONPath into JSON Schema (same placement forge already understands for UI hints).
- The `/` hotkey trigger char is a **default** — each text input can override.

Not hardcoded:

- No op names, no widget names, no domain entities, no MCP-server-specific shapes.
- No tree semantics in core — a tree is a forge dialog shape consuming whatever datasources it's wired to.
- Cache TTL / scope / refresh policy — per-datasource; 30m is just the default.
- **Auth is not a datasource concern at all** — it's a side effect of `context.Context` propagation through the existing MCP call path. Nothing to configure.

---

## 4. Workspace Kinds

Three new kinds, all loaded through the existing generic `Repository[T]` pattern. Grouped under a single `extension/forge/` subtree so it is obvious at a glance which files extend forge vs. which are pure agently concerns.

| Kind | Dir | Owner | Purpose |
|---|---|---|---|
| `forge-datasources` | `<workspace>/extension/forge/datasources/*.yaml` | hybrid (forge + agently) | Forge `DataSource` + agently `backend:` + `cache:` (§3.1). |
| `forge-dialogs` | `<workspace>/extension/forge/dialogs/*.yaml` | pure forge | The picker dialog/window definitions — title, columns, display label, quick filter, footer. Loaded by forge as-is. |
| `lookup-overlays` | `<workspace>/extension/forge/lookups/*.yaml` | pure agently | Bindings that wire schema paths or named tokens (`/name`) to a `forge-datasources/*` entry + a `forge-dialogs/*` entry. Corresponds to Activation (a), (b), (c). |

```
<workspace>/
└── extension/
    └── forge/
        ├── datasources/           # forge DS + MCP backend (this proposal)
        │   └── advertiser.yaml
        ├── dialogs/               # pure forge — no agently extension
        │   └── advertiserPicker.yaml
        └── lookups/               # agently overlays (bindings + named tokens)
            └── site_list_planner.yaml
```

Rationale for the `extension/forge/` namespace:

- **Clarity of ownership.** Anything under `extension/forge/` is understood to extend or directly consume the forge metadata vocabulary. Workspace authors browsing these files know forge conventions apply (Parameter syntax, `:form`/`:query`/`:output` sentinels, etc.).
- **Headroom.** Future extensions can land under `extension/<tool>/…` without having to relitigate workspace layout.
- **No coupling.** Moving files under `extension/forge/` is purely a path convention; every file is still a standalone YAML loaded by its own generic `Repository[T]`. Nothing in core cross-references by path.

Registry change in [workspace/workspace.go](../workspace/workspace.go) is ~6 lines (three constants).

### 4.1 `datasources/` — full shape

See §3.1 for the narrative. Formal shape:

```yaml
id: <unique-id>
title: <human label>

# === forge DataSource (inline) — every existing forge field is allowed ===
cardinality: collection            # or single
selectors:
  data: "<path>"                   # projection path into tool result
  dataInfo: "<path>"               # optional — pagination metadata path
parameters:                        # caller-supplied inputs
  - { from: :form, to: :args, name: q }
  - { from: :form, to: :args, name: parent }
paging: { enabled: true, size: 50 }
uniqueKey: [{ field: id }]

# === agently extension ===
backend:
  kind: mcp_tool                   # mcp_tool | mcp_resource | feed_ref | inline
  service: <mcp-service>           # for mcp_tool
  method:  <mcp-tool-name>
  pinned:                          # fixed args — workspace author's choice, never overridable by caller
    limit: 50
  # for mcp_resource:  uri: <mcp-resource-uri>
  # for feed_ref:      feed: <FeedSpec-id>
  # for inline:        rows: [ {...}, {...} ]

cache:
  scope: user                      # user (default) | conversation | global — cache-key only, not auth
  ttl: 30m                         # default 30m; any duration
  maxEntries: 5000
  key: [ args.q, args.parent ]     # which paths participate in key; omit = all args
  refreshPolicy: stale-while-revalidate
```

No `auth:` field. User identity is whatever `context.Context` carries into `Fetch`.

**What the framework knows:** how to load the file, how to merge `pinned` + caller-supplied `parameters` into the backend call, how to apply forge `selectors`/`paging`/`uniqueKey` to the result, how to cache under the declared policy. Nothing about what the datasource "means", nothing about auth.

### 4.2 Invocation contract

One primitive:

```go
Fetch(ctx context.Context, datasourceID string, inputs map[string]any) (forgeResult, error)
```

- `inputs` merge with `pinned` (pinned wins on conflict).
- `ctx` carries the caller's identity (user id, tokens, etc.); it flows untouched into the MCP invocation and drives `scope: user` cache keying. No side-channel for auth.
- Result is the already-projected forge payload — the same shape forge `Item.Lookup` dialogs consume today.

HTTP endpoints, internal MCP tools, scheduler, and widgets all route through `Fetch`. There are no per-datasource or per-MCP-server code paths in core.

### 4.3 `lookups/overlays/` — bindings for activation

An overlay attaches a datasource-backed picker to somewhere it should appear. Two binding kinds — (a) and (b) from §3.3:

```yaml
# lookups/overlays/<any-target>.yaml
target:
  kind: elicitation | template | tool | prompt | chat-composer | any-text-input
  id: <target-id-or-matcher>

bindings:
  # (a) schema-bound: attach forge Item.Lookup to a property
  - path: $.properties.<field>
    lookup:
      dataSource: <datasource-id>
      inputs:  [ ... ]             # forge-compatible Parameter list; supports ${form.*}
      outputs: [ ... ]              # forge-compatible Parameter list; typically maps id → field
      display: "${name} (#${id})"
      dialog: { size: md, tree: false }   # tree: true ⇒ hierarchical dialog variant

  # (b)+(c) named token — same declaration works for live hotkey AND authored prompts
  - named:
      trigger: "/"                  # char that introduces the token; default "/"
      name: advertiser              # so "/advertiser" in text OR user typing "/ad…" matches
      dataSource: <datasource-id>
      queryInput: q                 # which datasource input gets the typed text (for live mode)
      required: false               # if true, authored token blocks submission until resolved
      token:
        store: "${id}"              # what the form persists
        display: "${name}"          # what the chip shows
        modelForm: "${id}"          # what the LLM sees after the text is flattened
```

A `named` binding serves both (b) and (c) by design: it registers the name `advertiser` for the target context, so that (b) a `HotkeyLookupInput` component can autocomplete it live and (c) a prompt/template authored with literal `/advertiser` can be rendered with an inline picker at that position. The same binding drives both — one declaration, three activation paths.

Nothing here names an MCP server, an op, or a domain entity. Genericity follows from the fact that everything downstream is a plain forge DataSource fetch.

---

## 5. Activation — how overlays turn into a working picker

> **Side-of-the-wire reminder.** Elicitation overlay (Activation (a)) is a **server-side feature** — overlays load, match, and refine JSON Schemas entirely inside agently-core (Go). The client receives an already-refined schema with forge `Item.Lookup` metadata attached and treats it as a plain forge schema. Clients (web / iOS / Android) have no overlay code, no match-mode logic, no YAML loading. The only client-side work related to overlays is consuming the registry endpoint (for Activations (b) and (c)) — and even there, the registry itself is composed server-side; the client just reads the result.
>
> [lookups-test.mjs](../lookups-test.mjs) is therefore a **cross-cutting spec-as-code**, not a client test. The overlay-matching portion (T18–T24) pins down behaviour the Go package `service/lookup/overlay/` must implement; the token/chip/flatten portion (T12–T17) pins down behaviour the web + mobile clients must implement identically.

### 5.0 Scope of the new canonical overlay layer

A reviewer noted — correctly — that the proposal can read as if attaching lookups to a schema is "just a small hook into the existing Refiner". It isn't. The existing [service/elicitation/refiner/refiner.go](../service/elicitation/refiner/refiner.go) only adds simple defaults: inferring `type`, inferring `format` ("date"/"date-time") from title/description hints, setting `x-ui-widget: tags` for string arrays, and assigning `x-ui-order`. **There is no existing overlay model, no schema→forge bridge, no JSONPath matcher, no registry, and no token/chip layer.** These are real new subsystems, not minor edits.

What's genuinely new (listed so estimates are honest):

| Component | Where | What it does |
|---|---|---|
| `protocol/lookup/overlay/` | new package | Types: `Overlay`, `Binding` (two flavours: schema-path `path`, named-token `named`), `TokenFormat`, `LookupRef`. |
| `service/lookup/overlay/matcher.go` | new | JSONPath matcher that resolves an overlay's `path` against an incoming JSON Schema (tool input, elicitation request, template). Small, but new code. |
| `service/lookup/overlay/translator.go` | new | For matched schema-path bindings: emit forge `Item.Lookup` metadata (dialogId, inputs, outputs defaults, display) onto the property via `x-ui-widget: lookup` + attachment block. This is the **schema → forge Lookup bridge**; it does not exist today. |
| `service/lookup/overlay/registry.go` | new | For a given render context (`template:site_list_planner`, `chat:composer`, `tool:someTool`), compose the set of named bindings available for `/name` autocomplete. Served by `GET /v1/api/lookups/registry` (§13). |
| `service/lookup/overlay/apply.go` | new | Public `Apply(ctx, schema, contextID) (refinedSchema, registry)` entry point called by the elicitation refiner and the template renderer. |
| Call site in [service/elicitation/refiner/refiner.go](../service/elicitation/refiner/refiner.go) | modified | Append one call to `overlay.Apply` after the existing refinement. Pre-existing behaviour unchanged; this is the only line in the refiner that changes. |
| Token/chip model (frontend) | new | Grammar `@{name:id "label"}`, parse/serialize, chip render, send-time flatten. Shared pure functions across web / iOS / Android. |
| Authored-text parser | new | Scan template/scenario/free-text bodies for `/name` tokens, substitute with chips in unresolved state. Frontend. |

The Go backend side of the overlay layer is on the order of **4–6 files in a new package**, not a three-line hook. Keep that in mind when sizing phase 1.

### 5.0.1 Overlay matching — modes and composition

**Match mode is an overlay-level property, not a framework-wide setting.** Each overlay file declares its own `mode` independently. Two overlays applying to the same schema can have different modes (e.g. a strict template-specific overlay plus partial library field overlays) and they compose without interference. There is no global default-override chain, no inheritance, and no cross-overlay mode coupling — keep each overlay's match policy local to the file that declares it.

A schema emitted by a tool / LLM / template rarely lines up 1:1 with an overlay's bindings. The three modes each overlay can pick from:

1. **`mode: strict` (all-match)** — overlay applies only if **every** binding finds a matching property in the schema. Fail-fast for overlays that encode an invariant ("if this isn't a `site_list_planner` schema, don't touch it").
2. **`mode: partial` (any-match, default)** — overlay applies **each** binding that matches; unmatched bindings are silently skipped; unmatched schema properties are untouched. This is the common case ("MCP-generated schema has 5 fields; our overlay knows 3; apply the 3").
3. **`mode: threshold` with `threshold: N`** — apply only if at least N of the overlay's bindings match; otherwise skip the whole overlay. Guards against spurious matches when a schema coincidentally has one field of the right name.

And, composing orthogonally with the per-overlay mode, **multi-overlay composition**: many small single-field overlays (each typically `mode: partial` with one binding — the `1-of-N` case), arbitrary number (`M`) attached to the same target, compose into the final refinement. Each still evaluates its own mode in isolation.

### 5.0.1.1 Server vs. client responsibilities

| Responsibility | Side | Lives in |
|---|---|---|
| Load `extension/forge/lookups/*.yaml` overlays | **server** | `workspace/` loaders |
| Match bindings against incoming schema (JSONPath / glob / regex / fieldName) | **server** | `service/lookup/overlay/matcher.go` |
| Evaluate per-overlay `mode` (strict/partial/threshold) | **server** | `service/lookup/overlay/apply.go` |
| Compose surviving bindings across overlays with priority + deterministic tie-break | **server** | `service/lookup/overlay/apply.go` |
| Emit forge `Item.Lookup` metadata onto schema properties | **server** | `service/lookup/overlay/translator.go` |
| Call `overlay.Apply` from the elicitation refiner | **server** | [service/elicitation/refiner/refiner.go](../service/elicitation/refiner/refiner.go) (one-line call-site addition) |
| Compose & serve the named-token registry for a context | **server** | `service/lookup/overlay/registry.go` → `GET /v1/api/lookups/registry` |
| Render the already-refined schema (picker dialog, Inputs/Outputs round-trip) | **client** (forge) | forge [TextLookup.jsx](../../forge/src/packs/blueprint/TextLookup.jsx), [utils/lookup.js](../../forge/src/utils/lookup.js) — *no change* |
| `/name` hotkey + authored parser + chip rendering + token parse/serialize + send-time flatten | **client** | new `NamedLookupInput` (web) + SwiftUI/Compose equivalents (§14) |
| Fetch rows for a picker | **client → server** | `POST /v1/api/datasources/{id}/fetch` |

No overlay YAML ever leaves the server. The client only ever sees: (1) a schema with forge metadata attached, (2) a registry list of named tokens available in the current context, (3) row results from `datasources/{id}/fetch`. Everything else is backend.

### 5.0.2 Overlay declaration with matching

```yaml
# extension/forge/lookups/<any>.yaml
id: <unique-id>
priority: 100                        # higher wins on path collisions; default 0

target:
  kind: template | tool | elicitation | prompt | chat-composer
  # One of these — or combine:
  id: <exact-id>                     # match by id
  idGlob: "site_list_*"              # or glob
  schemaContains: [advertiser_id]    # or: require schema to have these properties

mode: strict | partial | threshold   # default: partial
threshold: 2                         # only when mode=threshold

bindings:
  - match:                           # each binding is matched independently
      # exactly one of:
      path: "$.properties.advertiser_id"   # JSONPath, exact
      pathGlob: "$.properties.*_id"        # glob form
      fieldName: "advertiser_id"           # matches any depth
      fieldNameRegex: "^.*_id$"
      # optional constraints:
      type: integer
      format: "int64"
    lookup:
      dataSource: advertiser
      dialogId: advertiserPicker
      outputs: [...]
```

**How the matcher runs:**

1. Gather all overlays whose `target` admits this context (id / idGlob / schemaContains).
2. Order by `priority` (descending).
3. For each overlay, evaluate every binding's `match` against the schema; collect matched ones.
4. Apply `mode`:
   - `strict` → require all bindings matched, else discard whole overlay.
   - `partial` → keep only matched bindings.
   - `threshold: N` → keep matched bindings iff count ≥ N; else discard whole overlay.
5. Compose surviving bindings across overlays. On path collision, higher-priority overlay wins; ties fall back to load order (deterministic by filename).
6. Translator emits forge `Item.Lookup` metadata for each surviving binding (§5.1).

### 5.0.3 Typical overlay portfolios

- **Library overlays (tiny, reusable, `partial` mode).** One file per concept — `overlays/fields/advertiser_id.yaml`, `overlays/fields/campaign_id.yaml`, `overlays/fields/feature_key.yaml`. Each has one binding matching by `fieldName` and/or `fieldNameRegex` + `type` constraint. These apply to **any** schema that has such a field — covers the `1-of-N × M` case automatically.
- **Template-specific overlays (`strict` or `threshold`).** One file per template id — `overlays/templates/site_list_planner.yaml`. Uses `target.id` + `mode: strict` so it only activates on the intended schema shape.
- **Tool-generated schemas (`partial` + field-pattern).** For LLM-emitted / MCP-emitted tool argument schemas where field inventory varies call-to-call. Use `pathGlob` or `fieldNameRegex` to catch structural patterns without knowing the exact schema ahead of time.

This gives workspace authors three recipes that compose cleanly without growing core semantics.

### 5.1 Elicitation overlay (Activation (a)) — server-side flow

The entire overlay step runs in the Go backend before the schema reaches any client:

1. A tool / template / LLM emits a JSON Schema **on the server**.
2. [service/elicitation/refiner/refiner.go](../service/elicitation/refiner/refiner.go) runs its existing refinement (unchanged), then calls the new `overlay.Apply(ctx, schema, contextID)`.
3. `Apply` walks loaded overlays, runs match + per-overlay mode + composition (§5.0.1), and the translator attaches forge `Item.Lookup` metadata (`x-ui-widget: lookup` + a structured attachment block carrying `dialogId`, `inputs`, `outputs`, `display`) onto surviving property paths.
4. The refined schema is sent over the wire to the client.
5. The forge renderer (client) sees a schema with `Item.Lookup` metadata and renders the picker — no overlay awareness required. The dialog's DataSource resolves through `POST /v1/api/datasources/{id}/fetch`, which the server backs with `extension/forge/datasources/<id>.yaml`.
6. User picks a row. Forge `Outputs` write the chosen fields back to the form. Default `outputs` writes id → caller field.

Submission payload is unchanged — same IDs the form has always sent. The model never sees display labels. The client never sees an overlay file.

### 5.2 Inline `/name` hotkey (Activation (b))

Today forge has no `/mention`-style trigger inside text fields (confirmed: no "mention", "slash", or "@" mechanism in [forge/src](../../forge/src)). This is the one new front-end component.

Behavior:

1. Any text input that opts in (chat composer, long-text form field, prompt box) is wrapped with a `MentionInput` / `HotkeyLookupInput` component that watches for the trigger character.
2. On trigger, the component asks the server: "what lookup names are registered for this context?" (context = target id + overlays matching it). It shows a small autocomplete of those names.
3. The user finishes typing the name (`/advertiser `) and continues with the query. The component calls `Fetch(dataSource, { q: "<typed>" }, user)` on every keystroke (debounced). Results appear in a popover; small sets can be picked inline, large sets open the same forge lookup dialog.
4. On selection, the component inserts a **token** into the text. The token is two-faced:
   - **On screen:** rendered as a chip showing `display` (e.g. `"Acme Corp"`).
   - **In storage:** serialized as `@{advertiser:123 "Acme Corp"}` (format is opaque; framework just needs roundtripping).
   - **To the model:** flattened to `modelForm` before send (usually just the id, sometimes `id (label)` depending on binding).
5. When the message is loaded back into the UI, tokens are re-hydrated from storage to chips.

The hotkey path shares 100% of the resolution with §5.1 — same `Fetch`, same datasource, same cache. Only the UI entry point differs.

### 5.3 HTTP surface

Two generic endpoints are enough for both (a) and (b):

- `POST /v1/api/datasources/{id}/fetch` body: `{ inputs: { ... } }` → forge result (rows + dataInfo)
- `DELETE /v1/api/datasources/{id}/cache[?inputsHash=...]`

No separate "search", "children", or "resolve" endpoints — those are just `Fetch` calls with different inputs. A tree dialog paginates through children by calling `Fetch` with the parent id; a resolver by id calls `Fetch` with an id filter. All three cases are the same HTTP call.

---

## 6. Per-User Cache

Lives in a new package `service/datasource/cache` — this is the new user-scoped cache primitive core currently lacks.

### 6.1 Keying & storage

```
key   = (scope-id, datasourceID, inputsHash)
value = forge result payload (rows + dataInfo, post-projection)
meta  = { fetchedAt, ttl, source, etag? }
```

- `scope-id` = userID when `scope: user`, conversationID when `scope: conversation`, the literal `"global"` when `scope: global`.
- `inputsHash` covers the subset of inputs listed in `cache.key` (or all merged inputs if omitted). Pinned args participate implicitly because they're part of the effective call.
- Backend: in-process LRU first; optional Datly DAO `pkg/dscache/` (mirrors `pkg/conversations`) in phase 4 for persistence across restarts.
- TTL and max entries from the datasource's `cache:` block. Defaults: 30m, 5000 entries.
- `stale-while-revalidate`: return stale payload instantly, kick off a background refresh if age > TTL.

### 6.2 Invalidation

- TTL expiry.
- Explicit: `DELETE /v1/api/datasources/{id}/cache[?inputsHash=…]`.
- Internal MCP tool `datasource/invalidate` (model- or scheduler-initiated — see §7).
- Future: MCP `resources/subscribe` → invalidate on `ResourceUpdated`.

### 6.3 Relationship to feeds

`FeedSpec` cache is turn-scoped and response-shaped (UI container snapshot), not a reusable query-indexed store. Datasource cache is cross-turn, per-user, inputs-indexed. The **projection engine is the same** ([internal/feedextract/extractor.go](../internal/feedextract/extractor.go)); the **storage** is new.

---

## 7. Programmatic entry points (no skills needed)

Three callers reach the `service.Fetch` primitive, all through the same underlying function. **No skills.**

| Caller | Entry | Why this one |
|---|---|---|
| UI picker (web + mobile) | HTTP: `POST /v1/api/datasources/{id}/fetch` | Client isn't in a Go process. |
| LLM during a turn | Internal MCP tool: `datasource/fetch`, `datasource/invalidate` | Tool calls are the natural LLM-initiated pattern; model can resolve an id → label or disambiguate user input. |
| Scheduler (prewarm / refresh cron) | Direct `service.Fetch(ctx, …)` call | In-process Go; no wrapping required. |

### 7.1 Why not skills

A skill wrapping `datasource:fetch` would be pure pass-through. It would not add:

- **Prompt preprocessing** — skill body is empty; nothing to velty-expand.
- **Conversation forking** — `Frontmatter.ContextMode` (`fork`/`detach`) is meaningless for a cache fetch.
- **Unique lifecycle events** — MCP tool calls and HTTP handlers already emit streaming events.
- **A different auth path** — identical `ctx`, identical service method.

Dropping the skill layer eliminates a redundant entry point and keeps the public surface to two verbs: fetch and invalidate, exposed via HTTP for UIs and MCP tool for models.

### 7.2 Prewarm and refresh

- **Prewarm** = `datasource/fetch` with `bypassCache: false, writeThrough: true` (optional hints carried in the input struct). Schedulers call it.
- **Refresh** = `datasource/invalidate` then `datasource/fetch`.

Neither is a distinct primitive. Both are compositions the scheduler or a scenario invokes directly.

---

## 8. End-to-end flows

### 8.1 Elicitation overlay flow (Activation (a))

1. User opens a form. Its target (template / tool / elicitation) has a matching overlay.
2. [service/elicitation/refiner/refiner.go](../service/elicitation/refiner/refiner.go) runs its usual refinement, then `overlay.Apply` injects forge `Item.Lookup` metadata onto bound properties.
3. Forge renderer (unchanged) renders a `TextLookup` field. On focus/search, it calls `POST /v1/api/datasources/{id}/fetch` with the form-supplied inputs + pinned args.
4. Cache miss → backend runs the MCP tool in the user's auth context, applies forge `selectors`/`paging`/`uniqueKey` ([internal/feedextract/extractor.go](../internal/feedextract/extractor.go)), stores rows (default TTL 30m, stale-while-revalidate).
5. Dialog renders rows. User picks one. Forge `Outputs` mappings write declared fields back to the form.
6. On submit, the form sends plain values (typically id only) — the model never sees the display label unless explicitly mapped.

### 8.2 Live inline `/name` hotkey flow (Activation (b))

1. User types `/` in a text input registered for named bindings.
2. Client calls `GET /v1/api/lookups/registry?context=…` (cached) to list available names.
3. Autocomplete narrows as the user types (`/adv…`). Commit the name and keep typing → the remaining text is passed as `queryInput` on debounced `POST /v1/api/datasources/{id}/fetch` calls.
4. Small result sets show in an inline popover; large sets open the forge dialog.
5. On selection, a token `@{advertiser:123 "Acme Corp"}` is inserted — rendered as a chip, stored verbatim, flattened via the binding's `modelForm` before the message reaches the LLM.

### 8.3 Authored `/name` in starting prompt flow (Activation (c))

1. The starting-prompt / template renderer receives a prompt body that contains literal `/name` occurrences — authored by a scenario owner.
2. On render, it asks `GET /v1/api/lookups/registry?context=…` and scans the body for every `/<name>` where `name` is in the registry.
3. Each matched occurrence is replaced by an inline picker widget in `unresolved` state (same component the live hotkey uses). The surrounding text stays as authored.
4. User fills each picker; the text is submitted with tokens flattened via `modelForm`.
5. If a binding is `required: true`, the Start button is disabled until every instance of that name is resolved.

---

## 9. File-Level Change List

### agently-core

- **New** `protocol/datasource/` — types `DataSource` (embeds forge `types.DataSource`), `Backend` (sum of kinds), `CachePolicy`. Plus `Overlay`, `Binding` under `protocol/lookup/overlay/`.
- **New** `service/datasource/` — one method: `Fetch(dsID, inputs, caller) (forgeResult, error)`, plus `InvalidateCache(...)`. No other public surface.
- **New** `service/datasource/backend/` — interface `Backend.Call(ctx, args, caller) (any, error)` + one file per kind (`mcp_tool.go`, `mcp_resource.go`, `feed_ref.go`, `inline.go`). Adding a kind = adding a file.
- **New** `service/lookup/overlay/` — merge overlays into any JSON Schema (works for elicitation, tools, templates alike).
- **New** internal MCP tools `datasource/fetch`, `datasource/invalidate` registered on the same internal MCP bus as [mcp/internal/](../mcp/internal) peers. No skill wrappers (see §7.1).
- **New** Datly DAO `pkg/dscache/` (mirrors `pkg/conversations`) for phase-4 persistence.
- **Modify** [workspace/workspace.go](../workspace/workspace.go) — add three kinds under `extension/forge/`: `KindForgeDataSource = "extension/forge/datasources"`, `KindForgeDialog = "extension/forge/dialogs"`, `KindForgeLookup = "extension/forge/lookups"`.
- **Modify** [service/elicitation/refiner/refiner.go](../service/elicitation/refiner/refiner.go) — call overlay merger after existing refinements.
- **Modify** [sdk/](../sdk/) — two HTTP routes: `POST /v1/api/datasources/{id}/fetch` and `DELETE /v1/api/datasources/{id}/cache[?inputsHash=…]`. Plus a small endpoint to list hotkey-registered lookups for a given text-input context.
- **Reuse** [internal/feedextract/extractor.go](../internal/feedextract/extractor.go) — projection engine unchanged.
- **Reuse** forge [types.DataSource](../../forge/backend/types/model.go) — embedded, not re-implemented.

### agently (UI)

- **Reuse** forge [Item.Lookup](../../forge/backend/types/model.go) + [TextLookup.jsx](../../forge/src/packs/blueprint/TextLookup.jsx) + [utils/lookup.js](../../forge/src/utils/lookup.js) for Activation (a). No new widget needed for the schema-bound case.
- **New** `NamedLookupInput` component covering Activation (b) **and** (c):
  - (b) wraps a text input, watches for the trigger char, autocompletes names from the registry, inserts tokens on selection.
  - (c) on initial render, parses the body for `/name` occurrences, replaces each with an inline picker at the matched position.
  - Shared token format `@{name:id "label"}`, chip rendering, and flattening to `modelForm`.
- **New** starting-prompt / template renderer hook that invokes the (c) parser before display.
- **New** optional tree dialog variant (a second forge dialog preset: lazy-children + breadcrumbs). Still forge `Item.Lookup` with a DataSource wired to a parent-id input.

### Workspace side (no core changes per domain)

Workspaces contribute under `extension/forge/`:

- `extension/forge/datasources/*.yaml` — forge DataSource + agently `backend:` + `cache:`.
- `extension/forge/dialogs/*.yaml` — pure forge picker dialogs.
- `extension/forge/lookups/*.yaml` — overlays (schema-bound + named-token bindings).

Core ships zero workspace-specific datasources or overlays. steward_ai is the first viable use case; any domain (CRM, ticketing, SRE inventory, …) plugs in the same way.

---

## 10. Open Questions

1. **Dependent fields** — diamond dependencies between form fields (A → B, A → C, B → C). Proposal: reject cycles; allow DAG; overlay loader validates.
2. **Unknown / free-text values** — when the user types a name no datasource resolves. Proposal: per-binding `allowFreeText: bool`; when true, pass through with `unresolved: true` metadata.
3. **Auth propagation** — architecturally flows via `context.Context`: [internal/tool/registry/registry.go:712](../internal/tool/registry/registry.go) injects the server-specific token via [`WithAuthTokenContext`](../protocol/mcp/manager/auth_token.go), which is then pulled out at [protocol/mcp/proxy/proxy.go:26](../protocol/mcp/proxy/proxy.go) `CallTool` as a `RequestOption`. **This is an assumption until it has an end-to-end integration test.** Phase 1 cannot ship without a test that:
   - Posts `/v1/api/datasources/{id}/fetch` with a user's JWT.
   - Asserts the downstream MCP server receives the expected `Authorization: Bearer <token>` (or ID-token, depending on `useIDToken` config).
   - Asserts two concurrent users' fetches produce two independent cache keys and two independent upstream auth contexts (no leakage).
   - Asserts `scope: user` cache isolation by sending the same inputs from two users and observing two `service.Fetch` miss counts.
   Without this, `scope: user` isolation is paper-only.
4. **Tree dialog cost** — lazy vs. eager. Proposal: lazy by default; allow `preload: { rootIds: [...] }` at binding level for small taxonomies.
5. **Cache store durability** — in-process only in phase 1; Datly-backed in phase 4.
6. **Overlay target matching** — JSONPath vs dotted path, how to match tool-name patterns. Proposal: JSONPath for schema paths; glob for target ids.
7. **Hotkey collision** — when two text inputs are live on the same screen and one wants `/`, another `@`. Proposal: trigger is per-input, set by the input's binding list; autocomplete name namespaces prevent collision within a single input.
8. **Token format** — `@{name:id "label"}`. Alternative: Markdown-style `[label](lookup://name/id)`. Proposal: prefer the JSON-like form; round-trippable, terse, and easy to flatten to `modelForm`.

---

## 11. Phased Delivery

**Phase 1 — DataSource layer + elicitation activation.**
`extension/forge/datasources/` kind + `mcp_tool` backend via [registry.go:662 Execute](../internal/tool/registry/registry.go) → [proxy.go:26 CallTool](../protocol/mcp/proxy/proxy.go) + in-process user-scoped cache + `POST /v1/api/datasources/{id}/fetch` + new `service/lookup/overlay/` package (matcher/translator/registry/apply) + single new call-site in [elicitation refiner](../service/elicitation/refiner/refiner.go) + forge `Item.Lookup` wired to datasource ids. Prove it with one advertiser picker in steward_ai.

**Phase 1 exit gate (must pass before ship):** an end-to-end integration test that posts `/v1/api/datasources/{id}/fetch` with a real user JWT, asserts the downstream MCP server sees the expected token, and asserts `scope: user` cache isolation across two concurrent users. See §10 item 3.

**Phase 2 — Named-token activation (both live hotkey and authored `/name`).**
`NamedLookupInput` component + `/v1/api/lookups/registry` endpoint + token rendering, flattening, and roundtripping. Wire into chat composer, long-text form fields, and starting-prompt renderer (so authored `/advertiser` in a scenario body becomes an inline picker).

**Phase 3 — Tree dialog variant.**
Lazy-children + breadcrumbs dialog preset. Parent-id parameterization on existing datasource. Migrate targeting taxonomy onto it.

**Phase 4 — Mobile SDK parity (iOS + Android).**
Mirror the three Go client methods in Swift + Kotlin; port Inputs/Outputs mapping + token parse/serialize/flatten as pure functions with platform-matched tests; ship native picker views (SwiftUI `List` / Compose `LazyColumn`) and `NamedLookupInput` equivalents consuming the same `extension/forge/dialogs/*.yaml` metadata. Mobile transcript rehydration uses the stored `@{…}` token — no re-fetch.

**Phase 5 — Durability & ops.**
Datly-backed cache (`pkg/dscache`), scheduler-driven prewarm, `stale-while-revalidate` (default on), metrics, optional MCP `resources/subscribe` invalidation, optional on-device cache mirror for offline mobile.

---

## 12. What this explicitly does NOT do

- Does not introduce a new schema language — JSON Schema + forge `Item.Lookup` via `x-ui-*` stays the contract.
- Does not change how tool calls are executed or how MCP auth flows — backends invoke through the existing MCP path; the caller's identity is a `context.Context` side effect, never re-declared in YAML.
- Does not require any MCP server to speak a new protocol. Any tool that returns rows (or a single row) works. Tool-specific input shape is absorbed by `parameters` + `pinned`; tool-specific output shape is absorbed by forge `Selectors`.
- Does not replace feeds — feeds drive dashboards; datasources drive pickers. Projection engine is shared.
- Does not hardcode any domain entity, widget name, cache policy, or auth model. Everything named here as a default (30m TTL, `scope: user`, `/` hotkey, stale-while-revalidate) is overridable per datasource or per binding.

---

## 13. SDK Extension

The feature is not usable without symmetric extensions across every SDK surface (Go `Client`, HTTP handlers, embedded `Backend`, wire types, language bindings, streaming). All of them follow existing patterns — no new plumbing.

### 13.1 Go `Client` interface — new methods

Add to [sdk/client.go](../sdk/client.go) alongside existing resource CRUD:

```go
type Client interface {
    // … existing methods …

    // Datasource fetch + cache management (ctx carries caller identity).
    FetchDatasource(ctx context.Context, in *api.FetchDatasourceInput) (*api.FetchDatasourceOutput, error)
    InvalidateDatasourceCache(ctx context.Context, in *api.InvalidateDatasourceCacheInput) error

    // Lookup registry (named bindings available for a given render context).
    ListLookupRegistry(ctx context.Context, in *api.ListLookupRegistryInput) (*api.ListLookupRegistryOutput, error)
}
```

### 13.2 HTTP routes

Registered in `registerCoreRoutes()` at [sdk/handler.go:135](../sdk/handler.go) under the existing router — which means **all three routes inherit the same middleware stack** (auth, debug, CORS, request id). If the workspace has OAuth enabled, OAuth applies uniformly. No bespoke exemption.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/api/datasources/{id}/fetch` | **required if workspace OAuth enabled** (same as tool routes) | Fetch rows; miss triggers MCP call under caller's ctx |
| `DELETE` | `/v1/api/datasources/{id}/cache` | **required if workspace OAuth enabled** | Invalidate cache; optional `?inputsHash=…` |
| `GET` | `/v1/api/lookups/registry` | **required if workspace OAuth enabled** (explicit callout — user question) | List named bindings for `?context=<target-kind>:<target-id>` |

The registry endpoint is **not public**: even though it returns metadata not rows, the set of visible lookup names is itself a product of the caller's authorization (e.g. overlays may be gated per tenant or role). It goes through the same `http_auth` chain as `/v1/api/tools/*` and `/v1/api/conversations/*`.

New handler files:

- `sdk/handler_datasources.go` — analogous to [handler_tools_resources.go](../sdk/handler_tools_resources.go). Pattern:
  ```go
  func (s *Server) handleFetchDatasource() http.HandlerFunc {
      return func(w http.ResponseWriter, r *http.Request) {
          id := r.PathValue("id")
          var in api.FetchDatasourceInput
          if err := json.NewDecoder(r.Body).Decode(&in); err != nil { httpError(w, 400, err); return }
          out, err := s.client.FetchDatasource(r.Context(), &api.FetchDatasourceInput{ID: id, Inputs: in.Inputs})
          if err != nil { httpError(w, statusFor(err), err); return }
          httpJSON(w, 200, out)
      }
  }
  ```
- `sdk/handler_lookups.go` — holds the registry endpoint.

### 13.3 Embedded backend (in-process)

[sdk/embedded.go](../sdk/embedded.go) `backendClient` gets three method implementations that delegate to `service/datasource` and `service/lookup/overlay` directly — same pattern as [embedded_resources.go](../sdk/embedded_resources.go). Same signatures as the HTTP client; callers that embed agently-core skip HTTP entirely. Auth still flows via `ctx` since there's no transport boundary.

### 13.4 Wire types

Add to [sdk/api/types.go](../sdk/api/types.go):

```go
type FetchDatasourceInput struct {
    ID     string                 `json:"id"`
    Inputs map[string]any         `json:"inputs,omitempty"`
    Cache  *DatasourceCacheHints  `json:"cache,omitempty"`   // bypass / writeThrough
}

type FetchDatasourceOutput struct {
    Rows     []map[string]any  `json:"rows"`
    DataInfo *PageInfo         `json:"dataInfo,omitempty"`
    Cache    *CacheMeta        `json:"cache,omitempty"`     // fetchedAt, stale, ttl
}

type InvalidateDatasourceCacheInput struct {
    ID         string `json:"id"`
    InputsHash string `json:"inputsHash,omitempty"`
}

type ListLookupRegistryInput struct {
    Context string `json:"context"`                          // "<target-kind>:<target-id>"
}

type LookupRegistryEntry struct {
    Name       string         `json:"name"`                  // e.g. "advertiser"
    DataSource string         `json:"dataSource"`
    Trigger    string         `json:"trigger"`               // "/" default
    Required   bool           `json:"required,omitempty"`
    Display    string         `json:"display,omitempty"`
    Token      *TokenFormat   `json:"token,omitempty"`
}

type ListLookupRegistryOutput struct {
    Entries []LookupRegistryEntry `json:"entries"`
}
```

Type aliases in [sdk/types.go](../sdk/types.go) re-export as needed to keep the public surface stable.

### 13.5 Streaming (cache invalidation events)

[sdk/feed_notifier.go](../sdk/feed_notifier.go) pattern generalizes — emit `datasource_cache_invalidated` on `streaming.Bus` when `InvalidateDatasourceCache` runs, so any open UI (e.g. a picker displaying stale rows) can refresh without polling. Tracked by the existing `StreamEvents()` subscriber in [sdk/stream_tracker.go](../sdk/stream_tracker.go). Optional — opt-in per workspace.

### 13.6 Language bindings (hand-written, 1:1 with Go)

- [sdk/ts/src/client.ts](../sdk/ts/src/client.ts) — add `fetchDatasource`, `invalidateDatasourceCache`, `listLookupRegistry`. Mirror Go method names.
- [sdk/ios/Sources/AgentlySDK/Models.swift](../sdk/ios/Sources/AgentlySDK/Models.swift) + client file — add Swift `Codable` structs and methods.
- [sdk/android](../sdk/android) — add Kotlin structs + client methods.

Since all three are hand-written and mirror the Go `Client` 1:1, keeping them in sync is a mechanical change per release; no codegen dependency introduced.

### 13.7 Auth behaviour summary

- Workspace **without OAuth**: routes open; `ctx` carries whatever identity the local auth middleware attaches (may be anonymous). Cache `scope: user` keys off that identity; for anonymous callers that collapses to a single shared key, which is a known trade-off for local dev.
- Workspace **with OAuth**: all three endpoints (`POST /v1/api/datasources/{id}/fetch`, `DELETE .../cache`, `GET /v1/api/lookups/registry`) sit behind the same [http_auth](../sdk/http_auth.go) chain as the rest of the API. **No bypass for the registry.** Token rides in `ctx` into `service/datasource.Fetch`, which hands it to the MCP invocation — the existing tool call path — so the MCP server sees the real user.

---

## 14. Mobile SDK parity (iOS + Android)

Mobile SDKs are hand-written mirrors of the Go `Client` ([sdk/ios/Sources/AgentlySDK/](../sdk/ios/Sources/AgentlySDK/), [sdk/android/src/main/java/com/viant/agentlysdk/](../sdk/android/src/main/java/com/viant/agentlysdk/)). The feature must work end-to-end on mobile, not just web. Scope:

### 14.1 What gets ported literally

**Transport + wire types** — mechanical 1:1 mirror, same release as the Go change:

- [sdk/ios/Sources/AgentlySDK/AgentlyClient.swift](../sdk/ios/Sources/AgentlySDK/AgentlyClient.swift) — add `fetchDatasource`, `invalidateDatasourceCache`, `listLookupRegistry`.
- [sdk/ios/Sources/AgentlySDK/Models.swift](../sdk/ios/Sources/AgentlySDK/Models.swift) — Swift `Codable` mirrors of `FetchDatasourceInput/Output`, `InvalidateDatasourceCacheInput`, `ListLookupRegistryInput/Output`, `LookupRegistryEntry`.
- [sdk/android/.../Client.kt](../sdk/android/src/main/java/com/viant/agentlysdk/Client.kt) — Kotlin `suspend fun fetchDatasource(…)` etc.
- [sdk/android/.../Models.kt](../sdk/android/src/main/java/com/viant/agentlysdk/Models.kt) — Kotlin `data class` equivalents.

**Streaming** — existing SSE transports ([sdk/ios/.../SSETransport.swift](../sdk/ios/Sources/AgentlySDK/SSETransport.swift), [sdk/android/.../stream/SSETransport.kt](../sdk/android/src/main/java/com/viant/agentlysdk/stream/SSETransport.kt)) carry the new `datasource_cache_invalidated` event without change — just add a case in each platform's event enum ([Models.kt](../sdk/android/src/main/java/com/viant/agentlysdk/stream/Models.kt), [Streaming.swift](../sdk/ios/Sources/AgentlySDK/Streaming.swift)).

**Auth** — same story as web: the SDK already attaches the user's token to every HTTP call. The datasource endpoints sit behind the same middleware, so `ctx`-equivalent identity propagation is transparent.

### 14.2 Native picker UI

**Overlays remain server-side on mobile too.** Mobile clients receive the already-refined schema over the wire and render it — no overlay loading, no match-mode logic, no YAML parsing on the device. This keeps mobile and web symmetric and prevents drift.

Forge's [TextLookup.jsx](../../forge/src/packs/blueprint/TextLookup.jsx) is web-only. Mobile needs native equivalents that consume the **same dialog metadata** (`extension/forge/dialogs/*.yaml` — served by the backend as part of the schema response). The dialog YAML becomes a cross-platform description:

| Forge dialog field | iOS rendering | Android rendering |
|---|---|---|
| `view.type: table` | `List` (SwiftUI) with one row per record; columns become a custom row view | `LazyColumn` (Compose) with item composable; columns → row content |
| `view.columns[].header` | column header in section header | table-style first row |
| `quickFilter` | `searchable(text:)` modifier on the list | `OutlinedTextField` above the list |
| `footer.ok/cancel` | toolbar buttons (`Select` / `Cancel`) | `TopAppBar` actions |
| `view.type: tree` | `DisclosureGroup` (SwiftUI) with lazy children | `LazyColumn` with expand/collapse via `AnimatedVisibility` |

The picker's rows come from a single `fetchDatasource` call (identical on every platform). Inputs/Outputs mapping is a pure Swift/Kotlin function — port the defaults from [utils/lookup.js](../../forge/src/utils/lookup.js) (`:form → :query`, `:output → :form`, `location` defaults to `name`). The test fixture in §15 pins these defaults.

### 14.3 Named-token input (Activation b + c)

Every platform ships a `NamedLookupInput` equivalent:

| Platform | Component |
|---|---|
| Web | SolidJS/React text input with `/` trigger, token chips, authored `/name` parser on render. |
| iOS | SwiftUI `TextEditor` wrapped with a custom `AttributedString` layer: trigger detection, attachment runs for chips, tap-to-reopen via `.onTapGesture` dispatched per chip range. |
| Android | Compose `BasicTextField` with a custom `VisualTransformation` that maps stored `@{…}` tokens to chip spans; tap via `pointerInput` on the chip range. |

All three implement the same token grammar (`@{name:id "label"}`), the same registry call (`GET /v1/api/lookups/registry?context=…`), and the same send-time flattening. This is tested end-to-end by the [lookups-test.mjs](../lookups-test.mjs) script — mobile ports must pass the equivalent unit tests in Swift/Kotlin.

### 14.4 Stored-vs-sent roundtrip on mobile

The contract from §3.3.1 applies identically:

- Messages persisted to transcript keep the rich `@{…}` token — the **backend** stores/returns it, so all clients (web, iOS, Android) see the same serialized string.
- Chips rehydrate from the token on load; no re-fetch. Mobile especially benefits — no network hit on conversation list scroll.
- Send-time flatten happens in the shared frontend logic (`flatten()` in the test) — port to Swift + Kotlin as a pure function. Not server-side, because the server should receive the already-flattened payload (this is the one moment the rich form leaves the client).

### 14.5 On-device cache (phase 4, optional)

The server-side `service/datasource` cache (§6) is the source of truth. Mobile can add a local mirror later for flaky connectivity — keyed identically `(userId, dsId, inputsHash)`, same TTL honoured. Not required for v1; `fetchDatasource` goes over the wire every time, hitting the backend cache.

### 14.6 Tests per platform

Each mobile SDK needs tests mirroring the JS fixture:

- **iOS** — `swift test` target `AgentlySDKTests` adds `LookupsTests.swift`: cache behaviour (via URLProtocol mock), Inputs/Outputs mapping (pure function), token parse/serialize/flatten.
- **Android** — `gradle test` module adds `LookupsTest.kt` with the same coverage.

CI gates all three (JS/Swift/Kotlin) must pass on every release touching this feature.

---

## 15. Executable reference — `lookups-test.mjs`

[lookups-test.mjs](../lookups-test.mjs) is a single-file Node.js script (no dependencies) that simulates every moving part so the design contract is verifiable before any Go code is written:

**Datasource + cache (T1–T5, T11):**
1. Fetch miss → mock MCP call + forge `selectors.data` projection + in-memory cache.
2. Fetch hit → no additional MCP call.
3. `scope: user` cache isolation — alice vs bob get separate entries.
4. TTL expiry.
5. Explicit `Invalidate`.
11. Pinned args override caller-supplied args.

**Forge Inputs/Outputs (T6, T7, T10):**
6. Inputs default `:form → :query`.
7. Outputs default `:output → :form`, `location` defaults to `name`.
10. End-to-end round-trip (fetch → pick → outputs → form state).

**Named-token activation (T8, T9):**
8. Authored `/name` parse; required unresolved blocks submit.
9. Resolved `/name` → chip render → `modelForm` flatten.

**Stored-vs-sent roundtrip (T12–T17):**
12. Rich token `@{name:id "label"}` preserves id + label; `parse(serialize(x)) = x`.
13. Chip reopens picker — stored id feeds dialog as pre-selection input.
14. Changing selection rewrites id and label atomically (no stale label left behind).
15. Rehydration on conversation reload uses the stored label — **no re-fetch**.
16. Send-time flatten on forms — storage keeps companion labels, LLM payload drops them.
17. Send-time flatten on free-text named tokens — chips visible to user, id-only to LLM.

**Overlay match modes + composition (T18–T24):** each overlay's mode is evaluated in isolation (§5.0.1).
18. `mode: partial` — library overlays attach to matching fields only.
19. `mode: strict` — whole overlay discarded if any binding is unmatched.
20. Strict+high-priority overlay overrides partial library overlays; untouched fields keep library lookup.
21. `mode: threshold` satisfied (≥N bindings match) → all matched fields attached.
22. Threshold unsatisfied → whole overlay discarded.
23. Per-overlay mode isolation — partial keeps its hits even when a threshold-peer is discarded.
24. `M` tiny single-binding overlays × schema → exactly the overlapping attachments, no noise (the `1-of-N × M` case).

Run:

```bash
node lookups-test.mjs
```

All 24 checks pass. The script doubles as a precise spec for anyone implementing the Go side — each test names the behaviour it pins down.

---

## 16. First viable use case (illustrative — not part of core)

Bring-up happens entirely in workspace YAML under `extension/forge/`. For steward_ai we'll add:

- `extension/forge/datasources/{advertiser,campaign,site_list,targeting_feature}.yaml`
- `extension/forge/dialogs/{…}Picker.yaml` (pure forge, one per lookup)
- `extension/forge/lookups/{scenario-or-template-name}.yaml` (overlays for the templates/scenarios currently using dummy IDs)

100% YAML, zero Go. A second workspace in an unrelated domain (CRM, SRE, ticketing) would add different YAML under the same `extension/forge/` tree and get identical UI behavior — that's the test that core stayed generic.
