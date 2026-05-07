# Planner

Planner is a **feature for strategy selection before execution**.

It exists for turns where:

- a direct static route is too brittle
- the workspace has several plausible execution shapes
- the runtime needs a validated strategy before the normal worker/reactor pass

Planner does **not** answer the user directly.
Planner does **not** execute business tools.
Planner produces a structured execution strategy that the normal turn then
follows.

## Problem It Solves

Static routing is fast and cheap when the request clearly fits one known path.
But some turns do not fit cleanly:

- the user asks for something mixed or exploratory
- the request spans several scenario families
- the best route depends on tool capabilities and output shape, not just tags
- the workspace wants extra guardrails before the worker starts

Without planner, the runtime has two bad options:

1. force a static route and risk the wrong profile / template / evidence plan
2. push all routing ambiguity into the main execution pass and hope the worker
   self-corrects

Planner solves that by inserting one planning-only model pass before execution.
That pass chooses the strategy, validates it, and turns it into explicit runtime
guidance.

## What Planner Is

Planner is:

- an **optional pre-execution feature**
- a **tool-disabled LLM pass**
- a **workspace/planner-agent contract consumer**
- a **runtime materializer** that converts validated planner output into:
  - execution input changes
  - planner guidance system documents

Planner is not:

- a generic replacement for static routing
- a universal fallback for every creative phrase
- a place for workspace-specific schema hardcoding in core

## When It Runs

Planner only runs when the turn is put in `ModePlanner`.

That can happen through:

- workspace intake / router choosing planner mode
- explicit creative / exploratory trigger rules
- low-confidence routing fallback
- planner-validator retry/second-failure policy paths

If planner mode is not selected, the normal turn runs without planner.

## Position In The Stack

```text
user request
  -> intake / routing
       -> static execution route
       -> planner mode
       -> clarify

  -> agent.Query
       -> maybeRunIntakeSidecar
       -> maybeRunPlannerPass      (only in planner mode)
            -> planner LLM call    (tools disabled)
            -> parse + validate
            -> apply planner output
            -> persist planner guidance docs
       -> normal execution pass
            -> reactor / worker / tools
```

So planner is not the end-user answering layer.
It is the layer that shapes the next execution pass.

## Core Architecture

`agently-core` owns the **planner mechanism**, not the workspace-specific
planning language.

Core owns:

- planner pass lifecycle
- planner-mode execution with tools disabled
- contract resolution
- parse / validate / retry loop
- applying validated output into runtime input
- persisting planner guidance docs
- planner events / observability

Workspace planner agents own:

- planner schema
- planner validation metadata
- planner-specific planning language

This is the important boundary:

- **core** = planner engine
- **planner agent** = planner contract

## Contract Boundary

Planner contracts are resolved per **planner agent**, not once per workspace.

That means a workspace can have several planners, each with a different
contract, and intake decides which planner agent to invoke.

Current contract path:

| Concern | Owner |
|---|---|
| Contract resolution | [service/planner/contract.go](../service/planner/contract.go) |
| Planner schema file | planner-agent directory |
| Planner validation rules file | planner-agent directory |
| Generic rule engine | [service/planner/validation.go](../service/planner/validation.go) |
| Output application / guidance rendering hooks | [service/planner/application.go](../service/planner/application.go) |

For Steward, the active planner-agent contract lives next to the planner agent:

- [planner.schema.json](/Users/awitas/go/src/github.vianttech.com/viant/steward_ai/deployment/steward/agents/steward-planner/planner.schema.json)
- [planner.validation.json](/Users/awitas/go/src/github.vianttech.com/viant/steward_ai/deployment/steward/agents/steward-planner/planner.validation.json)

## Planner Input

The planner pass is intentionally richer than a normal router pass.

It can see:

- planner-mode system prompt
- workspace topology
- tool catalog / tool schema details
- allowed profile business knowledge
- visible skill business knowledge
- current user turn and recent turn context

It cannot call business tools.

That matters because planner is supposed to choose a strategy based on
**available contracts and evidence shapes**, not by executing the work itself.

## Planner Output

The planner returns structured strategy output defined by the planner-agent
contract.

After validation, core materializes that output in two ways:

1. **Execution input updates**
   - tool bundles
   - template id
   - parallel tool call policy
   - planner runtime context

2. **Planner guidance system documents**
   - strategy
   - evidence
   - guards
   - policy

Those guidance docs are then visible to the normal execution pass.

## Validation

Validation is a real feature, not a hack.

It exists because planner output is not safe to trust just because it matches
JSON shape.

Validation checks that the chosen plan is actually legal in the current runtime:

- referenced profiles exist
- referenced profiles are allowed for the execution agent
- referenced templates exist
- referenced templates are allowed
- structural rules like `executionOrder` being a subset of
  `requiredEvidence`

Important boundary:

- validation **rules** are planner-agent/workspace owned
- validation **engine** is core-owned and generic

That keeps policy local without duplicating runtime validation machinery.

## Retry And Failure Policy

Planner gets one correction round when validation fails.

Flow:

1. planner returns output
2. validation fails
3. validation errors are fed back to planner
4. planner gets one retry
5. if it still fails, second-failure policy applies

Second failure is handled explicitly:

- `clarify`
- `block`

The runtime does not silently continue with an invalid plan.

## Observability

Planner is explicit in runtime state and events.

Relevant pieces:

- [service/planner/turn_context.go](../service/planner/turn_context.go)
- [runtime/streaming/event.go](../runtime/streaming/event.go)
- [service/agent/planner_pass.go](../service/agent/planner_pass.go)

Planner emits real lifecycle events such as:

- planner selected
- planner output
- planner validated
- planner failed

And it persists planner guidance into the conversation as system-document
messages so later execution can read the exact strategy that was chosen.

## Cost Model

Planner costs an extra model call.

That cost is intentional and should only be paid when planner mode is genuinely
useful.

Good planner use cases:

- mixed or exploratory asks
- weak static match
- multi-family orchestration decisions
- turns where output guardrails matter more than cheapest routing

Bad planner use cases:

- concrete single-entity requests with a strong known baseline path
- turns already cleanly covered by one static route
- using `use exploratory strategy` as a blanket reason to over-plan

Planner trigger discipline matters because planner is valuable, but not free.

## Current Design Rules

- Planner contracts are **planner-agent owned**
- Core must not hardcode workspace-specific planner schema
- Core should prefer explicit contract loading over built-in defaults
- Planner should reason from:
  - tool schemas
  - tool input/output shapes
  - business knowledge
  not just tool names or prompt-profile tags
- Planner should stay out of the way when a strong exact route already exists

## Key Files

| Concern | Path |
|---|---|
| Planner pass | [service/agent/planner_pass.go](../service/agent/planner_pass.go) |
| Planner contract resolution | [service/planner/contract.go](../service/planner/contract.go) |
| Generic planner application hooks | [service/planner/application.go](../service/planner/application.go) |
| Generic validation engine | [service/planner/validation.go](../service/planner/validation.go) |
| Planner runtime context | [service/planner/turn_context.go](../service/planner/turn_context.go) |
| Intake planner gating | [service/agent/intake_query.go](../service/agent/intake_query.go) |
| Agent intake config | [protocol/agent/intake.go](../protocol/agent/intake.go) |

## Related Docs

- [doc/prompts.md](prompts.md)
- [doc/prompt-binding.md](prompt-binding.md)
- [doc/workspace-system.md](workspace-system.md)
