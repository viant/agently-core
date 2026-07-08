# Claude Prompt: Report Builder To Multi-Dataset Dashboard Composer

Design a migration plan to evolve Forge Report Builder into a multi-dataset
dashboard composer.

## Context

Current system shape:

- Forge `dashboard.reportBuilder` is centered on one primary runtime dataset /
  one main request flow.
- Authored blocks already exist and compile into canonical reporting models:
  - `tableBlock`
  - `chartBlock`
  - `geoMapBlock`
  - `kpiBlock`
  - `markdownBlock`
  - `filterBarBlock`
  - `refinementBarBlock`
- `datasetRef` already exists on blocks, but the user experience is still
  mostly single-dataset-first.
- `agently-core` already owns persistence and export:
  - `save_report`
  - `get_report`
  - `list_reports`
  - `update_report`
  - `export_report`
- steward already seeds builder context for:
  - forecasting line / audience
  - performance order

Target system shape:

- a dashboard composer where each dataset is explicitly declared
- each dataset can have dedicated scope / filters
- blocks bind to datasets, not just a single `primary`
- entry context like forecasting line or performance order is first-class and
  can be inherited into each dataset
- persistence and export still compile through existing canonical models:
  - `reportDocument`
  - `reportSpec`
  - `reportFill`
  - `reportPrint`
  - `reportExportRequest`

The motivating example is a dashboard built from multiple `forge-data`
datasets such as:

- summary
- scope context
- category summary
- daily timeline
- findings
- input audit
- channel mix
- state mix
- age group
- site concentration

## Requirements For The Plan

Produce a concrete implementation-oriented migration plan with:

1. architecture direction
2. schema changes
3. runtime request / dataset execution model
4. UI / UX changes
5. compatibility strategy
6. persistence / reopen / export implications
7. steward integration implications
8. risks and pushback areas
9. rollout phases
10. acceptance criteria for each phase

## Constraints

- prefer minimal new abstractions
- reuse current Forge report/export contracts
- do not introduce a separate persistence system outside `agently-core`
- do not rely on opaque hook-only scope propagation as the primary mechanism
- do not propose a big-bang rewrite
- keep current forecasting / performance builders working during migration
- avoid browser-side cross-dataset joins in the first implementation phase

## Explicit Questions To Answer

1. What are the new first-class concepts?
2. How should context scope, dataset scope, and block scope differ?
3. How should seeded contexts like forecasting line and performance order be
   represented?
4. How should block-to-dataset binding be authored?
5. How do we preserve backward compatibility for current single-dataset
   builders?
6. What is the first real migrated dashboard target that proves the model?

## Output Format

Respond with these sections:

- `Summary`
- `Target Architecture`
- `New Schema`
- `Runtime Model`
- `UX Model`
- `Migration Phases`
- `Risks / Pushback`
- `Acceptance Gates`
- `Recommended First Target`

Be concrete. Avoid generic architecture language.
