# Agent-to-agent (A2A) protocol

Agently speaks an agent-to-agent HTTP protocol so one agent can call another
agent as if it were a tool — across processes, across organizations. The
local runtime also uses A2A semantics internally for child-agent delegation
(via the `llm/agents` tool).

## Packages

| Path | Role |
|---|---|
| [service/a2a/](../service/a2a/) | Service: agent card, message, task |
| [protocol/agent/](../protocol/agent/) | Shared types: `AgentCard`, `Capability`, `Message` |
| Workspace `a2a/*.yaml` | Remote-agent registrations |
| [sdk/embedded_a2a_scheduler.go](../sdk/embedded_a2a_scheduler.go) | Backend wiring |

## Agent card

An agent advertises itself through a card:

```yaml
id: data-analyst
title: Data analyst
description: Runs data-heavy queries and returns structured findings.
capabilities:
  - name: run
    input:  { $ref: "#/defs/RunInput" }
    output: { $ref: "#/defs/RunOutput" }
  - name: status
    ...
```

Served at `GET /v1/api/a2a/{agentId}/card`.

## Wire operations

| Method | Path | Effect |
|---|---|---|
| GET | `/v1/api/a2a/{agentId}/card` | Fetch agent card |
| POST | `/v1/api/a2a/{agentId}/message` | Send one message, block for reply |
| POST | `/v1/api/a2a/{agentId}/task` | Kick off an async task (returns task id) |
| GET | `/v1/api/a2a/{agentId}/task/{id}` | Poll an async task |

## Local vs. remote

- **Local**: `llm/agents:start` / `llm/agents:status` under [protocol/tool/service/llm/agents/](../protocol/tool/service/llm/agents/) use A2A semantics in-process. The caller writes the child's output back to the parent's transcript with cross-conversation linking.
- **Remote**: `a2a/<name>.yaml` registers an external agent; the runtime treats it like an MCP server with an A2A shape. Auth flows through the same BFF / bearer / OIDC pipeline ([doc/auth-system.md](auth-system.md)).

## Cross-conversation linking

Child turns reference their parent via `parentTurnId` on the turn record. The
canonical reducer joins these so UI clients can show nested conversations as
a tree.

## Extensibility

- **Register a remote agent**: drop a YAML under `<workspace>/a2a/`.
- **Expose your runtime via A2A**: nothing to do — every local agent is already exposed at `/v1/api/a2a/{id}/*`.
- **Custom capability**: add a verb + handler in `service/a2a/` and extend the card schema.

## Related docs

- [doc/async.md](async.md) — async tasks on top of A2A.
- [doc/mcp-integration.md](mcp-integration.md) — auth flow (identical).
