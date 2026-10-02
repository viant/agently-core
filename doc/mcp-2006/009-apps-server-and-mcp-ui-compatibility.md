# 009 — Implement MCP Apps Server Contracts and Migrate `mcp-ui`

Status: planned  
Parent workstream: WS-4  
Primary repositories: `mcp-ext`, `mcp-ui`, `agently-core`  
Depends on: [001](001-specification-pins-and-fixtures.md), [002](002-schema-packaging-and-generation.md), [003](003-stateless-core-and-discovery.md), [007](007-mcp-ext-foundation.md)

## Outcome

Make the official MCP Apps extension the canonical server-side UI contract.
Retain useful `mcp-ui` resource and metadata helpers behind a versioned
compatibility facade, while deprecating—not immediately deleting—the private
`mcpui:*` wire protocol.

## Deliverables

- `mcp-ext/apps` types and validators generated or traced to the pinned Apps
  schema, including `_meta.ui.resourceUri`, `ui://` resources, MIME types,
  CSP, permissions, visibility, and app-only tools;
- server registration APIs that bind a tool to its UI resource explicitly;
- complete `CallToolResult` delivery, including structured content, ordinary
  content, `_meta`, and errors; cancellation remains the host notification
  defined by the pinned Apps lifecycle and is implemented in task 010;
- a versioned `mcp-ui` adapter that translates private envelopes at the host
  boundary without redefining their wire meaning;
- reverse-DNS names for any surviving Viant-only metadata;
- non-UI fallback behavior for hosts that do not negotiate Apps;
- a released `mcp-ui` dependency pin and CI lane that runs without local
  `replace` directives;
- deprecation telemetry and documented removal gates for private messages.
- Apps advertisement/negotiation on the exact task-001 pinned extension surface;
- a host-supplied telemetry sink and server/adapter feature flags; task 009 owns
  server-side legacy envelope translation, while task 010 owns iframe bridging.

## Compatibility policy

`mcp-ui` is not removed in this task. Its reusable resource loaders, HTML
bundling, and metadata helpers may remain or move behind adapters. The private
`mcpui:*` messages are frozen, marked deprecated, and accepted only through a
compatibility layer. New functionality is implemented solely through Apps.

Never infer an App from response shape when registered tool metadata is
available. Unknown metadata must survive round trips and must not be granted
permissions by default.

## Implementation steps

1. Import the pinned Apps schema and conformance examples from task 001.
2. Implement resource, tool metadata, capability, and result codecs in
   `mcp-ext/apps`.
3. Add server registration and validation helpers with no Agently dependency.
4. Inventory `mcp-ui` packages; classify each as reusable, adapter-only, or
   deprecated wire surface.
5. Implement explicit legacy-to-Apps mappings and typed unsupported cases.
6. Add host-negotiation fallback for plain text/structured tool results.
7. Repin `mcp-ui` to tagged `mcp-protocol`/`mcp-ext` releases and test without
   workspace replacements.
8. Publish migration guidance and telemetry keys used by task 016.

## Tests

- upstream Apps basic-host resources and golden messages;
- tool-to-resource association and missing-resource rejection;
- CSP, permissions, visibility, and app-only tool validation;
- complete success, error, cancellation, and mixed-content results;
- unknown metadata preservation and deny-by-default permissions;
- legacy `mcpui:*` translation, malformed envelope, and unsupported operation;
- when both sides negotiate Apps, legacy metadata cannot silently win;
- a host with no Apps support receives a usable non-UI result;
- released-module CI with all local `replace` directives disabled.

## Acceptance criteria

- A minimal `mcp` conformance harness can negotiate and serve the App to the
  official basic host without Agently-specific messages.
- The same tool remains usable by a non-UI MCP host.
- No new feature is added to the private `mcpui:*` protocol.
- Existing Agently clients work through a feature-flagged compatibility adapter.
- `mcp-ui` removal remains prohibited until task 016's evidence gates pass.

## Rollout and rollback

Ship Apps registration additively and keep the legacy adapter enabled for
known old clients. Rollback disables Apps advertisement and restores legacy
selection; it does not rewrite stored transcripts or mutate private envelopes.

## Completion evidence

- Apps conformance test: _pending_
- Non-UI fallback test: _pending_
- `mcp-ui` compatibility matrix: _pending_
- Released-dependency CI: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
