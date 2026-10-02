# Forge window resource assignment

Workspace Forge assets live in `extension/forge/{datasources,dialogs,models}` and
are shared by every window of a deployment. Historically the window loader merged
**all** of them into **every** window, so a Steward deployment leaked hundreds of
advertiser/campaign/order datasources, dialogs and resource models into unrelated
windows such as the Agent window. Windows now own an explicit whitelist.

## Contract

A window declares the workspace assets it needs in a `resources` block at the root
of its definition (the `main.yaml` of each fully defined branch, or the flat
`<window>.yaml`). Forge ignores the key; Agently reads it with the same import and
target resolution as the window itself, so `$import(...)` works inside it.

```yaml
resources:
  dataSources:                 # extension/forge/datasources/**/*.yaml by id
    - advertiser_properties
    - resource_authorization
  dialogs:                     # extension/forge/dialogs/*.yaml by id
    - advertiserCampaignCreate
  models:                      # extension/forge/models/<name>.yaml, whole registry
    - advertiser
  schemas: []                  # individual schema names (optional)
  resourceModels: []           # individual resource model names (optional)
```

`resources: {}` (or no block) means the window uses nothing from the workspace
beyond what its own declarations need. Built-in application windows (for example
`metadata/window/agent`) may carry the same block; without it they receive no
workspace assets.

## What gets attached

`MergeWorkspaceForgeAssets` attaches exactly:

1. the assigned assets, and
2. their **transitive dependencies**, following the Forge reference vocabulary:
   - datasource → `resourceModelRef`, parent `dataSourceRef`
   - dialog → `dataSourceRef`, `lookup.dataSource`, nested `dialogId`,
     `window.openDialog` handler args, `modelRef`
   - resource model → `schemaRef`, read/write `dataSourceRef`, field `modelRef`
   - schema → `$ref`
   - window-local datasources, dialogs, schemas and resource models are seeds too,
     so a local datasource may still pull its global resource model.

Window-owned declarations always win over workspace assets with the same name.
Invalid or conflicting workspace assets are quarantined as before; an explicitly
assigned dialog that fails validation is an error.

## Validation

After attaching, the loader validates the effective window and fails the load when
any reference cannot be satisfied by an attached asset:

- **YAML**: every `dataSourceRef`, `dataSourceRefs` alias, `lookup.dataSource`,
  `dialogId`, dialog handler argument, `modelRef`/`resourceModelRef` and
  `schemaRef`/`$ref` in the view, dialogs, window settings and authorization.
- **Action code**: every JavaScript string literal (single, double or template
  quoted; comments and regex literals are skipped) that names a workspace dialog
  or datasource, which covers `openDialog?.({execution: {args: ['id']}})`,
  `closeDialog?.({dialogId: 'id'})`, `context.Context?.('datasource_id')` and
  lookup maps such as `{create: 'draftDialog', update: 'editDialog'}`.
- Assigned identities that no workspace asset provides are kept as declared
  intent and logged as unresolved; they never block the window. This lets a
  window keep declaring an asset that action code expects (for example Steward's
  `advertiser_custom_segment_patch`) until the workspace provides it.
- References to identities unknown to the workspace (host-provided pickers, etc.)
  are logged, not failed.
- Both kinds of unresolved identity are also surfaced in the browser console:
  the loader wraps the window's action code so `console.warn("[forge workspace]
  window <key> ...")` runs when the window's actions are imported.
- Dialog ids computed at runtime (`dialogId = byKind[kind]`, `{dialogId, …}`,
  `args: [variable]`, templates containing "dialog") cannot be checked statically
  and are logged once per window so the author can confirm the whitelist.

The error message lists the missing assignments by kind, e.g.

```
window campaign references workspace assets that are not assigned:
  resources.dataSources is missing: campaign_performance_period_7d
  resources.dialogs is missing: campaignFlightDraft
```

## Deriving a whitelist

`AnalyzeWorkspaceWindow(ctx, windowKey, target)` loads a workspace window without
merging and reports the workspace assets its YAML and action code reference
directly (`Suggested`), the declared block, unknown identities and dynamic dialog
sites. Run it per target (web, ios/phone, ios/tablet, android/phone) and declare
the union in the branch `main.yaml` that fully defines the window.

## Tests

`service/ui/window/assignment_test.go` covers exclusion, transitive attachment,
validation and the code scanner with synthetic fixtures.
`steward_regression_test.go` loads every real Steward window and the real Agently
Agent window against the Steward workspace when those checkouts are present
(`AGENTLY_STEWARD_WORKSPACE`, `AGENTLY_METADATA_ROOT`, or the default GOPATH
sibling layout) and skips otherwise.
