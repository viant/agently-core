# agently-core

[![Go Reference](https://pkg.go.dev/badge/github.com/viant/agently-core.svg)](https://pkg.go.dev/github.com/viant/agently-core)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Agently Core is an embeddable Go framework for agentic applications. It combines
model execution, tools, durable conversations, recovery, approvals, goals and
workspace configuration. Applications can call it in process or expose its HTTP
and AG-UI interfaces, then build their own web, mobile or command-line experience.

Use [Agently](https://github.com/viant/agently) for the assembled server, CLI and
application shells. Use this module when your service needs to own the runtime,
authentication, integrations or presentation.

## What the framework provides

| Area | Capabilities |
| --- | --- |
| Execution | Agent selection, prompt binding, model/tool loops, parallel tools, streaming, reasoning and linked agent invocations |
| Models | Adapters for OpenAI, Vertex AI Gemini/Claude, Bedrock Converse/Claude, Grok, InceptionLabs and Ollama |
| Tools | Internal services, MCP clients, tool bundles, policy, skills, resources and optional MCP tool exposure |
| Durable interaction | Conversation, turn, message, model/tool-call and payload persistence; queued turns, deferred elicitation, approval receipts and continuation |
| Recovery | Observer reattachment, admitted-run replay, native outcome reconstruction, cancellation and context-limit recovery |
| Goals and scheduling | Objective/budget accounting, pause/resume, scheduled wakeups, cron/interval/adhoc execution and distributed leases |
| Authentication | Local sessions, JWT RSA/HMAC, OAuth BFF/SPA/bearer/mixed modes and per-user MCP credentials |
| Presentation | Canonical transcripts, hosted workspace metadata, Forge content, feeds, layouts, themes, CSS and native font assets |
| Reporting | Authored report documents, scoped dataset requests, durable report lifecycle, frozen results and explicit export/publication operations |

Model availability and capabilities depend on the selected provider and adapter.
A provider adapter does not imply universal multimodal, streaming or exact
token-count support. See [provider configuration](doc/llm-providers.md).

## Start with an embedded runtime

The module requires Go 1.25.8 or newer. An application supplies configured agent
and model finders; the runtime owns the execution loop.

~~~go
ctx := context.Background()

rt, err := executor.NewBuilder().
    WithAgentFinder(agentFinder).
    WithModelFinder(modelFinder).
    Build(ctx)
if err != nil {
    log.Fatal(err)
}
defer rt.Close(ctx)

client, err := sdk.NewEmbeddedFromRuntime(rt)
if err != nil {
    log.Fatal(err)
}
out, err := client.Query(ctx, &agentsvc.QueryInput{
    ConversationID: "conversation-id",
    UserId:         "authenticated-user-id",
    Query:          "Summarize the project documentation",
})
~~~

This is a Go usage fragment: import the application/runtime types and provide
the finders and authenticated caller identity. Embedded Go Client.Query and the
internal executor remain native calls. They do not recursively POST through
AG-UI. See [SDK usage](doc/sdk.md) and [architecture](doc/architecture.md).

To expose the runtime over HTTP:

~~~go
handler, err := sdk.NewHandlerWithContext(ctx, client)
if err != nil {
    log.Fatal(err)
}
log.Fatal(http.ListenAndServe(":8090", handler))
~~~

Configure authentication for the deployment; the handler's health endpoint is
GET /healthz. The [auth guide](doc/auth-system.md) covers session managers,
JWT verification, OAuth configuration and middleware.

## Conversation APIs and outward SDKs

TypeScript, Swift and Kotlin SDKs default to **AG-UI** for outward conversation
submission, bootstrap, observation, attach, cancellation and continuation. Their
workspace, layout, upload, reporting and application APIs continue to use the
same configured BFF client.

Standard AG-UI requests/events carry runs, messages, tools, state, frontend tool
results, interrupts, resumes and subagent attribution. Versioned **Agently
extensions** add application commands and presentation, including native turn
identity, goals, approvals, queue controls, workspaces, feeds and MCP Apps.
Keep the extension contract explicit when integrating a generic AG-UI client.

| Endpoint family | Purpose |
| --- | --- |
| POST /v1/ag-ui/run | AG-UI run input and durable SSE output |
| /v1/conversations | Native conversation creation, metadata and canonical history |
| /v1/turns, /v1/elicitations, /v1/tool-approvals | Native turn controls and typed interaction services |
| /v1/tools, /v1/workspace/resources | Tool discovery/execution and workspace resource management |
| /v1/application-events | Explicit scoped native/application background observation |

An admitted run can outlive an HTTP connection or view. Reopening attaches to
the existing journal rather than sending the prompt again. An observer loss
does not itself mean the native execution failed.

Public thread/run identifiers are opaque and byte-preserved. Native
conversation/turn identities are separate. An authorized existing conversation
may bind its exact ID; an unknown public thread receives an internal conversation
ID. Authenticated native history exposes an optional aguiThreadId reference so
SDKs keep the native UI/history identity while reopening the original wire
thread. Account changes invalidate old cached bindings and transport responses.

Conversation SDKs expose one AG-UI interaction path; there is no legacy
protocol selector or automatic query/stream fallback. Current cookies,
authentication headers and injected networking remain in use. Dedicated
readConversationHistory/readApplicationState helpers access authorized native
BFF history and application snapshots; a shared reader denied the owner's
private protocol journal can use those reads without executing another query.

Read [TypeScript](sdk/ts/AG-UI.md), [Swift](sdk/ios/AG-UI.md),
[Kotlin](sdk/android/AG-UI.md), [approval coordination](doc/ag-ui-approval-coordination.md)
and the [operation matrix](doc/ag-ui-operation-matrix.md). Protocol/SDK tests
are not a claim of full product-shell parity or universal conformance.

## Configure and extend a workspace

The embedded workspace defaults to .agently in the working directory;
AGENTLY_WORKSPACE selects another root. Resource repositories hold agents,
models, embedders, MCP clients, tool bundles, workflows, OAuth configuration,
feeds and A2A definitions.

~~~text
.agently/
  agents/
  models/
  embedders/
  mcp/
  tools/bundles/
  intents/
  templates/
  workflows/
  feeds/
~~~

Agent definitions connect a model selection, prompt, tools and policies.
Intent profiles combine scenario instructions, scoped knowledge, bundles,
templates and evidence requirements. They live in intents/ and retain a
documented migration path from legacy prompts/. Invalid profiles fail rather
than silently selecting another definition.

Workspace YAML supports imported fragments, keyed imports and scoped parameters.
Exact parameter values retain YAML types; nested scopes inherit/override without
leaking into siblings. Use the workspace APIs to manage resources, or version
the authored files with your application.

Extend the framework by providing finders, registering internal tool services,
connecting MCP servers, selecting bundles/policies and adding application-owned
handlers or native Datly compositions. Per-user auth and approved origins remain
the boundary for MCP credential reuse. A2A and MCP exposure are optional
capabilities, not implicit public access to every internal service.

See [workspace configuration](doc/workspace-system.md), [intents](doc/prompts.md),
[tools](doc/tool-system.md), [MCP integration](doc/mcp-integration.md),
[skills](doc/skills.md), [templates](doc/templates.md) and [A2A](doc/a2a-protocol.md).

## Durable execution and interaction

Deferred elicitation and approvals preserve the original native turn and tool
operation. Approval decisions use persisted receipts; continuation validates
ownership and admitted scope before supplying a result or resuming. Queued
requests retain their own identity. Cancellation and recovery reconcile native
state rather than inventing a successful terminal result.

Async tools declare start/status/cancel behavior and extraction rules. Distinct
status tools use one runtime poller per operation; updates remain associated
with the persisted original tool/message identity. Linked agents retain parent,
child and invocation attribution, including detached execution.

Goals retain objective, budget/accounting and scheduler state independently of
transport observation. Management commands use domain services rather than a
model round trip. Scheduler API and runner deployment can be separated with
AGENTLY_SCHEDULER_API and AGENTLY_SCHEDULER_RUNNER.

Read [async execution](doc/async.md), [elicitation](doc/elicitation-system.md),
[approvals](doc/approval.md), [autonomous goals](doc/autonomous.md),
[scheduler](doc/scheduler.md) and [recovery](doc/conversation-model.md).

## Presentation, workspaces and reports

Applications retain three scopes: app navigation, conversation-owned hosted
workspaces and message/turn-owned inline content. Layouts and controls belong
to their authored workspace; AG-UI transports their state and progress without
requiring a replacement application UI. Tool feeds can be inline or detached,
while canonical history retains their actual tool identity.

Theme manifests, scoped CSS and authenticated native font assets support
application and workspace appearance. Forge remains the generic renderer;
applications own policy, admission and their BFF integration.

Reports capture authored documents and exact dataset requests before execution.
Begin, compile, complete and activate are separate phases. Saved completion and
active conversation context are distinct outcomes. Verified frozen rows can
restore a completed report without an implicit dataset rerun. Export and
publication remain explicit actions.

See [UI ownership](doc/ui-ownership-model.md), [feeds](doc/feed-system.md),
[MCP UI](doc/mcp-ui.md) and [native contracts](doc/datly-sdk-contract.md).
Application-specific forecast evidence binding remains gated and is not enabled
by default at startup.

## Optional proactive context compaction

Proactive compaction is off unless an agent explicitly opts in:

~~~yaml
# Agent
contextCompactionPercent: 80
~~~

The selected model must declare its actual positive capacity separately:

~~~yaml
# Model configuration; substitute this model/provider's actual capacity.
options:
  contextWindow: 400000
~~~

The percentage must be finite and in (0, 100]. Output-token limits are not
context capacity. At or above the threshold, the runtime compacts eligible
completed history, rebuilds/recounts the request and continues. The threshold
is a trigger, not a hard cap. Latest-user and pending-operation protections,
completed tool identities and durable full-history barriers cover continuation
after failures and restarts.

Exact prepared-input counting is implemented by the OpenAI Responses API
adapter through /responses/input_tokens with its normal HTTP client and
credentials. Input messages, instructions, tools and supported multimodal
content are retained; generation-only fields are excluded from the count
request. Chat Completions, the ChatGPT backend and other providers are not
implicitly exact-counter compatible. Unsupported configuration/counting and
endpoint failures produce actionable errors, without a character-count
approximation. Ordinary reactive provider-limit recovery remains available.

See [compaction configuration and tests](doc/proactive-context-compaction.md).
Live provider verification is opt-in; unit and HTTP failure tests use fixtures.

## Persistence and Datly authoring

SQLite and MySQL store native conversations, turns, messages, model/tool calls,
payloads, goals, schedules and reports. Workspace SQLite is bootstrapped by the
configured schema/upgrade path. Provision the versioned MySQL schema before
deployment; reporting stores do not create their own schema.

AG-UI reuses **conversation, run and call_payload**. Conversation columns hold
the protocol projection/revision; run_kind='agui' rows hold protocol admission
and observer leases; call_payload.kind='agui.event' stores ordered journal
events by internal run and sequence. Native execution remains isolated to
run_kind='execution'. Native writes cannot overwrite protocol identity/state.
Owned deletion follows journal payloads, protocol runs and conversation
projections in the existing managed transaction; ordinary GC retains run-owned
payloads. There is no separate four-table protocol storage layer.

| Setting | Purpose |
| --- | --- |
| AGENTLY_WORKSPACE | Workspace root |
| AGENTLY_DB_DRIVER | sqlite or mysql |
| AGENTLY_DB_DSN | Configured connection string |
| AGENTLY_RUNTIME_ROOT / AGENTLY_STATE_PATH | Runtime/state roots |
| AGENTLY_WORKSPACE_NO_DEFAULTS | Skip default workspace seeding |

DQL under dql/ and adjacent SQL are the reader/writer/cube source of truth.
Generated contracts/resources live under internal/datly/; managed application
compositions live under internal/store/. Prefer ordinary outer view wildcards;
keep explicit aliases/conversions/keys for genuine contract requirements.
Application lifecycle changes belong in authored create-once hooks.

~~~bash
cd e2e/datly
endly -t=transcribe
~~~

The pinned stock Datly tool transcribes the local schema fixture and overwrites
generated artifacts. Do not repair an authoring error by hand-patching generated
Go. This is the authoring workflow; no separate regeneration workflow is needed.
Linked native runtime components embed their SQL/resources and do not require a
source checkout or authoring command at application startup.

See [storage/schema upgrades](doc/ag-ui-storage-reuse.md),
[deletion](doc/conversation-deletion.md), [maintenance](doc/database-maintenance.md)
and the [transcribe task](e2e/datly/transcribe.yaml).

## Build, test and deploy

This branch requires Go 1.25.8+ and the compatible module graph in go.mod.
Its local fork replacements select sibling mcp-protocol-ag-ui, mcp-ag-ui and
forge-ag-ui checkouts. Preserve those paths/revisions when building this branch;
an application's module/workspace must select its own complete dependency graph.

~~~bash
# From the repository root
go test ./sdk ./internal/datly/contracttest ./app/store/data
(cd sdk/ts && npm ci && npm run typecheck && npm test)
swift test --package-path sdk/ios
(cd sdk/android && ./gradlew testDebugUnitTest testReleaseUnitTest)
~~~

Android requires JDK 17 and the configured Android SDK. Live LLM/MCP/device
tests need explicit environment setup and credentials; ordinary unit checks do
not establish service-connected UI acceptance. The [startup profiler](e2e/startup/README.md)
separates runtime initialization from launch and first-query materialization.

Deploy a protected HTTP handler with the selected workspace, schema, provider
configuration and supporting resource/assets. Retain current BFF/session auth
for supporting APIs. Debugging can be global via AGENTLY_DEBUG, or
request-scoped through SDK SessionDebug options and X-Agently-Debug headers;
avoid placing credentials or private input in diagnostics.

Further documentation is indexed in [doc/README.md](doc/README.md).
Apache License 2.0: [LICENSE](LICENSE), [NOTICE](NOTICE).
