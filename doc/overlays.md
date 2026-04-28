# Overlays (schema + UI refinement)

An **overlay** is a server-side rule that refines an incoming JSON Schema
before the client sees it. Overlays attach UI hints, lookup widgets, ordering,
and default values — without changing the schema's contract or requiring the
schema author's cooperation.

Overlays are the unifying mechanism behind:

- Forge `Item.Lookup` widgets for elicitation form fields (see [doc/lookups.md](lookups.md))
- `x-ui-widget`, `x-ui-order`, `format` auto-inference in the elicitation refiner
- Named-token registry for chat `/<name>` hotkeys ([doc/lookups.md](lookups.md))

## Packages

| Path | Role |
|---|---|
| [service/elicitation/refiner/refiner.go](../service/elicitation/refiner/refiner.go) | Built-in refinement (type/format/widget/order) + hook |
| [service/lookup/overlay/](../service/lookup/overlay/) | Overlay engine: matcher, mode evaluator, translator, registry |
| [protocol/lookup/overlay/](../protocol/lookup/overlay/) | Overlay & Binding types |
| Workspace `extension/forge/lookups/*.yaml` | Per-workspace overlay files |

## Match modes (per-overlay)

Each overlay declares its own matching discipline:

- **`strict`** — every binding must match, else the whole overlay is discarded.
- **`partial`** (default) — apply each binding that matches, skip the rest.
- **`threshold: N`** — apply iff ≥ N bindings match, else discard.

Separately, any overlay can be broad-targeted (by `target.kind` / `target.id` / `idGlob` / `schemaContains`) or universal (no Target → applies everywhere).

## Composition

When multiple overlays apply to the same schema:

- All surviving bindings are collected.
- On path collision, higher `priority` wins; ties break deterministically by overlay id.
- A single property can be targeted by library overlays (small, universal), template-scoped overlays (strict, specific id), and pattern overlays (regex over field names) at the same time. All evaluate independently.

## Runtime wiring

`SetDatasourceStack(ds, overlay)` in the embedded backend installs an overlay hook on the refiner via `overlay.NewWildcardHook()`. The hook is called from `refiner.Refine` after the built-in defaults and before the schema is sent to the client.

Because the hook is wildcard, overlays targeting *any* kind (template, tool,
elicitation, chat-composer, …) all fire through the default path. Workspace
authors who need strict kind-scoped matching can override the hook at
startup.

## What overlays don't do

- They do **not** invent schema keywords — they attach `x-ui-*` extensions the client already understands (forge, MentionInput, etc.).
- They do **not** execute against responses — only against schemas.
- They do **not** ship with core — agently-core carries the engine; each workspace provides its own YAML.

## Extensibility

- **New match criterion**: extend `protocol/lookup/overlay.Match` + matcher.
- **New translator target**: add a case to `service/lookup/overlay/apply.go` `attachLookup()` — e.g. emit a `x-ui-datepicker` hint instead of `x-ui-lookup`.
- **Schema source other than elicitation**: call `overlay.Apply(kind, id, props)` directly — nothing about the overlay engine is elicitation-specific.

## Related docs

- [doc/lookups.md](lookups.md) — primary consumer (datasource + picker integration).
- [doc/elicitation-system.md](elicitation-system.md) — where the hook is installed.
