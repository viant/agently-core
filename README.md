# agently-core

[![Go Reference](https://pkg.go.dev/badge/github.com/viant/agently-core.svg)](https://pkg.go.dev/github.com/viant/agently-core)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

`agently-core` is the embeddable Go runtime for building AI agent systems. It provides
the complete backend for agent query execution, conversation management, tool
orchestration, and workspace-driven configuration — designed to be embedded in your
own Go services or exposed as a standalone HTTP API.

## Features

- **Agent query execution** — multi-turn conversation, tool calling, streaming, and elicitation
- **Multi-LLM support** — OpenAI, Vertex AI (Gemini + Claude), Bedrock Claude, Grok, InceptionLabs, Ollama
- **MCP integration** — connect MCP servers as tool providers with secure BFF/bearer auth injection
- **A2A protocol** — agent-to-agent communication endpoints (`/.well-known/agent.json`, `/v1/api/a2a/*`)
- **MCP tool exposure** — expose workspace tools as an MCP HTTP server (`protocol/mcp/expose`)
- **Authentication** — JWT (RSA/HMAC), OAuth BFF/SPA/bearer/mixed, local sessions, distributed token refresh
- **Workspace-driven config** — agents, models, embedders, MCP clients, tools and policies as YAML files
- **Persistent conversations** — SQL-backed (SQLite/MySQL) via Datly; auto-creates workspace SQLite DB
- **Scheduler** — cron/interval/adhoc schedule execution with distributed lease coordination
- **Parallel tool calls** — enabled by default for models that support it
- **Embedded and HTTP SDKs** — use in-process or as a remote HTTP client

## Installation

```bash
go get github.com/viant/agently-core
```

Requires Go 1.25+.

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
handler, _ := sdk.NewHandlerWithContext(ctx, client)
log.Fatal(http.ListenAndServe(":8090", handler))
```

Health check: `GET /healthz` → `{"status":"ok"}`.

## HTTP API

Core endpoints mounted by `sdk.NewHandler`:

| Method | Path | Description |
|--------|------|-------------|
| POST | `/v1/agent/query` | Execute agent query |
| POST | `/v1/conversations` | Create conversation |
| GET | `/v1/conversations` | List conversations |
| GET | `/v1/conversations/{id}` | Get conversation |
| PATCH | `/v1/conversations/{id}` | Update conversation |
| GET | `/v1/conversations/{id}/transcript` | Get canonical transcript |
| POST | `/v1/conversations/{id}/terminate` | Terminate conversation |
| POST | `/v1/conversations/{id}/compact` | Compact conversation |
| POST | `/v1/conversations/{id}/prune` | Prune conversation |
| GET | `/v1/messages` | Get messages |
| GET | `/v1/elicitations` | List pending elicitations |
| POST | `/v1/elicitations/{conversationId}/{elicitationId}/resolve` | Resolve elicitation |
| GET | `/v1/stream` | SSE event stream |
| POST | `/v1/turns/{id}/cancel` | Cancel turn |
| GET | `/v1/tools` | List tool definitions |
| POST | `/v1/tools/{name}/execute` | Execute tool |
| POST | `/v1/tools/execute` | Execute tool (name in body) |
| GET | `/v1/tool-approvals/pending` | List pending tool approvals |
| POST | `/v1/tool-approvals/{id}/decision` | Approve/reject tool |
| GET | `/v1/workspace/resources` | List resources |
| GET | `/v1/workspace/resources/{kind}/{name}` | Get resource |
| PUT | `/v1/workspace/resources/{kind}/{name}` | Save resource |
| DELETE | `/v1/workspace/resources/{kind}/{name}` | Delete resource |
| POST | `/v1/workspace/resources/export` | Export resources |
| POST | `/v1/workspace/resources/import` | Import resources |

Optional handlers add auth, scheduler, speech, workflow, metadata, file browser, and A2A endpoints.

### Auth Endpoints

When `WithAuth` is configured:

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/api/auth/providers` | List configured auth providers (public) |
| GET | `/v1/api/auth/me` | Current user identity |
| POST | `/v1/api/auth/local/login` | Local username login |
| POST | `/v1/api/auth/logout` | Logout |
| GET | `/v1/api/auth/idp/login` | Redirect to IDP (OAuth BFF) |
| GET | `/v1/api/auth/oauth/callback` | OAuth callback |
| POST | `/v1/api/auth/jwt/keypair` | Generate RSA keypair |
| POST | `/v1/api/auth/jwt/mint` | Mint JWT |

### A2A Endpoints

When `WithA2AHandler` is configured:

| Method | Path | Description |
|--------|------|-------------|
| GET | `/.well-known/agent.json` | Well-known agent card |
| GET | `/v1/api/a2a/agents` | List A2A-enabled agents |
| GET | `/v1/api/a2a/agents/{id}/card` | Get agent card |
| POST | `/v1/api/a2a/agents/{id}/message` | Send message to agent |

## Authentication

Configure JWT or OAuth BFF via `WithAuth` using the public [`service/auth`](./service/auth) types:

```go
authCfg := &auth.Config{
    Enabled:    true,
    CookieName: "agently_session",
    IpHashKey:  "your-hmac-salt",
    Local:      &auth.Local{Enabled: true},
    JWT: &auth.JWT{
        Enabled:       true,
        RSA:           []string{"/path/to/public.pem"},
        RSAPrivateKey: "/path/to/private.pem",
    },
}
sessions := svcauth.NewManager(7*24*time.Hour, nil)
jwtSvc := svcauth.NewJWTService(authCfg.JWT)
jwtSvc.Init(ctx)

handler, _ := sdk.NewHandlerWithContext(ctx, client, sdk.WithAuth(authCfg, sessions))
protected := svcauth.Protect(authCfg, sessions, svcauth.WithJWTService(jwtSvc))(handler)
```

Supported auth modes: `local`, `bff`, `spa`, `bearer`, `mixed`, `jwt`.

When `JWTService` is provided, valid JWT Bearer tokens are always accepted regardless of the primary auth mode.

## Parallel Tool Calls

Parallel tool calls are **enabled by default** for models that support it (e.g. OpenAI).
To disable for a specific agent, set `parallelToolCalls: false` in the agent YAML:

```yaml
# my-agent.yaml
parallelToolCalls: false  # disable parallel, use sequential
```

When omitted, the agent inherits the default (true).

## Workspace

Workspace root defaults to `.agently` under the current directory unless overridden
by `AGENTLY_WORKSPACE`.

Predefined resource kinds: `agents`, `models`, `embedders`, `mcp`, `workflows`,
`tools` (bundles, hints), `oauth`, `feeds`, `a2a`.

| Env var | Purpose |
|---------|---------|
| `AGENTLY_WORKSPACE` | Workspace root path |
| `AGENTLY_RUNTIME_ROOT` | Runtime root (defaults to workspace root) |
| `AGENTLY_STATE_PATH` | Runtime state root |
| `AGENTLY_WORKSPACE_NO_DEFAULTS` | Skip default bootstrapping |

## Persistence

SQL-backed persistence via Datly. Falls back to workspace SQLite when `AGENTLY_DB_*` not set.
Schema is auto-applied on startup — supports incremental migrations via `CREATE IF NOT EXISTS`.

| Env var | Default | Purpose |
|---------|---------|---------|
| `AGENTLY_DB_DRIVER` | `sqlite` | Database driver |
| `AGENTLY_DB_DSN` | (auto, workspace SQLite) | Connection string |

SQLite DB location: `$AGENTLY_WORKSPACE/db/agently-core.db`

## Scheduler

Supports cron, interval, and adhoc schedules with distributed lease coordination.

**Serverless deployment** (suppress scheduler):
```bash
AGENTLY_SCHEDULER_API=false AGENTLY_SCHEDULER_RUNNER=false ./agently serve
```

**Dedicated scheduler runner** (watchdog only):
```bash
AGENTLY_SCHEDULER_RUNNER=true AGENTLY_SCHEDULER_API=false ./agently serve
```

| Env var | Default | Purpose |
|---------|---------|---------|
| `AGENTLY_SCHEDULER_API` | `true` | Mount scheduler CRUD endpoints |
| `AGENTLY_SCHEDULER_RUN_NOW` | `true` | Enable run-now endpoint |
| `AGENTLY_SCHEDULER_RUNNER` | `false` | Enable watchdog in-process |

## SDK Modes

| Mode | Constructor | Use case |
|------|-------------|----------|
| Embedded | `sdk.NewEmbeddedFromRuntime(rt)` | In-process calls, no HTTP overhead |
| HTTP | `sdk.NewHTTP(baseURL, opts...)` | Remote client for deployed services |

Both implement `sdk.Client`.

## Package Layout

```
agently-core/
  sdk/                      Public SDK surface (Client, Handler, HTTP, Embedded)
  app/                      Application plumbing
    executor/               Runtime builder (Builder, Runtime, Defaults config)
    store/
      conversation/         Conversation domain types and helpers
      data/                 Datly-backed persistence facade
  service/                  Business logic services
    agent/                  Agent query orchestration
    auth/                   OAuth/JWT auth, sessions, token refresh, chatgpt provider
    a2a/                    Agent-to-agent protocol
    scheduler/              Schedule CRUD, watchdog, execution
    workspace/              Workspace metadata and file browser
  protocol/                 Domain models
    agent/                  Agent definition, finder, loader
    mcp/
      auth/integrate/       MCP OAuth round-tripper factory (BFF/bearer)
      config/               MCP client config
      cookies/              Per-user cookie jar provider
      expose/               Expose workspace tools as MCP HTTP server
      manager/              MCP client lifecycle manager
    tool/                   Tool registry, bundles, policies, system tools
    prompt/                 Prompt/system template types
  genai/                    LLM and embedder providers
    llm/                    OpenAI, Vertex AI, Bedrock, Grok, Ollama, InceptionLabs
    embedder/               Embedder provider abstraction
  workspace/                Workspace domain
    repository/             YAML resource repositories (agents, models, mcp, ...)
    loader/                 Config loaders
    service/                Metadata/YAML parsing
  internal/                 Private implementation details
    auth/                   Auth context helpers, JWT token manager
    script/                 DDL schemas (SQLite, MySQL)
  pkg/                      Datly DAO layer
    agently/                Read/write components for conversations, turns, messages, sessions, tokens
  e2e/                      End-to-end test infrastructure
```

## LLM Providers

| Provider | Models |
|----------|--------|
| OpenAI | GPT-4, GPT-4o, o1, o3, o4-mini, GPT-5.x series |
| Vertex AI (Gemini) | Gemini 2.0+, Gemini Flash |
| Vertex AI (Claude) | Claude 3.x, 4.x |
| Bedrock (Claude) | Claude via AWS Bedrock |
| InceptionLabs | Mercury series |
| Grok (xAI) | Grok 4+ |
| Ollama | Local open-source models |

## Testing

```bash
# Unit and integration tests
go test ./...

# E2E tests (Endly-driven)
cd e2e && endly -t=build && endly -t=test

# Auth E2E tests
go test ./e2e/auth/ -v

# SDK unit tests (including auth guard)
go test ./sdk/ -v
```

## Related Projects

- [agently](https://github.com/viant/agently) — CLI and HTTP server built on agently-core
- [mcp-sqlkit](https://github.com/viant/mcp-sqlkit) — MCP server for database operations
- [datly](https://github.com/viant/datly) — Data access layer for persistence
- [forge](https://github.com/viant/forge) — React UI framework used by the embedded web UI

## License

Apache License 2.0 — see [LICENSE](./LICENSE) and [NOTICE](./NOTICE).
