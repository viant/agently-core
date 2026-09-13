# Table preference contract v1

This package defines and validates table presentation preferences. It contains
no store, listener, MCP server, or business-specific table definitions.

Forge defaults to a browser-local adapter. Hosts may inject an external adapter
with async `get(key)`, `set(key, preferences)`, and `reset(key)` methods. The Go
client validates both outbound preferences and inbound external responses.

An external MCP integration implements `Transport.Call` and maps these operations
to its own tool names. It handles MCP initialization/envelopes/authentication.
The normalized payload contract is:

| Operation | Request | Response |
| --- | --- | --- |
| get | `{"key":"..."}` | `null` or the preference JSON object |
| set | `{"key":"...","preferences":{...}}` | `{}` |
| reset | `{"key":"..."}` | `{}` |

Preferences have `version: 1`, ordered `columns` containing `id`, optional
`visible`, numeric `width`, `displayName`, `align`, and `tooltip`; optional
`sort: {columnId, direction}`, `density: compact|normal`, and `frozenColumnIds`.
The contract is intentionally limited to user choices: no row data, handlers,
links, tool definitions, authorization state, or arbitrary CSS is persisted.

Limits: 64 KiB payloads, 256 columns/frozen IDs, 256-character IDs/display names,
1024-character tooltips, widths 24–4096 pixels, and opaque keys up to 512
characters. IDs must be unique; sort is asc/desc; alignment is left/center/right.
Unknown fields and versions are rejected. The JSON schema is shipped alongside
the Go validation code for external implementations.

The host must scope keys by workspace and authenticated principal. A raw table
key is not an authorization boundary. External servers derive identity from
trusted authentication, not a user ID inside a preference payload. Adapter
failures must not silently fall back to another account's local preferences.

On hydration, Forge reconciles IDs against current metadata, ignores removed
columns, appends newly introduced columns, and preserves non-excludable and
authorization constraints. Preferences never authorize access to a column.

Run `go test ./service/ui/tablepreference`.

## Workspace configuration

Optional `extension/forge/preferences.yaml`:

```yaml
version: 1
tablePreferences:
  adapter: browser
  namespace: personal-tables
```

For an externally shipped MCP provider:

```yaml
version: 1
tablePreferences:
  adapter: mcp
  mcp:
    serverRef: user-preferences
    tools:
      get: table_preferences_get
      set: table_preferences_set
      reset: table_preferences_reset
```

`LoadConfig` validates the declaration. The host publishes its
`tablePreferences` value and passes it as `connectorConfig.tablePreferences`
to Forge. For MCP it resolves the already-configured server and injects
`services.tablePreferences`; no service is started by this package. Browser is
the missing-file default. Invalid MCP configuration or an unavailable configured
provider is an error, never an implicit fallback to browser storage.
