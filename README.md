# agently-core

[![Go Reference](https://pkg.go.dev/badge/github.com/viant/agently-core.svg)](https://pkg.go.dev/github.com/viant/agently-core)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

`agently-core` is the embeddable Go runtime for building AI agent systems. It provides
the complete backend for agent query execution, conversation management, tool
orchestration, and workspace-driven configuration — designed to be embedded in your
own Go services or exposed as a standalone HTTP API.

## Features

- **Agent query execution** with multi-turn conversation, tool calling, and streaming
- **Multi-LLM support** — OpenAI, Vertex AI, Bedrock Claude, Ollama, and more
- **MCP integration** — connect MCP servers as tool providers with secure auth injection
- **A2A protocol** — agent-to-agent communication endpoints
- **Workspace-driven config** — agents, models, embedders, MCP clients, and tools live as YAML files
- **Persistent conversations** — SQL-backed (SQLite/MySQL) via Datly with cursor pagination
- **Scheduler** — cron/interval/adhoc schedule execution with distributed lease coordination
- **Distributed token refresh** — CAS-based OAuth token refresh safe for multi-pod deployments
- **Embedded and HTTP SDKs** — use in-process or as a remote HTTP client

## Installation

```bash
go get github.com/viant/agently-core
```

Requires Go 1.25.5+.

## Quick Start (Embedded Runtime)

```go
ctx := context.Background()

rt, err := executor.NewBuilder().
    WithAgentFinder(agentFinder).
    WithModelFinder(modelFinder).
    Build(ctx)
if err != nil {
    log.Fatal(err)
}

client, err := sdk.NewEmbeddedFromRuntime(rt)
if err != nil {
    log.Fatal(err)
}

out, err := client.Query(ctx, &agentsvc.QueryInput{
    ConversationID: "conv_123",
    Request:        "Summarize workspace resources",
})
```

## Quick Start (HTTP Server)

```go
handler := sdk.NewHandler(client)
log.Fatal(http.ListenAndServe(":8090", handler))
```

Health check: `GET /healthz` returns `{"status":"ok"}`.

## Package Layout

```
agently-core/
  sdk/                      Public SDK surface (Client, Handler, HTTP, Embedded)
  app/                      Application plumbing
    executor/               Runtime builder (Builder, Runtime, config)
    store/
      conversation/         Conversation domain types and helpers
        cancel/             Turn cancellation registry
      data/                 Datly-backed persistence facade
        memory/             In-memory client for tests
    workspace/              Bootstrap re-exports for workspace paths
  service/                  Business logic services
    agent/                  Agent query orchestration and watchdog
    auth/                   OAuth token store, session management
    augmenter/              Knowledge augmentation (embeddings, RAG)
    core/                   LLM call execution, streaming, model calls
    scheduler/              Schedule CRUD, watchdog, execution
    elicitation/            Assistant elicitation routing
    a2a/                    Agent-to-agent protocol handler
    speech/                 Speech transcription
    workflow/               Workflow execution
    workspace/              Workspace metadata and file browser
  protocol/                 Domain models
    agent/                  Agent definition, finder, loader
    mcp/                    MCP client config, manager, session
    tool/                   Tool registry, bundles, policies
    prompt/                 Prompt/system template types
  genai/                    LLM and embedder providers
    llm/                    LLM provider abstraction and implementations
    embedder/               Embedder provider abstraction
  workspace/                Workspace domain (reusable across services)
    repository/             Generic + typed YAML resource repositories
    loader/                 Model and embedder config loaders
    hotswap/                Live-reload watcher for workspace changes
    store/                  Filesystem-backed workspace store
    service/                Metadata/YAML parsing services
  runtime/                  Runtime primitives
    memory/                 Conversation memory management
    streaming/              SSE event streaming bus
    usage/                  Token usage tracking
  internal/                 Private implementation details
    auth/                   Auth context helpers, token manager
    finder/                 Model/embedder finder implementations
    script/                 DDL schemas (SQLite, MySQL)
    service/                Internal service factories
  pkg/                      Datly-generated DAO layer
    agently/                Read/write components for all DB entities
    mcpname/                MCP name normalization
  dql/                      SQL query files for Datly operations
  e2e/                      End-to-end test infrastructure
```

## HTTP API

Core endpoints mounted by `sdk.NewHandler`:

| Method | Path | Description |
|--------|------|-------------|
| POST | `/v1/agent/query` | Execute agent query |
| POST | `/v1/conversations` | Create conversation |
| GET | `/v1/conversations` | List conversations |
| GET | `/v1/conversations/{id}` | Get conversation |
| GET | `/v1/conversations/{id}/transcript` | Get transcript |
| POST | `/v1/conversations/{id}/terminate` | Terminate conversation |
| POST | `/v1/conversations/{id}/compact` | Compact conversation |
| POST | `/v1/conversations/{id}/prune` | Prune conversation |
| GET | `/v1/messages` | Get messages |
| GET | `/v1/elicitations` | List pending elicitations |
| POST | `/v1/elicitations/{conversationId}/{elicitationId}/resolve` | Resolve elicitation |
| GET | `/v1/stream` | SSE event stream |
| POST | `/v1/turns/{id}/cancel` | Cancel turn |
| POST | `/v1/tools/{name}/execute` | Execute tool |
| POST | `/v1/tools/execute` | Execute tool (name in JSON body) |
| GET | `/v1/tool-approvals/pending` | List pending tool approvals |
| POST | `/v1/tool-approvals/{id}/decision` | Approve/reject queued tool |
| GET | `/v1/workspace/resources` | List resources |
| GET | `/v1/workspace/resources/{kind}/{name}` | Get resource |
| PUT | `/v1/workspace/resources/{kind}/{name}` | Save resource |
| DELETE | `/v1/workspace/resources/{kind}/{name}` | Delete resource |
| POST | `/v1/workspace/resources/export` | Export resources |
| POST | `/v1/workspace/resources/import` | Import resources |

Optional handlers add auth, scheduler, speech, workflow, metadata, file browser, and A2A endpoints.

## SDK Modes

| Mode | Constructor | Use case |
|------|-------------|----------|
| Embedded | `sdk.NewEmbeddedFromRuntime(rt)` | In-process calls, no HTTP overhead |
| HTTP | `sdk.NewHTTP(baseURL, opts...)` | Remote client for deployed services |

Both implement `sdk.Client`.

## SDK Conversation Architecture

Execution model used by SDK and HTTP API:

```text
conversation
  -> turn (user request lifecycle)
    <-> run (LLM/tool execution cycle, retries, usage, status)
      -> messages
        -> tool calls
        -> elicitations (pending/resolved)
        -> attachments
        -> downloads/files
        -> linked conversations
```

How to query this model via API:

- Conversation shell: `GET /v1/conversations/{id}`
- Turn+message timeline: `GET /v1/conversations/{id}/transcript`
- Include tool/model activity in transcript: `?includeToolCalls=true&includeModelCalls=true`
- Pending elicitation state: `GET /v1/elicitations?conversationId={id}`
- Resolve elicitation: `POST /v1/elicitations/{conversationId}/{elicitationId}/resolve`
- Tool approval queue (if enabled): `GET /v1/tool-approvals/pending`

## Workspace

Workspace root defaults to `.agently` under the current directory unless overridden.

Predefined kinds: `agents`, `models`, `embedders`, `mcp`, `workflows`, `tools` (bundles, hints), `oauth`, `feeds`, `a2a`.

| Env var | Purpose |
|---------|---------|
| `AGENTLY_WORKSPACE` | Workspace root path |
| `AGENTLY_RUNTIME_ROOT` | Runtime root (defaults to workspace) |
| `AGENTLY_STATE_PATH` | Runtime state root |
| `AGENTLY_WORKSPACE_NO_DEFAULTS` | Skip default bootstrapping |

## Persistence

`app/store/data` provides the Datly-backed persistence facade supporting conversations,
messages, turns, runs, tool calls, payloads, and generated files.

| Env var | Default | Purpose |
|---------|---------|---------|
| `AGENTLY_DB_DRIVER` | `sqlite` | Database driver |
| `AGENTLY_DB_DSN` | (auto) | Database connection string |

Falls back to `$AGENTLY_WORKSPACE/db/agently.db` when DSN/driver are unset.

## Scheduler

The scheduler supports cron, interval, and adhoc schedule execution with a background
watchdog loop.

### Deployment Modes

**Single-node** (API + watchdog in one process):
```go
h, _ := sdk.NewHandlerWithContext(ctx, client,
    sdk.WithScheduler(svc, handler, &sdk.SchedulerOptions{
        EnableAPI: true, EnableRunNow: true, EnableWatchdog: true,
    }),
)
```

**Multi-pod** (separate API and scheduler processes):
```go
// API pods — CRUD endpoints only, no execution
sdk.WithScheduler(svc, handler, &sdk.SchedulerOptions{EnableAPI: true})

// Scheduler pod — watchdog only, no HTTP endpoints
sdk.WithScheduler(svc, nil, &sdk.SchedulerOptions{EnableWatchdog: true})
```

### Distributed Token Refresh

When a `TokenStore` is configured, the token `Manager` automatically enables distributed
refresh coordination using SQL-based CAS (Compare-And-Swap) with lease acquisition.
This prevents token corruption when multiple pods attempt to refresh the same OAuth token simultaneously.

- Lease acquisition is atomic via SQL `UPDATE ... WHERE refresh_status='idle' OR lease_until < NOW()`
- CAS writes use version checks to prevent stale overwrites
- Dead pod leases expire after configurable TTL (default 30s)
- All timestamps use DB server time to avoid clock skew
- Falls back to local-only refresh on DB errors

## LLM Providers

Provider stack under `genai/llm/provider/*`:

- OpenAI (GPT-4, GPT-4o, o1, o3, etc.)
- Vertex AI (Gemini)
- Vertex AI Claude
- Bedrock Claude
- InceptionLabs (Mercury)
- Grok (xAI)
- Ollama (local models)

Common env: `OPENAI_API_KEY`, `VERTEX_PROJECT`, `AWS_REGION`, `INCEPTIONLABS_API_KEY`, `XAI_API_KEY`, etc.

## Testing

```bash
# Unit and integration tests
go test ./...

# Token refresh distributed coordination tests
go test ./internal/auth/token/... -v

# E2E tests (Endly-driven)
cd e2e
endly -t=build
endly -t=test

# Targeted E2E case
endly -t=test -i=test_<case_folder>
```

Async E2E cases (notably MCP round-trip and tool-approval queue flows) use `process:start`
with `curl --data-binary @<payload-file>` to avoid shell JSON quoting/expansion issues.
Request payload files are stored under:

- `/Users/awitas/go/src/github.com/viant/agently-core/e2e/build/payloads`

## Related Projects

- [agently](https://github.com/viant/agently) — Full-featured CLI and HTTP host application built on agently-core
- [mcp-sqlkit](https://github.com/viant/mcp-sqlkit) — MCP server for database operations
- [datly](https://github.com/viant/datly) — Data access layer used for persistence

## License

Apache License 2.0 — see [LICENSE](./LICENSE) and [NOTICE](./NOTICE).
