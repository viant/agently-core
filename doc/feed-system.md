# Feed system

A **feed** is a declarative recipe that turns a tool's JSON output into a
forge UI `DataSource` for live dashboards. Feeds are defined alongside the
tools they activate on, matched by service/method, and projected with the
same path engine used by the datasource layer.

## Packages

| Path | Role |
|---|---|
| [protocol/tool/metadata.go](../protocol/tool/metadata.go) | `FeedSpec`, `DataSource`, `MatchSpec`, `ActivationSpec` |
| [protocol/tool/feed.go](../protocol/tool/feed.go) | Feed emission types |
| [internal/feedextract/](../internal/feedextract/) | Projection engine (dot + `[idx]` + `Derive` + `UniqueKey`) |
| [sdk/feed.go](../sdk/feed.go), [sdk/feed_resolver.go](../sdk/feed_resolver.go), [sdk/feed_notifier.go](../sdk/feed_notifier.go) | Registry, resolver, SSE notifier |
| Workspace `feeds/*.yaml` | Feed declarations |

## Activation

A FeedSpec declares how it activates:

- `kind: history` — scan recent tool calls on turn completion and project matching payloads. Good for dashboards built from "what just happened".
- `kind: tool_call` — on demand: the UI requests data; the runtime executes the tool and projects. Used by charts that need fresh data without side effects.

Activation can be scoped to `last` (newest matching call) or `all` (union).

## Projection vocabulary (shared with datasources)

- `selectors.data` — path into the tool output that yields rows.
- `uniqueKey` — dedup key across merged rows.
- `derive` — computed fields via `${field}` templates.
- `merge` — `append` | `union` | `merge_object` | `replace_last`.

The same engine powers [doc/lookups.md](lookups.md) datasources — feed specs
are "datasources for UI panels", datasources are "datasources for pickers".
Nothing unifies them at the YAML level today, but the projection code is shared at [internal/feedextract/extractor.go](../internal/feedextract/extractor.go).

## Internal + external unification

Feed specs work identically against internal tools (`system/exec`, `llm/agents`, …) and MCP-external tools. The match happens on the fully qualified `service:method` name the registry uses; the underlying tool backend is irrelevant to the feed layer.

## SSE surface

Feed lifecycle events travel on the streaming bus:

- `tool_feed_active` — feed just populated with fresh data.
- `tool_feed_inactive` — feed cleared (tool result aged out).

See [doc/streaming-events.md](streaming-events.md). Web clients subscribe via `/v1/feeds/{id}/data` and `GET /v1/feeds` for discovery.

## Example

```yaml
id: performance-window
match: { service: platform, method: metrics_report }
activation: { kind: history, scope: last }
dataSources:
  performance:
    selectors: { data: "rows" }
    uniqueKey: [{ field: project_id }]
    derive:
      variance: "${actual_spend - planned_spend}"
```

## Extensibility

- **New activation kind**: add to `ActivationSpec.Kind` + an evaluator in `sdk/feed_resolver.go`.
- **Custom merger**: extend `Merge` options in `internal/feedextract/`.
- **Custom UI shape**: the forge `Container` under `FeedSpec.UI` is passed to the client verbatim — whatever forge renders.

## Related docs

- [doc/lookups.md](lookups.md) — shared projection engine, different consumer.
- [doc/streaming-events.md](streaming-events.md)
- [doc/tool-system.md](tool-system.md)
