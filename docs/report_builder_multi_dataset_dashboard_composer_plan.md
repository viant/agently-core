# Report Builder To Multi-Dataset Dashboard Composer

## Status

This document is the reviewed green-light plan approved for implementation.

Earlier subscription-auth Claude CLI attempts failed due to the org monthly
spend limit. On 2026-07-07, Claude was successfully run via API-key auth using
the local `--bare` flow, and its output was reviewed against the live codebase.

See:

- `claude_multi_dataset_dashboard_prompt.md`
- `report_builder_multi_dataset_claude_review.md`

## Goal

Evolve Forge `dashboard.reportBuilder` from a primary-query report composer into
a multi-dataset dashboard composer that can:

1. bind each dashboard block to its own dataset
2. allow each dataset to inherit or override contextual filters
3. support seeded entry contexts such as:
   - forecasting line / audience
   - performance order / campaign
4. preserve current report-builder persistence and export through
   `agently-core`

## Current Reality

Today the system already supports:

- canonical report persistence / export in `agently-core`
- authored report blocks in Forge:
  - `tableBlock`
  - `chartBlock`
  - `geoMapBlock`
  - `kpiBlock`
  - `markdownBlock`
  - `filterBarBlock`
  - `refinementBarBlock`
- `datasetRef` on blocks
- imported `forge-data` static dataset support
- seeded builder-open context in steward for forecasting and performance entry
  flows

But current Report Builder is still effectively centered on:

- one primary runtime dataset / one main request model
- one shared filter surface
- authored blocks layered around that primary runtime result

## What Must Change

The missing product and architecture concept is a three-layer filter model:

1. `context scope`
   - seeded by entry flow
   - e.g. order, line, campaign, targeting stack
2. `dataset scope`
   - each dataset decides how it consumes that context
   - inherit / override / append / exclude
3. `block scope`
   - optional local block refinements for cards or visuals

This requires moving from:

- one primary dataset with supporting authored blocks

to:

- a dashboard with multiple named datasets, each with an explicit request
  contract and filter policy

## Non-Goals

These should be explicitly out of scope for the first migration:

- arbitrary SQL or raw query authoring in the browser
- cross-dataset joins inside the browser composer
- per-block ad hoc scripting
- replacing current dashboard primitives outside Report Builder
- a big-bang rewrite of existing forecasting / performance builders

## Green-Light Architecture

### 1. Introduce a first-class dataset catalog in Report Builder config

Add a new top-level config concept under `reportBuilder`:

```yaml
datasets:
  - id: forecast_summary
    dataSourceRef: forecast_summary
    scope:
      inheritContext: true
      filters:
        window: last_3_days
  - id: forecast_daily_timeline
    dataSourceRef: forecast_daily_timeline
    scope:
      inheritContext: true
      filters:
        grain: day
```

Notes:

- `datasets[*].id` becomes the stable binding target for blocks
- `datasets[*].dataSourceRef` maps to an existing backend datasource or
  MCP-backed fetch contract
- each dataset can define its own scope policy

### 2. Add a context preset model

Add a `contextPreset` concept for entry flows:

```yaml
contextPresets:
  - id: forecasting_line
    kind: forecasting_line
    scopeSelectors:
      - audienceIds
      - adOrderIds
      - targetingIncl
      - targetingExcl
  - id: performance_order
    kind: performance_order
    scopeSelectors:
      - orderIds
      - campaignIds
      - audienceIds
```

The preset is responsible for:

- interpreting entry context from steward
- building canonical scope filters
- applying them into dataset scopes
- exposing them in UI as editable context filters

### 3. Add dataset scope policies

For each dataset:

```yaml
scope:
  inheritContext: true
  include:
    - audienceIds
    - dateRange
  exclude:
    - inventoryOnlyFlag
  overrides:
    pageSize: 10
    groupBy: country
```

Supported policies should be:

- `inherit`
- `append`
- `override`
- `exclude`

This must be declarative, not hidden in random hooks.

### 4. Make blocks dataset-first

Every dashboard block must bind to an explicit dataset:

```yaml
blocks:
  - id: posture_kpi
    kind: kpiBlock
    datasetRef: forecast_summary
  - id: timeline
    kind: chartBlock
    datasetRef: forecast_daily_timeline
```

We already partially support `datasetRef`; this becomes the default mental
model rather than an advanced case.

### 5. Split filter UI into three surfaces

UI should be organized into:

1. `Context filters`
   - inherited entry scope
   - order / line / audience / targeting stack / date window
2. `Dataset filters`
   - dataset-specific request shaping
   - top N, grain, comparison mode, ranking mode
3. `Block refinements`
   - local card-level changes

Without that split, the composer will remain confusing.

### 6. Preserve canonical reporting models

Do not invent a second persistence / export model.

Continue using:

- `reportDocument`
- `reportSpec`
- `reportFill`
- `reportPrint`
- `reportExportRequest`

The multi-dataset composer should compile down to those same models, with
multiple datasets in `reportSpec.datasets`.

## Migration Strategy

### Phase 0. Model Definition

Deliverables:

- `datasets` schema for Report Builder config
- `contextPresets` schema
- dataset scope policy schema
- docs showing how existing `primary` maps into the new model

Acceptance:

- existing single-dataset builders compile unchanged
- schema rejects ambiguous dataset ids and missing block dataset refs

### Phase 1. Compatibility Layer

Build a lowering layer:

- if no `datasets` are declared:
  - synthesize `datasets[0] = primary`
- if existing config uses current single-request model:
  - map it onto `primary`

Acceptance:

- no regressions for current forecasting / performance builders
- persistence / export outputs remain compatible

### Phase 2. Dataset Runtime Engine

Implement runtime request fan-out:

- compile context scope
- derive per-dataset request payloads
- fetch datasets independently
- materialize one `reportFill.datasets[*]` entry per dataset

Acceptance:

- report runtime can render blocks backed by multiple datasets
- failures in one dataset are isolated and surfaced at the block level

### Phase 3. UI Composer

Add:

- dataset list / dataset editor
- context preset panel
- per-dataset filter panel
- explicit dataset binding in block editor

Acceptance:

- user can author a dashboard with:
  - summary dataset
  - timeline dataset
  - findings dataset
  - audit dataset
- each block shows its bound dataset clearly

### Phase 4. Steward Context Integration

Enhance steward entry flows so:

- `forecastingCubeBuilder` seeds `contextPreset=forecasting_line`
- `metricReportBuilder` seeds `contextPreset=performance_order`

Acceptance:

- opening builder for line/order visibly populates context filters
- dataset scopes inherit from that context without custom ad hoc glue

### Phase 5. Persistence, Reopen, Export Hardening

Ensure:

- saved report files preserve:
  - datasets
  - context preset
  - dataset scope policies
  - block bindings
- reopen works for multi-dataset dashboards
- export works for dashboards whose blocks draw from multiple datasets

Acceptance:

- save / load / export works for a real multi-dataset forecasting review
- PDF and XLSX export still run through `agently-core`

## Pushback / Red Flags

These are the areas I would push back on if Claude proposed them:

### Reject: big-bang replacement

Do not replace current `reportBuilder` with a new product in one step.

Why:

- forecasting / performance builders already rely on current behavior
- persistence / export already landed on current canonical models

Required alternative:

- additive migration with compatibility layer

### Reject: per-block raw datasource config first

Do not start with every block defining ad hoc request JSON.

Why:

- it will explode complexity
- persistence and reopen become unstable

Required alternative:

- datasets are first-class, blocks bind to datasets

### Reject: hidden hook-only scope propagation

Do not rely primarily on JS hooks like forecasting builder custom glue to
propagate dataset scope.

Why:

- impossible to reason about
- hard to persist and reopen

Required alternative:

- explicit dataset scope policy in declarative config

### Reject: browser-side joins as phase 1

Do not attempt cross-dataset transformations or joins in the browser in the
first pass.

Why:

- creates a second analytics engine in UI

Required alternative:

- datasets stay independent in phase 1
- composition happens at layout and presentation layer first

## Recommended First Real Target

Use your `Audience forecast review` payload as the first migration target.

Map it as:

- `forecast_summary`
  - KPI / posture summary
- `forecast_scope_context`
  - context card / entity metadata
- `forecast_category_summary`
  - category comparison table / KPI tiles
- `forecast_daily_timeline`
  - timeline chart
- `forecast_input_audit`
  - audit table
- `forecast_channel_mix`
  - mix chart
- `forecast_state_mix`
  - geo map
- `forecast_site_concentration`
  - concentration table
- `forecast_findings`
  - findings markdown / ranked list

That is the best “does the architecture really work?” benchmark.

## Acceptance Gates For Green Light

I would only give implementation green light if the plan commits to all of
these:

1. Backward compatibility for single-dataset builders.
2. First-class `datasets` model, not only ad hoc block requests.
3. First-class `contextPreset` model.
4. Declarative dataset scope policy.
5. Multi-dataset compile down to current canonical report/export models.
6. No new persistence system outside `agently-core`.
7. Forecasting line and performance order become the first seeded contexts.
8. A real migrated forecasting review dashboard is the phase-1 proof target.

## Green-Light Recommendation

Proceed with implementation.

Approval conditions:

- compatibility-first rollout
- dataset-first model
- context preset abstraction
- declarative scope inheritance
- real forecasting dashboard as the first acceptance target

This is the implementation plan I would green-light.
