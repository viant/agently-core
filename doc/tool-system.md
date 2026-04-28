# Tool system

Tools are the unit of work the reactor dispatches. Internal tools (e.g. `llm/agents`, `prompt:*`, `template:*`, `system/exec`) live in-process; external tools come from MCP servers. A single registry exposes both.

## Key packages

| Responsibility | Path |
|---|---|
| Registry + dispatch | [internal/tool/registry/](../internal/tool/registry/) — `Registry.Execute(ctx, name, args)` |
| Policy (allow / block / approval mode) | [protocol/tool/policy.go](../protocol/tool/policy.go) + `service/shared/toolapproval/` |
| Metadata (feed, datasource, activation) | [protocol/tool/metadata.go](../protocol/tool/metadata.go), [protocol/tool/feed.go](../protocol/tool/feed.go) |
| In-process tool implementations | [protocol/tool/service/](../protocol/tool/service/) (llm/agents, prompt, skill, template, orchestration/plan, system/*, message, resources, printer, …) |
| Tool bundles (declarative groupings) | [protocol/tool/bundle/](../protocol/tool/bundle/), `workspace/repository/toolbundle/` |
| MCP tool bridge | [protocol/mcp/proxy/](../protocol/mcp/proxy/), auth via [protocol/mcp/manager/auth_token.go](../protocol/mcp/manager/auth_token.go) |

## Execution path

```
Registry.Execute(ctx, "service:method", args)
  └─ resolves tool name → local executor OR proxy to MCP client
  └─ attaches auth token via WithAuthTokenContext (MCP case)
  └─ dispatches with dedup / retry / timeout
  └─ runs result through applySelector if name has "|selector" suffix
  └─ emits tool_started / tool_completed / feed events
  └─ returns JSON string
```

Service:method naming is uniform across in-process and MCP tools — the registry strips the service prefix and picks the right backend.

## Bundles

Bundles group tools so an agent can reference `toolBundles: [analyst-baseline]` instead of enumerating tool names. See [doc/prompts.md](prompts.md) for how bundles drop orchestrator complexity.

## Feed specs

A `FeedSpec` declares how a tool's JSON result should project into UI-ready forge `DataSource`s. Activation can be passive (observe tool results when they happen) or on-demand (re-run the tool). See [doc/feed-system.md](feed-system.md).

## Approval

- `tool.Policy.Mode` = auto | ask | block.
- `AllowList` / `BlockList` narrow per-tool behaviour.
- Pending approvals queue at `pkg/agently/toolapprovalqueue/` and are surfaced to the UI via streaming events.
- Per-conversation overrides live under `service/shared/toolapproval/`.

## Extensibility

- **Add an in-process tool**: implement `ToolService` under `protocol/tool/service/<name>/` and register at runtime builder time.
- **Add an MCP server**: workspace `mcp/*.yaml` entries; the manager discovers and proxies automatically.
- **Add bundle**: drop a YAML under `<workspace>/tools/bundles/`.
- **Add feed spec**: declare it on the tool's `metadata.feed`; no runtime change needed.

## Related docs

- [doc/mcp-integration.md](mcp-integration.md)
- [doc/async.md](async.md) — long-running tool classes.
- [doc/feed-system.md](feed-system.md)
- [doc/prompts.md](prompts.md)
