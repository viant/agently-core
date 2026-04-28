# Planning & intake

Before the reactor starts its ReAct loop, an **intake** sidecar classifies the
user's query and selects a **prompt profile + tool bundle**. This keeps the
main orchestrator's system prompt small and lets the runtime inject the right
scope per turn.

## Packages

| Path | Role |
|---|---|
| [service/intake/](../service/intake/) | Pre-turn sidecar: confidence-scored profile selection |
| [protocol/agent/plan/](../protocol/agent/plan/) | Plan schema the model emits (steps, tool refs, dependencies) |
| [service/reactor/service_plan.go](../service/reactor/service_plan.go) | Plan consumption: step expansion, dispatch |
| [protocol/tool/service/orchestration/plan/](../protocol/tool/service/orchestration/plan/) | Exposed `orchestration/plan:*` tools |

## Intake sidecar

Runs before the reactor invokes the model:

1. Classifies the query (intent, profile candidates).
2. Emits a `TurnContext` with `SuggestedProfileId`, `Confidence`, `ToolBundles`.
3. When confidence ≥ threshold, the reactor pre-populates `RunInput.PromptProfileId` + `ToolBundles` and the orchestrator skips `prompt:list`.
4. Below threshold, the orchestrator runs `prompt:list`, reasons, and selects a profile itself.

See [doc/prompts.md](prompts.md) for the profile + bundle format.

## Plan schema

The model emits (or the reactor constructs) a plan with typed step kinds:

- `tool_call` — synchronous tool
- `child_agent` — delegate via `llm/agents`
- `async_op_start` — long-running operation (see [doc/async.md](async.md))
- `wait_for` — gate on a prior step's output
- `final` — terminal assistant response

Each step carries dependencies (`waitsFor: [stepId]`) and a dispatch mode. The
reactor topologically orders ready steps and fans out parallel-safe work.

## Extensibility

- **New step kind**: extend `protocol/agent/plan` + teach the reactor to dispatch it.
- **Custom intake classifier**: implement `intake.Classifier` and wire into the builder.
- **Per-profile guardrails**: add validators under `service/intake/` that reject profiles lacking required bundles.

## Related docs

- [doc/prompts.md](prompts.md) — profile + bundle details.
- [doc/agent-orchestration.md](agent-orchestration.md) — how the plan is executed.
- [doc/async.md](async.md) — long-running steps.
