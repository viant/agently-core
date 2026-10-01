# Workspace-owned CSS and themes for Agently and Forge

Status: implementation in progress; shared assets and initial web/native theme integration are implemented; remaining release checks are incomplete. Initial source inspection: 2026-09-11.

See the [completion audit](workspace-css-audit.md) for the requirement/evidence matrix and remaining release gaps.

## Implementation checkpoint: prerequisites

The first implementation slice provides the shared contract in [protocol/ui/theme](protocol/ui/theme/theme.go), including token validation, light/dark defaults, mode fallback, and web CSS generation. A [shared resolved fixture](protocol/ui/theme/testdata/baseline.json) is consumed by Go, Swift, and Kotlin tests. The [style asset service](service/ui/style/README.md) now validates workspace files, publishes versioned catalog/CSS routes through private metadata, and includes the style revision in MCP UI resource content. It retains valid snapshots on edit errors, bounds its cache, and confines file access to the configured root.

Native catalog decoders and baseline views exist for iOS and Android. iOS now also has production integration: authenticated catalog loading, scoped selection/cache persistence, offline restoration, logout/workspace cleanup, a Settings appearance section, and a shared SwiftUI theme environment consumed by Forge windows, inputs, textareas, and buttons. Android now has production loading, scoped preferences/cache, Settings selection, and Material/Forge token projection; the no-theme appearance remains unchanged. The iOS simulator visual check and remaining release proofs remain pending.

Forge now forwards authored classes on its ordinary container content root and existing card/section anchor, adds initial widget/control/part attributes, supplies accessible label/message targets, consumes a subset of tokens, and propagates host-provided themes to window/portal boundaries. The [readiness ledger](../forge/doc/workspace-theme-readiness.md) records remaining branches, labels, states, portal integration, and browser proof. Verification so far: Go theme tests, Swift package theme tests, Android debug Kotlin compilation and theme unit tests, Forge render contracts, Forge production build, and the browser theme suite passed. The browser suite exercises actual runtime binding, state retention during theme changes, popup/nested-dialog interaction, focus, disabled/invalid colors, wrapper bypass, and portal cleanup. Passing contract/build checks is not completion of the prerequisite release gates below.

### Web application checkpoint

Agently's main route tree now mounts the shared style provider, including UI Settings and the standalone MCP Forge renderer. Appearance settings expose named themes, light/dark/system preferences, effective-mode fallback, diagnostics, and explicit metadata reload. The SDK preserves asset descriptors and fetches CSS/catalog text with its configured authentication; metadata cache refresh and logout invalidation are implemented. Hosts may supply a style nonce explicitly, through `meta[name="agently-style-nonce"]`, or through an existing nonce-bearing script.

Browser verification against an isolated host confirmed that the dark selection survives reload, a standalone Forge window uses it, and changing theme from settings updates an already-open window without losing its input. The standalone renderer now fills the viewport with its selected surface color. Evidence: `/Users/awitas/Downloads/tmp/outcome/agently-workspace-appearance.png` and `agently-workspace-dark.png` in the same directory. Same-origin parent/guest selection messaging has source/origin/workspace/revision validation and unit coverage; end-to-end embedded-frame verification now passes with parent preference storage disabled, including a catalog/token revision change while retaining iframe text. Evidence: `/Users/awitas/Downloads/tmp/outcome/workspace-theme-iframe.png`.

Targeted SDK asset tests, UI lifecycle/auth/settings tests, and the production UI build passed. The SDK-wide TypeScript check has 167 existing errors; a temporary baseline comparison against pre-change client/types sources showed the same diagnostics and no new errors from this work. Android production selection/cache integration is now implemented; remaining visual and release checks are tracked below.

### iOS integration checkpoint

[WorkspaceThemeRuntime](../agently/ios/Sources/AgentlyAppFoundation/Theme/WorkspaceThemeRuntime.swift) scopes preferences and validated cached catalogs by server, authenticated account, and workspace ID. It restores the last validated scope for offline startup, clears the restore pointer on logout, resets deleted selections, and rejects late results after a scope change. Session-only Default selection also survives an in-session refresh. The app owns refresh/cancellation and passes theme state through a SwiftUI environment; Settings and presented content inherit that environment.

The Swift SDK now preserves theme descriptors in sparse metadata and uses its configured authenticated transport for catalog requests. Metadata requests retain the caller's URLSession configuration while using separate bounded timeouts; stored session cookies are applied consistently. Forge's iOS surface and shared text/password/numeric/textarea/button renderers consume a native projection of the portable tokens while preserving the no-theme path. A macOS-only checkbox style in the tree editor was made conditional so the iOS target builds.

Verified: seven theme/catalog lifecycle tests in Agently's Swift package; seven SDK metadata/theme asset tests; and an Xcode iOS Simulator-target build. Simulator visual execution is not yet verified: all available simulators were shut down, and the iOS debugging skill requires an explicit boot request. The boot question is pending. This does not block continued Android implementation or non-visual verification.

### Android integration checkpoint

Android now loads catalog descriptors through the existing SDK authentication/cookie configuration, with a 512 KiB response limit and cancellable requests. The app scopes saved preferences and validated catalogs by server/account/workspace, restores cached appearance during offline startup, and clears active state on workspace/auth resets. Settings includes named themes, light/dark/system preferences, reload, and an interactive preview using Forge's production native input/button adapters. `AgentlyTheme` and the Forge composition local project the shared tokens into native colors, typography, shapes, and control states.

Verified on a read-only API 35 test emulator: the actual app loads the workspace catalog; dark mode changes the input/button and surrounding native appearance; typed sample text survives switching modes and switching to Compact; the dark choice survives cold restart; and after stopping the server, cold-start Settings still shows the cached Compact theme with its declared light fallback. Settings header/text and status-bar icon contrast were corrected based on the rendered UI. Evidence is in `/Users/awitas/Downloads/tmp/outcome/android-workspace-theme-dark.png`, `android-workspace-theme-compact.png`, and `android-workspace-theme-offline.png`.

The debug build, seven app theme/lifecycle tests, and two SDK asset authentication/cancellation tests passed. The emulator and temporary server were shut down after verification. Native OS-driven mode changes, broader native presentation coverage, and the pending iOS simulator visual check still need release-gate verification; these results do not claim every native component is fully themed.

### Window preview coverage

The standalone `agently/preview` host also uses the shared style service. Its `--workspace-root` option selects the workspace containing `extension/forge/styles`; when omitted, it uses the preview `--root`. `--metadata-root` remains the Forge metadata root and does not implicitly select a style directory. Preview's `/api/workspace` advertises the same style/catalog descriptors, and its frontend uses the shared `WorkspaceStyleProvider` and Forge window/portal boundary. Preview refreshes style metadata without reopening windows; CSS edits must retain window parameters and form state.

The preview runtime must be included in end-to-end acceptance: change a scoped input/container rule in workspace CSS, observe the change in the rendered preview without a frontend rebuild, and verify a popup plus a second window receive the same scope. Keep a failed update's last valid styling and remove customization after manifest deletion. Backend preview tests cover revision changes and asset routes. An isolated built-preview instance was also verified in Chrome: editing the workspace CSS changed the actual container and input background without a rebuild or reload, while the typed value remained intact. Evidence was saved to `/Users/awitas/Downloads/tmp/outcome/window-preview-css-overrides.png`. Preview-specific popup/second-window coverage now passes, including manifest removal without form reset; the generic Forge browser suite also covers popup/nested-dialog propagation.

## Recommendation

Yes: a workspace should ship its own CSS alongside its Forge windows, models, dialogs, and actions. Forge already accepts named classes and inline style objects. What is missing is an asset delivery and lifecycle contract, plus consistent styling targets across renderers.

Add an optional `extension/forge/styles/manifest.yaml` with shared CSS files, named themes, token values, and light/dark variants. Agently-core validates the shared theme contract and serves a versioned token catalog plus a derived web CSS bundle; the Agently web host loads it after its application styles and establishes a workspace scope around Forge surfaces and their portals. Retain existing `className`, `style`, and `properties` semantics. Add stable Forge tokens and component parts incrementally so ordinary appearance changes stop requiring changes in Forge.

The smallest useful release needs one server bundle endpoint, an additive workspace metadata field, a shared host loader and theme selector, portal propagation, a small input/button token contract, and a fix for generic container class forwarding. It does not need a new widget framework, a workspace npm build, CSS-in-JS, or a rewrite of existing Forge CSS.

**Release prerequisite:** define a platform-neutral token/selection contract and prove its baseline on web, native iOS, and native Android; also standardize the public styling targets for the components advertised as theme-supported before shipping named themes. This is a bounded compatibility task, not a repository-wide class rename. Asset delivery can be developed in parallel; theme readiness requires the explicit gate below.

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

Those `--forge-*` names require Forge to consume them; merely defining a
variable does not change a component. The host also exposes
`.agently-application`, `data-agently-theme`, and
`data-agently-color-mode` on the document root. Generated theme CSS publishes
the same resolved values there as `--agently-theme-*` variables. Loading a
theme still does not automatically restyle the shell: a workspace opts in by
mapping those portable values to Agently's stable `--app-*` roles in scoped
CSS. This preserves existing appearances while providing one source of theme
values for the shell and Forge.

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
      typography.family: system
      typography.size: 14
      control.minHeight: 36
      control.radius: 8
      control.paddingInline: 10
    files:
      - themes/branded.css
    modes:
      light:
        tokens:
          surface: '#ffffff'
          text: '#171b26'
          control.background: '#fffdf5'
          control.foreground: '#171b26'
          control.border: '#788397'
          focus.color: '#4466cc'
          button.background: '#3453b3'
          button.foreground: '#ffffff'
      dark:
        tokens:
          surface: '#1b2230'
          text: '#edf1f7'
          control.background: '#242d3d'
          control.foreground: '#edf1f7'
          control.border: '#8491a6'
          focus.color: '#9db5ff'
          button.background: '#a9bfff'
          button.foreground: '#172447'
        files:
          - themes/branded-dark.css
  - id: compact
    label: Compact
    fallbackMode: light
    tokens:
      control.minHeight: 30
      control.radius: 4
      control.paddingInline: 6
    modes:
      light: {}                # Inherit shared built-in light token defaults
```

Theme IDs must be unique lowercase identifiers matching `[a-z][a-z0-9-]{0,63}`. Reserve `forge-default` for the framework fallback; it cannot be redefined by a workspace. Require at least one declared mode, a supported `fallbackMode`, and a `defaultTheme` referencing a declared theme whenever `themes` is nonempty. `defaultMode` defaults to `system`. Labels are plain display text.

Each `files` list is ordered and paths remain relative to `styles/`. Reject duplicate references within a list and overlapping references along a single shared/theme/mode chain. Reuse in different themes is allowed, but authors must scope that shared file correctly for each intended theme. Validate the entire manifest before activating any new revision.

### Token ownership and resolution

The shared Agently theme schema and default palettes live in agently-core as a versioned, platform-neutral contract. Forge owns the web token mapping and widget consumption; Agently's iOS and Android clients own native adapters. Generate or validate client bindings against shared fixtures so each client does not invent its own token names or defaults. Do not introduce a second copy of Blueprint's complete theming API in workspace YAML.

Manifest token keys are semantic names, not CSS custom-property names. Dotted
names are literal map keys, not nested objects. The original CSS-only examples
remain valid web overrides. Generated web CSS exposes each value as an
`--agently-theme-*` application variable and an established `--forge-*`
renderer variable; workspaces should not hand-maintain a second copy of the
same palette.

| Shared token | Application variable | Forge variable | Value and native interpretation |
| --- | --- | --- | --- |
| `typography.family` | `--agently-theme-font-family` | `--forge-font-family` | Enum `system` or semantic `workspace-primary`; the workspace registers its primary WOFF2 faces once, while platforms without those assets use their system fallback |
| `typography.size` | `--agently-theme-font-size` | `--forge-font-size` | Positive logical size; native text respects user scaling |
| `surface`, `text` | `--agently-theme-surface`, `--agently-theme-text` | `--forge-surface`, `--forge-text` | Surface and foreground colors |
| `control.minHeight` | `--agently-theme-control-height` | `--forge-control-height` | Minimum visual control height; never overrides platform minimum touch targets |
| `control.radius`, `control.paddingInline` | `--agently-theme-control-radius`, `--agently-theme-control-padding-inline` | `--forge-control-radius`, `--forge-control-padding-inline` | Nonnegative logical dimensions; padding follows writing direction |
| `control.background`, `control.foreground`, `control.border` | `--agently-theme-control-background`, `--agently-theme-control-foreground`, `--agently-theme-control-border` | `--forge-control-bg`, `--forge-control-text`, `--forge-control-border` | Input colors |
| `focus.color` | `--agently-theme-focus` | `--forge-focus-color` | Focus accent where platform customization is supported; retain native accessibility feedback |
| `button.background`, `button.foreground` | `--agently-theme-button-background`, `--agently-theme-button-foreground` | `--forge-button-bg`, `--forge-button-text` | Ordinary enabled buttons; semantic intent states retain their own styles |
| `disabled.background`, `disabled.foreground`, `validation.border` | `--agently-theme-disabled-background`, `--agently-theme-disabled-foreground`, `--agently-theme-validation-border` | `--forge-disabled-bg`, `--forge-disabled-text`, `--forge-invalid-border` | Disabled and validation colors |

Use finite JSON/YAML numbers for dimensions, not `px`, `rem`, `dp`, or CSS expressions. Web maps logical dimensions to CSS pixels (and accessible font scaling); iOS maps them to points, Android to density-independent dimensions and scaled text. This is semantic parity, not a promise of identical pixel geometry. Control adapters apply minimum sizes and text reflow required by their platforms. Textarea uses minimum sizing, not a fixed single-line height.

For each selected mode, resolve a complete token map in this order: shared built-in mode defaults → theme tokens → mode tokens. Unspecified values inherit defaults, never values left over from the previous selection. The backend publishes resolved typed maps. Web generates CSS declarations rather than passing tokens through item `StyleProperties` or applying them inline; native consumes the typed maps directly.

Version 1 accepts literal `#RRGGBB` or `#RRGGBBAA` colors, bounded nonnegative numeric dimensions (font size and minimum height must be positive), and the documented typography enum. Define numeric bounds in the shared schema. Reject unknown tokens and wrong types with diagnostics. No CSS `var()`, `calc()`, font download URLs, or token-reference expressions in portable tokens. A workspace may register contained WOFF2 faces for the `workspace-primary` role in the manifest. Core validates and content-addresses those files, generates the font-face CSS, and publishes the same semantic family to the application and Forge boundaries. The role name and workspace-declared fallback do not constrain a future replacement to a sans-serif family. Authored CSS remains the web extension point for selectors and custom variables, with the existing parser validation, but still cannot reference URLs or imports. Native must never parse or execute those files and falls back to its system family when the selected workspace asset is unavailable.

Both built-in palettes and token consumption for the listed input/button targets ship in phase 1. They do not imply complete dark-mode support for all Forge dashboards or third-party widgets. Publish a coverage list with the feature. Validate actual contrast/focus and disabled/invalid visibility rather than assuming the example palette proves accessibility.

### Compilation, CSS scoping, and precedence

Keep a single bundle containing all themes for v1. Generate fully resolved token rules using selectors such as:

```css
.agently-application[data-agently-theme="branded"][data-agently-color-mode="dark"] {
  --agently-theme-control-radius: 8px;
  --agently-theme-control-background: #242d3d;
  /* Remaining resolved tokens omitted in this illustration. */
}

.agently-workspace[data-forge-theme="branded"][data-forge-color-mode="dark"] {
  color-scheme: dark;
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

The first release themes Forge surfaces. Agently's surrounding navigation/chat shell retains its current styling. A future shell bridge can map `--forge-surface`, `--forge-text`, and additional semantic tokens to `--app-*` on an explicit shell scope; raw Forge CSS must not silently become global shell styling. Native iOS/Android consume the shared semantic token catalog through the adapters below, never browser CSS. Print/export remains a separate rendering contract.

The authenticated MCP Forge page loads the same catalog/bundle. When embedded, the parent sends its current selection and subsequent changes through a small theme message on the existing guest bridge; validate the exact origin, source window, workspace identity, and catalog revision. The guest validates theme IDs/modes against its own catalog, requests a metadata refresh on revision mismatch, and ignores stale messages. Theme messages carry IDs/preferences, never raw CSS or arbitrary asset URLs. Apply the parent's effective mode to match the parent precisely; a directly opened renderer resolves the local preference itself. Use storage events for other same-origin tabs and an in-document provider notification for immediate local updates. Inbound synchronization must not echo messages or write new preferences in a loop.

## Prerequisite: shared themes for native Agently

**Establish the shared contract and a small native proof before calling named themes cross-platform.** Full native component migration does not block the web CSS endpoint or loader. The prerequisite is a typed theme schema, common default palettes and selection rules, and demonstrated consumption by a representative surface, text input, and button on web, iOS, and Android. Native implementation details and current coverage still need a code audit; this document does not claim native theming already exists.

### Shared data, separate platform adapters

Keep the existing proposed workspace manifest location for compatibility with this design, but treat its semantic tokens as renderer-independent. The `files` entries are optional web-only extensions; a tokens-only workspace theme must work without any CSS files. Do not create a second native theme manifest or force native clients to depend on Forge JavaScript.

Add a private metadata descriptor `uiThemes` with `version`, `revision`, and `href`, alongside `uiStyles`. Serve a versioned JSON catalog at the proposed `/v1/workspace/ui/themes/<revision>.json`. The catalog contains contract version, shared default-palette version, default theme/mode, and each theme's ID, label, fallback mode, and fully resolved token map for each supported mode. It contains no filesystem paths or CSS source. Apply the same authenticated access, immutable revision lookup, private caching, and bounded snapshot retention as the CSS route.

Use one shared snapshot revision for the token catalog and its derived CSS in v1. Include canonical manifest data, all source bytes, resolved palettes, and compiler/contract version in that revision. A CSS-only change may therefore refresh a native catalog unnecessarily; accept that small cost initially to simplify consistency. Advertise both descriptors atomically. `uiStyles.themeRevision` identifies the catalog revision it was built from. The catalog is authoritative for selection/defaults; remove duplicated theme lists/defaults from `uiStyles` so clients cannot see conflicting catalogs.

CSS-only manifests can omit `uiThemes`; native then uses built-in appearance. Tokens-only manifests still generate web CSS. Missing manifest produces existing platform appearance. A backend validation failure retains the previous valid snapshot for that workspace; native does not bypass validation just because the invalid source is web-only.

| Adapter | Required baseline behavior |
| --- | --- |
| Web / Forge | Map shared tokens to `--forge-*`, stable parts and Blueprint theme integration; CSS can supplement the mapped tokens |
| Native iOS | Resolve a typed theme through the native view environment/state mechanism and apply it to the covered surface/input/button; preserve platform text scaling, focus, and presentation behavior |
| Native Android | Resolve the same typed theme through the app's native theme/state mechanism and apply it to the covered surface/input/button; preserve font scaling, touch targets, and system interaction feedback |

Do not require native components to emulate Blueprint or invent CSS-like class names. Each client documents which semantic tokens its components consume. Native sheets/dialogs must receive the same selected theme through their platform presentation mechanisms, just as web portals receive scope attributes. Broader coverage is incremental.

### Built-in themes, selection, and offline behavior

First implement built-in light/dark palettes through the same typed resolver used for workspace themes; then feed a workspace-defined catalog through it. This proves the adapter independently of networking and prevents two separate styling systems. Keep the web's proposed `forge-default` option web-only; it means existing Forge appearance, not a portable theme ID. Native uses a local Default option for its existing platform appearance. Workspace theme IDs, supported modes, fallback mode, and light/dark/system resolution remain identical across clients.

Persist theme ID and mode preference in platform-local settings under the authenticated account and stable workspace ID. Use native OS appearance for System and observe changes only while System is selected. Do not persist the resolved effective mode as the user's preference. Removing a selected theme resets its saved selection to workspace defaults; unknown schema versions fall back to a previously supported catalog or built-in appearance with a diagnostic.

Cache only validated catalogs for the correct account/workspace. On offline startup, use that workspace's last valid catalog; otherwise use built-in appearance. On logout/workspace change clear active theme state and prevent stale in-flight updates from applying. Do not display a cached theme from another workspace. Cross-device preference synchronization remains deferred: clients share rules and themes, not automatically the same locally selected preference.

### Cross-platform acceptance gate

- [x] One versioned schema/default-palette fixture yields identical resolved semantic values on web, iOS, and Android for both modes.
- [ ] A representative surface, text input, and button consume built-in palettes and one workspace-defined theme on all three platforms.
- [x] Native clients load the typed JSON catalog without requesting CSS or interpreting CSS tokens; web derives variables from the same values.
- [x] Light/dark/system, unsupported-mode fallback, deleted themes, workspace/account isolation, and offline recovery have shared scenario coverage.
- [ ] Theme changes retain form state and navigation state; native text scaling, screen-reader semantics, touch targets, focus, and modal presentation remain usable.
- [x] Coverage explicitly separates proven components from unmigrated components; the web-only CSS escape hatch does not imply native visual parity.

## Backend design: agently-core

Add a small style asset service, separate from YAML resource decoding, under a proposed `service/ui/style/` package. Use the effective workspace root and the same authentication boundary as private workspace/window reads.

1. Read the manifest and exact referenced CSS bytes. Missing manifest means no customization.
2. Validate schema version, relative paths, duplicate entries, file type, UTF-8, and size limits. Suggested initial limits: 32 unique CSS files and 512 KiB of emitted CSS, including all generated theme variants. Reject absolute paths, URL paths, traversal, and symlinks escaping the resolved styles directory. Do not expose a generic filesystem route.
3. Compile shared CSS and all theme variants in the order defined above, with explicit file separators. Compute a revision over canonical manifest/catalog data, ordered source names and bytes, generated CSS, and the token/compiler contract version. Theme labels/defaults and token-only edits must invalidate the revision too. Publish the bundle and catalog only after the entire snapshot succeeds.
4. Add optional `uiStyles` and `uiThemes` descriptors to `MetadataResponse`; publish them from the same snapshot and include its revision in metadata version computation.
5. Serve the exact immutable snapshot through the proposed authenticated endpoint below. A request for an unavailable revision must fail, never return newer bytes under the old revision.

```json
{
  "uiThemes": {
    "version": 1,
    "revision": "<sha256>",
    "href": "/v1/workspace/ui/themes/<sha256>.json"
  },
  "uiStyles": {
    "version": 1,
    "revision": "<sha256>",
    "themeRevision": "<sha256>",
    "href": "/v1/workspace/ui/styles/<sha256>.css"
  }
}
```

For CSS-only manifests, omit `uiThemes` and `uiStyles.themeRevision`. The implemented identity source is root `config.yaml` → `workspaceId`, with a generated `.workspace-id` fallback; copy that file or configure the same ID when relocating a workspace. Add a separate opaque stable `workspaceId` to private metadata for preference scoping; the shared asset revision is not a user preference or identity. The bundle endpoint never varies bytes by the requesting user's selected theme.

Respond with `Content-Type: text/css; charset=utf-8`, `X-Content-Type-Options: nosniff`, an ETag, and private caching with revalidation. Keep a bounded cache of compiled snapshots to cover metadata/bundle request races. Authorization still applies on cache hits and conditional requests. Never let a shared HTTP cache expose another workspace's assets.

Manifest deletion removes `uiStyles` and `uiThemes` from metadata. An invalid replacement retains the last valid bundle for the same workspace and reports a diagnostic; on first load it uses default styling. Do not convert a styling failure into an unavailable agent/window service. Log path-relative diagnostics without returning filesystem contents.

Initial freshness contract: metadata refresh/page reload reevaluates style bytes and produces a new revision. Do not claim existing model/agent hot-swap automatically watches CSS. Add file watching or a refresh notification later if editing without reload is needed. Avoid timestamp-only revision detection.

## Host design: Agently web

Implement one reusable workspace style manager, invoked by both normal application bootstrap and the standalone MCP Forge page. It consumes the descriptors rather than knowing workspace paths. Fetch the token catalog through the authenticated client, validate the contract version, and reconcile it with the matching CSS revision before activating either. A missing `uiThemes` means CSS-only behavior.

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

Release gates for the published initial web styling family (cross-platform visual acceptance remains a separate gate above):

- [x] Inventory current class and inline-style handling for every first-release renderer; distinguish public contracts from implementation details.
- [x] Preserve authored class tokens, framework classes, and existing style/property precedence; verify typed YAML → Go → JSON → rendered DOM behavior.
- [x] Fix supported container branches and cover normal wrappers, wrapper bypass, and validation wrappers.
- [x] Place public parts on actual nodes in every covered pack; publish supported states and any repeated-part rules.
- [x] Prove input/button token changes in both modes using computed styles, including focus/disabled/invalid states and default appearance without a manifest.
- [x] Prove matching scope and selection across window roots, portal mounts, and the MCP iframe without clipping, focus, or form-state regressions.
- [x] Verify a workspace example changes a covered control using only Forge parts/tokens, without `.bp6-*`, DOM-depth selectors, or `!important`.
- [x] Publish the coverage ledger and migration notes; do not label compatibility-only components as fully theme-supported.

### Compatibility and ownership of follow-up work

Introduce public hooks additively. Preserve existing Forge and Blueprint classes and legacy workspace selectors during phase 1. A future removal or semantic change to a documented hook requires explicit migration notes and a compatibility/deprecation window; an internal DOM refactor must preserve the hook's logical target.

Forge owns runtime/pack hooks, decorative defaults, and renderer proof. Agently owns boundary propagation, selection, portal integration, and iframe parity. Agently-core owns token/schema validation and metadata round-trip proof. Backend CSS delivery must not try to compensate for missing frontend targets by rewriting vendor selectors.

Global class renaming, a complete BEM conversion, removal of all inline styles, full Blueprint replacement, shell theming, and complete dashboard/print/native component coverage are not prerequisites. The small shared native baseline above is a prerequisite for cross-platform named themes. Apply the same readiness gate incrementally as each additional component family becomes theme-supported.

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

### Phase 0: shared theme contract and native baseline

Define the platform-neutral schema, default palettes, versioned catalog, and shared resolution fixtures in agently-core. Implement a baseline adapter on web, iOS, and Android, first with built-in light/dark themes, then with one workspace theme. Audit native component/state architecture during implementation and record proof in each client's coverage ledger. Complete the cross-platform acceptance gate above before claiming cross-platform theme support. Full native migration is not required to develop or deliver standalone web CSS loading.

### Phase 1: ship workspace CSS and named themes

**Entry work and release gate:** complete phase 0 and the styling standardization checklist above for the declared first-release coverage. Asset service and host-loader development may run in parallel, but successful CSS loading alone is not acceptance for named themes.

Agently-core adds the manifest/theme catalog, token validation and bundle compilation, authenticated endpoint, stable workspace identity, and metadata/revision handling. Agently adds the shared loader, Appearance selection/persistence, and window/portal boundaries, including standalone MCP loading and embedded theme synchronization. Forge fixes container class propagation, exposes portal integration, and ships the small token set, default palettes, and input/button part targets specified above. Document Blueprint-specific selectors and incomplete component coverage explicitly.

Acceptance: a workspace author adds the manifest and CSS, refreshes, and changes an input's appearance without rebuilding Agently or modifying Forge. Opening, closing, restoring, and embedding that window retains the same theme. No manifest produces existing appearance. Removing the manifest restores default styling after refresh. Named themes and light/dark/system preferences can be switched without a rebuild, network fetch, or lost form state; reload retains the selection for the same account/workspace, and portals/embedded windows match it.

### Phase 2: expand web and native component coverage

Extend the phase-1 pack styling contract beyond inputs/buttons to select/date controls, tables, dialogs, and dashboards as required. Expand native theme consumption to additional screens, sheets, dialogs, and controls with platform-specific proofs. Expand coverage tests and consider an explicit Agently web-shell token bridge. Update example workspaces to use those hooks. Keep direct `.bp6-*` rules available as an explicit compatibility escape hatch. Align and verify the actual Blueprint dependency/CSS versions independently of the theme feature.

### Required verification when implemented

- Backend: absent/invalid manifest, order, revision changes for CSS-only edits, duplicate/traversal/symlink rejection, unsupported import/URL rejection, authorization, conditional requests, and old-revision request races.
- Themes: CSS-only compatibility, invalid IDs/defaults/modes/tokens, token injection rejection, deterministic compilation, token/catalog-only revision changes, single-mode fallback, deleted themes, system changes, disabled storage, and account/workspace preference isolation.
- Native contract: shared resolution fixtures, typed catalog validation/version fallback, no CSS dependency, cached offline startup, account/workspace isolation, OS mode changes, text scaling, touch targets, and native modal/theme propagation.
- Metadata serialization: additive descriptor survives the SDK/host normalization path; unknown fields do not break older clients.
- Forge render proof: generic container branches, wrapper bypass, input versus wrapper parts, existing class/style precedence, validation/read-only/disabled states.
- Host lifecycle: deduplication, failed replacement, deletion, stale load callbacks, workspace switch/logout, late CSS imports, and restored windows.
- Browser integration: actual computed style for text/numeric/textarea/select/button, focused/disabled/invalid states, a portaled dropdown/dialog, and the MCP iframe. Check scrolling, clipping, keyboard focus, and mobile layout.
- Theme lifecycle: switching preserves form/scroll state, never leaks inactive-theme rules, updates portal/native control appearance, synchronizes embedded and directly opened Forge pages, rejects untrusted bridge messages, and reconciles catalog revisions atomically. Verify supported control contrast and focus in both modes.
- Run both development and production builds for style ordering. Verify a CSS-only workspace edit causes no frontend build dependency.

The implementation checkpoint above distinguishes code already added from planned work. Browser/native visual proof and the prerequisite release gates remain incomplete; the proposed workspace loader, HTTP endpoints, and application selection lifecycle are not implemented yet.


## Web tab sizing: ownership and source fixes

The host allocates available space. Forge owns the layout within that space;
metadata supplies content, behavior, and deliberate layout choices. Workspace
CSS supplies appearance through supported hooks. A theme must not repair tab
geometry with generated IDs, Blueprint internals, inline-style substring
selectors, or per-tab forced heights.

The September 12 source trace identified two independent leaks:

- Preview shell selectors matched every `section` and `section > div`, including
  Forge content. The host now uses explicit `preview-*` classes and allocates a
  viewport-sized flex column; status notices do not consume the window's flex space.
- WindowManager and FormPanel used descendant Blueprint panel selectors and
  `height: 100%`. Outer window rules reached nested panels, and panel height could
  consume the entire parent in addition to the tab rail. These components now
  pass owned panel classes through Blueprint's public `panelClassName` API,
  target direct children, and allocate remaining space with flex. Inactive
  mounted panels do not participate in layout.

`forge-window-manager-tabs__panel` and `forge-form-panel-tabs__panel` identify
owned panel surfaces. Their layout declarations belong to Forge. Section tabs
remain content-sized by default; `tabs.fill: true` participates in the available
space contract and gives its active panel a flex column and scrolling. Equal
panel heights are not promised for content-sized sections. A host must provide
bounded space before expecting fill mode to scroll within it.

Regression coverage: `forge/scripts/test-tab-layout.mjs` renders real Blueprint
Tabs with nested window/form panels at 1280px and 390px widths. It checks bounded
active panels, excluded inactive content, long-content scrolling, and a nested
content-sized section unaffected by preview shell rules.

Remaining advertiser migration: the active workspace still contains per-tab
minimum heights in metadata and geometry/message overrides in `advertiser.css`.
Those must be evaluated and removed as the relevant content/behavior contracts
are migrated. Passing the isolated layout regression does not certify that
customized advertiser screen. Operational messages must be rendered from actual
application state, never CSS-generated content.


### Explicit sizing and scroll ownership follow-up

Container metadata now supports `sizingMode: fill | content` (retained by the Go
model). Section-tab content receives content allocation unless `tabs.fill` is
true; ordinary nested stacks pass their allocation to children. The generic
container and its card/section chrome no longer repeat `height: 100%`. Explicit
metadata heights apply once at the outer boundary. Only that boundary handles
`scrollMode: self`.

WindowManager delegates scrolling to WindowLayout. When WindowLayout owns root
scrolling, it passes content allocation and parent-scroll mode to the root,
avoiding a second root scroll area. A residual broad WindowManager Blueprint
panel rule discovered in the follow-up was removed; the regression now asserts
nested computed overflow as well as bounding rectangles.

See `forge/doc/container-layout.md` for defaults, compatibility behavior, and the
scroll-owner table. Exact advertiser-pair validation is still pending; this is
not a claim that every customized advertiser tab now has identical geometry.


### Shared table and control consistency acceptance

The broader Campaigns/History review is tracked in
`forge/doc/web-component-consistency.md`. Shared component changes consolidate
collection actions with table tools, simplify table/pagination chrome, keep
short tables compact within fill panels, place overflow controls outside data,
budget sticky-column width, and guarantee a visible error fallback. Section-tab
arrows occupy layout space instead of covering labels. Actual preview acceptance
covers desktop, narrow, and error states; it does not certify fixture correctness.
The advertiser workspace now uses catalog tokens and supported CSS hooks instead
of generated-ID selectors, global Blueprint overrides, or injected messages.

### Final workspace overrides

The optional top-level `overrides: [overrides.css]` manifest list loads after
shared files, theme tokens, theme files, and mode files. Paths use the same
workspace-relative validation and size limits as `files`. Ordinary CSS
specificity still applies; use matching scoped selectors to override a theme.
Existing `files` ordering is unchanged.

### Composer selector availability

Workspace `config.yaml` can configure composer selectors:

```yaml
ui:
  composer:
    allowAgentSelection: true
    allowModelSelection: true
```

Both default to true. These settings control UI selector availability, not
backend authorization. Individual Forge chat metadata may further hide selectors
with the same keys; hiding a selector preserves the current/default selection.
