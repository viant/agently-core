# Review: Claude Multi-Dataset Dashboard Composer Plan

Date: 2026-07-07

## Outcome

Claude produced a strong migration plan for evolving Forge Report Builder into a
multi-dataset dashboard composer.

Verdict: `green light with revisions`

The plan is directionally correct and is approved to move forward only with the
pushback and constraints below folded into implementation planning.

## What Claude Got Right

- Keeps the migration additive instead of proposing a big-bang rewrite.
- Preserves `agently-core` as the persistence and export owner.
- Promotes datasets to a first-class authored concept.
- Treats seeded forecasting/performance context as explicit input rather than
  hidden runtime glue.
- Chooses the forecasting audience dashboard as the first real proof target.
- Avoids browser-side cross-dataset joins in the first phase.

## Required Pushback

### 1. Keep canonical `reportFill.datasets[]`

Claude proposed a keyed runtime/persisted fill shape such as `fills: {}` /
`datasetResultSet`.

That is acceptable as an internal runtime cache only, but not as the canonical
report/export contract. Current canonical validation requires
`reportFill.datasets[]` objects with `id`, `dataSourceRef`, `request`,
`provenance`, and `rows`.

Evidence:

- [export_validation.go](/Users/awitas/go/src/github.com/viant/agently-core/service/reporting/export_validation.go:533)
- [reportBuilderPublishedDatasetRuntime.test.js](/Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderPublishedDatasetRuntime.test.js:209)
- [reportBuilderStaticDatasetRuntime.test.js](/Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderStaticDatasetRuntime.test.js:133)

Decision:

- Runtime may use a map keyed by dataset id internally.
- Persisted / exported artifacts must continue lowering to
  `reportFill.datasets[]`.

### 2. Do not invent a second spec versioning story unless needed

Claude proposed `specVersion: 2`.

That may be useful later, but the current parser already supports
`reportSpec.datasets` without introducing a second canonical document family.
We should first add optional fields compatibly and prove the migration with the
existing `version` / `kind` contract.

Evidence:

- [compiler_report_spec.go](/Users/awitas/go/src/github.com/viant/agently-core/service/reporting/compiler_report_spec.go:97)

Decision:

- Prefer additive optional fields first:
  - `datasets`
  - `contextPreset` or `entryContext`
  - dataset scope policy
  - block binding metadata
- Add a distinct spec-version field only if compatibility pressure actually
  appears during rollout.

### 3. The current runtime is still too `primary`-special-cased

Claude's plan correctly assumes a dataset-first future, but implementation
cannot just add new schema and UI. Several live runtime paths still only refine
or derive request behavior for the `primary` dataset.

Evidence:

- request refinement only targets `primary`:
  [reportBuilderRuntimePreview.js](/Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderRuntimePreview.js:267)
- exported preview row refinement still derives from `primary` first:
  [reportBuilderRuntimePreview.js](/Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderRuntimePreview.js:706)
- filter bar scope options fall back to global options only for `primary`:
  [reportBuilderDocumentBlocks.js](/Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderDocumentBlocks.js:79)
- authored filter bar validation still treats `primary` as the special global
  case:
  [reportBuilderDocumentBlocks.js](/Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderDocumentBlocks.js:1245)
- semantic binding intentionally suppresses non-primary data blocks in some
  paths:
  [reportBuilderSelectedSemanticBinding.js](/Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderSelectedSemanticBinding.js:23)

Decision:

- Phase 0 must explicitly include a `primary`-assumption audit and removal plan.
- Any plan that does not retire those `primary` branches is incomplete.

### 4. UX must follow the wireframe direction, not only the data model

Claude suggested a new left-side datasets panel. That is reasonable as a tool,
but the wireframe direction is more opinionated:

- `1a` canvas + inspector
- `1b` outline + live preview
- `1c` query-first document

The common theme is reduced chrome, visible drill path, and a tighter
single-screen editing loop. A permanent heavy datasets rail would move in the
opposite direction if we are not careful.

Wireframe artifact:

- ![Report Builder Wireframe](/tmp/report-builder-wireframe-preview/Report Builder Wireframes.pdf.png)

Decision:

- Dataset authoring should likely live inside:
  - outline sections
  - block inspector
  - collapsible context/dataset panels
- Do not default to a large always-open dataset management rail.

### 5. Block refinements cannot be treated as purely authored/static

Claude positioned block scope as author-only in phase 1. That is too strict for
current behavior because the builder already supports runtime refinements and
drill behavior that affect what users inspect locally.

Evidence:

- [reportBuilderRuntimePreview.js](/Users/awitas/go/src/github.com/viant/forge/src/components/dashboard/reportBuilderRuntimePreview.js:279)

Decision:

- Keep the three-layer model:
  - context scope
  - dataset scope
  - block scope
- But allow local runtime block refinement to remain an explicit concept.
- The key rule is that dataset scope triggers fetches, while block scope should
  stay local unless promoted into dataset scope deliberately.

## Revised Green-Light Conditions

Implementation is approved if the migration plan commits to all of the
following:

1. Canonical persistence/export remains `reportDocument` / `reportSpec` /
   `reportFill.datasets[]` / `reportPrint` / `reportExportRequest`.
2. `primary`-only runtime branches are identified and retired phase by phase.
3. Seeded forecasting/performance context becomes first-class and metadata
   driven.
4. Dataset scope policy is declarative and persisted.
5. The UX follows the wireframe direction of lower chrome and tighter
   outline/canvas/inspector loops.
6. The first end-to-end proof target is the forecasting audience dashboard built
   from the provided `forge-data` datasets.

## Final Recommendation

Proceed with the migration plan, but use Claude's output as the first draft,
not the final contract.

The approved implementation direction is:

- dataset-first
- compatibility-first
- metadata-driven
- wireframe-aligned
- canonical-model-preserving
