# Review Rubric: Multi-Dataset Dashboard Composer Plan

Use this rubric to review a Claude-authored plan before approving it.

## Automatic Rejects

Reject the plan if it does any of the following:

1. Proposes a big-bang rewrite of Report Builder.
2. Introduces a second persistence / export system outside `agently-core`.
3. Makes per-block ad hoc raw request JSON the primary authoring model.
4. Depends mainly on hidden JS hooks for dataset scope propagation.
5. Requires browser-side cross-dataset joins in phase 1.
6. Breaks current forecasting or performance builders during migration.

## Required Architecture Qualities

The plan must include all of these:

1. A first-class `datasets` model.
2. A first-class seeded context model such as `contextPreset`.
3. A clear three-layer filter model:
   - context scope
   - dataset scope
   - block scope
4. Explicit dataset scope policies such as:
   - inherit
   - append
   - override
   - exclude
5. Blocks binding to datasets explicitly.
6. Multi-dataset composition still lowering into existing canonical report
   contracts.

## Compatibility Requirements

The plan must clearly explain:

1. How existing single-dataset builders map to the new dataset model.
2. How current persisted report files remain reopenable.
3. How forecasting and performance seeded entry flows continue to work while
   migration is in progress.
4. How current export contracts remain valid.

## Runtime Requirements

The plan must describe:

1. How per-dataset requests are built.
2. How per-dataset fetch failures are isolated.
3. How multi-dataset `reportFill.datasets[]` is materialized.
4. How dataset-local scope and context-inherited scope merge.

## UX Requirements

The plan must describe:

1. A visible distinction between:
   - context filters
   - dataset filters
   - block refinements
2. How a user adds / edits datasets.
3. How a user assigns a block to a dataset.
4. How seeded entry context is surfaced in the builder.

## Acceptance Gates

A plan is not green-light ready unless it defines:

1. phase-by-phase deliverables
2. explicit non-goals
3. rollout gating criteria
4. at least one real migrated dashboard target

## Best First Target

Prefer the forecasting review dashboard as first proof target because it
requires:

- multiple datasets
- context seeding
- multiple block kinds
- audit / findings / KPI / timeline composition

If a plan does not select a similarly demanding real target, push back.
