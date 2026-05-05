# agently-core documentation

Feature reference for agently-core. Each doc is a short-to-medium design +
extensibility guide that names the concrete files implementing the feature.

If you're new, read in this order:

1. [architecture.md](architecture.md) — **one-page diagram** of clients → SDK → core services → persistence, plus the turn lifecycle.
2. [agent-orchestration.md](agent-orchestration.md) — the `Query → plan → act → respond` cycle.
3. [tool-system.md](tool-system.md) — how tools (internal and MCP) are dispatched.
4. [workspace-system.md](workspace-system.md) — where configuration lives.
5. [sdk.md](sdk.md) — the contract every client (Go / HTTP / iOS / Android) shares.

---

## Overview

| Doc | Topic |
|---|---|
| [architecture.md](architecture.md) | End-to-end diagram (clients → SDK → services → persistence) + turn lifecycle |

## Runtime orchestration

| Doc | Topic |
|---|---|
| [agent-orchestration.md](agent-orchestration.md) | ReAct loop, approval, recovery |
| [planning-and-intake.md](planning-and-intake.md) | Pre-turn classification, plan schema |
| [planner.md](planner.md) | Light planner fallback over static profiles |
| [context-management.md](context-management.md) | Token budgets, pruning, overflow recovery |
| [followup-chains.md](followup-chains.md) | Multi-turn chains inside a conversation |
| [async.md](async.md) | Long-running operations (shell, child agents, external services) |

## Tools & services

| Doc | Topic |
|---|---|
| [tool-system.md](tool-system.md) | Registry, bundles, policy, feeds |
| [internal-tools.md](internal-tools.md) | Built-in services unified with external MCP |
| [mcp-integration.md](mcp-integration.md) | MCP client lifecycle + auth bridge |
| [a2a-protocol.md](a2a-protocol.md) | Agent-to-agent protocol |

## Prompt authoring

| Doc | Topic |
|---|---|
| [prompts.md](prompts.md) | Prompt profiles + tool bundles |
| [templates.md](templates.md) | Output templates + template bundles |
| [skills.md](skills.md) | `SKILL.md` support |
| [prompt-binding.md](prompt-binding.md) | How the model's message list is assembled |

## UI + schema

| Doc | Topic |
|---|---|
| [elicitation-system.md](elicitation-system.md) | Server-driven forms |
| [overlays.md](overlays.md) | Schema refinement engine |
| [lookups.md](lookups.md) | MCP-backed datasources + pickers + `/name` tokens |
| [feed-system.md](feed-system.md) | Tool-output → UI dashboard datasources |

## Platform

| Doc | Topic |
|---|---|
| [sdk.md](sdk.md) | Embedded / HTTP / Swift / Kotlin SDKs |
| [streaming-events.md](streaming-events.md) | Event bus + SSE |
| [conversation-model.md](conversation-model.md) | Persistence schema |
| [auth-system.md](auth-system.md) | Local / JWT / BFF OAuth / mixed |
| [scheduler.md](scheduler.md) | Cron / interval / ad-hoc, multi-node leasing |
| [workspace-system.md](workspace-system.md) | YAML workspace layout + hotswap |

## Data & models

| Doc | Topic |
|---|---|
| [llm-providers.md](llm-providers.md) | Multi-provider abstraction (`llm.GenerateRequest` / `llm.GenerateResponse`) |
| [embedius-embeddings.md](embedius-embeddings.md) | Embedder abstraction + vector index |
| [augmentation.md](augmentation.md) | RAG-style knowledge injection |
| [speech.md](speech.md) | Optional audio → text transcription endpoint |

---

## Index by need

| I want to… | Start with |
|---|---|
| Add a new tool | [tool-system.md](tool-system.md) → [internal-tools.md](internal-tools.md) |
| Integrate a new MCP server | [mcp-integration.md](mcp-integration.md) + [auth-system.md](auth-system.md) |
| Add a UI picker for a form field | [lookups.md](lookups.md) + [overlays.md](overlays.md) |
| Surface tool output as a live dashboard | [feed-system.md](feed-system.md) |
| Schedule recurring work | [scheduler.md](scheduler.md) + [auth-system.md](auth-system.md) |
| Run a long-running operation | [async.md](async.md) |
| Build a client app | [sdk.md](sdk.md) + [streaming-events.md](streaming-events.md) + [conversation-model.md](conversation-model.md) |
| Tune agent behavior | [prompts.md](prompts.md) + [skills.md](skills.md) + [templates.md](templates.md) + [prompt-binding.md](prompt-binding.md) |
| Swap or add an LLM provider | [llm-providers.md](llm-providers.md) |
| Reduce conversation context bloat | [context-management.md](context-management.md) |
| Delegate work to another agent | [a2a-protocol.md](a2a-protocol.md) + [async.md](async.md) |
| Add a new workspace resource kind | [workspace-system.md](workspace-system.md) |
| Run RAG over workspace knowledge | [augmentation.md](augmentation.md) + [embedius-embeddings.md](embedius-embeddings.md) |

---

## Cross-cutting themes

**Internal + MCP unification.** Tools, prompts, templates, resources, and
feeds all speak the same `service:method` contract regardless of whether
they're backed by an in-process implementation or a remote MCP server. See
[internal-tools.md](internal-tools.md) and [tool-system.md](tool-system.md).

**Auth rides `context.Context`.** No handler or service accepts a token
parameter; identity propagates through ctx from the HTTP session to the
MCP call at the edge. See [auth-system.md](auth-system.md) and
[mcp-integration.md](mcp-integration.md).

**Append-only transcript with derived views.** Persisted state is never
mutated; what the model sees per turn is computed from the persisted state
plus pruning rules. See [conversation-model.md](conversation-model.md) and
[context-management.md](context-management.md).

**Workspace YAML is the declarative surface.** Adding agents, tools, models,
skills, prompts, templates, datasources, and overlays is a YAML change — no
Go edits required. See [workspace-system.md](workspace-system.md).

---

## Contributing new docs

A feature is worth its own doc when it is cross-cutting, architecturally
notable, or involves multiple packages. Prefer one doc per feature over
one doc per package.

Each doc should include:

- A short "what this is" paragraph.
- A table of the key files/packages and their roles.
- A summary of the runtime flow.
- Extensibility hooks.
- Links to related docs under `doc/`.

File link format: relative paths (e.g. `[foo](../protocol/foo/bar.go)`) so
the doc reads the same on GitHub and in a local viewer.
