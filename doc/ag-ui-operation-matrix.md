# AG-UI backend operation inventory

This records the explicit SDK routes and their backend contract mapping. It describes the current backend milestone, not complete goal/conformance/backend parity. Production web/mobile/CLI shell migration is later.

`sdk/handler.go` currently declares 63 explicit method/path registrations, including two health aliases, the new AG-UI endpoint, and legacy aliases. These are route registrations, not 63 distinct missing features.

| Existing route | Intended AG-UI surface | Current evidence/status |
| --- | --- | --- |
| `GET /healthz` | Infrastructure | Outside the interaction migration |
| `GET /health` | Infrastructure | Outside the interaction migration |
| `POST /upload` | Artifact descriptors and explicit binary transfer binding | Pending mapping; ordinary message media is separately implemented |
| `POST /v1/agent/query` | Standard run/messages/tools | Implemented basic and client-tool flows; remaining lifecycle gates tracked |
| `POST /v1/ag-ui/run` | Unified AG-UI endpoint | Durable Datly 1.0 journal; all 31 pinned wire event schemas; frontend/root/nested client-tool handoff; detached `parentRunId`; focused approval/graph recovery regressions and assembled continuation checks pass; combined core/SDK, assembled protocol and authenticated Steward session/metadata checks pass; broader CLI and later route parity limits remain documented |
| `POST /v1/conversations` | Agently conversation/run resource commands | Pending mapping |
| `GET /v1/conversations/{id}` | Agently conversation/run resource commands | Pending mapping |
| `PATCH /v1/conversations/{id}` | Agently conversation/run resource commands | Pending mapping |
| `DELETE /v1/conversations/{id}` | Agently conversation/run resource commands | Pending mapping |
| `GET /v1/conversations/{id}/goal` | Agently goal v1 commands / activity subscription | Goal resource commands implemented; goal parity and background conformance remain open |
| `GET /v1/conversations/{id}/async` | Agently conversation/run resource commands | Pending mapping |
| `POST /v1/conversations/{id}/goal` | Agently goal v1 commands / activity subscription | Goal resource commands implemented; goal parity and background conformance remain open |
| `PATCH /v1/conversations/{id}/goal` | Agently goal v1 commands / activity subscription | Goal resource commands implemented; goal parity and background conformance remain open |
| `DELETE /v1/conversations/{id}/goal` | Agently goal v1 commands / activity subscription | Goal resource commands implemented; goal parity and background conformance remain open |
| `GET /v1/conversations` | Agently conversation/run resource commands | Pending mapping |
| `GET /v1/conversations/linked` | Agently conversation/run resource commands | Pending mapping |
| `GET /v1/conversations/{id}/transcript` | Standard message/state snapshots plus history management | Authoritative saved-history/recovery projection implemented; standalone history management remains pending |
| `GET /v1/conversations/{id}/live-state` | Standard message/state snapshots plus history management | Authoritative saved-history/recovery projection implemented; standalone history management remains pending |
| `POST /v1/conversations/{id}/terminate` | Agently targeted cancellation/steering/queue commands | Authorized cancellation, run inspection, and event paging commands wired; other controls pending |
| `POST /v1/conversations/{id}/compact` | Agently conversation/run resource commands | Pending mapping |
| `POST /v1/conversations/{id}/prune` | Agently conversation/run resource commands | Pending mapping |
| `GET /v1/runs/{id}` | Agently conversation/run resource commands | Pending mapping |
| `GET /v1/messages` | Standard message/state snapshots plus history management | Authoritative saved-history/recovery projection implemented; standalone history management remains pending |
| `GET /v1/elicitations` | Standard interrupts/resume + typed approval extensions | Standard interrupt/resume and durable approval receipts/claims implemented; standalone list/decision resource parity remains open |
| `POST /v1/api/payloads` | Artifact descriptors and explicit binary transfer binding | Pending mapping; ordinary message media is separately implemented |
| `GET /v1/api/payload/{id}` | Artifact descriptors and explicit binary transfer binding | Pending mapping; ordinary message media is separately implemented |
| `GET /v1/api/conversations/{id}/generated-files` | Artifact descriptors and explicit binary transfer binding | Pending mapping; ordinary message media is separately implemented |
| `GET /v1/api/generated-files/{id}/download` | Artifact descriptors and explicit binary transfer binding | Pending mapping; ordinary message media is separately implemented |
| `POST /v1/files` | Artifact descriptors and explicit binary transfer binding | Pending mapping; ordinary message media is separately implemented |
| `GET /v1/files` | Artifact descriptors and explicit binary transfer binding | Pending mapping; ordinary message media is separately implemented |
| `GET /v1/files/{id}` | Artifact descriptors and explicit binary transfer binding | Pending mapping; ordinary message media is separately implemented |
| `GET /v1/feeds` | Agently feed resources and background subscriptions | Typed `feed.*` resource commands implemented; subscription/feature parity remains open |
| `GET /v1/feeds/{id}/data` | Agently feed resources and background subscriptions | Typed `feed.*` resource commands implemented; subscription/feature parity remains open |
| `GET /v1/stream` | Per-run standard streams and separate subscription runs | Chat journal implemented; background migration partial |
| `POST /v1/turns/{id}/cancel` | Agently targeted cancellation/steering/queue commands | Authorized cancellation, run inspection, and event paging commands wired; other controls pending |
| `POST /v1/elicitations/{conversationId}/{elicitationId}/resolve` | Standard interrupts/resume + typed approval extensions | Standard interrupt/resume and durable approval receipts/claims implemented; standalone list/decision resource parity remains open |
| `POST /v1/conversations/{id}/turns/{turnId}/steer` | Agently targeted cancellation/steering/queue commands | Authorized cancellation, run inspection, and event paging commands wired; other controls pending |
| `DELETE /v1/conversations/{id}/turns/{turnId}` | Agently targeted cancellation/steering/queue commands | Authorized cancellation, run inspection, and event paging commands wired; other controls pending |
| `POST /v1/conversations/{id}/turns/{turnId}/move` | Agently targeted cancellation/steering/queue commands | Authorized cancellation, run inspection, and event paging commands wired; other controls pending |
| `PATCH /v1/conversations/{id}/turns/{turnId}` | Agently targeted cancellation/steering/queue commands | Authorized cancellation, run inspection, and event paging commands wired; other controls pending |
| `POST /v1/conversations/{id}/turns/{turnId}/force-steer` | Agently targeted cancellation/steering/queue commands | Authorized cancellation, run inspection, and event paging commands wired; other controls pending |
| `GET /v1/tools` | Standard tool execution plus Agently catalog/direct-command operations | Standard events implemented; management operations pending |
| `GET /v1/templates` | Agently skill/template discovery and resources | Pending mapping |
| `GET /v1/templates/{name}` | Agently skill/template discovery and resources | Pending mapping |
| `GET /v1/skills` | Agently skill/template discovery and resources | Pending mapping |
| `GET /v1/skills/diagnostics` | Agently skill/template discovery and resources | Pending mapping |
| `POST /v1/skills/{name}/activate` | Agently skill/template discovery and resources | Pending mapping |
| `POST /v1/tools/{name}/execute` | Standard tool execution plus Agently catalog/direct-command operations | Standard events implemented; management operations pending |
| `POST /v1/api/tools/{toolName}` | Standard tool execution plus Agently catalog/direct-command operations | Standard events implemented; management operations pending |
| `POST /v1/tools/execute` | Standard tool execution plus Agently catalog/direct-command operations | Standard events implemented; management operations pending |
| `GET /v1/tool-approvals/pending` | Standard interrupts/resume + typed approval extensions | Standard interrupt/resume and durable approval receipts/claims implemented; standalone list/decision resource parity remains open |
| `POST /v1/tool-approvals/{id}/decision` | Standard interrupts/resume + typed approval extensions | Standard interrupt/resume and durable approval receipts/claims implemented; standalone list/decision resource parity remains open |
| `POST /v1/workspace/resources/export` | Agently workspace resource commands | Typed dispatch and durable endpoint wired; domain tests pass; filesystem crash recovery reports uncertain effects without repeating writes |
| `POST /v1/workspace/resources/import` | Agently workspace resource commands | Typed dispatch and durable endpoint wired; domain tests pass; filesystem crash recovery reports uncertain effects without repeating writes |
| `GET /v1/workspace/resources/{kind}/{name}` | Agently workspace resource commands | Typed dispatch and durable endpoint wired; domain tests pass; filesystem crash recovery reports uncertain effects without repeating writes |
| `PUT /v1/workspace/resources/{kind}/{name}` | Agently workspace resource commands | Typed dispatch and durable endpoint wired; domain tests pass; filesystem crash recovery reports uncertain effects without repeating writes |
| `DELETE /v1/workspace/resources/{kind}/{name}` | Agently workspace resource commands | Typed dispatch and durable endpoint wired; domain tests pass; filesystem crash recovery reports uncertain effects without repeating writes |
| `GET /v1/workspace/resources` | Agently workspace resource commands | Typed dispatch and durable endpoint wired; domain tests pass; filesystem crash recovery reports uncertain effects without repeating writes |
| `POST /v1/api/datasources/{id}/fetch` | Agently datasource and lookup commands | Typed dispatch and durable endpoint wired; configured capability discovery; native fetch validation in progress |
| `DELETE /v1/api/datasources/{id}/cache` | Agently datasource and lookup commands | Typed dispatch and durable endpoint wired; configured capability discovery; native fetch validation in progress |
| `GET /v1/api/lookups/registry` | Agently datasource and lookup commands | Typed dispatch and durable endpoint wired; configured capability discovery; native fetch validation in progress |
| `GET /v1/api/mcp-ui/resources/read` | Agently host-only MCP Apps resource/tool commands | Pending mapping; preserve original result envelope and approvals |
| `POST /v1/api/mcp-ui/tools/call` | Agently host-only MCP Apps resource/tool commands | Explicit binding APIs and full host result capture exist; automatic app-scope resolution and native approval continuation are verified; current binding/configuration revocation is enforced |

## Optional and indirect routes

The explicit list does not include routes registered inside optional handlers. Audit these before declaring full Agently backend parity:

- Workspace metadata/layout/windows/themes/file browser: typed resources, renderer/device capabilities, permissions, revisions and transfer bindings.
- UI bridge `/v1/ui/rpc`: attach/client lease, targeted commands, snapshots and acknowledgments dispatched independently of the chat queue.
- Report runs/exports: deterministic begin/complete/adopt/export and artifact delivery with existing authorization.
- Scheduler: schedule resources, run-now and independent update subscriptions.
- Speech: audio input/output and streaming transfer bindings; model/media support must be explicit.
- Authentication: remains a transport/authentication concern; never accept forwarded identity as authority.
- A2A and backend MCP: remain backend interoperability protocols, not alternate shell interaction protocols.

Unsupported Forge activity/custom payloads must stay outside assistant text. A generic client may ignore the extension; rich commands must be capability-gated, with acknowledgment, timeout and permission behavior defined. Native Forge code fences require a plain-text projection so raw authoring JSON does not become chat prose.

## Completion gates

For each operation: publish a versioned payload/result schema; preserve authenticated authority and service semantics; prove idempotency and failure behavior; advertise only implemented capabilities; test the backend through AG-UI rather than a legacy network route. Map both desired workspace state and acknowledged device state. Goal/feed/workspace updates may outlive chat and need independent subscription lifecycles.

## Current implementation evidence

The durable endpoint accepts `workspace.*`, `datasource.*`, `lookup.*`, `feed.*`,
`run.get`, `run.events.list`, and `run.cancel` resource commands with strict payload
validation. Goal/state resource commands are also implemented. Metadata services
are supplied by trusted handler configuration. Resource commands use independent
protocol runs and do not submit model queries. The backend milestone includes
scoped public tool IDs, client-tool handoff at frontend/root/nested levels,
detached runs with `parentRunId`, and backend Forge fence extraction ahead of
legacy TypeScript client parsing. The official CopilotKit shell is an
interoperability harness; it does not establish production shell migration.

HTTP regression tests verify metadata and run resource command replay, unchanged
chat messages/state, scoped turn-based public tool IDs, causal child-event order,
and Forge extraction for preset responses. MySQL 8.4 `newAGUI` table two-runtime
lease/resume race behavior and exact JSON were verified. The full legacy-schema
bootstrap encountered a preexisting invalid `op_id` index and was not validated.
Do not claim complete goal/conformance/backend parity: focused approval and graph
integration checks pass, while combined repository acceptance and remaining
operation parity are tracked separately. Interactive MCP Apps
automatic app-scope resolution is implemented and verified as described in
[`ag-ui-mcp-app-scope-plan.md`](ag-ui-mcp-app-scope-plan.md); explicit binding APIs
and full host result capture exist. Keep host results and `_meta` outside model
history. The local `../mcp-protocol-ag-ui` replacement is required until its
wire fix is published and pinned. No commits have been made; local builds may
need sibling worktree replacements.

`run.events.list` returns standard event objects plus scoped cursor and sequence;
`run.get` omits accepted input, credentials, principal, and native execution IDs.
Filesystem commands write a dispatch boundary before effects; recovery after
that boundary reports uncertainty and requires inspection rather than repeating
an operation whose first result might have committed.
