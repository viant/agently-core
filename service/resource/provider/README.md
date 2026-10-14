# Reusable resource provider

This package exports an authoritative primitive catalog through standard MCP
`tools/list` and `tools/call`. It depends on neutral primitive/resource contracts,
MCP libraries and the standard library. It imports no Agently application,
workspace, SQL service, SDK or Forge domain code.

Use `NewLocalProvider(LocalConfig)` with a stable host-assigned `ProviderIdentity`,
verified request actor callbacks, resource authorization callback, explicit logical
bindings, validators, and revision resolvers. `NewMCPHandler(provider, protect)`
serves protected HTTP MCP; `NewLocalMCPClient(provider)` uses the same registered
handler in memory. Protection must derive identity from authenticated transport
state. Arguments never supply actor, credentials, source paths or provider URLs.

A menu is optional. Without an explicit menu binding the producer advertises no
`navigations/list`, `navigations/get` or navigation namespace capability. Adding a format-1 binding
requires `ValidateMenu` and a trusted `MenuActionVisibility` callback. The callback
checks every authored action, including actions on groups; groups whose children
are all denied disappear. Callback inputs are logical menu identity and current
verified actor. Managing MCPs authorize window and datasource operations; menu checks control UI visibility only.

Menu source format:

```json
{"schemaVersion":1,"applications":[{"id":"workspace","title":"Workspace","icon":"applications","menus":[{"id":"advertisers","title":"Advertisers","icon":"office","action":{"type":"window","windowKey":"advertiserList","parameters":{"page":1}}}]}]}
```

Node fields, action payloads and parameters retain their JSON meaning. Envelope
fields are exactly `schemaVersion` and `applications`; nested groups preserve `children` (existing layout) or `menus`.
Providers decide which authored menus to publish; no server URL supplies a namespace,
and authoring a menu does not automatically install or expose it.

`navigations/get` returns `primitive.GetResult`. Its `resource` has exact source metadata
and content fingerprint, but no source `definition` or `definitionBytes`.
`resolvedResource` binds the source revision, provider, authority and original
lease. `menuProjection` contains format version (`schemaVersion:1`), visible
`definitionBytes` (base64 JSON carrier) and its own `contentFingerprint`. The
projection hash is never represented as an authored source hash. Neither MCP
text output nor structured content includes denied source bytes.

The provider resolves omitted revisions through injected policy. Menu resolvers
may expose working and stamped candidates; explicit stamps resolve exactly and
are independently authorized. Reads repeat exact-source, visibility and actor
checks and retain the original lease. Working-only local layout adapters belong
in host packages, not this neutral producer.

Agently's gateway consumer uses `resource.Gateway.GetMenu`, which verifies source
provenance and the separate visible carrier and forces a fresh provider read.
Generic `Gateway.Get` rejects menu projections. Host execution should reread the
same menu source pin before acting, then use existing guarded window-open APIs.

The runnable example is `examples/primitive-navigation-mcp`. Set `NAVIGATION_EXAMPLE_TOKEN` to
an opaque secret of at least 16 characters, run the command, and connect an MCP
client with `Authorization: Bearer <secret>` to its printed loopback endpoint.
The example uses a configured identity for demonstration; production hosts inject
IdP verification, revocation, account scope and revision policy callbacks.

Validation:

```sh
go test -race ./service/resource/provider ./service/resource
```

For an unpublished feature checkout, use an explicit local module graph that
includes its matching `protocol/primitive` and `protocol/resource` sources. The
release must publish and pin those modules together; a temporary workspace is
verification setup, not a runtime dependency.

The external-package standalone test constructs only the reusable producer and
MCP client/server, exercising tools discovery, namespace capability, menu list/get,
nested denied targets, lossless parameters, source/projection isolation, expiry
and revocation. Host tests add exact stamped selection, YAML preservation, source
drift, gateway provenance, collision and authority isolation.

Dynamic inventories implement `LocalCatalog` and are registered once in `LocalConfig.Catalog`. The existing MCP list/get handler performs direct lookups through that source; it does not reconstruct a provider on each call. Optional `Authoring` uses the existing lifecycle DTOs and advertises only the operations explicitly declared by each namespace. Read-only sources remain read-only.
