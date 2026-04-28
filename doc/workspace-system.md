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
