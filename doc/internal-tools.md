# Internal tools (in-process MCP-style services)

Agently-core ships a suite of built-in tools served from in-process "MCP-like"
services. They share the same registry, the same invocation contract, and
the same streaming lifecycle as external MCP tools — so the agent (and the
rest of the runtime) cannot tell internal apart from external.

## Catalog

| Service | Path | Purpose |
|---|---|---|
| `llm/agents` | [protocol/tool/service/llm/agents/](../protocol/tool/service/llm/agents/) | Start/status/cancel a child agent (the A2A bridge) |
| `llm/skills` | [protocol/tool/service/skill/](../protocol/tool/service/skill/) | List / activate workspace skills ([doc/skills.md](skills.md)) |
| `prompt` | [protocol/tool/service/prompt/](../protocol/tool/service/prompt/) | `prompt:list` / `prompt:get` for profiles ([doc/prompts.md](prompts.md)) |
| `template` | [protocol/tool/service/template/](../protocol/tool/service/template/) | `template:list` / `template:get` for output templates ([doc/templates.md](templates.md)) |
| `orchestration/plan` | [protocol/tool/service/orchestration/plan/](../protocol/tool/service/orchestration/plan/) | Emit / advance a structured plan |
| `system/exec` | [protocol/tool/service/system/exec/](../protocol/tool/service/system/exec/) | Shell + long-lived process orchestration ([doc/async.md](async.md)) |
| `system/patch` | [protocol/tool/service/system/patch/](../protocol/tool/service/system/patch/) | Apply patches (diff) to workspace files |
| `system/os` | [protocol/tool/service/system/os/](../protocol/tool/service/system/os/) | File / dir / env introspection |
| `system/image` | [protocol/tool/service/system/image/](../protocol/tool/service/system/image/) | Image helpers |
| `system/platform` | [protocol/tool/service/system/](../protocol/tool/service/system/) | Platform detection (web vs iOS vs Android renderers) |
| `resources` | [protocol/tool/service/resources/](../protocol/tool/service/resources/) | Read / list resources (local + MCP-backed) |
| `message` | [protocol/tool/service/message/](../protocol/tool/service/message/) | Append messages to a conversation from a tool body |
| `printer` | [protocol/tool/service/printer/](../protocol/tool/service/printer/) | Structured output emission |

## Unified with external MCP

- Internal services register under the same `service:method` namespace as external MCP tools.
- The registry ([internal/tool/registry/](../internal/tool/registry/)) picks the right executor by name — nothing in the tool call site knows or cares.
- Every internal service can publish instruction blocks via [protocol/mcp/expose/](../protocol/mcp/expose/), resources via the same mechanism, and prompts/templates the same way.
- Auth flows through `context.Context` identically; internal services that need identity read it via [internal/auth/](../internal/auth/).

This unification is why the agent's tool list, prompt binding, feed matching,
and overlay targeting all work uniformly across internal + external. See [doc/tool-system.md](tool-system.md).

## Lifecycle events

All tool calls — internal or external — emit `tool_started` / `tool_completed` events on the streaming bus. Internal services additionally may emit feed events, elicitation requests, and cancellation signals via the same channels.

## Extensibility

Adding a new internal service is identical to adding an external MCP server:

1. Implement a service at `protocol/tool/service/<name>/` with methods callable via `service:method`.
2. Register it in the runtime builder.
3. If it should appear in the model's tool list, publish its schema through `expose.Provider`.
4. Optionally register `FeedSpec` under workspace `feeds/` so the UI can consume its output.

From the caller's side, `Registry.Execute(ctx, "mynamespace:mymethod", args)` works the same day one.

## Related docs

- [doc/tool-system.md](tool-system.md)
- [doc/mcp-integration.md](mcp-integration.md)
- [doc/skills.md](skills.md), [doc/prompts.md](prompts.md), [doc/templates.md](templates.md), [doc/async.md](async.md)
