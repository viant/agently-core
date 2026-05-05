# Agent orchestration & ReAct loop

The orchestration layer turns a single `Query(ctx, input)` call into a
multi-turn conversation: it plans, executes tools, streams model output,
handles elicitation, and recovers from context overflow — all while
persisting every observable event.

## Entry points

- [service/agent/run_query.go](../service/agent/run_query.go) — `Service.Query` is the top-level entry; it wraps an `agentsvc.QueryInput` into a conversation turn.
- [service/reactor/](../service/reactor/) — the ReAct loop: plan → act → observe → iterate. Owns model invocation, tool dispatch, approval gating, async operation start, and response synthesis.
- [service/intake/](../service/intake/) — pre-reactor sidecar that classifies the user query, selects a prompt profile, and seeds `TurnContext` before the main loop runs.

## Core moving parts

| Responsibility | Where |
|---|---|
| Turn creation + lifecycle | [service/agent/run_query.go](../service/agent/run_query.go), [pkg/agently/turn/](../pkg/agently/turn/) |
| Plan emission | [protocol/agent/execution/](../protocol/agent/execution/) + [service/reactor/service_plan.go](../service/reactor/service_plan.go) |
| Tool call dispatch | [service/shared/toolexec/](../service/shared/toolexec/) + [internal/tool/registry/](../internal/tool/registry/) |
| Streaming model output | [runtime/streaming/](../runtime/streaming/) + `genai/llm/` providers |
| Elicitation mid-turn | [service/elicitation/](../service/elicitation/) (see [elicitation-system.md](elicitation-system.md)) |
| Context-overflow recovery | [service/reactor/overflow.go](../service/reactor/overflow.go), [service/reactor/service_context_limit.go](../service/reactor/service_context_limit.go) |
| Async operation kick-off & parent-turn gating | [doc/async.md](async.md) |

## High-level flow

```
Query(ctx, input)
  └─ intake sidecar: classify, pick profile, seed TurnContext
  └─ reactor loop:
        ├─ build prompt (see prompt-binding.md)
        ├─ invoke LLM (streaming)
        ├─ parse tool_calls / plan steps
        ├─ for each step:
        │    ├─ approval (if policy requires)
        │    ├─ tool execute / child agent run / async op start
        │    └─ observation back to the loop
        ├─ on context overflow → prune + retry without losing transcript
        └─ synthesize final assistant message
  └─ persist turn, messages, tool calls (see conversation-model.md)
```

## Extensibility hooks

- **New tool type**: register via [internal/tool/registry/](../internal/tool/registry/) — see [tool-system.md](tool-system.md).
- **Pre-turn routing**: add/replace intake stages under [service/intake/](../service/intake/).
- **Plan shape**: extend [protocol/agent/execution/](../protocol/agent/execution/) types; the reactor consumes whatever the model emits that matches the schema.
- **Approval policy**: `tool.Policy` on the runtime controls allow/block lists and approval mode.
- **Streaming observers**: subscribe via [runtime/streaming.Bus](../runtime/streaming/) — see [streaming-events.md](streaming-events.md).

## Related docs

- [doc/async.md](async.md) — how long-running operations (shell, child agents, external services) integrate with the reactor.
- [doc/prompts.md](prompts.md) — how prompt profiles + tool bundles reduce orchestrator complexity.
- [doc/skills.md](skills.md) — SKILL.md support for reusable agent capabilities.
