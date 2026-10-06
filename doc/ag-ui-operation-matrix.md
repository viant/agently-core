# AG-UI backend operation matrix

Agently exposes standard [AG-UI](https://docs.ag-ui.com/spec/1.0) agent interaction, versioned Agently extensions, and supporting application APIs. This matrix records their responsibilities, implemented operations, and verification limits. Conversation resource CRUD, binary upload/download, authentication, and workspace management remain application API responsibilities.

The route authority is [`sdk/handler.go`](../sdk/handler.go). The standard interaction endpoint is `POST /v1/ag-ui/run`; application notifications use `GET /v1/application-events`. The handler does not register `/v1/agent/query` or `/v1/stream`.

## Standard AG-UI interaction

The durable backend implements [AG-UI 1.0](https://docs.ag-ui.com/spec/1.0): it accepts `RunAgentInput` and streams standard events on the POST response. Agently-specific data uses extension points rather than additional standard input fields. Reproducible schema and consumer test versions are recorded in the [schema reference](../protocol/agui/SCHEMA.md) and [conformance guide](ag-ui-reducer-conformance.md).

| Surface | Implemented behavior and evidence |
| --- | --- |
| Run lifecycle and streaming | Ordered journal observation with run start and terminal outcomes; queued and preset responses are covered by [`handler_agui_durable_test.go`](../sdk/handler_agui_durable_test.go) and [`handler_agui_test.go`](../sdk/handler_agui_test.go). |
| Messages and history | Text/tool messages and authoritative saved-history projection; client history is reconciled rather than appended as a new query. See [`agui_host_history_test.go`](../sdk/agui_host_history_test.go) and [`agui_native_history_mapping_test.go`](../sdk/agui_native_history_mapping_test.go). |
| Tools and continuation | Backend tool events and client-tool handoff at frontend, root, and nested execution levels; dependency continuation has focused coverage in [`agui_dependency_resume_test.go`](../sdk/agui_dependency_resume_test.go). |
| Human input | Interrupt/resume, approval receipts, and coordinated continuation. See [`agui_interrupts_test.go`](../sdk/agui_interrupts_test.go), [`agui_approval_frontend_resume_test.go`](../sdk/agui_approval_frontend_resume_test.go), and [`agui_approval_commands_test.go`](../sdk/agui_approval_commands_test.go). |
| Shared state | State snapshots, RFC 6902 deltas, persistent state and input reconciliation. See [`agui_state_commands_test.go`](../sdk/agui_state_commands_test.go) and [`runtime/aguistate`](../runtime/aguistate). |
| Child runs and attribution | Scoped tool identity, nested execution and detached `parentRunId` behavior. See [`agui_child_recovery_test.go`](../sdk/agui_child_recovery_test.go) and [`agui_native_identity_test.go`](../sdk/agui_native_identity_test.go). |
| Disconnect and recovery | Disconnect detaches HTTP observation; work continues journaling. Reattachment/replay does not resubmit an admitted input. Explicit cancellation is an Agently command. See [`agui_run_attach_test.go`](../sdk/agui_run_attach_test.go) and [`agui_recovery_test.go`](../sdk/agui_recovery_test.go). |

These behaviors refer to the durable runtime selected by [`handleAGUIRun`](../sdk/handler_agui.go). An injected backend without the durable runtime interface uses a more limited handler that rejects parent runs, resume, client tools, supplied context, and nonempty shared state; it does not provide durable replay.

## Versioned Agently extensions

Commands use `forwardedProps.agently` with version `"1"` and a named operation. Resource commands run independently of model queries, retain authenticated scope, and return their results within standard run boundaries. Schemas reside in [`protocol/agui/extensions`](../protocol/agui/extensions); admission and dispatch are implemented in [`handler_agui_durable.go`](../sdk/handler_agui_durable.go).

| Family | Implemented operation names |
| --- | --- |
| Discovery and execution | `capabilities`, `chat`; typed execution selection for supported agent/model/runtime controls. |
| Conversation bootstrap | `conversation.bootstrap`. This is not a general conversation CRUD command family. |
| Run observation and control | `run.get`, `run.events.list`, `run.attach`, `run.cancel`. |
| State | `state.get`, `state.patch`. |
| Approvals | `approval.decide`. |
| Goals | `goal.get`, `goal.create`, `goal.update`, `goal.clear`, `goal.pause`, `goal.resume`, `goal.subscribe`. |
| Workspace metadata | `workspace.metadata.get`, `workspace.publicagents.list`, `workspace.layout.get`, `workspace.tools.list`, `workspace.models.list`, `workspace.model.get`, `workspace.model.save`. |
| Workspace resources | `workspace.resource.list`, `workspace.resource.get`, `workspace.resource.save`, `workspace.resource.delete`, `workspace.resource.export`, `workspace.resource.import`. |
| Datasources and lookups | `datasource.fetch`, `datasource.cache.invalidate`, `lookup.registry`. |
| Feeds | `feed.list`, `feed.get`, `feed.subscribe`. |

Discovery reflects configured services, feature switches, metadata bindings and MCP Apps host bindings; availability must be read from [`aguiDurableCapabilities`](../sdk/agui_capabilities.go), not inferred from this union of operations. Goal/feed subscriptions have independent run lifecycles, with tests in [`agui_goal_subscription_test.go`](../sdk/agui_goal_subscription_test.go) and [`agui_feed_subscription_test.go`](../sdk/agui_feed_subscription_test.go).

MCP Apps use a separate scoped proxy envelope and configured host bindings. Host results, including `_meta`, are kept outside model history; authorization and approval continuation are covered by [`agui_mcp_apps_native_approval_test.go`](../sdk/agui_mcp_apps_native_approval_test.go) and [`agui_mcp_apps_host_test.go`](../sdk/agui_mcp_apps_host_test.go). Forge presentation uses versioned presentation metadata/activity rather than raw authoring JSON in assistant prose; see [`agui_rendering_test.go`](../sdk/agui_rendering_test.go).

## Supporting application APIs

These implemented HTTP handlers serve application resources and host services alongside AG-UI interaction. Their presence does not imply that a generic AG-UI agent implements the same application features. The SDK wraps routes with authentication middleware when authentication is enabled and a session manager is configured; domain handlers also apply their own scope and ownership checks.

| API family | Current registered surface |
| --- | --- |
| Conversations and history | `/v1/conversations` CRUD/list, linked conversations, goal CRUD, async operations, transcript, live state, terminate, compact and prune; `/v1/messages`; `/v1/runs/{id}`. |
| Turn controls and human input | Turn cancellation; steer, queued-turn delete/move/edit/force-steer; elicitation list/resolve; pending tool approvals and decisions. |
| Files and payloads | `/upload`, `/v1/files` upload/list/download, payload retrieval, generated-file list/download. Binary transfer uses these HTTP APIs; the AG-UI capability descriptor advertises `httpBinary: false`. |
| Tools, templates and skills | Tool discovery/direct execution; template list/get; skill list/diagnostics/activation. |
| Workspace resources and data | Resource list/get/save/delete/import/export; datasource fetch/cache invalidation and lookup registry. Datasource handlers return 501 when the service is not configured. |
| Application notifications | `/v1/application-events`, independent of an individual chat run. |
| Infrastructure | `/healthz`, `/health`. |

Optional handler configuration mounts authentication/preferences, speech, scheduler, workspace metadata, file browser, A2A, callback dispatch, UI bridge (`/v1/ui/rpc`), report runs, and MCP UI resource-read/tool-call APIs. Their availability depends on the installed handler/service and its configuration; route registration is in [`registerOptionalRoutes`](../sdk/handler.go). Authentication and backend interoperability remain transport/host responsibilities.

## HTTP route matrix

The following 60 method/path registrations are present in `registerCoreRoutes`. “Supporting API” means a registered application handler, not a pending AG-UI feature. Service availability, authentication, ownership and validation still apply. The extension column identifies related operations; it does not imply that HTTP and protocol-run identities or payloads are interchangeable.

| HTTP route | Role / status | Related AG-UI surface |
| --- | --- | --- |
| `GET /healthz` | Infrastructure health | Outside agent interaction |
| `GET /health` | Infrastructure health | Outside agent interaction |
| `POST /upload` | Staged upload · supporting API | Application API; no dedicated extension command |
| `POST /v1/ag-ui/run` | Standard AG-UI endpoint | `RunAgentInput` → SSE events; configured extensions |
| `POST /v1/conversations` | Create conversation · supporting API | Application API; no dedicated extension command |
| `GET /v1/conversations/{id}` | Read conversation · supporting API | Application API; no dedicated extension command |
| `PATCH /v1/conversations/{id}` | Update conversation · supporting API | Application API; no dedicated extension command |
| `DELETE /v1/conversations/{id}` | Delete conversation and owned state · supporting API | Application API; no dedicated extension command |
| `GET /v1/conversations/{id}/goal` | Supporting API | `goal.get` (Agently extension) |
| `GET /v1/conversations/{id}/async` | List asynchronous operations · supporting API | Application API; no dedicated extension command |
| `POST /v1/conversations/{id}/goal` | Supporting API | `goal.create` (Agently extension) |
| `PATCH /v1/conversations/{id}/goal` | Supporting API | `goal.update` (Agently extension) |
| `DELETE /v1/conversations/{id}/goal` | Supporting API | `goal.clear` (Agently extension) |
| `GET /v1/conversations` | List conversations · supporting API | Application API; no dedicated extension command |
| `GET /v1/conversations/linked` | List linked conversations · supporting API | Application API; no dedicated extension command |
| `GET /v1/conversations/{id}/transcript` | Read canonical transcript · supporting API | Standard message snapshots / `conversation.bootstrap` also expose admitted history |
| `GET /v1/conversations/{id}/live-state` | Read native conversation state · supporting API | Application API; no dedicated extension command |
| `POST /v1/conversations/{id}/terminate` | Terminate native conversation work · supporting API | `run.cancel` targets a protocol run; this API targets native work |
| `POST /v1/conversations/{id}/compact` | Compact model context · supporting API | Application API; no dedicated extension command |
| `POST /v1/conversations/{id}/prune` | Prune conversation context · supporting API | Application API; no dedicated extension command |
| `GET /v1/runs/{id}` | Inspect native execution run · supporting API | `run.get` inspects protocol runs; this API inspects native execution |
| `GET /v1/messages` | Read messages · supporting API | Standard message snapshots / `conversation.bootstrap` also expose admitted history |
| `GET /v1/elicitations` | List pending elicitations · supporting API | Application API; no dedicated extension command |
| `POST /v1/api/payloads` | Retrieve payload batch · supporting API | Application API; no dedicated extension command |
| `GET /v1/api/payload/{id}` | Retrieve payload · supporting API | Application API; no dedicated extension command |
| `GET /v1/api/conversations/{id}/generated-files` | List generated files · supporting API | Application API; no dedicated extension command |
| `GET /v1/api/generated-files/{id}/download` | Download generated file · supporting API | Application API; no dedicated extension command |
| `POST /v1/files` | Upload file · supporting API | Application API; no dedicated extension command |
| `GET /v1/files` | List files · supporting API | Application API; no dedicated extension command |
| `GET /v1/files/{id}` | Download file · supporting API | Application API; no dedicated extension command |
| `GET /v1/feeds` | Supporting API | `feed.list` (Agently extension) |
| `GET /v1/feeds/{id}/data` | Supporting API | `feed.get` (Agently extension) |
| `GET /v1/application-events` | Application notification stream | Independent host notifications |
| `POST /v1/turns/{id}/cancel` | Cancel native turn · supporting API | `run.cancel` targets a protocol run; this API targets native work |
| `POST /v1/elicitations/{conversationId}/{elicitationId}/resolve` | Resolve elicitation · supporting API | Standard interrupt/resume also continues admitted interactions |
| `POST /v1/conversations/{id}/turns/{turnId}/steer` | Steer turn · supporting API | Application API; no dedicated extension command |
| `DELETE /v1/conversations/{id}/turns/{turnId}` | Delete queued turn · supporting API | Application API; no dedicated extension command |
| `POST /v1/conversations/{id}/turns/{turnId}/move` | Move queued turn · supporting API | Application API; no dedicated extension command |
| `PATCH /v1/conversations/{id}/turns/{turnId}` | Edit queued turn · supporting API | Application API; no dedicated extension command |
| `POST /v1/conversations/{id}/turns/{turnId}/force-steer` | Force-steer queued turn · supporting API | Application API; no dedicated extension command |
| `GET /v1/tools` | Discover tools · supporting API | Standard tool declarations; `workspace.tools.list` for configured workspace catalog |
| `GET /v1/templates` | List templates · supporting API | Application API; no dedicated extension command |
| `GET /v1/templates/{name}` | Read template · supporting API | Application API; no dedicated extension command |
| `GET /v1/skills` | List skills · supporting API | Application API; no dedicated extension command |
| `GET /v1/skills/diagnostics` | Inspect skill diagnostics · supporting API | Application API; no dedicated extension command |
| `POST /v1/skills/{name}/activate` | Activate skill · supporting API | Application API; no dedicated extension command |
| `POST /v1/tools/{name}/execute` | Execute named tool directly · supporting API | Application API; no dedicated extension command |
| `POST /v1/api/tools/{toolName}` | Execute named tool directly · supporting API | Application API; no dedicated extension command |
| `POST /v1/tools/execute` | Execute tool named in request · supporting API | Application API; no dedicated extension command |
| `GET /v1/tool-approvals/pending` | List pending approvals · supporting API | Application API; no dedicated extension command |
| `POST /v1/tool-approvals/{id}/decision` | Supporting API | `approval.decide` (Agently extension) |
| `POST /v1/workspace/resources/export` | Supporting API | `workspace.resource.export` (Agently extension) |
| `POST /v1/workspace/resources/import` | Supporting API | `workspace.resource.import` (Agently extension) |
| `GET /v1/workspace/resources/{kind}/{name}` | Supporting API | `workspace.resource.get` (Agently extension) |
| `PUT /v1/workspace/resources/{kind}/{name}` | Supporting API | `workspace.resource.save` (Agently extension) |
| `DELETE /v1/workspace/resources/{kind}/{name}` | Supporting API | `workspace.resource.delete` (Agently extension) |
| `GET /v1/workspace/resources` | Supporting API | `workspace.resource.list` (Agently extension) |
| `POST /v1/api/datasources/{id}/fetch` | Supporting API | `datasource.fetch` (Agently extension) |
| `DELETE /v1/api/datasources/{id}/cache` | Supporting API | `datasource.cache.invalidate` (Agently extension) |
| `GET /v1/api/lookups/registry` | Supporting API | `lookup.registry` (Agently extension) |

### Optional mounts

These routes are registered only when the corresponding handler or binding is configured. Additional methods and subpaths are owned by the mounted handler.

| Route / mount | Role / status | Related AG-UI surface |
| --- | --- | --- |
| `/v1/api/report-runs`, `/v1/api/report-runs/` | Configured report-run handler | Supporting report lifecycle and artifact APIs |
| `/v1/ui/rpc` | Configured UI bridge | Host command delivery, snapshots and acknowledgments |
| `GET /v1/api/mcp-ui/resources/read` | Configured MCP UI resource reader | Scoped MCP Apps proxy also supports `resources/read` |
| `POST /v1/api/mcp-ui/tools/call` | Configured MCP UI tool caller | Scoped MCP Apps proxy also supports `tools/call` |

The other optional handler families are listed above. Discovery/demo routes are additionally registered when their configured registry is present.

## Verification scope and limits

Checked-in wire fixtures cover all 31 standard event variants. Consumer tests compare normalization, accepted/rejected sequences, messages and state against the upstream reference reducer. This is evidence for those fixtures, not proof that every producer emits every variant or that every application feature has complete cross-client parity. [`ag-ui-reducer-conformance.md`](ag-ui-reducer-conformance.md) records the intentional stricter server validation rules.

The custom command surface does not include general conversation CRUD/list/compact/prune, queued-turn steering/edit/move, file transfer, template/skill management, scheduler management, or direct arbitrary tool execution. Those operations have supporting APIs where registered above; their absence from the extension dispatch is not an AG-UI standard compliance defect.

Durable recovery is operation-specific. Filesystem resource commands record a dispatch boundary before effects; recovery after an uncertain effect requires inspection instead of repeating a possibly committed write. MCP Apps tool effects likewise expose uncertainty when completion cannot be established. Runtime configuration can disable services or remove bindings, so clients must honor advertised capabilities and authorization failures.

The focused tests linked here document implemented behavior and regression coverage. They are not an exhaustive interoperability certification, a record of tests executed on every deployment, or a claim of complete web/iOS/Android/CLI application parity.
