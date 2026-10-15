# Resource delivery

Core uses the generic gateway, catalog, in-memory MCP producer, resource readers,
and connection pool from Forge's shared backend. `shared.go` preserves existing
Core imports with type aliases and forwarding generic functions. It does not own
another discovery cache, connection registry, or resource implementation.

Core retains application adapters: workspace documents, intent/template decoding,
authored asset compilation, and UI composition. The snapshot constructor supplies
Core's intent, template, and skill validators to the shared compiler. Transport
identity, resource selection, and dispatch context use the shared context keys.

The executor creates gateway instances during runtime composition and injects the
existing MCP manager. Reads reuse those instances. The bootstrap MCP-definition
reader has its own public metadata context; it uses the same gateway implementation
and connection pool without borrowing a conversation's resource-read sequence.

External namespace discovery feeds the generic resource catalog. Internal typed
list/get APIs aggregate that catalog; they do not require every external server
to implement a distinct tool for every primitive. Managing MCPs remain responsible
for resource and tool authorization.

Generic implementation tests live with `forge/backend/resourcehost` and
`forge/backend/clientpool`. Core retains workspace, executor, SDK, and UI integration
tests. Neither implementation imports the Agently application from Forge.
