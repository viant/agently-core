# Workspace-owned CSS and themes for Agently and Forge

Status: design proposal; no runtime changes implemented. Source inspection: 2026-09-11.

## Recommendation

Yes: a workspace should ship its own CSS alongside its Forge windows, models, dialogs, and actions. Forge already accepts named classes and inline style objects. What is missing is an asset delivery and lifecycle contract, plus consistent styling targets across renderers.

Add an optional `extension/forge/styles/manifest.yaml` with shared CSS files, named themes, token values, and light/dark variants. Agently-core validates and serves them as one versioned CSS bundle; the Agently web host loads it after its application styles and establishes a workspace scope around Forge surfaces and their portals. Retain existing `className`, `style`, and `properties` semantics. Add stable Forge tokens and component parts incrementally so ordinary appearance changes stop requiring changes in Forge.

The smallest useful release needs one server bundle endpoint, an additive workspace metadata field, a shared host loader and theme selector, portal propagation, a small input/button token contract, and a fix for generic container class forwarding. It does not need a new widget framework, a workspace npm build, CSS-in-JS, or a rewrite of existing Forge CSS.

**Release prerequisite:** standardize the public styling targets for the components advertised as theme-supported before shipping named themes. This is a bounded compatibility task, not a repository-wide class rename. Asset delivery can be developed in parallel; theme readiness requires the explicit gate below.

## Current architecture and ownership

| Layer | Current responsibility | Styling consequence |
| --- | --- | --- |
| Workspace | Agent definitions, workflows, tools, and `extension/forge/{windows,models,dialogs,datasources,lookups}` | Presentation metadata is already workspace-owned; CSS files have no equivalent explicit loading contract. |
| Agently-core | Resolves workspace resources and effective Forge windows; exposes HTTP and MCP UI resources | Correct owner of style asset discovery, validation, identity, and delivery. |
| Forge | Metadata-driven React rendering, widget registry, framework packs, layout, tables, dialogs, dashboards | Correct owner of stable styling hooks and defaults. Should not know filesystem workspace paths. |
| Agently web UI | Boots Forge, loads Blueprint and shell CSS, hosts windows and MCP renderer pages | Correct owner of stylesheet attachment, removal, scope boundaries, and document lifecycle. |
| Blueprint | Concrete input, button, dialog, and other implementations used by Forge and the host | Vendor DOM/classes remain relevant today, but should be hidden behind Forge hooks where practical. |

Workspace ownership of business behavior is substantial, but not absolute: generic behavior remains in Forge and the host. Styling should follow that same split: workspace appearance choices, framework rendering mechanics.

The active workspace is currently process-oriented: `workspace.Root()` resolves an explicit root, `AGENTLY_WORKSPACE`, or the working directory's `.agently`. This proposal does not invent per-request tenant selection from an arbitrary client-supplied workspace path.

Sources: [workspace kinds/root](workspace/workspace.go), [canonical window loader](service/ui/window/loader.go), [HTTP Forge handler](adapter/http/ui/handler.go), [workspace metadata](service/workspace/metadata.go).

## What styling already works

### Metadata to widget DOM

`ControlRenderer` delegates to `WidgetRenderer`. The latter classifies the item, selects a registered widget, merges properties and dynamic properties/events, then uses `ControlWrapper`.

| Input | Actual behavior | Limitation |
| --- | --- | --- |
| `item.className` | Applied to `.forge-control-wrapper`; forwarded to widget if its computed props do not already define `className` | A class can appear on both wrapper and widget. It is not a unique DOM target. |
| `item.style` | Merged after wrapper `gridColumn`; forwarded to widget if computed props have no `style` | Layout and visual properties can affect two different elements. |
| `item.properties.className` / `.style` | Flow into widget props; top-level fallback does not replace an already defined prop | Generally a more specific widget-level escape hatch; adapters can still handle props differently. |
| `wrapper: none` | Bypasses ordinary wrapper; validation can add a separate shell | Wrapper class/anchor targeting is not universal. |
| `container.style` | Used by several layout branches and specialized renderers | Some wrappers impose their own inline sizing; behavior varies by branch. |
| `container.className` | Present in the Go model | Generic `Container.jsx` does not consistently forward it to a container root. A typed field alone does not prove rendering support. |
| Table formatting | Cell `style` and `className` are documented, including built-in tone classes | This is cell formatting, not a global stylesheet loader. |

The Blueprint text adapter renders `<InputGroup {...rest} ... />`, so it already accepts forwarded style/class props. Numeric, date, selection, button, and composite widgets have their own prop and DOM handling. Do not promise that `style` always lands on the native `<input>`.

Go's `StyleProperties` is a typed set of sizing, spacing, typography, layout, border, background, positioning, and animation properties. It is not an arbitrary CSS declaration map. Other metadata locations use maps. In particular, do not assume a top-level `style: {"--custom-token": ...}` survives typed YAML → Go → JSON decoding. External CSS avoids that limitation.

Sources: [WidgetRenderer](../forge/src/runtime/WidgetRenderer.jsx), [ControlWrapper](../forge/src/runtime/ControlWrapper.jsx), [Blueprint pack](../forge/src/packs/blueprint/index.jsx), [Container](../forge/src/components/Container.jsx), [container anchors](../forge/src/components/containerAnchor.js), [Go model](../forge/backend/types/model.go), [table formatting](../forge/doc/table-formatting.md).

### CSS, inline defaults, and Blueprint overlap

The host's [index.css](../agently/ui/src/index.css) imports Blueprint core, icons, and table styles, Forge table CSS, then host styles. Forge components also import their own CSS. Actual final order must be checked in the built application; the index file is not a complete ordering contract for all component imports.

[shell.css](../agently/ui/src/styles/shell.css) defines `--app-bg`, `--app-surface`, `--app-border`, `--app-text`, `--app-accent`, and other variables. It also targets `.bp6-*` and `.forge-*` selectors directly and uses `!important` in places. Forge likewise mixes CSS files, inline literal colors/sizing, and some custom properties. There is no single comprehensive Forge token system in the inspected rendering path.

[Container.css](../forge/src/components/Container.css) includes Blueprint checkbox/radio compatibility rules and selectors spanning `bp4`, `bp5`, and `bp6`. The two package manifests declare different Blueprint version ranges, including alpha ranges. These are declared ranges, not proof of the exact deployed version. Compatibility fixes belong in Forge or dependency alignment, rather than being copied into every workspace theme.

Forge follows a framework-pack methodology: widget classification and binding are separate from the Blueprint implementation. [widget-runtime.md](../forge/doc/widget-runtime.md) describes replacement through registration, including another framework such as MUI. That extension mechanism is not a stylesheet system, and replacing a widget is unnecessary for a color, border, or density change. The separation is also incomplete outside this runtime: containers and specialized UI still use Blueprint directly.

Blueprint is therefore the underlying component library, not a competing workspace framework. Workspace CSS should extend Forge's metadata methodology while accepting a documented Blueprint-specific escape hatch during migration.

### Missing delivery path

The canonical window loader resolves structured/flat YAML, JavaScript actions and action references, and merges workspace Forge assets. It does not discover a neighboring `.css` file. The filesystem store's generic resource listing/loading is YAML-oriented; adding CSS to `AllKinds()` alone would not make raw asset loading work.

Workspace metadata exposes branding and versions, but no stylesheet descriptor. The host publishes a metadata snapshot through [workspaceMetadata.js](../agently/ui/src/services/workspaceMetadata.js), called by [chatService.js](../agently/ui/src/services/chatService.js). This is a suitable integration point, not an existing CSS loader.

MCP UI adds a second document lifecycle. [workspace resource publication](protocol/ui/resource/workspace.go) returns a structural HTML fallback and an authenticated Forge renderer URL. [MCPUIForgeWindowPage](../agently/ui/src/components/mcpApps/MCPUIForgeWindowPage.jsx) fetches the window and renders `WindowContent` independently. Parent-page CSS does not cross the iframe boundary. The fallback [HTML template](protocol/ui/resource/workspace_forge.html.tmpl) is a summary, not a styled equivalent of Forge.

## Proposed workspace contract

All fields and endpoints below are new unless explicitly described above as existing.

```text
<workspace>/extension/forge/
  styles/
    manifest.yaml
    theme.css
    forms.css
    orders.css
  windows/
    order.yaml
```

```yaml
# extension/forge/styles/manifest.yaml
version: 1
files:
  - theme.css
  - forms.css
  - orders.css
```

The manifest is optional and conventionally located: no second enablement setting is needed. Paths are relative to `styles/`; list order is cascade order. Duplicate entries are invalid. Only explicitly listed files are included. No directory globbing, remote stylesheets, JavaScript injection, or automatic per-window CSS discovery.

For v1, all shared styles and declared theme variants load once per document. Theme-specific declarations are gated by the active theme/mode attributes described below. Per-window rules target `data-forge-window-key`; do not fetch CSS every time a window opens. A future large-workspace optimization can add per-window bundles without changing item metadata.

### Authoring example

Existing item metadata, shown as a fragment of a window's content:

```yaml
items:
  - id: customerName
    type: text
    label: Customer name
    className: customer-name
    properties:
      placeholder: Enter a customer name
```

After the proposed loader and scope boundary exist, this CSS uses the current Blueprint implementation:

```css
/* forms.css: wrapper-qualified because className can be on two nodes. */
.agently-workspace .forge-control-wrapper.customer-name .bp6-input {
  border-radius: 8px;
  min-height: 36px;
  background: #fffdf5;
}

.agently-workspace .forge-control-wrapper.customer-name .bp6-input:focus {
  box-shadow: 0 0 0 2px #4466cc;
}

/* New host boundary attribute; applies to this logical window only. */
.agently-workspace[data-forge-window-key="order"] .forge-control-wrapper {
  margin-block-end: 6px;
}
```

`theme.css` can define the proposed Forge tokens:

```css
.agently-workspace {
  --forge-control-radius: 8px;
  --forge-control-height: 36px;
  --forge-control-bg: #fffdf5;
  --forge-focus-color: #4466cc;
}
```

Those `--forge-*` names require Forge to consume them; merely defining a variable does not change a component. Existing `--app-*` variables can influence descendants that already consume them, but loading this bundle does not automatically theme the application shell. Keep shell branding as an explicit later scope, with a separate selector/contract if requested.

## Named themes: first-release contract

A theme is a named set of semantic tokens plus optional CSS overrides. A mode is its `light` or `dark` variant. `system` is a user preference that resolves to one of those modes, never a third palette. Names such as `branded` and `compact` identify complete themes; v1 does not compose independent theme and density packs or support theme inheritance.

CSS-only manifests above remain valid. The same manifest can optionally declare themes; there is no second discovery mechanism or separate theme loader.

### Manifest example

```yaml
version: 1
files:                         # Shared rules, active for every theme
  - forms.css
  - orders.css
defaultTheme: branded
defaultMode: system             # light | dark | system
themes:
  - id: branded
    label: Branded
    fallbackMode: light
    tokens:                    # Shared by this theme's variants
      --forge-font-family: '"Inter", system-ui, sans-serif'
      --forge-font-size: 14px
      --forge-control-height: 36px
      --forge-control-radius: 8px
      --forge-control-padding-inline: 10px
    files:
      - themes/branded.css
    modes:
      light:
        tokens:
          --forge-surface: '#ffffff'
          --forge-text: '#171b26'
          --forge-control-bg: '#fffdf5'
          --forge-control-text: '#171b26'
          --forge-control-border: '#788397'
          --forge-focus-color: '#4466cc'
          --forge-button-bg: '#3453b3'
          --forge-button-text: '#ffffff'
      dark:
        tokens:
          --forge-surface: '#1b2230'
          --forge-text: '#edf1f7'
          --forge-control-bg: '#242d3d'
          --forge-control-text: '#edf1f7'
          --forge-control-border: '#8491a6'
          --forge-focus-color: '#9db5ff'
          --forge-button-bg: '#a9bfff'
          --forge-button-text: '#172447'
        files:
          - themes/branded-dark.css
  - id: compact
    label: Compact
    fallbackMode: light
    tokens:
      --forge-control-height: 30px
      --forge-control-radius: 4px
      --forge-control-padding-inline: 6px
    modes:
      light: {}                # Inherit Forge's built-in light token defaults
```

Theme IDs must be unique lowercase identifiers matching `[a-z][a-z0-9-]{0,63}`. Reserve `forge-default` for the framework fallback; it cannot be redefined by a workspace. Require at least one declared mode, a supported `fallbackMode`, and a `defaultTheme` referencing a declared theme whenever `themes` is nonempty. `defaultMode` defaults to `system`. Labels are plain display text.

Each `files` list is ordered and paths remain relative to `styles/`. Reject duplicate references within a list and overlapping references along a single shared/theme/mode chain. Reuse in different themes is allowed, but authors must scope that shared file correctly for each intended theme. Validate the entire manifest before activating any new revision.

### Token ownership and resolution

Forge owns the token vocabulary and default palettes. Agently-core serializes valid token declarations; the host selects a theme/mode; Blueprint adapters consume those variables. Do not introduce a second copy of Blueprint's complete theming API in workspace YAML.

| First-release tokens | Consumer and purpose |
| --- | --- |
| `--forge-font-family`, `--forge-font-size` | Typography on themed Forge surfaces and supported controls |
| `--forge-surface`, `--forge-text` | Themed surface background and foreground |
| `--forge-control-height`, `--forge-control-radius`, `--forge-control-padding-inline` | Text/numeric inputs and buttons; textarea uses minimum sizing rather than a fixed single-line height |
| `--forge-control-bg`, `--forge-control-text`, `--forge-control-border` | Supported input appearance |
| `--forge-focus-color` | Visible keyboard focus indicator |
| `--forge-button-bg`, `--forge-button-text` | Ordinary enabled button appearance; semantic intent states retain their own styles |
| `--forge-disabled-bg`, `--forge-disabled-text`, `--forge-invalid-border` | Disabled and validation states; framework supplies mode-appropriate defaults |

For each selected mode, resolve a complete token map in this order: Forge built-in mode defaults → theme tokens → mode tokens. Unspecified values inherit defaults, never values left over from the previous selection. Generate CSS declarations rather than putting theme tokens through typed item `StyleProperties` or applying them as inline styles.

Validate known token names and property-appropriate string values before CSS serialization. Use a CSS parser to reject declaration/rule injection and unsupported resource references; reject unknown tokens with a diagnostic. CSS files remain the extension point for workspace-specific variables. Reject token dependency cycles if `var()` references are supported; a first implementation may restrict manifest values to literal colors, lengths, and font lists. The example uses only literal values. A listed font is not downloaded automatically.

Both built-in palettes and token consumption for the listed input/button targets ship in phase 1. They do not imply complete dark-mode support for all Forge dashboards or third-party widgets. Publish a coverage list with the feature. Validate actual contrast/focus and disabled/invalid visibility rather than assuming the example palette proves accessibility.

### Compilation, CSS scoping, and precedence

Keep a single bundle containing all themes for v1. Generate fully resolved token rules using selectors such as:

```css
.agently-workspace[data-forge-theme="branded"][data-forge-color-mode="dark"] {
  --forge-control-radius: 8px;
  --forge-control-bg: #242d3d;
  /* Remaining resolved tokens omitted in this illustration. */
}
```

Compile in deterministic order: shared files; then themes sorted by ID, with each theme's generated light/dark token rules, theme files, and mode files (light then dark). Preserve each file list's order. Sorting theme definitions must not decide which theme wins: only the selected theme's selectors should match.

Shared CSS must use `.agently-workspace`; theme CSS must additionally match its `data-forge-theme`; mode CSS must also match `data-forge-color-mode`. Listing a file under a theme does not automatically scope raw CSS. The parser linter must report missing scope; v1 retains the trusted-author contract below rather than promising automatic isolation. Do not use unqualified `body`, `:root`, or `.bp6-dark` rules for theme overrides.

```css
/* themes/branded-dark.css */
.agently-workspace[data-forge-theme="branded"][data-forge-color-mode="dark"]
  .forge-control-wrapper.customer-name .bp6-input {
  background: var(--forge-control-bg);
}
```

Within an identical selector priority, later theme/mode files can override generated token values. Shared rules should consume tokens instead of redefining them. Normal CSS specificity still applies, as do explicit item inline styles. The earlier CSS-only example's hardcoded light background illustrates the escape hatch; replace it with token consumption when adopting variants.

### Selection, persistence, and fallback

Add an Appearance control in Agently's existing settings surface with Theme and Color mode selections. Show only declared themes plus `Forge default`; show Light, Dark, and System, disabling an explicit unsupported mode for the selected theme. Hide workspace theme selection when no themes are declared. `Forge default` clears named-theme attributes and uses existing default appearance; it does not disable shared workspace CSS. A complete customization reset requires removing the manifest.

On first load, select the workspace default. A valid saved user theme overrides it. A valid saved mode preference overrides `defaultMode`. If the saved theme was deleted, discard its saved theme/mode pair and return to workspace defaults. Resolve `system` from `prefers-color-scheme`; if that mode is unsupported, use the theme's `fallbackMode` and indicate the effective mode in settings. For a theme with only light mode, a dark OS preference must still render light.

Store `{themeId, modePreference}` locally under a versioned key scoped by authenticated account and an opaque stable workspace ID. Add that ID to private workspace metadata; do not use the stylesheet revision (changes on edits), filesystem path, or window ID as the storage key. Server configuration should allow preserving the ID when moving a workspace. If a stable identity is unavailable or browser storage is disabled, use session memory rather than writing an ambiguous persistent key. Account-wide preference synchronization is deferred.

A selection updates the shared provider's `themeId`, requested preference, and effective mode. Apply `data-forge-theme`, `data-forge-color-mode`, and the matching CSS `color-scheme` to every window boundary and associated portal mount in the same render cycle. Explicit modes ignore OS changes; System subscribes to OS changes and cleans up that subscription. Do not remount windows or reset form values, selections, scroll, or datasource state when changing appearance.

Because every variant is already in the bundle, changing theme/mode needs no request, rebuild, or new asset revision. Load/validate the bundle before committing a selection that depends on it. When a new manifest revision arrives, reconcile selection against its catalog and replace stylesheet/catalog/attributes together; retain the old snapshot on a same-workspace load failure.

### Blueprint, shell, and embedded synchronization

For dark mode, the Blueprint adapter applies its supported dark-theme class to the same scope and portal boundaries, alongside Forge tokens. Keep the vendor class inside the adapter; workspace authors select `dark`, not a Blueprint class name. Confirm behavior with the installed Blueprint version, including nested overlays and date/select components; adding a class is not proof of full coverage.

The first release themes Forge surfaces. Agently's surrounding navigation/chat shell retains its current styling. A future shell bridge can map `--forge-surface`, `--forge-text`, and additional semantic tokens to `--app-*` on an explicit shell scope; raw Forge CSS must not silently become global shell styling. Native iOS/Android and print/export are separate renderers and do not consume this browser CSS contract automatically.

The authenticated MCP Forge page loads the same catalog/bundle. When embedded, the parent sends its current selection and subsequent changes through a small theme message on the existing guest bridge; validate the exact origin, source window, workspace identity, and catalog revision. The guest validates theme IDs/modes against its own catalog, requests a metadata refresh on revision mismatch, and ignores stale messages. Theme messages carry IDs/preferences, never raw CSS or arbitrary asset URLs. Apply the parent's effective mode to match the parent precisely; a directly opened renderer resolves the local preference itself. Use storage events for other same-origin tabs and an in-document provider notification for immediate local updates. Inbound synchronization must not echo messages or write new preferences in a loop.

## Backend design: agently-core

Add a small style asset service, separate from YAML resource decoding, under a proposed `service/ui/style/` package. Use the effective workspace root and the same authentication boundary as private workspace/window reads.

1. Read the manifest and exact referenced CSS bytes. Missing manifest means no customization.
2. Validate schema version, relative paths, duplicate entries, file type, UTF-8, and size limits. Suggested initial limits: 32 unique CSS files and 512 KiB of emitted CSS, including all generated theme variants. Reject absolute paths, URL paths, traversal, and symlinks escaping the resolved styles directory. Do not expose a generic filesystem route.
3. Compile shared CSS and all theme variants in the order defined above, with explicit file separators. Compute a revision over canonical manifest/catalog data, ordered source names and bytes, generated CSS, and the token/compiler contract version. Theme labels/defaults and token-only edits must invalidate the revision too. Publish the bundle and catalog only after the entire snapshot succeeds.
4. Add an optional `uiStyles` descriptor to `MetadataResponse`, and include its revision in metadata version computation.
5. Serve the exact immutable snapshot through the proposed authenticated endpoint below. A request for an unavailable revision must fail, never return newer bytes under the old revision.

```json
{
  "uiStyles": {
    "version": 1,
    "revision": "<sha256>",
    "href": "/v1/workspace/ui/styles/<sha256>.css",
    "defaultTheme": "branded",
    "defaultMode": "system",
    "themes": [
      {"id": "branded", "label": "Branded", "modes": ["light", "dark"], "fallbackMode": "light"},
      {"id": "compact", "label": "Compact", "modes": ["light"], "fallbackMode": "light"}
    ]
  }
}
```

For CSS-only manifests, omit theme defaults and return an empty theme catalog. Add a separate opaque stable `workspaceId` to private metadata for preference scoping; the shared asset revision is not a user preference or identity. The bundle endpoint never varies bytes by the requesting user's selected theme.

Respond with `Content-Type: text/css; charset=utf-8`, `X-Content-Type-Options: nosniff`, an ETag, and private caching with revalidation. Keep a bounded cache of compiled snapshots to cover metadata/bundle request races. Authorization still applies on cache hits and conditional requests. Never let a shared HTTP cache expose another workspace's assets.

Manifest deletion removes `uiStyles` from metadata. An invalid replacement retains the last valid bundle for the same workspace and reports a diagnostic; on first load it uses default styling. Do not convert a styling failure into an unavailable agent/window service. Log path-relative diagnostics without returning filesystem contents.

Initial freshness contract: metadata refresh/page reload reevaluates style bytes and produces a new revision. Do not claim existing model/agent hot-swap automatically watches CSS. Add file watching or a refresh notification later if editing without reload is needed. Avoid timestamp-only revision detection.

## Host design: Agently web

Implement one reusable workspace style manager, invoked by both normal application bootstrap and the standalone MCP Forge page. It consumes the descriptor rather than knowing workspace paths.

- Insert a managed `<link rel="stylesheet">` after base/application styles. Use a stable insertion anchor; keep later lazy component CSS ahead of this anchor or reestablish order when it is inserted.
- Deduplicate by document, workspace identity, and revision. Track load/error completion; reference count if multiple providers share one document.
- For the same workspace, load the replacement before removing the previous link. On failure, retain the previous revision and expose a non-blocking diagnostic.
- On workspace/session change, remove old styles before displaying new workspace content. Cancel stale requests/load callbacks; never retain the previous workspace's theme as a fallback. Remove styles on logout/unmount.
- For initial loading, a bounded loading state can avoid an unstyled flash. Failure falls back to base styles, with the UI still usable.
- Read through the normal authenticated metadata client in both entry points. Same-origin links work with cookies. If a deployment requires bearer headers, fetch with the configured client and attach the bytes through a CSP-compatible nonce-bearing style element; do not assume link requests carry arbitrary authorization headers.

Use a shared Forge-surface boundary in the host integrations for `WindowManager`, `ConversationWorkspaceSurface`, and `MCPUIForgeWindowPage`. Each logical window root gets `.agently-workspace` and `data-forge-window-key="<logical key>"`, plus the selected theme/mode attributes when applicable. Avoid redundant nested theme boundaries. Identity is the window definition key, not a random instance ID.

This v1 supports the current single active workspace per document. Simultaneous different workspaces in the same document require identity-specific selector compilation or separate iframe documents; a common `.agently-workspace` prefix does not isolate those themes.

### Portals and MCP UI

Blueprint overlays may render outside the window root. Descendant selectors and inherited tokens then stop applying. The installed Blueprint typings expose `PortalProvider` with `portalContainer` and `portalClassName`; Forge's `ViewDialog` already forwards a dialog-specific `portalClassName`.

Provide a workspace/window-associated portal mount outside clipped/scrolled window content, carrying the same scope class, logical window key, selected theme/mode attributes, and theme token rules. Supply that mount through the Blueprint pack's portal context, preserving explicit component portal behavior and caller classes. Do not solve this by placing all overlays under an `overflow: hidden` container. Test direct `portalContainer` overrides and nested dialogs; provider defaults alone do not guarantee coverage.

Forge should accept a generic styling/portal context from its host so standalone Forge consumers are not coupled to Agently. The pack bridges that context to Blueprint. Another pack would supply its own bridge.

Inside MCP iframe documents, run the same loader and create the same scope/portal roots. Workspace CSS applies only to the authenticated Forge renderer, not arbitrary third-party MCP content. Keep the structural fallback readable without CSS in v1.

User theme selection does not change the shared MCP resource identity; synchronize it through the guest bridge. The current resource content hash is derived from the fallback HTML. Extend workspace resource revision input to include the style revision, and represent that revision in the assembled fallback HTML (for example, an escaped metadata value) before hashing it. This preserves a content-derived hash while making the renderer URL change on CSS edits. Parent resource caches still need their normal refetch/invalidation trigger; changing the hash does not itself push an update to an already open iframe.

## Prerequisite: standardize Forge's public styling contract

**Named themes require stable, consistently rendered styling targets.** Supporting `className` in a Go type, or loading a CSS file successfully, does not satisfy this prerequisite. A workspace author must know which node receives a class, which selectors are supported across releases, and which visual properties remain controlled by inline styles.

Do not block the asset endpoint or loader on a complete Forge cleanup. Implement those in parallel. Block the phase-1 theme release on the contract and proof for its declared coverage: text/password, numeric, textarea, and button controls, their labels/validation shells, the container branches hosting them, and host/portal boundaries. Select/date controls and complex dashboards can remain compatibility-only until their own coverage passes.

### Why the current classes are not yet a sufficient contract

The source findings above expose four distinct issues:

- **Propagation:** `container.className` exists in metadata but is not consistently forwarded by the generic renderer. Workspace CSS cannot match a class that never reaches the DOM.
- **Target ambiguity:** `item.className` and `item.style` can reach both the wrapper and the widget. A selector intended to size an input may also size its layout wrapper.
- **Implementation coupling:** `.bp6-*` names identify Blueprint internals. A vendor upgrade or a different widget pack can change them independently of the workspace's logical control.
- **Boundary gaps:** wrapper bypass and portaled content do not necessarily retain the same anchors, inherited tokens, or ancestor chain. A rule that works in a form can fail in a dropdown or embedded window.

Renaming existing classes alone would not fix these issues. Standardize the rendered targets and propagation rules first, then migrate cosmetic defaults to tokens.

### Public naming and target rules

Use Forge-owned semantic attributes as the primary public targeting API. Retain established classes for compatibility and use classes for author-defined groups. Do not publish every existing `forge-*` class as stable merely because it has that prefix.

| Surface | Required contract | Ownership |
| --- | --- | --- |
| Workspace/window boundary | `.agently-workspace`, logical `data-forge-window-key`, active theme/mode attributes | Agently host; generic context supplied to Forge |
| Container root | Existing `data-forge-container-id` plus merged authored `container.className` | Forge container renderer |
| Control identity | Existing `data-forge-control-id`; identity is local to its window, not globally unique | Forge runtime and adapter |
| Widget root | `data-forge-widget` with the resolved logical widget key, such as `text`; must not encode Blueprint's DOM structure | Forge runtime supplies the key; pack places the attribute |
| Internal styling targets | `data-forge-part` on the actual relevant node: `input`, `label`, `button`, `trigger`, `menu`, or `validation-message` | Each widget pack/renderer |
| State | Native `:disabled`, `:read-only`, `:focus-visible`, and applicable ARIA attributes; optional mirrored state on a composite root | Runtime and pack; CSS observes actual state |
| Authored class | Preserve `className` exactly as user-defined class tokens and merge with framework classes | Renderer at each existing receiving node |

New framework-owned classes should use a `forge-` prefix and a documented semantic name. New host classes remain host-owned; workspace authors should use a distinct prefix such as `ws-` for new classes to reduce collisions. Do not require renaming existing workspace classes such as `customer-name`.

`data-forge-part` identifies a role within a widget; the same role may occur multiple times in a composite widget. Document cardinality, placement, and any qualifiers when publishing that component's contract. Do not promise arbitrary child order, wrapper depth, generated IDs, or vendor class names. Use an actual `<label>` with its existing association where applicable; styling attributes must not replace label or ARIA semantics.

### Preserve metadata semantics; fix forwarding deliberately

Retain the existing `item.className` and `item.style` behavior on wrapper and widget. Retain widget-property precedence when `item.properties` or computed props already supply these values. Do not silently reinterpret top-level fields as wrapper-only or input-only. Document the current receiving nodes for every covered adapter.

Forward `container.className` to the existing logical container root in each supported rendering branch, alongside its ID anchor. Merge with framework classes rather than replace them. Do not add a universal outer wrapper: flex/grid sizing, scroll ownership, and child selectors depend on the existing DOM. Where a branch has no single root, identify and document the appropriate target or mark that branch unsupported until resolved.

With `wrapper: none`, put the logical widget/control identity on the adapter's existing root and keep parts on the relevant native nodes. The normal wrapped path can retain the wrapper control ID. Selectors should work with either a widget root that is itself the part or a descendant part; avoid adding a wrapper solely for CSS. Validation shells must preserve their own behavior and associations.

For example, after the proposed attributes are implemented:

```css
/* Authored input sizing, independent of Blueprint class names and wrapper depth. */
.agently-workspace [data-forge-widget="text"][data-forge-control-id="customerName"][data-forge-part="input"],
.agently-workspace [data-forge-control-id="customerName"] [data-forge-part="input"] {
  border-radius: var(--forge-control-radius);
}
```

The first selector supports an unwrapped native input carrying both identity and part. The second supports a composite/wrapped control. For repeated control IDs in different windows, qualify the workspace boundary with `data-forge-window-key`. Portal menus must carry their associated control identity on a portal-local root, because the original control wrapper is no longer an ancestor.

### Tokens and inline styles are part of the prerequisite

Class standardization alone cannot make themes effective when an adapter sets decorative colors or borders inline. For the phase-1 targets, move framework-owned decorative defaults to stylesheet rules consuming the token table above, with fallbacks matching existing appearance. Keep genuinely dynamic geometry and explicitly authored inline styles.

Inventory existing `!important` rules and vendor-specific compatibility rules for these targets. Record which ones are still necessary and test their interaction with tokens. Do not solve the migration by adding blanket important rules or moving all styling into a new cascade layer. Token-only customization of covered properties must work without workspace `!important` overrides.

Keep disabled/read-only/invalid/focus states authoritative. If composite root state attributes are added, derive them from the same resolved runtime state as the native control; do not let metadata CSS independently redefine behavioral state. Use platform focus pseudo-classes where possible rather than introducing a second JavaScript focus state.

### Readiness ledger and release gate

Maintain a checked-in styling coverage ledger in Forge as part of implementation. Each row should name the renderer/pack, metadata-to-DOM mapping, public selectors/parts, supported tokens/states, portal behavior, and linked proof. The table below defines the initial required scope; it is a plan, not a claim that checks have passed.

| Component family | Required before phase-1 theme release | Follow-up scope |
| --- | --- | --- |
| Text/password, numeric, textarea, button | Class/style receiving-node inventory; part placement; token consumption; focus, disabled/read-only where applicable, and invalid-state proof | Additional appearance variants and custom packs |
| Labels and validation shells | Stable label/message parts and preserved associations; wrapper bypass covered | Specialized composite field layouts |
| Generic containers used by those controls | Class/anchor forwarding proven for every supported branch; no layout/scroll regression | Remaining specialized container renderers |
| Workspace roots and portal mounts | Matching theme/mode/key and control association where relevant; token inheritance and overlay positioning | Further third-party portal implementations |
| MCP Forge renderer | Same styling targets and selected theme as normal hosted windows | Non-Forge MCP content remains outside the contract |
| Select/date, tables, dashboards, charts | Document as compatibility-only; smoke-check that the theme infrastructure does not break them | Full stable-part/token coverage before claiming theme support |

Release gates:

- [ ] Inventory current class and inline-style handling for every first-release renderer; distinguish public contracts from implementation details.
- [ ] Preserve authored class tokens, framework classes, and existing style/property precedence; verify typed YAML → Go → JSON → rendered DOM behavior.
- [ ] Fix supported container branches and cover normal wrappers, wrapper bypass, and validation wrappers.
- [ ] Place public parts on actual nodes in every covered pack; publish supported states and any repeated-part rules.
- [ ] Prove input/button token changes in both modes using computed styles, including focus/disabled/invalid states and default appearance without a manifest.
- [ ] Prove matching scope and selection across window roots, portal mounts, and the MCP iframe without clipping, focus, or form-state regressions.
- [ ] Verify a workspace example changes a covered control using only Forge parts/tokens, without `.bp6-*`, DOM-depth selectors, or `!important`.
- [ ] Publish the coverage ledger and migration notes; do not label compatibility-only components as fully theme-supported.

### Compatibility and ownership of follow-up work

Introduce public hooks additively. Preserve existing Forge and Blueprint classes and legacy workspace selectors during phase 1. A future removal or semantic change to a documented hook requires explicit migration notes and a compatibility/deprecation window; an internal DOM refactor must preserve the hook's logical target.

Forge owns runtime/pack hooks, decorative defaults, and renderer proof. Agently owns boundary propagation, selection, portal integration, and iframe parity. Agently-core owns token/schema validation and metadata round-trip proof. Backend CSS delivery must not try to compensate for missing frontend targets by rewriting vendor selectors.

Global class renaming, a complete BEM conversion, removal of all inline styles, full Blueprint replacement, shell theming, and complete dashboard/print/native theming are not prerequisites. Apply the same readiness gate incrementally as each additional component family becomes theme-supported.

## Cascade and override policy

Loading last helps only when cascade priority and specificity allow it. Ordinary stylesheet declarations do not defeat normal inline declarations; `!important` changes precedence. New workspace authors should prefer tokens and named classes, retaining metadata `style` for explicit instance layout or an intentional inline override. See [CSS specificity](https://developer.mozilla.org/en-US/docs/Web/CSS/Guides/Cascade/Specificity).

Keep v1 bundles unlayered, matching the current application. Wrapping only workspace CSS in `@layer workspace` would put its normal declarations below existing unlayered normal rules. A later coordinated migration can place vendor, Forge, host, and workspace styles in ordered layers, but must audit existing important declarations because important layer order reverses. See [CSS cascade layers](https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/At-rules/@layer).

Prefer no new `!important` rules. Permit a documented, narrow compatibility exception where existing defaults require it; that exception is not a promise that arbitrary CSS overrides every property. Framework tokens/classes should remove the recurring need for those exceptions.

## Trust and supported CSS

Treat workspace CSS as trusted application customization authored by the workspace maintainer. Agent output, chat messages, attachments, and MCP responses must not become stylesheet sources automatically. Styling can hide controls or generate network requests; it cannot serve as an authorization mechanism.

For the minimal release, require authors to scope selectors under `.agently-workspace`. This is an authoring contract, not a security sandbox: raw CSS is still document-wide and can intentionally violate the convention. Offer validation/lint diagnostics using a CSS parser, not regex-based prefix rewriting. Untrusted theme installation would require a separately designed parser-enforced subset or document isolation; it is outside this release.

Ship self-contained CSS initially. Do not support `@import` or relative `url(...)` assets: relative URLs would resolve against the served bundle URL, not the workspace file. Validate these restrictions with a CSS parser before publishing the bundle. A later manifest revision may list images/fonts explicitly, serve them under authenticated content-addressed URLs, and rewrite references. The bundle must work without changing the host CSP to allow arbitrary external origins.

## Alternatives and tradeoffs

| Approach | Assessment |
| --- | --- |
| More inline style metadata | Already useful for single instances; poor for pseudo-classes, media queries, reusable styling, and typed custom properties. |
| Edit Forge or host CSS for each workspace | Continues coupling business appearance to framework releases. |
| Inject CSS through workspace JavaScript actions | Mixes lifecycle, styling, and behavior; encourages duplicated tags and stale styles. |
| Replace Blueprint widgets | Appropriate for behavior/structure changes, excessive for appearance changes. |
| Full theme-token system first | Desirable eventually, but delays a useful delivery path and still needs escape hatches. |
| Scoped workspace bundle plus gradual tokens | Recommended: small initial integration, existing metadata preserved, better portability over time. |
| Shadow DOM for every Forge window | Stronger encapsulation but substantial portal, inherited-style, and third-party integration work. Reserve for an isolation requirement. |

## Delivery plan and acceptance criteria

### Phase 1: ship workspace CSS and named themes

**Entry work and release gate:** complete the styling standardization checklist above for the declared first-release coverage. Asset service and host-loader development may run in parallel, but successful CSS loading alone is not acceptance for named themes.

Agently-core adds the manifest/theme catalog, token validation and bundle compilation, authenticated endpoint, stable workspace identity, and metadata/revision handling. Agently adds the shared loader, Appearance selection/persistence, and window/portal boundaries, including standalone MCP loading and embedded theme synchronization. Forge fixes container class propagation, exposes portal integration, and ships the small token set, default palettes, and input/button part targets specified above. Document Blueprint-specific selectors and incomplete component coverage explicitly.

Acceptance: a workspace author adds the manifest and CSS, refreshes, and changes an input's appearance without rebuilding Agently or modifying Forge. Opening, closing, restoring, and embedding that window retains the same theme. No manifest produces existing appearance. Removing the manifest restores default styling after refresh. Named themes and light/dark/system preferences can be switched without a rebuild, network fetch, or lost form state; reload retains the selection for the same account/workspace, and portals/embedded windows match it.

### Phase 2: reduce vendor coupling

Extend the phase-1 pack styling contract beyond inputs/buttons to select/date controls, tables, dialogs, and dashboards as required. Expand coverage tests and consider an explicit Agently shell token bridge. Update example workspaces to use those hooks. Keep direct `.bp6-*` rules available as an explicit compatibility escape hatch. Align and verify the actual Blueprint dependency/CSS versions independently of the theme feature.

### Required verification when implemented

- Backend: absent/invalid manifest, order, revision changes for CSS-only edits, duplicate/traversal/symlink rejection, unsupported import/URL rejection, authorization, conditional requests, and old-revision request races.
- Themes: CSS-only compatibility, invalid IDs/defaults/modes/tokens, token injection rejection, deterministic compilation, token/catalog-only revision changes, single-mode fallback, deleted themes, system changes, disabled storage, and account/workspace preference isolation.
- Metadata serialization: additive descriptor survives the SDK/host normalization path; unknown fields do not break older clients.
- Forge render proof: generic container branches, wrapper bypass, input versus wrapper parts, existing class/style precedence, validation/read-only/disabled states.
- Host lifecycle: deduplication, failed replacement, deletion, stale load callbacks, workspace switch/logout, late CSS imports, and restored windows.
- Browser integration: actual computed style for text/numeric/textarea/select/button, focused/disabled/invalid states, a portaled dropdown/dialog, and the MCP iframe. Check scrolling, clipping, keyboard focus, and mobile layout.
- Theme lifecycle: switching preserves form/scroll state, never leaks inactive-theme rules, updates portal/native control appearance, synchronizes embedded and directly opened Forge pages, rejects untrusted bridge messages, and reconciles catalog revisions atomically. Verify supported control contrast and focus in both modes.
- Run both development and production builds for style ordering. Verify a CSS-only workspace edit causes no frontend build dependency.

This document is based on source inspection, not a completed browser/runtime proof. It makes no claim that the proposed loader, tokens, scope attributes, or endpoints exist today.
