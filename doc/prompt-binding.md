# Prompt binding & model invocation

Turning an agent definition + a user query + conversation history into the
exact message list an LLM sees is called **prompt binding**. Agently's binder
combines agent YAML, MCP server instructions, injected documents, and
knowledge into one deterministic build.

## Packages

| Path | Role |
|---|---|
| [protocol/binding/](../protocol/binding/) | Types: `Bindings`, `BindPayload`, `Document` |
| [protocol/mcp/expose/](../protocol/mcp/expose/) | Emits MCP-side instruction blocks consumed by the binder |
| [service/core/](../service/core/) | `generate_prepare.go` — binding pipeline; `generate_execute.go` — LLM call |
| [genai/llm/](../genai/llm/) | Provider abstraction (OpenAI, Vertex, Bedrock, Ollama, Grok, …) |
| [genai/embedders/](../genai/embedders/) | Embedder abstraction (see [doc/embedius-embeddings.md](embedius-embeddings.md)) |

## Build order

Binding runs per turn in this order:

1. **Agent metadata** — name, description, `systemKnowledge`, policies.
2. **Profile instructions** — from `prompt:get` when a profile is active ([doc/prompts.md](prompts.md)).
3. **Skill preambles** — active SKILL.md bodies ([doc/skills.md](skills.md)).
4. **MCP server instructions** — each attached server contributes a block via `protocol/mcp/expose/`.
5. **Internal tool instructions** — in-process tools declare instructions the same way.
6. **Documents / knowledge** — workspace `knowledge/` injected by id or tag.
7. **Tool definitions** — the model's tool schema list, narrowed by active bundles.
8. **Conversation history** — the pruned slice from [doc/context-management.md](context-management.md).
9. **User turn** — the current message + any elicitation answers.

Each step emits structured blocks, not raw strings; the final prompt is assembled by the provider adapter so each LLM sees its native format.

## Internal + external unification

Prompt binding treats internal tools and MCP-external tools identically: both
provide instruction blocks through the same `expose` interface, both appear in
the tool list with qualified `service:method` names, both are callable via the
single registry ([doc/tool-system.md](tool-system.md)). The agent author
cannot tell which tools are in-process vs. remote from the system prompt's
shape.

## Providers

[genai/llm/provider/](../genai/llm/provider/) holds one sub-package per provider. Each implements a common `Model.Generate(ctx, messages, tools, opts)` interface and handles streaming tokens, tool_call parsing, and provider-specific retries.

Model selection flows from workspace YAML (`models/*.yaml`) through the agent's `model:` field.

## Extensibility

- **New provider**: implement `Model` + register in `genai/llm/provider/registry/`.
- **New instruction block**: publish via `expose.Provider`; binder picks it up automatically.
- **Knowledge injection**: drop a file under `knowledge/` and reference it from the agent's `knowledge:` list.

## Related docs

- [doc/prompts.md](prompts.md)
- [doc/skills.md](skills.md)
- [doc/context-management.md](context-management.md)
- [doc/tool-system.md](tool-system.md)
