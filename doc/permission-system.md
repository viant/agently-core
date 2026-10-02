# Permission system

The permission adapter belongs to agently-core. Forge is transport-agnostic: it knows
only the authorization declaration, normalized snapshot, condition grammar, and
permitted metadata tree. Forge does not know what MCP is and does not select or invoke
MCP tools.

## Ownership

| Layer | Responsibility |
|---|---|
| Authored Forge metadata | Declare whether a view needs permission filtering, its resource selector, and capability conditions |
| agently-core | Resolve the configured adapter, invoke it, normalize its response, and compile the permitted tree |
| Forge | Evaluate normalized authorization conditions and render only the supplied permitted metadata |
| Web/native host | Fetch complete metadata, supply the concrete resource context, call the SDK operation, then render |
| Backend tools | Independently authorize every read and mutation |

## Opt-in behavior

Permission filtering is optional per view.

If metadata does not contain `authorization`, agently-core and every client skip the
adapter. Metadata and datasource behavior remains unchanged:

```yaml
id: document
view:
  content:
    id: documentRoot
```

If `authorization` is present, the adapter is required and failures are closed:

```yaml
id: document
authorization:
  dataSourceRef: resource_authorization
  scope: resource
  resource:
    type: document
    id:
      source: resource
      selector: DocumentId.0
  requestedCapabilities: [read, write, manageSettings]
  behavior:
    failClosed: true
    authorizeBeforeDatasourceInit: true
    clearProtectedStateOnChange: true
view:
  content:
    id: documentRoot
```

The `dataSourceRef` is an opaque logical adapter reference from Forge's perspective.
agently-core owns its backend resolution.

## Runtime sequence

```text
load complete authored metadata
        ↓
obtain the concrete resource context
        ↓
agently-core applyPermission(windowKey, resource, windowParams, conversationId)
        ↓
agently-core invokes the configured authorization adapter
        ↓
compile and return resource-specific permitted metadata
        ↓
host publishes metadata to Forge/native renderer
        ↓
initialize remaining optional datasources
```

The complete protected document may be loaded before its resource is known, but it
must not be rendered. Permission is applied once the concrete resource context exists.

The datasource fetch contract is unchanged. It does not carry a window ID, permit
token, or authorization version.

## Configure the agently-core adapter

The adapter is workspace configuration. A minimal workspace contains:

```text
workspace/
├── mcp/
│   └── policy.yaml
└── extension/
    └── forge/
        └── datasources/
            └── resource_authorization.yaml
```

The two files have separate responsibilities:

| File | Owner | Purpose |
|---|---|---|
| `mcp/policy.yaml` | agently-core | MCP endpoint and authentication transport |
| `extension/forge/datasources/resource_authorization.yaml` | agently-core workspace | Maps the logical adapter ID to an MCP tool |
| window `authorization.dataSourceRef` | authored metadata | Selects the logical adapter without knowing its transport |

An unprotected workspace may omit both adapter files. Protected views require the
referenced adapter to be configured.

### 1. Register the MCP server in the Agently workspace

```yaml
name: policy
transport:
  type: streamable
  url: https://policy.example.test/mcp
auth:
  backendForFrontend: true
  useIdToken: true
```

### 2. Define the logical authorization datasource

```yaml
id: resource_authorization
title: Resource Authorization
cardinality: object
autoFetch: false
selectors:
  data: data
backend:
  kind: mcp_tool
  service: policy
  method: ResourceAuthorization
cache:
  scope: user
  ttl: 1m
  key:
    - args.ResourceType
    - args.ResourceIDs
    - args.RequestedCapabilities
    - args.RequestedGlobalCapabilities
```

### 3. Bind the resolver during agently-core bootstrap

The current runtime constructs an `MCPResolver` over the Agently tool registry:

```go
resolver := &permittedview.MCPResolver{
    Executor: registry,
    ToolName: "policy:ResourceAuthorization",
}
permittedview.SetDefaultRuntime(permittedview.NewRuntime(resolver))
```

When `ToolName` is omitted, the current default is
`steward:ResourceAuthorization`. Hosts using another adapter must set the tool name
explicitly. A future registry may resolve `authorization.dataSourceRef` directly; that
resolution still belongs to agently-core, not Forge.

### Configuration resolution rule

For a protected view, these identifiers must agree:

```text
window.authorization.dataSourceRef
        ↓
workspace datasource id
        ↓
backend.service + ":" + backend.method
        ↓
registered MCP server + tool
```

For the neutral example above:

```text
resource_authorization
        ↓
extension/forge/datasources/resource_authorization.yaml
        ↓
policy:ResourceAuthorization
        ↓
mcp/policy.yaml
```

Resolution is performed once during host bootstrap. A missing datasource, mismatched
service, unavailable MCP server, or absent tool is a configuration error for protected
views. It has no effect on views that omit `authorization`.

## Adapter contract

Input:

```json
{
  "ResourceType": "document",
  "ResourceIDs": [42],
  "RequestedCapabilities": ["read", "write", "manageSettings"],
  "RequestedGlobalCapabilities": [],
  "IncludePrincipal": true
}
```

Output:

```json
{
  "status": "ok",
  "authorizationVersion": "revision-123",
  "expiresAt": "2026-09-02T12:00:00Z",
  "globalCapabilities": {},
  "resources": {
    "42": {
      "type": "document",
      "id": 42,
      "capabilities": {
        "read": true,
        "write": false,
        "manageSettings": false
      }
    }
  }
}
```

Raw credentials and complete ACL maps must not be returned.

## SDK operation

Permission application is separate from normal metadata retrieval:

```go
complete, err := client.GetForgeWindowMetadata(ctx, "document")
permitted, err := client.ApplyPermission(ctx, "document", &sdk.ApplyPermissionInput{
    ConversationID: conversationID,
    Resource: map[string]any{"DocumentId": []int{42}},
    WindowParams: map[string]any{"DocumentId": []int{42}},
})
```

Equivalent `applyPermission` operations exist in the TypeScript, Android, and iOS
SDKs. The operation returns the permitted metadata tree; it does not modify the normal
datasource request.

## Forge condition contract

Forge consumes normalized conditions such as:

```yaml
- id: settings
  title: Settings
  visibleWhen:
    source: authorization
    field: resource.capabilities.manageSettings
    equals: true
```

Authorization conditions may occur on containers, controls, table columns, options,
events, and actions. Supported behavior includes `visibleWhen`, `hiddenWhen`,
`disabledWhen`, `readOnlyWhen`, `contains`, and `exists`.

Missing fields and unknown operators are never truthy. Compilation removes denied
nodes and datasource declarations no longer reachable from the permitted tree.

## Client integration

### Web

The host calls `applyPermission` after complete metadata and a concrete resource are
available. It stores only the returned tree in the window metadata signal. Forge then
performs a matching defensive compilation without knowing the adapter transport.

### Android

The native window metadata loader receives the window key, parameters, and
conversation ID. It:

1. fetches complete metadata;
2. checks for `authorization`;
3. skips permission handling entirely when the declaration is absent;
4. otherwise calls the Android SDK `applyPermission` operation with the resource
   parameters;
5. decodes only the returned permitted document into native `WindowMetadata`.

### iOS

The iOS SDK exposes the same operation. Its native host must follow the same ordering
before handing metadata to its renderer.

## Failure rules

| Situation | Result |
|---|---|
| no `authorization` declaration | render normally; no adapter required |
| protected view, adapter unavailable | fail closed |
| resource selector unresolved | fail closed |
| resource `read` denied | deny the resource view |
| expired, malformed, or mismatched snapshot | fail closed |
| optional capability denied | prune or disable as authored |
| direct backend request | backend applies its own authorization |

## Verification checklist

- An unprotected view renders with no adapter configured.
- A protected view never reaches a renderer before `applyPermission` completes.
- The same authored metadata can produce different trees for different resources.
- Removed nodes cannot initialize their optional datasources.
- Web and native clients receive equivalent semantic trees.
- Metadata and datasource fetch request shapes remain backward compatible.
