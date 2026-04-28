# Architecture overview

One picture of how the pieces fit together. Each box links to the doc that
covers its subsystem.

```
┌──────────────────────────────────────────────────────────────────────────────────┐
│                                    CLIENTS                                       │
│  ┌───────────┐  ┌───────────┐  ┌───────────┐                ┌──────────────┐     │
│  │  Web UI   │  │   iOS     │  │  Android  │                │  CLI / Tests │     │
│  │ (agently) │  │  AgentlyS.│  │ AgentlyS. │                │  NewHTTP(..) │     │
│  └─────┬─────┘  └─────┬─────┘  └─────┬─────┘                └──────┬───────┘     │
│        │              │              │                              │             │
│        │  JSON/HTTPS + Server-Sent Events  (session cookie, OAuth)  │             │
│        ▼              ▼              ▼                              ▼             │
└────────────────────────────────────────────────────────────────────────────────────┘
                                        │
                                        ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                              HTTP / SDK LAYER  (sdk.md)                          │
│                                                                                  │
│   ┌──────────────────────┐         ┌───────────────────────┐                     │
│   │   HTTPClient         │◄───────►│   handler.go routes   │                     │
│   │   (Go over HTTP)     │         │   /v1/* + /v1/api/*   │                     │
│   └──────────────────────┘         └──────────┬────────────┘                     │
│                                               │                                  │
│   ┌──────────────────────┐                    │  auth middleware                 │
│   │   backendClient      │◄──── same Client interface ──► auth-system.md         │
│   │   (embedded)         │                    │                                  │
│   └──────────┬───────────┘                    │                                  │
│              │                                │                                  │
│              └────────────────┬───────────────┘                                  │
│                               │                                                  │
│              Events ◄────── streaming.Bus ──────► SSE (streaming-events.md)      │
└──────────────────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                                 CORE SERVICES                                    │
│                                                                                  │
│   ┌─────────────────────────────────────────────────────────────┐                │
│   │    Agent orchestration  (service/agent, service/reactor)    │   agent-       │
│   │    ReAct: plan → act → observe → synthesize                 │   orchestra-   │
│   │                                                             │   tion.md      │
│   │   ┌──────────────┐   ┌───────────────────┐                  │                │
│   │   │  intake      │──►│  reactor loop     │                  │                │
│   │   │  sidecar     │   │  ┌──────────────┐ │                  │                │
│   │   │ (planning-   │   │  │ overflow /   │ │  context-        │                │
│   │   │  and-intake) │   │  │ pruning      │ │  management.md   │                │
│   │   └──────────────┘   │  └──────────────┘ │                  │                │
│   │                      │  ┌──────────────┐ │                  │                │
│   │                      │  │ approvals    │ │                  │                │
│   │                      │  └──────────────┘ │                  │                │
│   │                      └──────┬────────────┘                  │                │
│   └─────────────────────────────┼─────────────────────────────┐─┘                │
│                                 │                             │                  │
│        ┌────────────────────────┼──────────────────────┐      │                  │
│        │                        ▼                      │      │                  │
│        │          ┌──────────────────────────┐         │      │                  │
│        │          │    Tool registry         │         │      │  tool-system.md  │
│        │          │  service:method dispatch │         │      │  internal-tools  │
│        │          └──────┬──────────┬────────┘         │      │                  │
│        │                 │          │                  │      │                  │
│        │                 ▼          ▼                  │      │                  │
│        │   ┌──────────────────┐ ┌───────────────────┐  │      │                  │
│        │   │ Internal tools   │ │ External MCP proxy│  │      │  mcp-integration │
│        │   │ llm/agents       │ │ + auth token      │  │      │  a2a-protocol    │
│        │   │ prompt, template │ │ (ctx-attached)    │  │      │                  │
│        │   │ skills, system/* │ │                   │  │      │                  │
│        │   │ orchestration,   │ │  MCP servers ────────┼──────┼──►   (remote)    │
│        │   │ resources, etc.  │ │                   │  │      │                  │
│        │   └──────────────────┘ └───────────────────┘  │      │                  │
│        │                                               │      │                  │
│        │   ┌─────────────────────────────────────────┐ │      │                  │
│        │   │  Async operation manager                │ │      │  async.md        │
│        │   │  (parent-turn gating, poller, events)   │ │      │                  │
│        │   └─────────────────────────────────────────┘ │      │                  │
│        │                                               │      │                  │
│        │   ┌─────────────────────────────────────────┐ │      │                  │
│        │   │  Elicitation                             │ │      │  elicitation-   │
│        │   │  schema + refiner + overlay hook         │ │      │  system.md      │
│        │   │                    │                     │ │      │  overlays.md    │
│        │   │                    ▼                     │ │      │                  │
│        │   │  Overlay engine (service/lookup/overlay) │ │      │                  │
│        │   │  matcher · mode · translator · registry  │ │      │                  │
│        │   └─────────────────────────────────────────┘ │      │                  │
│        │                                               │      │                  │
│        │   ┌─────────────────────────────────────────┐ │      │                  │
│        │   │  Datasource service                     │ │      │  lookups.md      │
│        │   │  mcp_tool / mcp_resource / feed_ref /   │ │      │                  │
│        │   │  inline  +  per-user cache              │ │      │                  │
│        │   └─────────────────────────────────────────┘ │      │                  │
│        │                                               │      │                  │
│        │   ┌──────────────────┐  ┌───────────────────┐ │      │                  │
│        │   │  Feed registry   │  │  Augmentation /   │ │      │  feed-system     │
│        │   │  (UI dashboards) │  │  retrieval        │ │      │  augmentation    │
│        │   └──────────────────┘  └───────────────────┘ │      │                  │
│        │                                               │      │                  │
│        │   ┌──────────────────┐  ┌───────────────────┐ │      │                  │
│        │   │  Scheduler       │  │  A2A service      │ │      │  scheduler       │
│        │   │  (cron/interval/ │  │  (agent-to-agent) │ │      │  a2a-protocol    │
│        │   │  adhoc,  lease)  │  │                   │ │      │                  │
│        │   └──────────────────┘  └───────────────────┘ │      │                  │
│        └───────────────────────────────────────────────┘      │                  │
│                                                               │                  │
│   ┌─────────────────────────────────────────────────────────┐ │                  │
│   │  Prompt binder + LLM invocation (service/core, genai)   │─┘                  │
│   │                                                         │   prompt-binding   │
│   │  ┌────────────┐  ┌──────────────┐  ┌────────────────┐   │   llm-providers    │
│   │  │  bindings  │  │ Model.Genera │  │ Provider adapt │   │                    │
│   │  │ assembly   │─►│ streaming +  │◄─│ OpenAI/Claude/ │   │                    │
│   │  │ (prompts,  │  │ tool_calls   │  │ Gemini/Ollama/ │   │                    │
│   │  │ skills,    │  │              │  │ Grok/Inception │   │                    │
│   │  │ knowledge) │  │              │  │                │   │                    │
│   │  └────────────┘  └──────────────┘  └────────────────┘   │                    │
│   └─────────────────────────────────────────────────────────┘                    │
│                                                                                  │
│   ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐               │
│   │  Embedders       │  │  Speech          │  │  Auth service    │  embedius     │
│   │  (embedius)      │  │  (transcribe)    │  │  (BFF/OAuth/JWT) │  speech       │
│   └──────────────────┘  └──────────────────┘  └──────────────────┘  auth-system  │
└──────────────────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                              PERSISTENCE / STATE                                 │
│                                                                                  │
│   ┌─────────────────────────────────────────────────────────────┐                │
│   │   Datly-backed tables  (pkg/agently/*)                      │  conversation  │
│   │   conversation · turn · message · tool_call · run ·         │  -model.md     │
│   │   turn_queue · tool_approval_queue · payload · files ·      │                │
│   │   schedules                                                 │                │
│   └─────────────────────────────────────────────────────────────┘                │
│                                                                                  │
│   ┌─────────────────────────────────────────────────────────────┐                │
│   │   Workspace YAML  (extension/forge/*, agents/, models/, ..) │  workspace-    │
│   │   loaded via Repository[T], hotswap-aware                   │  system.md     │
│   └─────────────────────────────────────────────────────────────┘                │
└──────────────────────────────────────────────────────────────────────────────────┘
```

## Request lifecycle — one turn end-to-end

```
┌─ Web client ──────────────────────────────────────────────────────────┐
│  1. POST /v1/agent/query          (session cookie, conversation id)   │
│  2. Opens SSE /v1/stream          (reconnect-aware, reducer feeds UI) │
└─────────────────────┬──────────────────────────────────────────────────┘
                      │
                      ▼
┌─ HTTP handler (sdk/handler.go) ────────────────────────────────────────┐
│  auth middleware → decodes input → calls Client.Query(ctx, input)      │
└─────────────────────┬──────────────────────────────────────────────────┘
                      │
                      ▼
┌─ backendClient.Query (sdk/embedded.go) ────────────────────────────────┐
│  feed notifier + streaming bus attached via ctx                        │
│  → agent.Service.Query(ctx, input, out)                                │
└─────────────────────┬──────────────────────────────────────────────────┘
                      │
                      ▼
┌─ Agent / reactor loop ─────────────────────────────────────────────────┐
│  intake sidecar          → TurnContext {profile, bundles, confidence}  │
│  prompt binder           → GenerateRequest                             │
│  llm.Model.Generate      → GenerateResponse (streaming)                │
│      │                                                                 │
│      ├─ tool_call  → registry.Execute(ctx, "svc:method", args)         │
│      │               ├─ internal service                               │
│      │               └─ MCP proxy (auth attached from ctx)             │
│      │                                                                 │
│      ├─ child agent → llm/agents:start (A2A)                           │
│      ├─ async op    → asynccfg manager (poll / subscribe / flush)      │
│      ├─ elicit      → refiner + overlay → client form                  │
│      └─ overflow    → prune + retry                                    │
│  synthesize final assistant message                                    │
└─────────────────────┬──────────────────────────────────────────────────┘
                      │
                      ▼
┌─ Persistence + events ─────────────────────────────────────────────────┐
│  turn + messages + tool_calls committed to Datly tables                │
│  streaming.Bus events delivered to SSE subscribers                     │
│  canonical reducer updates per-conversation state snapshot             │
└────────────────────────────────────────────────────────────────────────┘
```

## Cross-cutting lanes

Some concerns don't belong to any one box — they run across the whole stack:

```
     Clients            SDK              Core services             External

auth context ──────── session ────── ctx.Context ──── MCP auth  ─── remote server
                                                   └─ LLM token ─── LLM provider
                                                   └─ DB auth   ─── Datly

event stream ──────── SSE  ◄──────── streaming.Bus ◄─ reactor / tools / async

hot config  ────────         ◄──── workspace hotswap ◄── YAML edits
```

- **Auth** ([auth-system.md](auth-system.md)) — session cookie or bearer at the edge; `ctx`-attached at every hop from then on, including outbound MCP calls.
- **Streaming** ([streaming-events.md](streaming-events.md)) — one `runtime/streaming.Bus` feeds SSE, in-process reducers, feed notifier, and debug sinks.
- **Hotswap** ([workspace-system.md](workspace-system.md)) — YAML edits propagate without restart.

## Where each topic doc fits

| Layer | Docs |
|---|---|
| Clients + SDK | [sdk.md](sdk.md) |
| HTTP + events | [streaming-events.md](streaming-events.md), [auth-system.md](auth-system.md) |
| Reactor + planning | [agent-orchestration.md](agent-orchestration.md), [planning-and-intake.md](planning-and-intake.md), [context-management.md](context-management.md), [followup-chains.md](followup-chains.md), [async.md](async.md) |
| Tools + MCP | [tool-system.md](tool-system.md), [internal-tools.md](internal-tools.md), [mcp-integration.md](mcp-integration.md), [a2a-protocol.md](a2a-protocol.md) |
| Prompts + output | [prompts.md](prompts.md), [skills.md](skills.md), [templates.md](templates.md), [prompt-binding.md](prompt-binding.md) |
| Schemas + UI | [elicitation-system.md](elicitation-system.md), [overlays.md](overlays.md), [lookups.md](lookups.md), [feed-system.md](feed-system.md) |
| Models + data | [llm-providers.md](llm-providers.md), [embedius-embeddings.md](embedius-embeddings.md), [augmentation.md](augmentation.md), [speech.md](speech.md) |
| Platform | [scheduler.md](scheduler.md), [workspace-system.md](workspace-system.md), [conversation-model.md](conversation-model.md) |

## Related

- [doc/README.md](README.md) — index + reading-order suggestion.
