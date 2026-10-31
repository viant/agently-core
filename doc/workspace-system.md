# Workspace system

A workspace is a directory of YAML describing the agents, tools, models,
prompts, datasources, and overlays a deployment needs. Everything except code
lives here, and the runtime reloads it live.

## Anatomy

```
<workspace>/                          # env: AGENTLY_WORKSPACE (default: ./.agently)
├── agents/
├── models/
├── embedders/
├── mcp/
├── workflows/
├── skills/
├── tools/            tools/bundles/  tools/instructions/
├── templates/        templates/bundles/
├── prompts/
├── feeds/
├── oauth/
├── a2a/
├── callbacks/
└── extension/
    └── forge/
        ├── datasources/   # see doc/lookups.md
        ├── dialogs/
        └── lookups/
```

## Key packages

| Responsibility | Path |
|---|---|
| Kind constants + root resolution | [workspace/workspace.go](../workspace/workspace.go) |
| Generic YAML repository `Repository[T]` | [workspace/repository/base/repository.go](../workspace/repository/base/repository.go) |
| Per-kind repositories | [workspace/repository/*/](../workspace/repository/) |
| Live reload (hotswap) | [workspace/hotswap/](../workspace/hotswap/) |
| Service layer (routing, validation) | [service/workspace/](../service/workspace/) |
| Codec | [workspace/service/meta/](../workspace/service/meta/) |
| Bootstrap defaults | [bootstrap/](../bootstrap/) |

## Kinds

`workspace.AllKinds()` enumerates the predefined kinds; adding a new one is a
constant + a per-kind repository wrapper (see [workspace/repository/forgedatasource/](../workspace/repository/forgedatasource/) for a minimal example).

## Loading pipeline

1. Boot: `workspace.Root()` resolves the directory (CLI > env > `.agently`).
2. `bootstrap.EnsureDefaults()` seeds minimal config if missing.
3. Executor builder ([app/executor/builder.go](../app/executor/builder.go)) wires repositories into the runtime.
4. Services read YAML on demand via their per-kind repository.
5. Hotswap watcher notifies services when a file changes; services swap in-memory caches atomically.

Optional `policy.authorization` configuration delegates report, window,
starter-prompt, and prompt-profile visibility to a workspace MCP tool. See
[doc/authorization-policy.md](authorization-policy.md).

## File layout per kind

- **Flat**: `<kind>/<name>.yaml` (default).
- **Nested**: `<kind>/<name>/<name>.yaml` (legacy; still read when present).

Both layouts coexist — writes go to the flat form.

## HTTP surface

- `GET /v1/workspace/resources?kind=<kind>`
- `GET /v1/workspace/resources/{kind}/{name}`
- `PUT`, `DELETE` — for admin/CLI edits.
- Export / import: `/v1/workspace/resources/{export,import}`.

## Extensibility

- **New kind**: add to `workspace.AllKinds()`, create `workspace/repository/<kind>/` wrapping `Repository[T]`, register consumer.
- **Schema validation**: implement per-kind validator in `service/workspace/`.
- **Live reload consumer**: subscribe to hotswap events and re-read as needed.

## Related docs

- [doc/lookups.md](lookups.md) — `extension/forge/*` subtree.
- [doc/prompts.md](prompts.md) — prompt + template + bundle YAML.
- [doc/auth-system.md](auth-system.md) — `oauth/` config.
- [doc/authorization-policy.md](authorization-policy.md) — shared MCP visibility authorization.


### Optional section navigation presentation

`ui/layout.yaml` may select `left.navigation.presentation: sections`. The only
accepted nonempty value is `sections`; omission preserves the existing host
layout. This presentation is serialized to the host without changing navigation
permissions, application sources, action parameters or window identity. Agently
owns the rail, section menus and conversation-state behavior. Workspace assets
continue to own brand values. The workspace layout tests cover validation and
JSON delivery, and run in the existing design-system workflow.

### Optional task and composer presentation

Agent YAML may supply `starterTasks[].resultLabel`, exposed as the optional
`StarterTask.ResultLabel` field in the protocol and workspace metadata JSON:

```yaml
starterTasks:
  - id: review-data
    title: Review data
    description: Prepare a request from the available data.
    resultLabel: Inline overview
    prompt: Review the available data.
```

This is a short presentation caption, not an execution type, permission,
persisted-output promise or replacement for the prompt. Agently preserves it
when normalizing task metadata and passes it to Forge's document task browser,
which displays and searches the caption. Omission leaves the caption absent.

Workspace `config.yaml` may also supply an optional composer hint:

```yaml
ui:
  composer:
    placeholder: Describe the work
```

`Root.Composer()` publishes this value under `composer.placeholder` in workspace
metadata; the Go SDK exposes `WorkspaceComposer.Placeholder`. Agently supplies
it to the existing named lookup input through Forge's composer input props.
An absent or empty hint keeps the existing input default. It does not change
drafts, task prompts, agent/model selection or sending behavior. Workspace
configuration owns the wording; the public contract and examples stay generic.
The existing workspace metadata and composer-config tests cover these optional
fields alongside the retained selector defaults.

### Canonical assistant finals in client projections

The TypeScript chat-store projector exposes the existing
`ClientTurnState.assistantFinal` on `IterationRenderRow`. Hosts can inspect its
canonical rendered content even when the turn has no execution pages. This
projection adds no wire contract and does not manufacture rounds. Consumers
retain the row's host-trust admission checks and distinguish committed supported
reports from ordinary answers, incomplete assemblies and diagnostics.

Agently adapts admitted reports to its existing workspace-object contract and
reads their current source from the conversation store. Its fallback identity
uses conversation, turn (message fallback), scope and report ID; revisions/reset
generations update that object. A matching report ID in a different turn does
not establish shared identity without an explicit backend descriptor. Core does
not infer report persistence or product placement from starter-task captions.
The existing SDK tests and type check cover final projection; host lifecycle
checks do not establish authenticated access recovery or cross-device retention.

## Application stylesheet overrides

Agently uses its workspace theme by default. To allow an MCP application's
linked stylesheets to override that appearance, opt in on the host's source:

```yaml
applicationSources:
  - connection: mcp://workspace/catalog
    resource: navigation://example/main
    applicationId: example
    allowStyleOverrides: true
```

Omitted or false disables delivery of `styleResources` in the application
projection. A provider cannot enable this option in its navigation payload.
The UI removes previously installed application sheets when the option is
turned off. Local workspace themes, authored layout and chart colors remain
unchanged. Original verified artifacts and producer preview CSS are retained;
this option does not rewrite signed or fingerprinted bundle bytes.
