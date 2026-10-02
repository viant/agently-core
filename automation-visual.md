# Automation visual refresh plan

## Goal

Make Automation feel like a first-class Agently workspace: a clear primary action, a coherent icon language, a balanced toolbar, accessible controls, and a usable phone/tablet layout. Keep reusable layout and accessibility behavior in Forge, automation-specific content in Agently, and only shared window/API contracts in Agently Core.

## Review baseline

Reviewed on 2026-08-27 against the local `main` branches:

| Repository | Reviewed revision | Role in this work |
| --- | --- | --- |
| `viant/agently` | `eb6cf3f8` / tag `v0.3.115` | Automation metadata, navigation icon, schedule service decoration, product-specific CSS, browser tests |
| `viant/agently-core` | `6e8856b` / tag `v0.1.28` | Window snapshot/registry contract and dependency coordination; no Automation React UI is implemented here |
| `viant/forge` | `ad2a73c` / tag `v0.3.21` | Generic table toolbar, filters, table overflow, window tabs, and window controls |

The live view was inspected at desktop width and at a 390 × 844 phone viewport using the current Agently server and local linked UI dependencies.

## Dependency audit

The current dependency graph is:

```text
agently
├── Go: github.com/viant/agently-core v0.1.27
├── Go: github.com/viant/forge v0.3.21
├── UI: agently-core-ui-sdk -> ../../agently-core/sdk/ts
└── UI: forge -> ../../forge

agently-core
└── Go: github.com/viant/forge v0.3.21
```

Findings:

- Both Go modules agree on Forge `v0.3.21`, and local Forge `main` is exactly that tag.
- Agently pins Agently Core `v0.1.27`, while local and remote latest is `v0.1.28`.
- Agently UI development resolves both packages directly from the sibling repositories. `npm ls` confirms `forge@1.0.0 -> ../../forge` and `agently-core-ui-sdk@0.1.0 -> ../../agently-core/sdk/ts`.
- The visual changes in Forge will therefore appear immediately in the local Agently UI, but reproducible builds still require a new Forge tag and coordinated Go version bumps.

Release rule: implement and validate Forge first, tag it, update Agently Core to that Forge version if its Go-side dependency is affected, tag Agently Core, then update both pins in Agently. Independently bump Agently from Agently Core `v0.1.27` to `v0.1.28`; keep that dependency-only change separate from visual CSS changes so rollback is clear.

## Current-state findings

### Desktop

- The main toolbar has two square, unlabeled buttons on the left, the filter and destructive/selection actions on the right, and a large unused center. The controls do not read as one action hierarchy.
- The New control uses Blueprint's `new-object` glyph, which looks like a circular/clock-like symbol in this context. It is too similar to the Automation navigation clock and does not clearly communicate “create”.
- `tooltip` values exist in YAML but Forge `Toolbar` does not pass them to a tooltip, `title`, or `aria-label`. The rendered refresh, new, run, and delete buttons are unnamed to assistive technology.
- Enabled, disabled, primary, and destructive actions have nearly the same visual weight. The primary workflow—create an automation—is not obvious.
- Run History places three independent filter inputs in one horizontal strip; labels truncate even at desktop width.
- The configured rich empty state is not rendered. The decorator looks for `bp6-tab-panel_form-tabs_schedulesCatalogue`, while the current panel is `bp6-tab-panel_form-tabs-schedulesCatalogue_schedulesCatalogue`, so users see Forge's bare “No data.” state.
- The docked workspace title includes red and green traffic-light controls. They compete with the title, depend on color alone, and do not match the Blueprint/Agently icon buttons elsewhere.
- The top Automation icon is a clock inside a circular button with another selected ring. The double-circle silhouette is visually heavier than neighboring navigation icons and overlaps semantically with scheduling time and run history.

### Phone width

- The toolbar does not wrap: Run and Delete clip beyond the right edge.
- Eight schedule columns are compressed into narrow, broken headings. This is not a viable phone table even though platform-specific metadata exists.
- The three workspace tabs fit at 390 px but leave little room for localization or larger text.
- Interactive toolbar targets are roughly 30–32 px, below the desired 40–44 px mobile target.

## Design direction

Use the existing Agently blue/neutral surface language and reduce decorative rings and gradients. Color should communicate state, not identify every icon.

### Icon language

| Action | Icon | Treatment |
| --- | --- | --- |
| Automation navigation | `calendar` | Neutral by default; blue/accent selected state; one selected surface/ring only |
| Run history navigation | `history` | Same neutral/selected treatment as Automation |
| New automation | `plus` | Labeled primary button: “New automation” |
| Refresh | `refresh` | Minimal icon button with tooltip and accessible name |
| Run now | `play` | Labeled selection action on desktop; icon plus short label or compact action on phone |
| Remove/disable | `trash` | Danger styling; separated from primary actions; never color-only |
| Close / undock window | `cross` / `maximize` | Standard minimal icon buttons with labels, not traffic-light dots |

### Desktop toolbar

```text
[ + New automation ] [ Refresh ]        [ Search automations… ] [ Run now ] [ Delete ]
```

- Put creation and refresh on the left; search and selection-dependent actions on the right.
- Use a 40 px control height, 8 px group gaps, and one shared radius scale.
- Give New automation the only primary fill. Run now is outlined or minimal until a row is selected. Delete is a minimal danger action separated by a divider or placed in an overflow menu if Forge gains menu support.
- Hide the unused center group when it has no items. Keep pagination in its existing bottom bar.
- Disabled controls must remain legible but clearly inactive; preserve the current selection guards.

### Phone toolbar and data view

```text
[ + New automation ] [ Refresh ]
[ Search automations………………… ]
[ Run now ] [ More ]   (only when a row is selected)
```

- Wrap the toolbar into action and search rows below 640 px. Search spans the available width.
- Use at least 40 × 40 px targets and prevent horizontal clipping.
- Do not squeeze the desktop table. The first implementation may use a horizontally scrollable table with sticky Name/Status columns, but the target design is a compact list/card row showing Name, Active, cadence summary, Next run, and Last status; reveal secondary fields and destructive actions in row details/overflow.
- Use the Android/iOS phone metadata to select the reduced columns or compact presentation. Tablet can retain the table with fewer low-priority columns.

## Implementation plan

### Phase 1 — Forge shared primitives

Files:

- `forge/src/components/table/basic/Toolbar.jsx`
- `forge/src/components/table/basic/Toolbar.css`
- `forge/src/components/table/Basic.css`
- `forge/src/components/table/basic/QuickFilterInputs.jsx`

Changes:

1. Extend toolbar item rendering without breaking existing YAML:
   - map `tooltip` to a Blueprint tooltip and to a fallback `title`;
   - set `aria-label` for icon-only buttons from `ariaLabel`, `tooltip`, `label`, or a humanized item id;
   - expose stable classes/data attributes for item id, alignment, appearance, and disabled state;
   - support `appearance: primary|minimal|outlined|danger`, while preserving current `intent` behavior;
   - replace inline spacing with CSS classes so responsive rules can control gaps and wrapping.
2. Add responsive toolbar layout:
   - collapse empty alignment regions;
   - allow left/right groups to wrap;
   - allow quick filters to grow and shrink safely;
   - below 640 px, place filters on a full-width row and keep actions in an overflow-safe row.
3. Make multiple quick filters responsive: use a bounded width on desktop, wrap/stack on narrower widths, and preserve debounce/fetch behavior.
4. Ensure `.basic-table-scroll` owns horizontal overflow. Do not let `.basic-table-wrapper { overflow: hidden; }` clip wide tables or toolbar actions.
5. Add focused tests for accessible names, tooltip propagation, appearance classes, disabled state, empty alignment groups, and a narrow-width layout contract.

Compatibility requirement: existing metadata with only `id`, `icon`, and `align` must continue to render and behave as before.

### Phase 2 — Forge window chrome alignment

Files:

- `forge/src/components/WindowManager.jsx`
- `forge/src/components/WindowManager.css`
- `forge/src/components/WindowControls.jsx`
- `forge/src/components/WindowControls.css`

Changes:

1. Replace red/green traffic-light dots in docked tabs with Blueprint minimal icon buttons for Close and Undock/Maximize.
2. Add explicit `aria-label`, tooltip, hover, focus-visible, and disabled states.
3. Move docked-tab actions to the far right of the title row so the title starts on the same visual axis as the workspace content.
4. Keep the traffic-light treatment only as an optional floating-window variant if another host depends on it; default docked workspaces to the shared icon language.
5. Add WindowManager/WindowControls tests for action labels, event propagation, keyboard focus, and unchanged close/undock behavior.

### Phase 3 — Agently automation-specific polish

Files:

- `agently/ui/src/components/MenuBar.jsx`
- `agently/ui/src/styles/shell.css`
- `agently/metadata/window/schedule/table/toolbar/schedules.yaml`
- `agently/metadata/window/schedule/history/table/runs.yaml`
- platform schedule metadata under `web`, `android`, and `ios`
- `agently/ui/src/services/scheduleService.js`

Changes:

1. Change the top Automation icon from `time` to `calendar`. Normalize all top navigation controls to the same neutral default and blue selected treatment; remove the second concentric selected ring.
2. Update schedule toolbar metadata:
   - `addNew`: `icon: plus`, `label: New automation`, `intent: primary`;
   - `refresh`: minimal, icon-only, accessible tooltip;
   - `run`: `label: Run now`, outlined/minimal selection action;
   - `delete`: danger treatment and safer separation from Run now;
   - rename the filter placeholder to `Search automations…`.
3. Apply the same hierarchy to Run History: Refresh plus a responsive Search/filters region. Prefer a single search field with an optional filter popover over three always-visible inputs if API filtering semantics permit it.
4. Replace generated Blueprint-id targeting in `scheduleService` with a stable class/data attribute supplied by Forge, or add a metadata-driven Forge empty-state API. Render an icon, title, explanatory text, and New automation CTA for empty schedules; render a separate no-runs state for Run History.
5. Reduce selector duplication in `shell.css`. Scope Automation rules under a stable host class such as `.app-automation-view` instead of listing multiple Blueprint 4/6 generated ids.
6. Consolidate the six byte-identical schedule toolbar YAML files. Prefer one canonical import if cross-directory imports are supported; otherwise add a drift test that asserts all platform copies match the canonical file.
7. Tailor phone/tablet schedule columns. Phone must not render all eight desktop columns compressed into the viewport.

### Phase 4 — Agently Core integration and dependency update

Files:

- `agently-core/service/ui/window/registry/registry_test.go`
- `agently-core/sdk/api/types.go` only if a stable window presentation/class field is required
- `agently-core/go.mod` only after a new Forge Go tag is published
- `agently/go.mod` and `agently/go.sum`

Changes:

1. Keep the existing Automation window identity (`windowKey: schedule`, title `Automation`) unchanged so snapshots and restored workspaces remain compatible.
2. Avoid adding presentation fields to Agently Core solely for CSS. Add a shared contract only if Forge cannot derive a stable window key/class from existing window state.
3. If a new contract is required, add serialization and registry coverage before consuming it in Forge/Agently.
4. Bump Agently's Agently Core dependency from `v0.1.27` to `v0.1.28` and run module tidy/tests as a separate dependency commit.
5. After Forge changes are tagged, align both Agently Core and Agently on the same new Forge version. Verify that no local `replace` directive is needed for the release build.

## Verification plan

### Automated

- Forge unit/component tests for toolbar semantics, responsive classes, quick filters, and window controls.
- Agently tests for MenuBar icon/selected state and schedule metadata action configuration.
- Existing `scheduleService.test.js` suite to protect refresh, selection, run-now, delete, validation, and empty-state behavior.
- Agently Core registry/snapshot tests to protect `schedule` window identity and restore behavior.
- `go test ./...` in affected Go modules, plus `npm test`/targeted UI tests and an Agently production UI build.

### Browser matrix

Validate Automations, Definition, and Run History in these states:

| Width | States |
| --- | --- |
| 1440 desktop | empty, populated, row selected, loading, error, disabled action, keyboard focus |
| 1024 tablet | same states; no clipped filters/actions |
| 768 compact tablet | wrapped toolbar, readable table or compact rows |
| 390 phone | no horizontal toolbar clipping; 40 px targets; compact schedule rows; usable pagination |

Also verify light/dark mode if the host exposes both, 200% zoom, long schedule names, long localized labels, and visible focus rings.

## Acceptance criteria

- New automation is the unmistakable primary action and uses the `plus` icon.
- Automation navigation uses a calendar icon and the same selected-state grammar as adjacent navigation items.
- Every icon-only button has an accessible name and tooltip.
- Toolbar actions never clip at 390 px; filters wrap or stack intentionally.
- Phone schedule content is readable without eight compressed columns.
- Run History filters do not truncate or push actions off-screen.
- Empty schedules and empty run history show intentional empty states rather than bare “No data.” rows.
- Docked window actions use labeled standard icons, not color-only traffic-light dots.
- Existing run, delete, refresh, selection, pagination, window restore, and undock behaviors remain unchanged.
- Agently and Agently Core resolve the same published Forge version for release builds, and Agently no longer lags Agently Core `v0.1.28`.

## Suggested delivery sequence

1. Forge toolbar accessibility/layout changes and tests.
2. Forge docked window controls and tests.
3. Tag Forge and align Go dependencies.
4. Agently metadata, icon, empty-state, responsive CSS, and mobile column changes.
5. Agently Core contract work only if stable host styling cannot use the existing window key.
6. Bump Agently Core to `v0.1.28`, run the full dependency/build matrix, and capture desktop/tablet/phone visual proofs.
